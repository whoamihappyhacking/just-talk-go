//go:build windows

package voice

import (
	"bytes"
	"fmt"
	"io"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	winmm                     = windows.NewLazySystemDLL("winmm.dll")
	procWaveInOpen            = winmm.NewProc("waveInOpen")
	procWaveInPrepareHeader   = winmm.NewProc("waveInPrepareHeader")
	procWaveInUnprepareHeader = winmm.NewProc("waveInUnprepareHeader")
	procWaveInAddBuffer       = winmm.NewProc("waveInAddBuffer")
	procWaveInStart           = winmm.NewProc("waveInStart")
	procWaveInStop            = winmm.NewProc("waveInStop")
	procWaveInReset           = winmm.NewProc("waveInReset")
	procWaveInClose           = winmm.NewProc("waveInClose")
	procWaveInGetNumDevs      = winmm.NewProc("waveInGetNumDevs")
	procWaveInGetDevCapsW     = winmm.NewProc("waveInGetDevCapsW")
	procWaveInGetErrorTextW   = winmm.NewProc("waveInGetErrorTextW")
)

const (
	waveMapper       = ^uint32(0)
	waveFormatPCM    = 1
	callbackEvent    = 0x00050000
	waveHeaderDone   = 0x00000001
	waveHeaderBuffer = 3200
	waveHeaderCount  = 8
)

type waveFormatEx struct {
	FormatTag      uint16
	Channels       uint16
	SamplesPerSec  uint32
	AvgBytesPerSec uint32
	BlockAlign     uint16
	BitsPerSample  uint16
	Size           uint16
}

type waveHeader struct {
	Data          uintptr
	BufferLength  uint32
	BytesRecorded uint32
	User          uintptr
	Flags         uint32
	Loops         uint32
	Next          uintptr
	Reserved      uintptr
}

type waveInCaps struct {
	ManufacturerID uint16
	ProductID      uint16
	DriverVersion  uint32
	ProductName    [32]uint16
	Formats        uint32
	Channels       uint16
	Reserved       uint16
}

type windowsAudioStream struct {
	mu     sync.Mutex
	cond   *sync.Cond
	buffer bytes.Buffer
	closed bool
}

func newWindowsAudioStream() *windowsAudioStream {
	s := &windowsAudioStream{}
	s.cond = sync.NewCond(&s.mu)
	return s
}

func (s *windowsAudioStream) Read(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for s.buffer.Len() == 0 && !s.closed {
		s.cond.Wait()
	}
	if s.buffer.Len() == 0 && s.closed {
		return 0, io.EOF
	}
	return s.buffer.Read(p)
}

func (s *windowsAudioStream) Write(p []byte) {
	if len(p) == 0 {
		return
	}
	s.mu.Lock()
	if !s.closed {
		_, _ = s.buffer.Write(p)
		s.cond.Signal()
	}
	s.mu.Unlock()
}

func (s *windowsAudioStream) Close() error {
	s.mu.Lock()
	if !s.closed {
		s.closed = true
		s.cond.Broadcast()
	}
	s.mu.Unlock()
	return nil
}

type windowsWaveRecorder struct {
	mu       sync.Mutex
	handle   windows.Handle
	event    windows.Handle
	stream   *windowsAudioStream
	buffers  [][]byte
	headers  []waveHeader
	stopping bool
	stopOnce sync.Once
	wg       sync.WaitGroup
}

func startCaptureWithDevice(logger *slog.Logger, device string) (io.ReadCloser, string, func() error, error) {
	deviceID, deviceName, err := resolveWaveInDevice(device)
	if err != nil {
		return nil, "", nil, err
	}

	event, err := windows.CreateEvent(nil, 0, 0, nil)
	if err != nil {
		return nil, "", nil, fmt.Errorf("create recorder event: %w", err)
	}

	format := waveFormatEx{
		FormatTag:      waveFormatPCM,
		Channels:       1,
		SamplesPerSec:  16000,
		AvgBytesPerSec: 32000,
		BlockAlign:     2,
		BitsPerSample:  16,
	}
	var handle windows.Handle
	if code, _, _ := procWaveInOpen.Call(
		uintptr(unsafe.Pointer(&handle)),
		uintptr(deviceID),
		uintptr(unsafe.Pointer(&format)),
		uintptr(event),
		0,
		callbackEvent,
	); code != 0 {
		_ = windows.CloseHandle(event)
		return nil, "", nil, waveInError("open microphone", uint32(code))
	}

	stream := newWindowsAudioStream()
	recorder := &windowsWaveRecorder{
		handle:  handle,
		event:   event,
		stream:  stream,
		buffers: make([][]byte, waveHeaderCount),
		headers: make([]waveHeader, waveHeaderCount),
	}
	if err := recorder.prepare(); err != nil {
		recorder.cleanup()
		return nil, "", nil, err
	}
	if code, _, _ := procWaveInStart.Call(uintptr(handle)); code != 0 {
		recorder.cleanup()
		return nil, "", nil, waveInError("start microphone", uint32(code))
	}

	recorder.wg.Add(1)
	go recorder.captureLoop()
	logger.Info("Windows microphone opened", "device", deviceName)
	return stream, "winmm", recorder.stop, nil
}

func (r *windowsWaveRecorder) prepare() error {
	for i := range r.headers {
		r.buffers[i] = make([]byte, waveHeaderBuffer)
		r.headers[i] = waveHeader{
			Data:         uintptr(unsafe.Pointer(&r.buffers[i][0])),
			BufferLength: uint32(len(r.buffers[i])),
		}
		header := &r.headers[i]
		if code, _, _ := procWaveInPrepareHeader.Call(
			uintptr(r.handle), uintptr(unsafe.Pointer(header)), unsafe.Sizeof(*header),
		); code != 0 {
			return waveInError("prepare microphone buffer", uint32(code))
		}
		if code, _, _ := procWaveInAddBuffer.Call(
			uintptr(r.handle), uintptr(unsafe.Pointer(header)), unsafe.Sizeof(*header),
		); code != 0 {
			return waveInError("queue microphone buffer", uint32(code))
		}
	}
	return nil
}

func (r *windowsWaveRecorder) captureLoop() {
	defer r.wg.Done()
	defer r.stream.Close()

	for {
		_, _ = windows.WaitForSingleObject(r.event, windows.INFINITE)

		r.mu.Lock()
		stopping := r.stopping
		for i := range r.headers {
			header := &r.headers[i]
			if header.Flags&waveHeaderDone == 0 {
				continue
			}
			if n := int(header.BytesRecorded); n > 0 && n <= len(r.buffers[i]) {
				r.stream.Write(r.buffers[i][:n])
			}
			header.BytesRecorded = 0
			if !stopping {
				_, _, _ = procWaveInAddBuffer.Call(
					uintptr(r.handle), uintptr(unsafe.Pointer(header)), unsafe.Sizeof(*header),
				)
			}
		}
		r.mu.Unlock()

		if stopping {
			return
		}
	}
}

func (r *windowsWaveRecorder) stop() error {
	var stopErr error
	r.stopOnce.Do(func() {
		r.mu.Lock()
		r.stopping = true
		handle := r.handle
		event := r.event
		if handle != 0 {
			_, _, _ = procWaveInStop.Call(uintptr(handle))
			if code, _, _ := procWaveInReset.Call(uintptr(handle)); code != 0 {
				stopErr = waveInError("reset microphone", uint32(code))
			}
		}
		r.mu.Unlock()
		if event != 0 {
			_ = windows.SetEvent(event)
		}
		r.wg.Wait()
		r.cleanup()
	})
	return stopErr
}

func (r *windowsWaveRecorder) cleanup() {
	if r.handle != 0 {
		_, _, _ = procWaveInReset.Call(uintptr(r.handle))
		for i := range r.headers {
			header := &r.headers[i]
			if header.Data != 0 {
				_, _, _ = procWaveInUnprepareHeader.Call(
					uintptr(r.handle), uintptr(unsafe.Pointer(header)), unsafe.Sizeof(*header),
				)
			}
		}
		_, _, _ = procWaveInClose.Call(uintptr(r.handle))
		r.handle = 0
	}
	if r.event != 0 {
		_ = windows.CloseHandle(r.event)
		r.event = 0
	}
	_ = r.stream.Close()
}

func resolveWaveInDevice(device string) (uint32, string, error) {
	device = strings.TrimSpace(device)
	if device == "" || strings.EqualFold(device, "default") {
		return waveMapper, "default", nil
	}
	if id, err := strconv.ParseUint(device, 10, 32); err == nil {
		if id < uint64(waveInDeviceCount()) {
			return uint32(id), device, nil
		}
	}

	count := waveInDeviceCount()
	for id := uint32(0); id < count; id++ {
		name, err := waveInDeviceName(id)
		if err == nil && strings.EqualFold(name, device) {
			return id, name, nil
		}
	}
	return 0, "", fmt.Errorf("microphone %q was not found", device)
}

func waveInDeviceCount() uint32 {
	count, _, _ := procWaveInGetNumDevs.Call()
	return uint32(count)
}

func waveInDeviceName(id uint32) (string, error) {
	var caps waveInCaps
	if code, _, _ := procWaveInGetDevCapsW.Call(
		uintptr(id), uintptr(unsafe.Pointer(&caps)), unsafe.Sizeof(caps),
	); code != 0 {
		return "", waveInError("read microphone information", uint32(code))
	}
	return windows.UTF16ToString(caps.ProductName[:]), nil
}

func waveInError(action string, code uint32) error {
	var message [256]uint16
	if result, _, _ := procWaveInGetErrorTextW.Call(
		uintptr(code), uintptr(unsafe.Pointer(&message[0])), uintptr(len(message)),
	); result == 0 {
		if text := strings.TrimSpace(windows.UTF16ToString(message[:])); text != "" {
			return fmt.Errorf("%s: %s", action, text)
		}
	}
	return fmt.Errorf("%s: winmm error %d", action, code)
}

// ListDevices returns Windows audio input devices exposed by winmm.
func ListDevices() ([]string, error) {
	devices := []string{"default"}
	count := waveInDeviceCount()
	for id := uint32(0); id < count; id++ {
		name, err := waveInDeviceName(id)
		if err != nil {
			continue
		}
		devices = append(devices, name)
	}
	if count == 0 {
		return nil, fmt.Errorf("no Windows microphone input device was found")
	}
	return devices, nil
}

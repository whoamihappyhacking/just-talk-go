package voice

import (
	"bytes"
	"errors"
	"io"
	"log/slog"
	"os"
	"sync"
)

// Recorder captures audio from the default microphone as PCM 16kHz 16bit mono.
type Recorder struct {
	logger   *slog.Logger
	device   string
	gain     int
	mu       sync.Mutex
	readMu   sync.Mutex
	stdout   io.ReadCloser
	stopFunc func() error
	drainBuf bytes.Buffer
	started  bool
	backend  string
}

func NewRecorder(logger *slog.Logger, gain int) *Recorder {
	if gain < 1 {
		gain = 1
	}
	return &Recorder{logger: logger, gain: gain}
}

func NewRecorderWithDevice(logger *slog.Logger, device string, gain int) *Recorder {
	if gain < 1 {
		gain = 1
	}
	return &Recorder{logger: logger, device: device, gain: gain}
}

func (r *Recorder) Backend() string { return r.backend }

func (r *Recorder) Start() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.started {
		return nil
	}

	stdout, name, stopFunc, err := startCaptureWithDevice(r.logger, r.device)
	if err != nil {
		return err
	}
	r.stdout = stdout
	r.stopFunc = stopFunc
	r.started = true
	r.backend = name
	r.logger.Info("recording started", "backend", name)
	return nil
}

func (r *Recorder) Read(p []byte) (int, error) {
	r.readMu.Lock()
	defer r.readMu.Unlock()

	r.mu.Lock()
	if r.drainBuf.Len() > 0 {
		n, _ := r.drainBuf.Read(p)
		r.mu.Unlock()
		return n, nil
	}
	stdout := r.stdout
	r.mu.Unlock()

	if stdout == nil {
		return 0, io.EOF
	}

	n, err := stdout.Read(p)
	if n > 0 && r.gain > 1 {
		applyGain(p[:n], r.gain)
	}
	if errors.Is(err, os.ErrClosed) {
		err = io.EOF
	}
	return n, err
}

func (r *Recorder) Stop() ([]byte, error) {
	r.mu.Lock()
	if !r.started {
		r.mu.Unlock()
		return nil, nil
	}
	r.started = false
	stopFunc := r.stopFunc
	stdout := r.stdout
	r.stopFunc = nil
	r.stdout = nil
	r.mu.Unlock()

	if stopFunc != nil {
		_ = stopFunc()
	}

	r.readMu.Lock()
	defer r.readMu.Unlock()
	r.mu.Lock()
	defer r.mu.Unlock()

	if stdout != nil {
		buf := make([]byte, 640)
		for {
			n, err := stdout.Read(buf)
			if n > 0 {
				applyGain(buf[:n], r.gain)
				r.drainBuf.Write(buf[:n])
			}
			if err != nil {
				break
			}
		}
	}

	remaining := r.drainBuf.Bytes()
	r.drainBuf.Reset()
	r.logger.Info("recording stopped", "remaining_bytes", len(remaining))
	return remaining, nil
}

// applyGain multiplies s16le samples by gain factor, clamping to valid range.
func applyGain(pcm []byte, gain int) {
	for i := 0; i+1 < len(pcm); i += 2 {
		s := int16(pcm[i]) | int16(pcm[i+1])<<8
		s32 := int32(s) * int32(gain)
		if s32 > 32767 {
			s32 = 32767
		} else if s32 < -32768 {
			s32 = -32768
		}
		pcm[i] = byte(s32)
		pcm[i+1] = byte(s32 >> 8)
	}
}

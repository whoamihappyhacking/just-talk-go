//go:build darwin && cgo

package voice

// #cgo LDFLAGS: -framework AudioToolbox -framework CoreAudio
// #include "audioqueue_darwin.h"
import "C"

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"syscall"
)

type darwinAudioReadCloser struct {
	file *os.File
	rec  *C.jt_audio_recorder_t
}

func (r *darwinAudioReadCloser) Read(p []byte) (int, error) {
	return r.file.Read(p)
}

func (r *darwinAudioReadCloser) Close() error {
	if r.rec != nil {
		C.jt_audio_stop(r.rec)
		r.rec = nil
	}
	if r.file != nil {
		return r.file.Close()
	}
	return nil
}

func startCaptureWithDevice(logger *slog.Logger, device string) (io.ReadCloser, string, func() error, error) {
	if device != "" {
		logger.Warn("macOS native recorder ignores explicit device for now", "device", device)
	}
	readFile, writeFile, err := os.Pipe()
	if err != nil {
		return nil, "", nil, err
	}
	cWriteFD, err := syscall.Dup(int(writeFile.Fd()))
	if err != nil {
		_ = readFile.Close()
		_ = writeFile.Close()
		return nil, "", nil, err
	}

	var rec *C.jt_audio_recorder_t
	rc := int(C.jt_audio_start(C.int(cWriteFD), &rec))
	_ = writeFile.Close()
	if rc != 0 {
		_ = syscall.Close(cWriteFD)
		_ = readFile.Close()
		return nil, "", nil, fmt.Errorf("AudioQueue start failed: %d", rc)
	}

	closer := &darwinAudioReadCloser{file: readFile, rec: rec}
	stop := func() error { return closer.Close() }
	return closer, "coreaudio", stop, nil
}

func ListDevices() ([]string, error) {
	return []string{"default"}, nil
}

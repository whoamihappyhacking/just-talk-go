//go:build windows

package voice

import (
	"io"
	"log/slog"
	"os"
	"sync/atomic"
	"testing"
	"time"
)

func TestWindowsRecorderIntegration(t *testing.T) {
	if os.Getenv("JUST_TALK_TEST_WINDOWS_AUDIO") == "" {
		t.Skip("set JUST_TALK_TEST_WINDOWS_AUDIO=1 to test the default Windows microphone")
	}

	recorder := NewRecorder(slog.New(slog.NewTextHandler(io.Discard, nil)), 1)
	if err := recorder.Start(); err != nil {
		t.Fatalf("start recorder: %v", err)
	}

	var streamedBytes atomic.Int64
	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		buf := make([]byte, 6400)
		for {
			n, err := recorder.Read(buf)
			streamedBytes.Add(int64(n))
			if err != nil {
				return
			}
		}
	}()

	time.Sleep(500 * time.Millisecond)
	type stopResult struct {
		audio []byte
		err   error
	}
	stopDone := make(chan stopResult, 1)
	go func() {
		audio, err := recorder.Stop()
		stopDone <- stopResult{audio: audio, err: err}
	}()

	var result stopResult
	select {
	case result = <-stopDone:
	case <-time.After(3 * time.Second):
		t.Fatal("stop recorder timed out with an active reader")
	}
	if result.err != nil {
		t.Fatalf("stop recorder: %v", result.err)
	}
	select {
	case <-readDone:
	case <-time.After(time.Second):
		t.Fatal("recorder reader did not exit after stop")
	}
	if streamedBytes.Load()+int64(len(result.audio)) == 0 {
		t.Fatal("recorder returned no PCM audio")
	}
	if len(result.audio)%2 != 0 {
		t.Fatalf("PCM byte count must be aligned to 16-bit samples: %d", len(result.audio))
	}
	if recorder.Backend() != "winmm" {
		t.Fatalf("unexpected recorder backend %q", recorder.Backend())
	}
}

//go:build linux

package voice

import (
	"bytes"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"testing"
	"time"
)

func TestCaptureCommandHelperProcess(t *testing.T) {
	if os.Getenv("JUST_TALK_CAPTURE_HELPER") != "1" {
		return
	}
	_, _ = os.Stdout.Write(bytes.Repeat([]byte{1, 0}, 1024))
	for {
		time.Sleep(time.Hour)
	}
}

func TestCommandRecorderStopPreservesBufferedTail(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	cmd := exec.Command(os.Args[0], "-test.run=^TestCaptureCommandHelperProcess$")
	cmd.Env = append(os.Environ(), "JUST_TALK_CAPTURE_HELPER=1")
	stdout, _, stop, err := startCaptureCommand(logger, cmd, "test")
	if err != nil {
		t.Fatal(err)
	}
	rec := NewRecorder(logger, 2)
	rec.stdout, rec.stopFunc, rec.started = stdout, stop, true
	defer rec.Stop()
	prefix := make([]byte, 128)
	n, err := rec.Read(prefix)
	if err != nil {
		t.Fatal(err)
	}
	tail, err := rec.Stop()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(append(prefix[:n], tail...), bytes.Repeat([]byte{2, 0}, 1024)) {
		t.Fatal("stop discarded buffered audio or applied gain twice")
	}
}

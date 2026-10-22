package singleton

import (
	"errors"
	"os"
	"strconv"
	"testing"
)

func TestAcquireRejectsSecondInstance(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())

	first, err := Acquire("just-talk-test")
	if err != nil {
		t.Fatalf("first Acquire: %v", err)
	}
	defer first.Release()

	// flock and LockFileEx are per open file description, so a second attempt
	// from this same process behaves exactly like a second instance.
	second, err := Acquire("just-talk-test")
	if err == nil {
		second.Release()
		t.Fatal("second Acquire succeeded; two instances would duplicate output")
	}
	var running *AlreadyRunningError
	if !errors.As(err, &running) {
		t.Fatalf("second Acquire error = %v, want AlreadyRunningError", err)
	}
	if running.PID != os.Getpid() {
		t.Errorf("reported PID = %d, want %d", running.PID, os.Getpid())
	}
}

func TestAcquireAfterRelease(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())

	first, err := Acquire("just-talk-test")
	if err != nil {
		t.Fatalf("first Acquire: %v", err)
	}
	if err := first.Release(); err != nil {
		t.Fatalf("Release: %v", err)
	}

	second, err := Acquire("just-talk-test")
	if err != nil {
		t.Fatalf("Acquire after Release: %v", err)
	}
	defer second.Release()

	data, err := os.ReadFile(lockPath("just-talk-test"))
	if err != nil {
		t.Fatalf("read lock file: %v", err)
	}
	if string(data) != strconv.Itoa(os.Getpid()) {
		t.Errorf("lock file = %q, want %q", data, strconv.Itoa(os.Getpid()))
	}
}

func TestReleaseIsIdempotent(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())

	lock, err := Acquire("just-talk-test")
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if err := lock.Release(); err != nil {
		t.Fatalf("first Release: %v", err)
	}
	if err := lock.Release(); err != nil {
		t.Fatalf("second Release: %v", err)
	}
}

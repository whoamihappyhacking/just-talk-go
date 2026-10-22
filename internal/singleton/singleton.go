// Package singleton keeps a second Just Talk process off the same desktop.
package singleton

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// AlreadyRunningError reports that another process holds the lock. Nothing
// stops two instances from sharing one hotkey: the Wayland provider reads
// /dev/input passively and never grabs it, and evdev delivers every event to
// every reader. Both instances then record, recognize, and paste the same
// speech, which reaches the user as one sentence pasted twice.
type AlreadyRunningError struct {
	Path string
	PID  int
}

func (e *AlreadyRunningError) Error() string {
	if e.PID > 0 {
		return fmt.Sprintf("just-talk 已在运行（PID %d）", e.PID)
	}
	return "just-talk 已在运行"
}

// Lock is an acquired single-instance lock.
type Lock struct{ file *os.File }

// Acquire takes the exclusive lock for name and records the current PID, so a
// later start can name the process that is holding it.
func Acquire(name string) (*Lock, error) {
	path := lockPath(name)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("创建锁目录失败: %w", err)
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("打开锁文件 %s 失败: %w", path, err)
	}
	locked, err := lockFile(file)
	if err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("锁定 %s 失败: %w", path, err)
	}
	if !locked {
		pid := readPID(file)
		_ = file.Close()
		return nil, &AlreadyRunningError{Path: path, PID: pid}
	}
	writePID(file)
	return &Lock{file: file}, nil
}

// Release drops the lock. The kernel also releases it when the process exits,
// so an exit path that skips this loses nothing.
func (l *Lock) Release() error {
	if l == nil || l.file == nil {
		return nil
	}
	file := l.file
	l.file = nil
	err := unlockFile(file)
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	return err
}

// writePID is best effort: the lock alone keeps the second instance out, and
// the PID only exists to make its error message actionable.
func writePID(file *os.File) {
	if err := file.Truncate(0); err != nil {
		return
	}
	_, _ = file.WriteAt([]byte(strconv.Itoa(os.Getpid())), 0)
}

func readPID(file *os.File) int {
	buf := make([]byte, 32)
	n, _ := file.ReadAt(buf, 0)
	pid, err := strconv.Atoi(strings.TrimSpace(string(buf[:n])))
	if err != nil {
		return 0
	}
	return pid
}

func lockPath(name string) string { return filepath.Join(runtimeDir(), name+".lock") }

// runtimeDir prefers a per-user runtime directory so the lock never collides
// with another user's on a shared /tmp.
func runtimeDir() string {
	if dir := strings.TrimSpace(os.Getenv("XDG_RUNTIME_DIR")); dir != "" {
		return filepath.Join(dir, "just-talk")
	}
	if dir, err := os.UserCacheDir(); err == nil {
		return filepath.Join(dir, "just-talk")
	}
	return filepath.Join(os.TempDir(), fmt.Sprintf("just-talk-%d", os.Getuid()))
}

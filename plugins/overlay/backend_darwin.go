//go:build darwin && cgo

package overlay

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"strconv"
	"sync"

	"github.com/c/just-talk-go/config"
)

type darwinBackend struct {
	cmd   *exec.Cmd
	stdin io.WriteCloser
	mu    sync.Mutex
}

func newBackend(cfg config.OverlayConfig) (backend, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(exe,
		"--overlay-helper",
		"--overlay-position", cfg.Position,
		"--overlay-scale", strconv.FormatFloat(cfg.Scale, 'f', -1, 64),
	)
	cmd.Stdout = io.Discard
	if errLog, err := os.OpenFile("/tmp/just-talk-overlay.log", os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644); err == nil {
		cmd.Stderr = errLog
	} else {
		cmd.Stderr = io.Discard
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		return nil, fmt.Errorf("start macOS overlay helper: %w", err)
	}
	slog.Default().Info("macOS overlay helper started", "pid", cmd.Process.Pid)
	return &darwinBackend{cmd: cmd, stdin: stdin}, nil
}

func (b *darwinBackend) Show(label string, color statusColor) error {
	slog.Default().Debug("macOS overlay show", "label", label, "r", color.R, "g", color.G, "b", color.B)
	return b.send(helperCommand{Cmd: "show", Label: label, R: color.R, G: color.G, B: color.B})
}

func (b *darwinBackend) Hide() error {
	slog.Default().Debug("macOS overlay hide")
	return b.send(helperCommand{Cmd: "hide"})
}

func (b *darwinBackend) Close() error {
	b.mu.Lock()
	stdin := b.stdin
	cmd := b.cmd
	b.stdin = nil
	b.cmd = nil
	b.mu.Unlock()

	if stdin != nil {
		_ = writeHelperCommand(stdin, helperCommand{Cmd: "close"})
		_ = stdin.Close()
	}
	if cmd != nil {
		_ = cmd.Wait()
	}
	return nil
}

func (b *darwinBackend) send(cmd helperCommand) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.stdin == nil {
		return nil
	}
	return writeHelperCommand(b.stdin, cmd)
}

func writeHelperCommand(w io.Writer, cmd helperCommand) error {
	data, err := json.Marshal(cmd)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	_, err = w.Write(data)
	return nil
}

//go:build linux || windows

package voice

import (
	"bytes"
	"io"
	"log/slog"
	"os/exec"
)

type candidate struct {
	name string
	args []string
}

func firstFound(candidates []candidate) (*exec.Cmd, string, error) {
	for _, c := range candidates {
		if path, err := exec.LookPath(c.name); err == nil {
			return exec.Command(path, c.args...), c.name, nil
		}
	}
	return nil, "", errNoBackend(candidates)
}

func startCaptureWithDevice(logger *slog.Logger, device string) (io.ReadCloser, string, func() error, error) {
	return startCommandCapture(logger, device)
}

func startCommandCapture(logger *slog.Logger, device string) (io.ReadCloser, string, func() error, error) {
	cmd, name, err := pickCommandWithDevice(device)
	if err != nil {
		return nil, "", nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, "", nil, err
	}
	var stderrBuf bytes.Buffer
	cmd.Stderr = &stderrBuf
	if err := cmd.Start(); err != nil {
		logger.Error("recorder start failed", "backend", name, "error", err, "stderr", stderrBuf.String())
		return nil, "", nil, err
	}
	stop := func() error {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		return cmd.Wait()
	}
	return stdout, name, stop, nil
}

//go:build linux

package voice

import (
	"bytes"
	"io"
	"log/slog"
	"os"
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
	return startCaptureCommand(logger, cmd, name)
}

func startCaptureCommand(logger *slog.Logger, cmd *exec.Cmd, name string) (io.ReadCloser, string, func() error, error) {
	// Own the read end: exec.Cmd.Wait closes StdoutPipe before Recorder.Stop
	// can drain its buffered tail. Network buffering makes that tail matter.
	stdout, writer, err := os.Pipe()
	if err != nil {
		return nil, "", nil, err
	}
	cmd.Stdout = writer
	var stderrBuf bytes.Buffer
	cmd.Stderr = &stderrBuf
	if err := cmd.Start(); err != nil {
		_ = writer.Close()
		_ = stdout.Close()
		logger.Error("recorder start failed", "backend", name, "error", err, "stderr", stderrBuf.String())
		return nil, "", nil, err
	}
	// The child owns its copy of the write end. EOF follows capture shutdown.
	_ = writer.Close()
	stop := func() error {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		return cmd.Wait()
	}
	return stdout, name, stop, nil
}

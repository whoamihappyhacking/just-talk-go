package voice

import (
	"bytes"
	"io"
	"log/slog"
	"os/exec"
	"sync"
)

// Recorder captures audio from the default microphone as PCM 16kHz 16bit mono.
type Recorder struct {
	logger   *slog.Logger
	device   string
	gain     int
	mu       sync.Mutex
	cmd      *exec.Cmd
	stdout   io.ReadCloser
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

	cmd, name, err := pickCommandWithDevice(r.device)
	if err != nil {
		return err
	}

	r.stdout, err = cmd.StdoutPipe()
	if err != nil {
		return err
	}
	// Capture stderr for debugging recording failures
	var stderrBuf bytes.Buffer
	cmd.Stderr = &stderrBuf

	if err := cmd.Start(); err != nil {
		r.logger.Error("recorder start failed", "backend", name, "error", err, "stderr", stderrBuf.String())
		return err
	}

	r.cmd = cmd
	r.started = true
	r.backend = name
	r.logger.Info("recording started", "backend", name)
	return nil
}

func (r *Recorder) Read(p []byte) (int, error) {
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
	return n, err
}

func (r *Recorder) Stop() ([]byte, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if !r.started {
		return nil, nil
	}
	r.started = false

	if r.cmd != nil && r.cmd.Process != nil {
		r.cmd.Process.Kill()
		r.cmd.Wait()
		r.cmd = nil
	}

	if r.stdout != nil {
		buf := make([]byte, 640)
		for {
			n, err := r.stdout.Read(buf)
			if n > 0 {
				applyGain(buf[:n], r.gain)
				r.drainBuf.Write(buf[:n])
			}
			if err != nil {
				break
			}
		}
		r.stdout = nil
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

// ---- Platform-specific (in recorder_*.go) ----

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

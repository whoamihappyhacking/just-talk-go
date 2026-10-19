// Package statusfile publishes the voice plugin's state as JSON on disk so
// external status bars can render it. Omarchy's Quickshell bar widget is the
// first consumer; waybar, eww, and polybar can read the same file.
package statusfile

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/c/just-talk-go/config"
	"github.com/c/just-talk-go/engine"
	"github.com/c/just-talk-go/plugins/voice"
)

// Matches the overlay's cadence: fast enough that the bar and the capsule
// never disagree visibly, cheap enough to leave running all session.
const pollInterval = 100 * time.Millisecond

// Status is the on-disk contract. Field names are part of the public
// interface for bar widgets; renaming one breaks every consumer.
type Status struct {
	State     string `json:"state"`
	Detail    string `json:"detail"`
	Recording bool   `json:"recording"`
	// Empty unless recording, so a widget can run its own elapsed-time timer
	// without this file being rewritten every tick.
	StartedAt       string `json:"started_at,omitempty"`
	PendingFinishes int    `json:"pending_finishes"`
	UpdatedAt       string `json:"updated_at"`
	PID             int    `json:"pid"`
}

type Plugin struct {
	logger    *slog.Logger
	cfg       config.StatusFileConfig
	path      string
	startedAt time.Time
	last      Status
	wrote     bool
}

func NewStatusFilePlugin() *Plugin { return &Plugin{} }

func (p *Plugin) Name() string    { return "statusfile" }
func (p *Plugin) Version() string { return "0.1.0" }

func (p *Plugin) Init(env engine.PluginEnv) error {
	p.logger = env.Logger()
	p.cfg = env.Config().StatusFile
	p.path = p.cfg.Path
	if p.path == "" {
		p.path = DefaultPath()
	}
	return nil
}

func (p *Plugin) Start(ctx context.Context) error {
	if !p.cfg.Enabled {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(p.path), 0o700); err != nil {
		p.logger.Warn("status file unavailable", "path", p.path, "error", err)
		return nil
	}
	// Remove on exit so a widget can tell "Just Talk is not running" from
	// "Just Talk is idle".
	defer func() {
		if err := os.Remove(p.path); err != nil && !os.IsNotExist(err) {
			p.logger.Debug("status file cleanup failed", "error", err)
		}
	}()

	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	p.logger.Info("status file started", "path", p.path)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			p.sync(voice.TUIStatus(), time.Now())
		}
	}
}

func (p *Plugin) Stop() error { return nil }

// sync writes only on change. A bar widget watching this file would otherwise
// redraw ten times a second while nothing happens.
func (p *Plugin) sync(status voice.TUIVoiceStatus, now time.Time) {
	if status.Recording && p.startedAt.IsZero() {
		p.startedAt = now
	} else if !status.Recording {
		p.startedAt = time.Time{}
	}

	next := Status{
		State:           status.State,
		Detail:          status.Detail,
		Recording:       status.Recording,
		PendingFinishes: status.PendingFinishes,
		PID:             os.Getpid(),
	}
	if !p.startedAt.IsZero() {
		next.StartedAt = p.startedAt.Format(time.RFC3339Nano)
	}
	if p.wrote && next == p.last {
		return
	}
	p.last, p.wrote = next, true

	next.UpdatedAt = now.Format(time.RFC3339Nano)
	if err := writeStatus(p.path, next); err != nil {
		p.logger.Debug("status file write failed", "error", err)
	}
}

// writeStatus truncates in place rather than renaming into position: the
// inode has to stay stable or a watching widget loses its inotify handle.
// A status record is far smaller than a page, so readers never see a tear.
func writeStatus(path string, status Status) error {
	payload, err := json.Marshal(status)
	if err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if _, err := file.Write(append(payload, '\n')); err != nil {
		file.Close()
		return err
	}
	return file.Close()
}

// DefaultPath prefers the runtime directory so the file is per-session and
// cleaned up by the system on logout.
func DefaultPath() string {
	if dir := os.Getenv("XDG_RUNTIME_DIR"); dir != "" {
		return filepath.Join(dir, "just-talk", "status.json")
	}
	return filepath.Join(os.TempDir(), fmt.Sprintf("just-talk-%d", os.Getuid()), "status.json")
}

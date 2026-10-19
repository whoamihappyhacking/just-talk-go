package statusfile

import (
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/c/just-talk-go/plugins/voice"
)

func testPlugin(t *testing.T) *Plugin {
	t.Helper()
	return &Plugin{
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		path:   filepath.Join(t.TempDir(), "status.json"),
	}
}

func readStatus(t *testing.T, path string) Status {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read status: %v", err)
	}
	var status Status
	if err := json.Unmarshal(data, &status); err != nil {
		t.Fatalf("unmarshal status %q: %v", data, err)
	}
	return status
}

// A widget watching this file redraws on every write, so an unchanged status
// must not produce one.
func TestSyncWritesOnlyWhenStatusChanges(t *testing.T) {
	p := testPlugin(t)
	now := time.Date(2026, 10, 19, 15, 0, 0, 0, time.UTC)
	status := voice.TUIVoiceStatus{State: "idle", Detail: "等待热键"}

	p.sync(status, now)
	first, err := os.Stat(p.path)
	if err != nil {
		t.Fatalf("first sync wrote nothing: %v", err)
	}
	if got := readStatus(t, p.path); got.State != "idle" || got.Detail != "等待热键" {
		t.Fatalf("status = %+v", got)
	}

	p.sync(status, now.Add(time.Second))
	second, err := os.Stat(p.path)
	if err != nil {
		t.Fatal(err)
	}
	if !first.ModTime().Equal(second.ModTime()) {
		t.Fatal("unchanged status was rewritten")
	}

	p.sync(voice.TUIVoiceStatus{State: "recording", Recording: true}, now.Add(2*time.Second))
	if got := readStatus(t, p.path); got.State != "recording" || !got.Recording {
		t.Fatalf("changed status not published: %+v", got)
	}
}

// started_at drives the widget's own elapsed-time timer, so it must be set
// once when recording begins and cleared the moment it ends.
func TestStartedAtTracksOneRecordingSpan(t *testing.T) {
	p := testPlugin(t)
	start := time.Date(2026, 10, 19, 15, 0, 0, 0, time.UTC)

	p.sync(voice.TUIVoiceStatus{State: "recording", Recording: true}, start)
	first := readStatus(t, p.path).StartedAt
	if first == "" {
		t.Fatal("recording published without started_at")
	}

	// A later poll during the same recording must not restart the clock.
	p.sync(voice.TUIVoiceStatus{State: "recording", Detail: "转写中", Recording: true}, start.Add(3*time.Second))
	if got := readStatus(t, p.path).StartedAt; got != first {
		t.Fatalf("started_at moved mid-recording: %q then %q", first, got)
	}

	p.sync(voice.TUIVoiceStatus{State: "idle"}, start.Add(5*time.Second))
	if got := readStatus(t, p.path); got.StartedAt != "" {
		t.Fatalf("started_at survived the recording: %+v", got)
	}

	p.sync(voice.TUIVoiceStatus{State: "recording", Recording: true}, start.Add(9*time.Second))
	if got := readStatus(t, p.path).StartedAt; got == first {
		t.Fatal("second recording reused the first start time")
	}
}

// Rename-into-place would break a watcher's inotify handle, so the file has
// to keep its inode across writes.
func TestWritesKeepTheSameInode(t *testing.T) {
	p := testPlugin(t)
	now := time.Now()
	p.sync(voice.TUIVoiceStatus{State: "idle"}, now)
	before, err := os.Stat(p.path)
	if err != nil {
		t.Fatal(err)
	}
	p.sync(voice.TUIVoiceStatus{State: "recording", Recording: true}, now.Add(time.Second))
	after, err := os.Stat(p.path)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(before, after) {
		t.Fatal("status file was replaced instead of truncated in place")
	}
}

func TestDefaultPathPrefersRuntimeDir(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", "/run/user/1000")
	if got, want := DefaultPath(), "/run/user/1000/just-talk/status.json"; got != want {
		t.Fatalf("DefaultPath() = %q, want %q", got, want)
	}
	t.Setenv("XDG_RUNTIME_DIR", "")
	if got := DefaultPath(); filepath.Base(got) != "status.json" || filepath.Dir(got) == "" {
		t.Fatalf("DefaultPath() without XDG_RUNTIME_DIR = %q", got)
	}
}

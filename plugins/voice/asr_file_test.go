package voice

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/c/just-talk-go/config"
)

func fileTestClient(t *testing.T, handler http.HandlerFunc) *FileASRClient {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	client := NewFileASRClient(ASRConfig{AppKey: "app", AccessKey: "token", ResourceID: "stream-only", Language: "zh-CN", Hotwords: []string{"术语", "术语"}}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	client.endpoint = server.URL
	return client
}

func TestFileASRSubmitsOneWholeWAV(t *testing.T) {
	pcm := bytes.Repeat([]byte{0x12, 0x34}, 16000*3)
	var calls atomic.Int32
	client := fileTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != http.MethodPost || r.Header.Get("Upgrade") != "" {
			t.Error("expected one HTTP POST, not WebSocket")
		}
		for key, want := range map[string]string{
			"Content-Type": "application/json", "X-Api-App-Key": "app", "X-Api-Access-Key": "token",
			"X-Api-Resource-Id": fileASRResource, "X-Api-Sequence": "-1",
		} {
			if r.Header.Get(key) != want {
				t.Errorf("%s = %q, want %q", key, r.Header.Get(key), want)
			}
		}
		if r.Header.Get("X-Api-Request-Id") == "" {
			t.Error("missing request ID")
		}
		var body struct {
			User struct {
				UID string `json:"uid"`
			} `json:"user"`
			Audio struct {
				Data     []byte `json:"data"`
				URL      string `json:"url"`
				Language string `json:"language"`
			} `json:"audio"`
			Request map[string]any `json:"request"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		if body.User.UID != "app" || body.Audio.URL != "" || body.Audio.Language != "zh-CN" {
			t.Error("incorrect user/audio metadata")
		}
		wav := body.Audio.Data
		if len(wav) != len(pcm)+44 {
			t.Errorf("WAV bytes = %d", len(wav))
			w.WriteHeader(400)
			return
		}
		if string(wav[:4]) != "RIFF" || string(wav[8:16]) != "WAVEfmt " || string(wav[36:40]) != "data" {
			t.Error("invalid WAV container")
		}
		for offset, want := range map[int]uint32{4: uint32(len(wav) - 8), 16: 16, 24: 16000, 28: 32000, 40: uint32(len(pcm))} {
			if binary.LittleEndian.Uint32(wav[offset:]) != want {
				t.Errorf("invalid WAV uint32 at %d", offset)
			}
		}
		for offset, want := range map[int]uint16{20: 1, 22: 1, 32: 2, 34: 16} {
			if binary.LittleEndian.Uint16(wav[offset:]) != want {
				t.Errorf("invalid WAV uint16 at %d", offset)
			}
		}
		if !bytes.Equal(wav[44:], pcm) {
			t.Error("whole recording was truncated or changed")
		}
		if body.Request["model_name"] != "bigmodel" || body.Request["show_utterances"] != false || body.Request["enable_punc"] != true {
			t.Error("incorrect recognition options")
		}
		corpus, ok := body.Request["corpus"].(map[string]any)
		if !ok || corpus["context"] != `{"hotwords":[{"word":"术语"}]}` {
			t.Error("hotwords not preserved")
		}
		w.Header().Set("X-Api-Status-Code", "20000000")
		fmt.Fprint(w, `{"result":{"text":"完整识别结果"}}`)
	})
	text, err := client.Recognize(context.Background(), pcm)
	if err != nil || text != "完整识别结果" || calls.Load() != 1 {
		t.Fatalf("text=%q err=%v requests=%d", text, err, calls.Load())
	}
}

func TestFileASRErrorsNeverBecomeEmptySuccess(t *testing.T) {
	for _, tc := range []struct {
		name             string
		status           int
		code, body, want string
	}{
		{"auth", 403, "", "", "auc_turbo"},
		{"http failure", 503, "20000000", `{"result":{"text":"ignore"}}`, "HTTP 503"},
		{"missing API status", 200, "", `{"result":{"text":"ignore"}}`, "文件识别失败"},
		{"business failure", 200, "45000001", `{}`, "45000001"},
		{"silence", 200, "20000003", `{}`, "静音"},
		{"busy", 200, "55000031", `{}`, "55000031"},
		{"empty result", 200, "20000000", `{"result":{"text":"  "}}`, "未返回有效文字"},
		{"missing result", 200, "20000000", `{}`, "未返回有效文字"},
		{"malformed", 200, "20000000", `{"result":`, "格式错误"},
		{"large response", 200, "20000000", strings.Repeat("x", maxASRResponseBytes+1), "超过 8 MiB"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			client := fileTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.Header().Set("X-Api-Status-Code", tc.code)
				w.Header().Set("X-Tt-Logid", "test-log-id")
				w.WriteHeader(tc.status)
				fmt.Fprint(w, tc.body)
			})
			text, err := client.Recognize(context.Background(), []byte{1, 2})
			if text != "" || err == nil || !strings.Contains(err.Error(), tc.want) || !strings.Contains(err.Error(), "test-log-id") || calls.Load() != 1 {
				t.Fatalf("text=%q err=%v requests=%d", text, err, calls.Load())
			}
		})
	}
}

func TestFileASRRejectsRedirects(t *testing.T) {
	var leaked atomic.Bool
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { leaked.Store(true) }))
	defer target.Close()
	client := fileTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	})
	_, err := client.Recognize(context.Background(), []byte{1, 2})
	if err == nil || leaked.Load() {
		t.Fatalf("redirect followed: leaked=%v err=%v", leaked.Load(), err)
	}
}

func TestFileASRCancelInFlight(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	client := fileTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		close(started)
		<-release
	})
	defer close(release)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := client.Recognize(ctx, []byte{1, 2}); done <- err }()
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("HTTP cancellation blocked")
	}
}

type fileTestTransport func(*http.Request) (*http.Response, error)

func (f fileTestTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestFileModePinnedUntilNextRecording(t *testing.T) {
	original := http.DefaultTransport
	defer func() { http.DefaultTransport = original }()
	var calls atomic.Int32
	http.DefaultTransport = fileTestTransport(func(r *http.Request) (*http.Response, error) {
		calls.Add(1)
		if r.URL.String() != fileASREndpoint || r.Header.Get("X-Api-Resource-Id") != fileASRResource {
			t.Error("active session switched back to WebSocket")
		}
		return &http.Response{
			StatusCode: 200, Header: http.Header{"X-Api-Status-Code": {"20000000"}},
			Body: io.NopCloser(strings.NewReader(`{"result":{"text":"whole file"}}`)),
		}, nil
	})
	work := newRecognitionTask()
	work.saveTraffic = true
	work.cache.append([]byte{1, 2})
	// Simulate changing the setting back to false while this session records.
	p := &VoicePlugin{
		cfg: config.Default(), work: work, recording: true,
		env: retryTestEnv{}, logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	p.publishStatusLocked()
	if status := TUIStatus(); status.State != "recording" || !strings.Contains(status.Detail, "停止后整段识别") {
		t.Fatalf("file mode misleadingly shows connecting: %+v", status)
	}
	done := make(chan struct{})
	go func() { p.runRecognition(1, work); close(done) }()
	defer func() { work.cancel(); <-done }()
	select {
	case <-work.result:
		t.Fatal("file mode ran before stopping")
	case <-time.After(20 * time.Millisecond):
	}
	if calls.Load() != 0 {
		t.Fatal("file mode dialed while recording")
	}
	p.mu.Lock()
	p.recording = false
	p.pendingDone = 1
	p.finishingSessions = map[uint64]*recognitionTask{1: work}
	p.mu.Unlock()
	work.cache.seal()
	select {
	case result := <-work.result:
		if result.err != nil || result.text != "whole file" || calls.Load() != 1 {
			t.Fatalf("result=%+v calls=%d", result, calls.Load())
		}
	case <-time.After(time.Second):
		t.Fatal("whole-file recognition did not complete")
	}
}

func TestFilePCMSizeLimits(t *testing.T) {
	for _, n := range []int{0, 1, maxFileWAVBytes - wavHeaderBytes + 2} {
		if validateFilePCMSize(n) == nil {
			t.Errorf("accepted invalid size %d", n)
		}
	}
	for _, n := range []int{2, maxFileWAVBytes - wavHeaderBytes} {
		if err := validateFilePCMSize(n); err != nil {
			t.Errorf("size %d: %v", n, err)
		}
	}
}

func TestCachedFileWaitsForStopAndReplaysOnlyOnR(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	cache := newAudioCache()
	cache.append([]byte{1, 2})
	retry := make(chan struct{})
	failed := make(chan error, 1)
	done := make(chan recognitionResult, 1)
	var calls atomic.Int32
	go func() {
		text, err := recognizeCachedFile(ctx, cache, func(ctx context.Context, pcm []byte) (string, error) {
			if !bytes.Equal(pcm, []byte{1, 2, 3, 4}) {
				return "", fmt.Errorf("incomplete cached recording: %v", pcm)
			}
			if calls.Add(1) == 1 {
				return "", errors.New("offline")
			}
			return "complete", nil
		}, func(attempt int, err error) <-chan struct{} { failed <- err; return retry })
		done <- recognitionResult{text, err}
	}()
	select {
	case err := <-failed:
		t.Fatalf("submitted before stop: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	if calls.Load() != 0 {
		t.Fatal("submitted during recording")
	}
	cache.append([]byte{3, 4})
	cache.seal()
	select {
	case err := <-failed:
		if err.Error() != "offline" {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if calls.Load() != 1 {
		t.Fatal("unexpected automatic replay")
	}
	close(retry)
	select {
	case result := <-done:
		if result.err != nil || result.text != "complete" || calls.Load() != 2 {
			t.Fatalf("result=%+v calls=%d", result, calls.Load())
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	pcm, err := cache.waitSealed(ctx)
	if err != nil || !bytes.Equal(pcm, []byte{1, 2, 3, 4}) {
		t.Fatal("retry consumed cache")
	}
}

func TestCachedFileCancelBeforeStopSendsNothing(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := recognizeCachedFile(ctx, newAudioCache(), func(context.Context, []byte) (string, error) {
		t.Fatal("canceled recording was uploaded")
		return "", nil
	}, func(int, error) <-chan struct{} { t.Fatal("cancel asked for retry"); return nil })
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v", err)
	}
}

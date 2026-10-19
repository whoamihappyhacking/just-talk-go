package voice

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
)

type localASR struct {
	*ASRClient
	endpoint string
}

func (c *localASR) Connect(ctx context.Context) error { return c.connect(ctx, c.endpoint) }

func TestNetworkRecoveryReplaysAudioCapturedDuringDisconnect(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	var connections atomic.Int32
	received := make(chan []byte, 1)
	serverErr := make(chan error, 2)
	final := testASRResponse(t, 3, "complete recording", false)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			serverErr <- err
			return
		}
		defer conn.CloseNow()
		attempt := connections.Add(1)
		if _, _, err = conn.Read(ctx); err != nil {
			serverErr <- err
			return
		}
		var pcm []byte
		for {
			_, frame, err := conn.Read(ctx)
			if err != nil {
				serverErr <- err
				return
			}
			if len(frame) < 8 {
				serverErr <- errors.New("short audio frame")
				return
			}
			pcm = append(pcm, frame[8:]...)
			if attempt == 1 {
				return
			} // Abrupt transport loss during live capture.
			if frame[1]&2 != 0 {
				received <- pcm
				if err := conn.Write(ctx, websocket.MessageBinary, final); err != nil {
					serverErr <- err
				}
				// Keep reading so a normal close handshake cannot hide the final result.
				_, _, _ = conn.Read(ctx)
				return
			}
		}
	}))
	defer server.Close()
	work := newRecognitionTask()
	defer work.cancel()
	reader, writer := io.Pipe()
	defer reader.Close()
	defer writer.Close()
	rec := NewRecorder(logger, 1)
	rec.stdout = reader
	go work.capture(rec)
	first := bytes.Repeat([]byte{1, 2}, 1600)
	tail := bytes.Repeat([]byte{3, 4}, 9000)
	factory := func() recognitionClient {
		return &localASR{NewASRClient(ASRConfig{}, logger), "ws" + strings.TrimPrefix(server.URL, "http")}
	}
	result := make(chan recognitionResult, 1)
	go func() {
		text, err := recoverRecognition(ctx, work.cache, factory, func() {}, func(_ int, _ error) {
			// The microphone continues producing audio while no ASR connection exists.
			if _, err := writer.Write(tail); err != nil {
				return
			}
			writer.Close()
			<-work.captureDone
			work.cache.seal()
		}, []time.Duration{0})
		result <- recognitionResult{text, err}
	}()
	if _, err := writer.Write(first); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-result:
		if got.err != nil || got.text != "complete recording" {
			t.Fatalf("result = %+v", got)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	select {
	case got := <-received:
		if !bytes.Equal(got, append(first, tail...)) {
			t.Fatal("reconnected stream lost or duplicated audio")
		}
	default:
		t.Fatal("server did not receive final audio")
	}
	if connections.Load() != 2 {
		t.Fatalf("connections = %d", connections.Load())
	}
	select {
	case err := <-serverErr:
		t.Fatal(err)
	default:
	}
}

func TestAutomaticRecoveryBoundedAndCancelable(t *testing.T) {
	cache := newAudioCache()
	cache.seal()
	attempts := 0
	factory := func() recognitionClient {
		attempts++
		c := newScriptedASR()
		c.connectErr = errors.New("offline")
		return c
	}
	_, err := recoverRecognition(context.Background(), cache, factory, func() {}, func(int, error) {}, []time.Duration{0, 0, 0})
	if err == nil || attempts != 4 {
		t.Fatalf("attempts=%d, err=%v", attempts, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	attempts = 0
	_, err = recoverRecognition(ctx, cache, factory, func() {}, func(int, error) { cancel() }, []time.Duration{time.Hour})
	if !errors.Is(err, context.Canceled) || attempts != 1 {
		t.Fatalf("cancel: attempts=%d err=%v", attempts, err)
	}
}

func TestAuthenticationFailureDoesNotAutomaticallyRetry(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests.Add(1); w.WriteHeader(http.StatusUnauthorized) }))
	defer server.Close()
	factory := func() recognitionClient {
		return &localASR{NewASRClient(ASRConfig{}, slog.New(slog.NewTextHandler(io.Discard, nil))), "ws" + strings.TrimPrefix(server.URL, "http")}
	}
	_, err := recoverRecognition(context.Background(), newAudioCache(), factory, func() {}, func(int, error) { t.Error("authentication retried") }, []time.Duration{0, 0, 0})
	if err == nil || requests.Load() != 1 {
		t.Fatalf("requests=%d err=%v", requests.Load(), err)
	}
}

func TestPrematureFinalRejectsIncompleteTranscript(t *testing.T) {
	cache := newAudioCache()
	cache.append([]byte{1, 2})
	cache.seal()
	client := newScriptedASR()
	close(client.final)
	text, err := recognizeAttempt(context.Background(), cache, client, func() {})
	if err == nil || text != "" {
		t.Fatalf("premature result = %q, %v", text, err)
	}
}

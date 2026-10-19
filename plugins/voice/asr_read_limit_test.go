package voice

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func TestASRResponseReadLimit(t *testing.T) {
	for _, tc := range []struct {
		name     string
		text     string
		tooLarge bool
	}{
		{name: "large transcript", text: strings.Repeat("语音识别。", 10000)},
		{name: "bounded message size", text: strings.Repeat("x", 4<<20), tooLarge: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			final := testASRResponse(t, 3, tc.text, false)
			if len(final) <= 32768 {
				t.Fatal("test response must exceed the default WebSocket read limit")
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				conn, err := websocket.Accept(w, r, nil)
				if err != nil {
					return
				}
				defer conn.CloseNow()
				if _, _, err := conn.Read(ctx); err != nil {
					return
				}
				if err := conn.Write(ctx, websocket.MessageBinary, final); err != nil {
					return
				}
				// Let the client finish parsing before closing the connection.
				_, _, _ = conn.Read(ctx)
			}))
			defer server.Close()

			client := NewASRClient(ASRConfig{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
			if err := client.connect(ctx, "ws"+strings.TrimPrefix(server.URL, "http")); err != nil {
				t.Fatal(err)
			}
			go client.ReceiveLoop(ctx)
			defer func() {
				cancel()
				client.conn.CloseNow()
				<-client.Done()
			}()

			if tc.tooLarge {
				select {
				case <-client.Done():
					if err := client.Err(); err == nil || !strings.Contains(err.Error(), "message too big") {
						t.Fatalf("error = %v, want message-too-big close", client.Err())
					}
				case <-ctx.Done():
					t.Fatal("oversized response was not rejected")
				}
				select {
				case <-client.Final():
					t.Fatal("oversized response signaled a final result")
				default:
				}
				return
			}

			select {
			case <-client.Final():
			case <-client.Done():
				t.Fatalf("connection closed before final result: %v", client.Err())
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			if client.LastText() != tc.text {
				t.Fatal("large transcript was lost or truncated")
			}
			select {
			case result := <-client.Results():
				if result.Text != tc.text || !result.IsFinal || result.Error != nil {
					t.Fatal("incorrect final result")
				}
			default:
				t.Fatal("missing final result")
			}
		})
	}
}

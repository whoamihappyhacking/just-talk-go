package voice

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"
)

func TestParseResponseClosesFinalForEmptyText(t *testing.T) {
	client := NewASRClient(ASRConfig{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	client.parseResponse(testASRResponse(t, 0x03, "", false))

	select {
	case <-client.Final():
	default:
		t.Fatal("empty final response did not close Final")
	}
}

func TestParseResponseDoesNotCloseFinalForDefiniteUtterance(t *testing.T) {
	client := NewASRClient(ASRConfig{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	client.parseResponse(testASRResponse(t, 0x01, "partial", true))

	select {
	case <-client.Final():
		t.Fatal("definite utterance closed Final before the last packet")
	default:
	}

	select {
	case result := <-client.Results():
		if !result.IsFinal {
			t.Fatal("definite utterance was not marked final in result stream")
		}
	default:
		t.Fatal("definite utterance result was not published")
	}
}

func TestSendAudioHonorsContextWhileWaitingForWriter(t *testing.T) {
	client := NewASRClient(ASRConfig{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	<-client.writeSlot

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	err := client.SendAudio(ctx, nil, true)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("SendAudio error = %v, want context deadline exceeded", err)
	}
}

func testASRResponse(t *testing.T, flags byte, text string, definite bool) []byte {
	t.Helper()
	payload, err := json.Marshal(map[string]any{
		"result": map[string]any{
			"text": text,
			"utterances": []map[string]any{
				{"text": text, "definite": definite},
			},
		},
	})
	if err != nil {
		t.Fatalf("marshal response: %v", err)
	}
	response := make([]byte, 12+len(payload))
	response[0] = hdrVersion | hdrHeaderSize
	response[1] = 0x90 | flags
	binary.BigEndian.PutUint32(response[4:8], ^uint32(0))
	binary.BigEndian.PutUint32(response[8:12], uint32(len(payload)))
	copy(response[12:], payload)
	return response
}

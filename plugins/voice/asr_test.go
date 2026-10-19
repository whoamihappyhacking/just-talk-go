package voice

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"strings"
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

// A server error never carries the last-packet flag. Without surfacing it the
// session waits out the 30s final-result timeout and reports the wrong cause.
func TestServerErrorResponseEndsTheSessionWithItsCode(t *testing.T) {
	client := NewASRClient(ASRConfig{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	message := "quota exceeded"
	response := make([]byte, 12+len(message))
	response[0] = hdrVersion | hdrHeaderSize
	response[1] = msgServerError << 4
	binary.BigEndian.PutUint32(response[4:8], 45000001)
	binary.BigEndian.PutUint32(response[8:12], uint32(len(message)))
	copy(response[12:], message)

	err := client.parseResponse(response)
	if err == nil || !strings.Contains(err.Error(), "45000001") || !strings.Contains(err.Error(), message) {
		t.Fatalf("parseResponse error = %v; want the server code and message", err)
	}
	var permanent *permanentASRError
	if !errors.As(err, &permanent) {
		t.Fatal("a rejected request must skip the reconnect ladder")
	}
	select {
	case <-client.Final():
		t.Fatal("server error closed Final, which would report an empty transcript instead")
	default:
	}
}

func TestSlowResultConsumerDoesNotBlockFinal(t *testing.T) {
	client := NewASRClient(ASRConfig{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	for i := 0; i < cap(client.resultCh); i++ {
		client.resultCh <- ASRResult{Text: "old"}
	}
	final := testASRResponse(t, 3, "complete", false)
	finished := make(chan struct{})
	go func() { client.parseResponse(final); close(finished) }()
	select {
	case <-finished:
	case <-time.After(time.Second):
		<-client.resultCh // release a blocked producer before failing
		<-finished
		t.Fatal("UI backpressure blocked final processing")
	}
	select {
	case <-client.Final():
	default:
		t.Fatal("missing final signal")
	}
	if client.LastText() != "complete" {
		t.Fatal("final text lost")
	}
}

// A weak link must keep retrying; a rejected request must not.
func TestPermanentASRCodeSeparatesRejectionsFromTransientFaults(t *testing.T) {
	for _, tc := range []struct {
		code uint32
		want bool
	}{
		{45000001, true},  // 请求参数无效
		{45000002, true},  // 空音频
		{45000151, true},  // 音频格式不正确
		{45000081, false}, // 等包超时：上传卡住，重连可能成功
		{55000031, false}, // 服务器繁忙
		{55000000, false}, // 服务内部错误
		{20000003, false}, // 静音，由文本为空的路径处理
	} {
		if got := permanentASRCode(tc.code); got != tc.want {
			t.Errorf("permanentASRCode(%d) = %v, want %v", tc.code, got, tc.want)
		}
	}
}

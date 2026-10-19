package voice

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"

	"github.com/coder/websocket"
	"github.com/google/uuid"
)

// Configuration/authentication errors need user action, not automatic redial.
type permanentASRError struct{ error }

func (e *permanentASRError) Unwrap() error { return e.error }

type ASRConfig struct {
	AppKey     string
	AccessKey  string
	ResourceID string
	Language   string
	Hotwords   []string
}

type ASRClient struct {
	cfg       ASRConfig
	logger    *slog.Logger
	conn      *websocket.Conn
	writeSlot chan struct{}
	resultCh  chan ASRResult
	done      chan struct{}
	final     chan struct{}
	finalOnce sync.Once
	textMu    sync.RWMutex
	lastText  string
	lastErr   error
}

type ASRResult struct {
	Text    string
	IsFinal bool
	Error   error
}

// Full transcripts include utterance metadata and can exceed the WebSocket
// library's 32 KiB default. Keep a finite cap to bound per-message memory use.
const maxASRResponseBytes = 8 << 20 // 8 MiB, including the protocol header.

const (
	hdrVersion       = 0x10
	hdrHeaderSize    = 0x01
	msgFullClientReq = 0x10
	msgAudioOnly     = 0x20
	msgServerFull    = 0x09
	msgServerError   = 0x0F
	hdrJSON          = 0x10
	hdrRawAudio      = 0x00
)

func NewASRClient(cfg ASRConfig, logger *slog.Logger) *ASRClient {
	writeSlot := make(chan struct{}, 1)
	writeSlot <- struct{}{}
	return &ASRClient{
		cfg: cfg, logger: logger,
		writeSlot: writeSlot,
		resultCh:  make(chan ASRResult, 64),
		lastText:  "",
		done:      make(chan struct{}),
		final:     make(chan struct{}),
	}
}

func (c *ASRClient) Connect(ctx context.Context) error {
	return c.connect(ctx, "wss://openspeech.bytedance.com/api/v3/sauc/bigmodel_async")
}

func (c *ASRClient) connect(ctx context.Context, url string) error {
	c.logger.Info("connecting to ASR", "url", url)
	conn, resp, err := websocket.Dial(ctx, url, &websocket.DialOptions{
		HTTPHeader: map[string][]string{
			"X-Api-App-Key":     {c.cfg.AppKey},
			"X-Api-Access-Key":  {c.cfg.AccessKey},
			"X-Api-Resource-Id": {c.cfg.ResourceID},
			"X-Api-Request-Id":  {uuid.New().String()},
			"X-Api-Connect-Id":  {uuid.New().String()},
			"X-Api-Sequence":    {"-1"},
		},
	})
	if err != nil {
		if resp != nil {
			dialErr := fmt.Errorf("websocket dial: HTTP %d %s: %w", resp.StatusCode, resp.Status, err)
			if resp.StatusCode >= 400 && resp.StatusCode < 500 && resp.StatusCode != 408 && resp.StatusCode != 429 {
				return &permanentASRError{dialErr}
			}
			return dialErr
		}
		return fmt.Errorf("websocket dial: %w", err)
	}
	conn.SetReadLimit(maxASRResponseBytes)
	c.conn = conn
	if err := c.sendFullClientRequest(ctx); err != nil {
		conn.Close(websocket.StatusInternalError, "init failed")
		return fmt.Errorf("send init: %w", err)
	}
	c.logger.Info("ASR connected")
	return nil
}

func (c *ASRClient) SendAudio(ctx context.Context, pcm []byte, isLast bool) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-c.writeSlot:
	}
	defer func() { c.writeSlot <- struct{}{} }()
	if c.conn == nil {
		return fmt.Errorf("ASR connection is not open")
	}
	flags := byte(0x00)
	if isLast {
		c.logger.Debug("ASR: sending final audio", "bytes", len(pcm))
		flags = 0x02
	}
	header := []byte{hdrVersion | hdrHeaderSize, msgAudioOnly | flags, hdrRawAudio, 0x00}
	size := make([]byte, 4)
	binary.BigEndian.PutUint32(size, uint32(len(pcm)))
	return c.conn.Write(ctx, websocket.MessageBinary, append(append(header, size...), pcm...))
}

func (c *ASRClient) Results() <-chan ASRResult { return c.resultCh }
func (c *ASRClient) Done() <-chan struct{}     { return c.done }
func (c *ASRClient) Final() <-chan struct{}    { return c.final }

func (c *ASRClient) LastText() string {
	c.textMu.RLock()
	defer c.textMu.RUnlock()
	return c.lastText
}

// Err preserves the transport failure after Done is closed.
func (c *ASRClient) Err() error {
	c.textMu.RLock()
	defer c.textMu.RUnlock()
	return c.lastErr
}

func (c *ASRClient) ReceiveLoop(ctx context.Context) {
	defer close(c.resultCh)
	defer close(c.done)
	for {
		_, data, err := c.conn.Read(ctx)
		if err != nil {
			c.setErr(err)
			return
		}
		// A server error response never reaches the last-packet flag, so
		// continuing here would wait out the final-result timeout instead.
		if err := c.parseResponse(data); err != nil {
			c.setErr(err)
			return
		}
	}
}

func (c *ASRClient) setErr(err error) {
	c.textMu.Lock()
	defer c.textMu.Unlock()
	if c.lastErr == nil {
		c.lastErr = err
	}
}

func (c *ASRClient) Close() error {
	c.logger.Debug("ASR: closing client")
	if c.conn != nil {
		return c.conn.Close(websocket.StatusNormalClosure, "done")
	}
	return nil
}

func (c *ASRClient) sendFullClientRequest(ctx context.Context) error {
	request := map[string]interface{}{
		"model_name": "bigmodel", "enable_itn": true, "enable_punc": true,
		"enable_ddc": false, "enable_word": false,
		"enable_nonstream": true, "result_type": "full", "show_utterances": true,
	}
	if len(c.cfg.Hotwords) > 0 {
		if contextJSON, err := hotwordsContext(c.cfg.Hotwords); err == nil && contextJSON != "" {
			request["corpus"] = map[string]interface{}{"context": contextJSON}
		}
	}
	payload := map[string]interface{}{
		"user":    map[string]string{"uid": "just-talk"},
		"audio":   map[string]interface{}{"format": "pcm", "rate": 16000, "bits": 16, "channel": 1, "codec": "raw"},
		"request": request,
	}
	jsonBytes, _ := json.Marshal(payload)
	header := []byte{hdrVersion | hdrHeaderSize, msgFullClientReq, hdrJSON, 0x00}
	size := make([]byte, 4)
	binary.BigEndian.PutUint32(size, uint32(len(jsonBytes)))
	return c.conn.Write(ctx, websocket.MessageBinary, append(append(header, size...), jsonBytes...))
}

func hotwordsContext(words []string) (string, error) {
	hotwords := make([]map[string]string, 0, len(words))
	seen := make(map[string]struct{}, len(words))
	for _, word := range words {
		word = strings.TrimSpace(word)
		if word == "" {
			continue
		}
		if _, ok := seen[word]; ok {
			continue
		}
		seen[word] = struct{}{}
		hotwords = append(hotwords, map[string]string{"word": word})
	}
	if len(hotwords) == 0 {
		return "", nil
	}
	b, err := json.Marshal(map[string]interface{}{"hotwords": hotwords})
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func (c *ASRClient) parseResponse(data []byte) error {
	if len(data) < 12 {
		return nil
	}
	flags := data[1] & 0x0F
	msgType := (data[1] >> 4) & 0x0F
	if msgType == msgServerError {
		return serverErrorFrom(data)
	}
	if msgType != msgServerFull {
		return nil
	}
	payloadSize := binary.BigEndian.Uint32(data[8:12])
	if int(payloadSize)+12 > len(data) {
		return nil
	}
	payload := data[12 : 12+payloadSize]
	var resp struct {
		Result struct {
			Text       string `json:"text"`
			Utterances []struct {
				Text     string `json:"text"`
				Definite bool   `json:"definite"`
			} `json:"utterances"`
		} `json:"result"`
	}
	if json.Unmarshal(payload, &resp) != nil {
		return nil
	}
	text := resp.Result.Text
	isLastPacket := flags == 0x02 || flags == 0x03
	isFinalResult := isLastPacket
	for _, u := range resp.Result.Utterances {
		if u.Definite {
			isFinalResult = true
		}
	}
	if text != "" {
		c.textMu.Lock()
		c.lastText = text
		c.textMu.Unlock()
		// UI/log consumers must not delay reading the network or signaling
		// Final. The authoritative transcript is retained in lastText.
		select {
		case c.resultCh <- ASRResult{Text: text, IsFinal: isFinalResult}:
		default:
		}
	}
	if isLastPacket {
		c.logger.Debug("ASR: final response received")
		c.finalOnce.Do(func() { close(c.final) })
	}
	return nil
}

// Error responses carry the code in place of the sequence number, so the
// payload sits at the same offset as a full server response.
func serverErrorFrom(data []byte) error {
	code := binary.BigEndian.Uint32(data[4:8])
	message := ""
	if size := binary.BigEndian.Uint32(data[8:12]); int(size)+12 <= len(data) {
		message = strings.TrimSpace(string(data[12 : 12+size]))
	}
	err := fmt.Errorf("ASR 服务返回错误 code=%d", code)
	if message != "" {
		err = fmt.Errorf("ASR 服务返回错误 code=%d: %s", code, message)
	}
	if permanentASRCode(code) {
		return &permanentASRError{err}
	}
	return err
}

// permanentASRCode reports whether redialing can ever help. Replaying a whole
// recording is the most expensive thing this client does on a weak link, so a
// request the server already rejected must not trigger the backoff ladder.
func permanentASRCode(code uint32) bool {
	switch code {
	case 45000081: // 等包超时：the upload stalled, so a fresh attempt can work.
		return false
	}
	// 45xxxxxx rejects the request itself (bad parameters, empty or malformed
	// audio); 55xxxxxx is a server-side fault that typically clears.
	return code >= 45000000 && code < 46000000
}

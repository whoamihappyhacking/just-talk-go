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
	connMu    sync.Mutex
	resultCh  chan ASRResult
	done      chan struct{}
	final     chan struct{}
	finalOnce sync.Once
	textMu    sync.RWMutex
	lastText  string
}

type ASRResult struct {
	Text    string
	IsFinal bool
	Error   error
}

const (
	hdrVersion       = 0x10
	hdrHeaderSize    = 0x01
	msgFullClientReq = 0x10
	msgAudioOnly     = 0x20
	hdrJSON          = 0x10
	hdrRawAudio      = 0x00
)

func NewASRClient(cfg ASRConfig, logger *slog.Logger) *ASRClient {
	return &ASRClient{
		cfg: cfg, logger: logger,
		resultCh: make(chan ASRResult, 64),
		lastText: "",
		done:     make(chan struct{}),
		final:    make(chan struct{}),
	}
}

func (c *ASRClient) Connect(ctx context.Context) error {
	url := "wss://openspeech.bytedance.com/api/v3/sauc/bigmodel_async"
	c.logger.Info("connecting to ASR", "url", url)
	conn, _, err := websocket.Dial(ctx, url, &websocket.DialOptions{
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
		return fmt.Errorf("websocket dial: %w", err)
	}
	c.conn = conn
	if err := c.sendFullClientRequest(ctx); err != nil {
		conn.Close(websocket.StatusInternalError, "init failed")
		return fmt.Errorf("send init: %w", err)
	}
	c.logger.Info("ASR connected")
	return nil
}

func (c *ASRClient) SendAudio(ctx context.Context, pcm []byte, isLast bool) error {
	c.connMu.Lock()
	defer c.connMu.Unlock()
	flags := byte(0x00)
	if isLast {
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

func (c *ASRClient) ReceiveLoop(ctx context.Context) {
	defer close(c.resultCh)
	defer close(c.done)
	for {
		_, data, err := c.conn.Read(ctx)
		if err != nil {
			return
		}
		c.parseResponse(data)
	}
}

func (c *ASRClient) Close() error {
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

func (c *ASRClient) parseResponse(data []byte) {
	if len(data) < 12 {
		return
	}
	flags := data[1] & 0x0F
	msgType := (data[1] >> 4) & 0x0F
	if msgType != 0x09 {
		return
	}
	payloadSize := binary.BigEndian.Uint32(data[8:12])
	if int(payloadSize)+12 > len(data) {
		return
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
		return
	}
	text := resp.Result.Text
	isFinal := flags == 0x02 || flags == 0x03
	for _, u := range resp.Result.Utterances {
		if u.Definite {
			isFinal = true
		}
	}
	if text != "" {
		c.textMu.Lock()
		c.lastText = text
		c.textMu.Unlock()
		c.resultCh <- ASRResult{Text: text, IsFinal: isFinal}
		if isFinal {
			c.finalOnce.Do(func() { close(c.final) })
		}
	}
}

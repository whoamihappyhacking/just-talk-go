package voice

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	fileASREndpoint = "https://openspeech.bytedance.com/api/v3/auc/bigmodel/recognize/flash"
	fileASRResource = "volc.bigasr.auc_turbo"
	fileASRTimeout  = 5 * time.Minute
	maxFileWAVBytes = 100_000_000 // Official limit: 100 MB and 2 hours.
	wavHeaderBytes  = 44
)

// FileASRClient uses the synchronous whole-file HTTP API, not the streaming
// WebSocket protocol. Contract: https://www.volcengine.com/docs/6561/1631584
// The application must separately enable the auc_turbo resource.
type FileASRClient struct {
	cfg      ASRConfig
	logger   *slog.Logger
	endpoint string
	http     *http.Client
}

func NewFileASRClient(cfg ASRConfig, logger *slog.Logger) *FileASRClient {
	return &FileASRClient{
		cfg: cfg, logger: logger, endpoint: fileASREndpoint,
		http: &http.Client{
			Timeout: fileASRTimeout,
			// Never forward credentials or recording data to a redirect target.
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
	}
}

func validateFilePCMSize(n int) error {
	switch {
	case n == 0:
		return fmt.Errorf("没有可提交的音频，请检查麦克风")
	case n%2 != 0:
		return fmt.Errorf("音频数据不完整：16 位 PCM 字节数必须为偶数")
	case n > maxFileWAVBytes-wavHeaderBytes:
		return fmt.Errorf("整段音频超过文件识别 100 MB 上限，请缩短录音")
	case int64(n) > 2*60*60*32000:
		return fmt.Errorf("整段音频超过文件识别 2 小时上限，请缩短录音")
	}
	return nil
}

// pcmWAV wraps the native recorder's 16 kHz, mono, PCM16 little-endian audio.
// Flash accepts WAV (not bare PCM); no external converter is required.
func pcmWAV(pcm []byte) ([]byte, error) {
	if err := validateFilePCMSize(len(pcm)); err != nil {
		return nil, err
	}
	wav := make([]byte, wavHeaderBytes+len(pcm))
	copy(wav, "RIFF")
	binary.LittleEndian.PutUint32(wav[4:8], uint32(len(wav)-8))
	copy(wav[8:16], "WAVEfmt ")
	binary.LittleEndian.PutUint32(wav[16:20], 16)
	binary.LittleEndian.PutUint16(wav[20:22], 1) // PCM
	binary.LittleEndian.PutUint16(wav[22:24], 1) // mono
	binary.LittleEndian.PutUint32(wav[24:28], 16000)
	binary.LittleEndian.PutUint32(wav[28:32], 32000)
	binary.LittleEndian.PutUint16(wav[32:34], 2)
	binary.LittleEndian.PutUint16(wav[34:36], 16)
	copy(wav[36:40], "data")
	binary.LittleEndian.PutUint32(wav[40:44], uint32(len(pcm)))
	copy(wav[44:], pcm)
	return wav, nil
}

func (c *FileASRClient) Recognize(ctx context.Context, pcm []byte) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, fileASRTimeout)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return "", err
	}
	wav, err := pcmWAV(pcm)
	if err != nil {
		return "", err
	}
	request := map[string]any{
		"model_name": "bigmodel", "enable_itn": true, "enable_punc": true,
		"enable_ddc": false, "show_utterances": false,
	}
	contextJSON, err := hotwordsContext(c.cfg.Hotwords)
	if err != nil {
		return "", fmt.Errorf("编码热词失败: %w", err)
	}
	if contextJSON != "" {
		request["corpus"] = map[string]any{"context": contextJSON}
	}
	// encoding/json encodes []byte as base64. No public file URL or separate
	// upload/polling service is involved.
	audio := map[string]any{"data": wav}
	if c.cfg.Language != "" {
		audio["language"] = c.cfg.Language
	}
	body, err := json.Marshal(map[string]any{
		"user":  map[string]string{"uid": c.cfg.AppKey},
		"audio": audio, "request": request,
	})
	if err != nil {
		return "", fmt.Errorf("编码整段音频请求失败: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Api-App-Key", c.cfg.AppKey)
	req.Header.Set("X-Api-Access-Key", c.cfg.AccessKey)
	req.Header.Set("X-Api-Resource-Id", fileASRResource)
	requestID := uuid.NewString()
	req.Header.Set("X-Api-Request-Id", requestID)
	req.Header.Set("X-Api-Sequence", "-1")
	c.logger.Debug("file ASR: submitting whole recording", "pcm_bytes", len(pcm), "request_id", requestID)
	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("整段音频 HTTP 请求失败: %w", err)
	}
	defer resp.Body.Close()
	code := resp.Header.Get("X-Api-Status-Code")
	logID := resp.Header.Get("X-Tt-Logid")
	c.logger.Debug("file ASR: response", "http_status", resp.StatusCode, "api_code", code, "log_id", logID, "request_id", requestID)
	if resp.StatusCode != http.StatusOK || code != "20000000" {
		message := strings.TrimSpace(resp.Header.Get("X-Api-Message"))
		if code == "20000003" {
			message = "服务端判定为静音，录音保留待重试"
		}
		if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
			message = "请检查 App Key / Access Key，并开通 " + fileASRResource + " 权限；" + message
		}
		return "", fmt.Errorf("文件识别失败：HTTP %d，code=%s，%s（logid=%s）", resp.StatusCode, code, message, logID)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxASRResponseBytes+1))
	if err != nil {
		return "", fmt.Errorf("读取文件识别结果失败: %w", err)
	}
	if len(data) > maxASRResponseBytes {
		return "", fmt.Errorf("文件识别响应超过 8 MiB 上限（logid=%s）", logID)
	}
	var result struct {
		Result struct {
			Text string `json:"text"`
		} `json:"result"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return "", fmt.Errorf("文件识别响应格式错误（logid=%s）: %w", logID, err)
	}
	if strings.TrimSpace(result.Result.Text) == "" {
		return "", emptyTranscriptError(peakAmplitude(pcm), fmt.Sprintf("（logid=%s）", logID))
	}
	return result.Result.Text, nil
}

// A single HTTP submission per user attempt. Retrying an uncertain HTTP result
// may be billed again, so this mode never automatically resubmits or falls back
// to WebSocket. Only an explicit R press permits another request.
func recognizeCachedFile(ctx context.Context, cache *audioCache,
	recognize func(context.Context, []byte) (string, error),
	failed func(int, error) <-chan struct{},
) (string, error) {
	pcm, err := cache.waitSealed(ctx)
	if err != nil {
		return "", err
	}
	var text string
	err = retryUntilSuccess(ctx, func() error {
		var err error
		text, err = recognize(ctx, pcm)
		return err
	}, failed)
	return text, err
}

func (p *VoicePlugin) runFileRecognition(sessionID uint64, work *recognitionTask) {
	text, err := recognizeCachedFile(work.ctx, work.cache, func(ctx context.Context, pcm []byte) (string, error) {
		p.mu.Lock()
		vc := p.cfg.Voice // Allow corrected credentials/hotwords on R, not a mode switch.
		work.retryDetail = ""
		work.recoveryDetail = "整段音频正在提交并识别（最长等待 5 分钟）"
		p.publishStatusLocked()
		p.mu.Unlock()
		client := NewFileASRClient(ASRConfig{
			AppKey: vc.AppKey, AccessKey: vc.AccessKey, Language: vc.Language, Hotwords: vc.Hotwords,
		}, p.logger)
		return client.Recognize(ctx, pcm)
	}, func(attempt int, err error) <-chan struct{} {
		return p.setRetryStatus(sessionID, work, attempt, err)
	})
	work.result <- recognitionResult{text: text, err: err}
}

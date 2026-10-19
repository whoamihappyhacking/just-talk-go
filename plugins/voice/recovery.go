package voice

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"
)

// audioCache has one capture writer and independent readers for each ASR attempt.
// Sending never removes audio: a failed connection can replay from byte zero.
type audioCache struct {
	mu      sync.Mutex
	pcm     []byte
	sealed  bool
	changed chan struct{}
}

func newAudioCache() *audioCache { return &audioCache{changed: make(chan struct{})} }

func (c *audioCache) append(pcm []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.pcm = append(c.pcm, pcm...)
	close(c.changed)
	c.changed = make(chan struct{})
}

func (c *audioCache) seal() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sealed = true
	close(c.changed)
	c.changed = make(chan struct{})
}

func (c *audioCache) read(ctx context.Context, done <-chan struct{}, offset int, dst []byte) (int, error) {
	for {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		select {
		case <-done:
			return 0, fmt.Errorf("ASR connection closed before final audio")
		default:
		}
		c.mu.Lock()
		n := copy(dst, c.pcm[offset:])
		sealed, changed := c.sealed, c.changed
		c.mu.Unlock()
		if n > 0 {
			return n, nil
		}
		if sealed {
			return 0, io.EOF
		}
		select {
		case <-ctx.Done():
			return 0, ctx.Err()
		case <-done:
			return 0, fmt.Errorf("ASR connection closed before final audio")
		case <-changed:
		}
	}
}

// waitSealed does not initiate network I/O while recording. Once sealed, the
// capture writer has finished and pcm is immutable for all retry attempts.
func (c *audioCache) waitSealed(ctx context.Context) ([]byte, error) {
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		c.mu.Lock()
		pcm, sealed, changed := c.pcm, c.sealed, c.changed
		c.mu.Unlock()
		if sealed {
			return pcm, nil
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-changed:
		}
	}
}

func (c *audioCache) duration() time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	return time.Duration(len(c.pcm)) * time.Second / 32000
}

// Capture that never rises above this 16-bit peak carries no speech. A muted
// input device is the usual cause, and "ASR 未返回有效文字" hides it.
const silentPeakThreshold = 200

func peakAmplitude(pcm []byte) int {
	peak := 0
	for i := 0; i+1 < len(pcm); i += 2 {
		s := int(int16(uint16(pcm[i]) | uint16(pcm[i+1])<<8))
		if s < 0 {
			s = -s
		}
		if s > peak {
			peak = s
		}
	}
	return peak
}

func (c *audioCache) peak() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return peakAmplitude(c.pcm)
}

// emptyTranscriptError names the microphone when the recording itself was
// silent, so a muted input device is not reported as an ASR fault.
func emptyTranscriptError(peak int, suffix string) error {
	if peak <= silentPeakThreshold {
		return fmt.Errorf("录音几乎没有声音（峰值 %d/32768），请检查麦克风是否静音或选错输入设备%s", peak, suffix)
	}
	return fmt.Errorf("ASR 未返回有效文字，录音保留待重试%s", suffix)
}

type recognitionClient interface {
	Connect(context.Context) error
	SendAudio(context.Context, []byte, bool) error
	ReceiveLoop(context.Context)
	Results() <-chan ASRResult
	Final() <-chan struct{}
	Done() <-chan struct{}
	LastText() string
	Err() error
	Close() error
}

type recognitionResult struct {
	text string
	err  error
}

type recognitionTask struct {
	// Chosen when recording starts; config reloads only affect future sessions.
	saveTraffic bool
	ctx         context.Context
	cancel      context.CancelFunc
	cache       *audioCache
	captureDone chan struct{}
	result      chan recognitionResult
	// UI fields below are protected by VoicePlugin.mu.
	connected      bool
	recoveryDetail string
	retryDetail    string
	retryRequest   chan struct{}
}

func newRecognitionTask() *recognitionTask {
	ctx, cancel := context.WithCancel(context.Background())
	return &recognitionTask{
		ctx: ctx, cancel: cancel, cache: newAudioCache(),
		captureDone: make(chan struct{}), result: make(chan recognitionResult, 1),
	}
}

// capture runs independently of network I/O, including while dialing or waiting for R.
func (w *recognitionTask) capture(rec *Recorder) {
	defer close(w.captureDone)
	buf := make([]byte, 6400)
	for {
		n, err := rec.Read(buf)
		if n > 0 {
			w.cache.append(buf[:n])
		}
		if err != nil {
			if err != io.EOF {
				rec.logger.Error("read audio error", "error", err)
			}
			return
		}
	}
}

// retryRecognition keeps the same cache across all attempts. The factory also
// allows reloaded credentials to take effect on the next connection.
func retryRecognition(ctx context.Context, cache *audioCache, factory func() recognitionClient,
	connected func(), failed func(int, error) <-chan struct{},
) (string, error) {
	var text string
	err := retryUntilSuccess(ctx, func() error {
		var err error
		text, err = recognizeAttempt(ctx, cache, factory(), connected)
		return err
	}, failed)
	return text, err
}

// recoverRecognition bounds automatic recovery; exhausted or permanent failures
// fall back to the existing R gate. Each connection replays the entire cache.
func recoverRecognition(ctx context.Context, cache *audioCache, factory func() recognitionClient,
	connected func(), recovering func(int, error), delays []time.Duration,
) (string, error) {
	for attempt := 0; ; attempt++ {
		text, err := recognizeAttempt(ctx, cache, factory(), connected)
		if err == nil || ctx.Err() != nil {
			return text, err
		}
		var permanent *permanentASRError
		if attempt >= len(delays) || errors.As(err, &permanent) {
			return "", err
		}
		recovering(attempt+1, err)
		timer := time.NewTimer(delays[attempt])
		select {
		case <-ctx.Done():
			timer.Stop()
			return "", ctx.Err()
		case <-timer.C:
		}
	}
}

func retryUntilSuccess(ctx context.Context, action func() error,
	failed func(int, error) <-chan struct{},
) error {
	for attempt := 1; ; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		err := action()
		if err == nil {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		request := failed(attempt, err)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-request:
			// Exactly one explicit R press permits the next attempt.
		}
	}
}

func recognizeAttempt(ctx context.Context, cache *audioCache, client recognitionClient, connected func()) (string, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer func() {
		cancel()
		_ = client.Close()
	}()
	dialCtx, dialCancel := context.WithTimeout(ctx, 15*time.Second)
	err := client.Connect(dialCtx)
	dialCancel()
	if err != nil {
		return "", fmt.Errorf("ASR 连接失败: %w", err)
	}
	connected()
	go client.ReceiveLoop(ctx)
	go func() {
		for result := range client.Results() {
			if result.Error != nil {
				pout("❌ ASR 错误: %v", result.Error)
			} else if result.Text != "" {
				pout("🎤 %s", result.Text)
			}
		}
	}()

	buf := make([]byte, 6400)
	for offset := 0; ; {
		n, readErr := cache.read(ctx, client.Done(), offset, buf)
		if readErr != nil && readErr != io.EOF {
			if client.Err() != nil {
				return "", fmt.Errorf("ASR 连接中断: %w", client.Err())
			}
			return "", readErr
		}
		// An early server final cannot acknowledge audio still in the cache.
		// Replay from the beginning on a new connection instead of outputting
		// a transcript that may omit the rest of the recording.
		select {
		case <-client.Final():
			return "", fmt.Errorf("ASR 提前结束，完整录音尚未发送")
		default:
		}
		final := readErr == io.EOF
		sendCtx, sendCancel := context.WithTimeout(ctx, audioSendTimeout)
		err := client.SendAudio(sendCtx, buf[:n], final)
		sendCancel()
		if err != nil {
			return "", fmt.Errorf("音频发送失败: %w", err)
		}
		offset += n
		if final {
			break
		}
	}

	finalCtx, finalCancel := context.WithTimeout(ctx, 30*time.Second)
	defer finalCancel()
	if err := waitASRFinal(finalCtx, client); err != nil {
		return "", err
	}
	text := client.LastText()
	if strings.TrimSpace(text) == "" {
		return "", &permanentASRError{emptyTranscriptError(cache.peak(), "")}
	}
	return text, nil
}

func waitASRFinal(ctx context.Context, client recognitionClient) error {
	select {
	case <-client.Final():
		return ctx.Err()
	case <-client.Done():
		// Final and Done may become ready together. A closed transport alone
		// must never turn a partial transcript into successful output.
		select {
		case <-client.Final():
			return ctx.Err()
		default:
			if err := client.Err(); err != nil {
				return fmt.Errorf("ASR 在最终结果前断开连接: %w", err)
			}
			return fmt.Errorf("ASR 在最终结果前断开连接")
		}
	case <-ctx.Done():
		return fmt.Errorf("等待 ASR 最终结果失败: %w", ctx.Err())
	}
}

package voice

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/c/just-talk-go/config"
	"github.com/c/just-talk-go/engine"
	"github.com/c/just-talk-go/hotkey"
	"github.com/c/just-talk-go/internal/autotype"
	"github.com/c/just-talk-go/internal/clipboard"
)

const (
	defaultStopDelayMs = 800
	errorHoldDuration  = 10 * time.Second
	audioSendTimeout   = 15 * time.Second
)

var (
	cancelRecordingCombo = hotkey.Combo{Mods: hotkey.ModNone, Key: hotkey.KeyEscape}
	retryErrorCombo      = hotkey.Combo{Mods: hotkey.ModNone, Key: hotkey.KeyR}
)

var TUILog func(string)
var TUILogBuf []string
var tuilogMu sync.Mutex
var outputMu sync.Mutex
var outputWriter io.Writer = os.Stdout
var tuiStatus = TUIVoiceStatus{State: "idle", UpdatedAt: time.Now()}
var tuiStatusMu sync.Mutex
var tuiStats TUIVoiceStats
var tuiStatsMu sync.Mutex

type TUIVoiceStatus struct {
	State           string
	Detail          string
	Recording       bool
	Stopping        bool
	StopAt          time.Time
	ErrorUntil      time.Time
	UpdatedAt       time.Time
	SessionID       uint64
	PendingFinishes int
	LastHotkeyAt    time.Time
	LastHotkeyType  string
	LastHandledAt   time.Time
	LastHandledType string
	QueuedHotkeys   uint64
	HandledHotkeys  uint64
	EventQueueLen   int
}

type TUIVoiceStats struct {
	Sessions          uint64
	Chars             uint64
	AudioDuration     time.Duration
	LastTextChars     int
	LastAudioDuration time.Duration
}

func pout(format string, args ...interface{}) {
	msg := strings.ReplaceAll(fmt.Sprintf(format, args...), "\n", " ")
	if TUILog != nil {
		TUILog(msg)
		return
	}
	outputMu.Lock()
	defer outputMu.Unlock()
	if outputWriter != nil {
		fmt.Fprint(outputWriter, msg)
	}
}

func SetOutput(w io.Writer) {
	outputMu.Lock()
	outputWriter = w
	outputMu.Unlock()
}

func SetupTUILog() {
	TUILog = func(msg string) {
		tuilogMu.Lock()
		TUILogBuf = append(TUILogBuf, msg)
		if len(TUILogBuf) > 200 {
			TUILogBuf = TUILogBuf[len(TUILogBuf)-100:]
		}
		tuilogMu.Unlock()
	}
}

func DisableTUILog() {
	tuilogMu.Lock()
	TUILog = nil
	TUILogBuf = nil
	tuilogMu.Unlock()
}

func TUIStats() TUIVoiceStats {
	tuiStatsMu.Lock()
	defer tuiStatsMu.Unlock()
	return tuiStats
}

func recordTUIStats(text string, audioDuration time.Duration) {
	chars := countTextRunes(text)
	tuiStatsMu.Lock()
	tuiStats.Sessions++
	tuiStats.Chars += uint64(chars)
	tuiStats.AudioDuration += audioDuration
	tuiStats.LastTextChars = chars
	tuiStats.LastAudioDuration = audioDuration
	snapshot := tuiStats
	tuiStatsMu.Unlock()
	go saveTUIStats(snapshot)
}

func countTextRunes(text string) int {
	n := 0
	for _, r := range text {
		if !unicode.IsSpace(r) {
			n++
		}
	}
	return n
}

type persistedStats struct {
	Sessions        uint64 `json:"sessions"`
	Chars           uint64 `json:"chars"`
	AudioDurationMs int64  `json:"audio_duration_ms"`
}

func loadTUIStats() {
	data, err := os.ReadFile(statsPath())
	if err != nil {
		return
	}
	var ps persistedStats
	if json.Unmarshal(data, &ps) != nil {
		return
	}
	tuiStatsMu.Lock()
	tuiStats.Sessions = ps.Sessions
	tuiStats.Chars = ps.Chars
	tuiStats.AudioDuration = time.Duration(ps.AudioDurationMs) * time.Millisecond
	tuiStats.LastTextChars = 0
	tuiStats.LastAudioDuration = 0
	tuiStatsMu.Unlock()
}

func saveTUIStats(stats TUIVoiceStats) {
	path := statsPath()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return
	}
	ps := persistedStats{
		Sessions:        stats.Sessions,
		Chars:           stats.Chars,
		AudioDurationMs: stats.AudioDuration.Milliseconds(),
	}
	data, err := json.MarshalIndent(ps, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(path, data, 0644)
}

func statsPath() string {
	base := os.Getenv("XDG_STATE_HOME")
	if base == "" {
		if cache, err := os.UserCacheDir(); runtime.GOOS == "windows" && err == nil {
			base = cache
		} else if home, err := os.UserHomeDir(); err == nil {
			base = filepath.Join(home, ".local", "state")
		} else {
			base = "."
		}
	}
	return filepath.Join(base, "just-talk", "stats.json")
}

func TUIStatus() TUIVoiceStatus {
	tuiStatusMu.Lock()
	defer tuiStatusMu.Unlock()
	if tuiStatus.State == "error" && !tuiStatus.ErrorUntil.IsZero() && time.Now().After(tuiStatus.ErrorUntil) {
		tuiStatus.State = "idle"
		tuiStatus.Detail = "等待热键"
		tuiStatus.Recording = false
		tuiStatus.Stopping = false
		tuiStatus.StopAt = time.Time{}
		tuiStatus.ErrorUntil = time.Time{}
		tuiStatus.UpdatedAt = time.Now()
	}
	return tuiStatus
}

func setTUIStatus(update func(*TUIVoiceStatus)) {
	tuiStatusMu.Lock()
	defer tuiStatusMu.Unlock()
	update(&tuiStatus)
	tuiStatus.UpdatedAt = time.Now()
}

func markTUIHotkey(evt hotkey.Event) {
	tuiStatusMu.Lock()
	defer tuiStatusMu.Unlock()
	tuiStatus.LastHotkeyAt = time.Now()
	tuiStatus.LastHotkeyType = evt.Type.String()
	tuiStatus.UpdatedAt = time.Now()
}

func markTUIQueued(evt hotkey.Event, queueLen int) {
	tuiStatusMu.Lock()
	defer tuiStatusMu.Unlock()
	tuiStatus.LastHotkeyAt = time.Now()
	tuiStatus.LastHotkeyType = evt.Type.String()
	tuiStatus.QueuedHotkeys++
	tuiStatus.EventQueueLen = queueLen
	tuiStatus.UpdatedAt = time.Now()
}

func markTUIHandled(evt hotkey.Event) {
	tuiStatusMu.Lock()
	defer tuiStatusMu.Unlock()
	tuiStatus.LastHandledAt = time.Now()
	tuiStatus.LastHandledType = evt.Type.String()
	tuiStatus.HandledHotkeys++
	tuiStatus.UpdatedAt = time.Now()
}

type VoicePlugin struct {
	env                    engine.PluginEnv
	logger                 *slog.Logger
	cfg                    *config.Config
	mu                     sync.Mutex
	eventOnce              sync.Once
	events                 chan hotkey.Event
	combo                  hotkey.Combo
	mode                   string
	recording              bool
	stopping               bool
	holdReleased           bool
	userStopped            bool
	stopTimer              *time.Timer
	stopAt                 time.Time
	sessionID              uint64
	recorder               *Recorder
	work                   *recognitionTask
	shutdown               bool
	autoSubmit             bool
	stopDelayMs            int
	pendingDone            int
	finishingSessions      map[uint64]*recognitionTask
	canceledSessions       map[uint64]struct{}
	outputSessions         map[uint64]struct{}
	errorUntil             time.Time
	errorTimer             *time.Timer
	lastError              string
	flowActive             bool
	cancelHotkeyRegistered bool
	retryHotkeyRegistered  bool
}

type recordingSession struct {
	sessionID   uint64
	recorder    *Recorder
	work        *recognitionTask
	autoSubmit  bool
	userStopped bool
}

func NewVoicePlugin() *VoicePlugin     { return &VoicePlugin{stopDelayMs: defaultStopDelayMs} }
func (p *VoicePlugin) Name() string    { return "voice" }
func (p *VoicePlugin) Version() string { return "0.6.0" }

func (p *VoicePlugin) Init(env engine.PluginEnv) error {
	p.env = env
	p.logger = env.Logger()
	p.cfg = env.Config()
	loadTUIStats()
	return p.registerFromConfig(env.Config())
}

func (p *VoicePlugin) Start(ctx context.Context) error {
	p.logger.Info("voice plugin started", "mode", p.mode)
	p.startEventWorker(ctx)
	p.mu.Lock()
	p.publishStatusLocked()
	p.mu.Unlock()
	<-ctx.Done()
	return ctx.Err()
}

func (p *VoicePlugin) Stop() error {
	p.mu.Lock()
	p.shutdown = true
	session := p.detachRecordingLocked()
	p.trackFinishLocked(session)
	for _, work := range p.finishingSessions {
		work.cancel()
	}
	p.clearErrorLocked()
	p.cleanupTransientHotkeysLocked()
	p.mu.Unlock()
	p.finishRecordingSession(session)
	return nil
}

func (p *VoicePlugin) OnConfigReload(cfg *config.Config) error { return p.registerFromConfig(cfg) }

func (p *VoicePlugin) registerFromConfig(cfg *config.Config) error {
	p.mu.Lock()
	p.cfg = cfg
	p.mu.Unlock()
	vc := cfg.Voice
	if !vc.Enabled {
		p.mu.Lock()
		oldCombo := p.combo
		p.cleanupTransientHotkeysLocked()
		p.combo = hotkey.Combo{}
		p.mu.Unlock()
		if oldCombo.Key != hotkey.KeyNone || oldCombo.Mods != hotkey.ModNone {
			p.env.UnregisterHotkey(oldCombo)
		}
		p.logger.Info("voice disabled")
		return nil
	}
	combo, err := config.ParseHotkey(vc.PushToTalk)
	if err != nil {
		return fmt.Errorf("parse hotkey %q: %w", vc.PushToTalk, err)
	}
	mode := vc.Mode
	if mode == "" {
		mode = "hold"
	}
	if err := validateVoiceHotkey(combo); err != nil {
		return err
	}

	p.mu.Lock()
	oldCombo := p.combo
	oldMode := p.mode
	sameRegistration := oldCombo == combo && oldMode == mode
	p.combo = combo
	p.mode = mode
	p.autoSubmit = vc.AutoSubmit
	p.stopDelayMs = vc.StopDelayMs
	p.mu.Unlock()

	p.logger.Info("config_reloaded", "hotkey", combo, "mode", mode,
		"auto_submit", vc.AutoSubmit, "stop_delay_ms", vc.StopDelayMs, "save_traffic", vc.SaveTraffic)

	if !sameRegistration {
		isOld := oldCombo.Key != hotkey.KeyNone || oldCombo.Mods != hotkey.ModNone
		if isOld {
			p.env.UnregisterHotkey(oldCombo)
		}
		suppress := mode == "hold"
		if runtime.GOOS == "windows" && combo.IsModifierOnly() {
			suppress = false
		}
		opts := hotkey.RegisterOptions{Suppress: suppress}
		if err := p.env.RegisterHotkeyWithOptions(combo, opts, p.onHotkey); err != nil {
			return fmt.Errorf("register hotkey: %w", err)
		}
		p.logger.Info("hotkey_registered", "combo", combo, "suppress", opts.Suppress)
	}
	return nil
}

func (p *VoicePlugin) onHotkey(evt hotkey.Event) {
	markTUIHotkey(evt)
	p.logger.Debug("voice hotkey received", "type", evt.Type, "combo", evt.Combo)
	p.startEventWorker(p.env.Engine().Context())
	p.mu.Lock()
	mode := p.mode
	p.mu.Unlock()
	if mode == "toggle" && evt.Type != hotkey.KeyDown {
		p.logger.Debug("voice hotkey ignored", "type", evt.Type, "mode", mode)
		return
	}
	p.events <- evt
	markTUIQueued(evt, len(p.events))
	p.logger.Debug("voice hotkey queued", "type", evt.Type, "queue_len", len(p.events))
}

func (p *VoicePlugin) onCancelHotkey(evt hotkey.Event) {
	if evt.Type != hotkey.KeyDown {
		return
	}
	p.cancelRecording()
}

func (p *VoicePlugin) onRetryHotkey(evt hotkey.Event) {
	if evt.Type != hotkey.KeyDown {
		return
	}
	p.retryLastError()
}

func (p *VoicePlugin) startEventWorker(ctx context.Context) {
	p.eventOnce.Do(func() {
		p.events = make(chan hotkey.Event, 256)
		go p.eventLoop(ctx)
	})
}

func (p *VoicePlugin) eventLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case evt := <-p.events:
			p.handleHotkey(evt)
		}
	}
}

func (p *VoicePlugin) handleHotkey(evt hotkey.Event) {
	markTUIHandled(evt)
	p.mu.Lock()
	mode, rec, stopping := p.mode, p.recording, p.stopping
	p.mu.Unlock()
	p.logger.Debug("voice hotkey handling", "type", evt.Type, "mode", mode, "recording", rec, "stopping", stopping)
	switch mode {
	case "hold":
		if evt.Type == hotkey.KeyDown {
			p.clearHoldReleased()
			if stopping {
				p.cancelStopDelay()
			} else if !rec {
				p.startRecording()
			}
		} else if evt.Type == hotkey.KeyUp {
			p.markHoldReleased()
		}
	case "toggle":
		if evt.Type != hotkey.KeyDown {
			return
		}
		if !rec {
			p.startRecording()
		} else if stopping {
			p.restartRecording()
		} else {
			p.startStopDelay()
		}
	}
}

func (p *VoicePlugin) startStopDelay() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.stopping || !p.recording {
		return
	}
	p.holdReleased = false
	p.stopping, p.userStopped = true, true
	delay := time.Duration(p.stopDelayMs) * time.Millisecond
	p.stopAt = time.Now().Add(delay)
	p.stopTimer = time.AfterFunc(delay, func() {
		p.stopRecordingAsync()
	})
	p.publishStatusLocked()
	pout("🎤 即将停止... (%dms 缓冲)", p.stopDelayMs)
}

func (p *VoicePlugin) cancelStopDelay() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.cancelStopDelayLocked()
}

func (p *VoicePlugin) cancelStopDelayLocked() {
	p.cancelStopDelayOnlyLocked()
	p.holdReleased = false
	p.publishStatusLocked()
}

func (p *VoicePlugin) cancelStopDelayOnlyLocked() {
	if p.stopTimer != nil {
		p.stopTimer.Stop()
		p.stopTimer = nil
	}
	if p.stopping {
		pout("🎤 继续录音")
	}
	p.stopping = false
	p.stopAt = time.Time{}
}

func (p *VoicePlugin) clearHoldReleased() {
	p.mu.Lock()
	p.holdReleased = false
	p.mu.Unlock()
}

func (p *VoicePlugin) markHoldReleased() {
	p.mu.Lock()
	p.holdReleased = true
	shouldStop := p.recording && !p.stopping
	p.mu.Unlock()
	if shouldStop {
		p.startStopDelay()
	}
}

func (p *VoicePlugin) startRecording() {
	p.mu.Lock()
	p.cancelStopDelayOnlyLocked()
	if p.shutdown || p.recording || p.work != nil {
		p.mu.Unlock()
		pout("⚠️  已经在录音中")
		return
	}
	vc := p.cfg.Voice
	var rec *Recorder
	if vc.Device != "" {
		rec = NewRecorderWithDevice(p.logger, vc.Device, vc.Gain)
	} else {
		rec = NewRecorder(p.logger, vc.Gain)
	}
	if err := rec.Start(); err != nil {
		p.sessionID++
		sessionID := p.sessionID
		p.publishErrorLocked("录音启动失败: "+shortError(err), sessionID)
		p.mu.Unlock()
		pout("❌ 录音启动失败: %v", err)
		return
	}
	pout("🎤 开始录音... (后端: %s)", rec.Backend())
	work := newRecognitionTask()
	work.saveTraffic = vc.SaveTraffic
	p.sessionID++
	sessionID := p.sessionID
	p.recorder, p.recording, p.userStopped = rec, true, false
	p.stopping = false
	p.stopAt = time.Time{}
	p.clearErrorLocked()
	p.work = work
	shouldStopImmediately := p.mode == "hold" && p.holdReleased
	p.publishStatusLocked()
	p.mu.Unlock() // Release lock before slow WebSocket dial

	go work.capture(rec)
	go p.runRecognition(sessionID, work)
	if shouldStopImmediately {
		p.startStopDelay()
	}
}

func (p *VoicePlugin) runRecognition(sessionID uint64, work *recognitionTask) {
	if work.saveTraffic {
		p.runFileRecognition(sessionID, work)
		return
	}
	factory := func() recognitionClient {
		p.mu.Lock()
		vc := p.cfg.Voice
		p.mu.Unlock()
		cfg := ASRConfig{AppKey: vc.AppKey, AccessKey: vc.AccessKey, ResourceID: vc.ResourceID, Language: vc.Language, Hotwords: vc.Hotwords}
		if cfg.ResourceID == "" {
			cfg.ResourceID = "volc.bigasr.sauc.duration"
		}
		if cfg.Language == "" {
			cfg.Language = "zh-CN"
		}
		return NewASRClient(cfg, p.logger)
	}
	connected := func() {
		p.mu.Lock()
		defer p.mu.Unlock()
		work.connected = true
		work.recoveryDetail = ""
		work.retryDetail = ""
		p.publishStatusLocked()
	}
	failed := func(attempt int, err error) <-chan struct{} {
		return p.setRetryStatus(sessionID, work, attempt, err)
	}
	recovering := func(attempt int, err error) {
		p.mu.Lock()
		defer p.mu.Unlock()
		if work.ctx.Err() != nil {
			return
		}
		work.connected = false
		work.recoveryDetail = fmt.Sprintf("音频已缓存，正在自动重连（%d/3）", attempt)
		p.logger.Debug("recovering ASR connection", "session", sessionID, "attempt", attempt, "error", err)
		p.publishStatusLocked()
	}
	var text string
	err := retryUntilSuccess(work.ctx, func() error {
		var err error
		text, err = recoverRecognition(work.ctx, work.cache, factory, connected, recovering,
			[]time.Duration{time.Second, 2 * time.Second, 4 * time.Second})
		return err
	}, failed)
	work.result <- recognitionResult{text: text, err: err}
}

func (p *VoicePlugin) setRetryStatus(sessionID uint64, work *recognitionTask, attempt int, err error) <-chan struct{} {
	p.mu.Lock()
	defer p.mu.Unlock()
	if work.ctx.Err() != nil {
		return nil
	}
	work.connected = false
	work.recoveryDetail = ""
	work.retryRequest = make(chan struct{})
	work.retryDetail = fmt.Sprintf("录音已缓存，第 %d 次失败，按 R 重试，Esc 取消: %s", attempt, asrConnectErrorDetail(err))
	p.publishStatusLocked()
	pout("⚠️ %s", work.retryDetail)
	p.logger.Debug("session waiting for R", "session", sessionID, "attempt", attempt, "error", err)
	return work.retryRequest
}

func (p *VoicePlugin) restartRecording() {
	p.mu.Lock()
	session := p.detachRecordingLocked()
	p.trackFinishLocked(session)
	p.publishStatusLocked()
	p.mu.Unlock()
	if session != nil {
		go p.finishRecordingSession(session)
	}
	p.startRecording()
}

func (p *VoicePlugin) cancelRecording() {
	p.mu.Lock()
	session := p.detachRecordingLocked()
	hadError := p.lastError != ""
	hadPending := p.pendingDone > 0
	p.trackFinishLocked(session)
	if p.canceledSessions == nil {
		p.canceledSessions = make(map[uint64]struct{})
	}
	for id, work := range p.finishingSessions {
		p.canceledSessions[id] = struct{}{}
		work.cancel()
	}
	p.pendingDone = 0
	p.clearErrorLocked()
	p.publishStatusLocked()
	p.mu.Unlock()

	if session != nil {
		go p.finishRecordingSession(session)
		pout("🎤 已取消本次录音和待重试缓存")
	} else if hadPending {
		pout("🎤 已取消等待识别结果和待重试缓存")
	} else if hadError {
		pout("⚠️ 已关闭错误状态")
	}
}

// retryTaskLocked selects the most recent failed session. Busy sessions cannot
// queue presses for future failures, and one press never retries multiple sessions.
func (p *VoicePlugin) retryTaskLocked() (uint64, *recognitionTask) {
	var selected *recognitionTask
	var sessionID uint64
	for id, work := range p.finishingSessions {
		if work.ctx.Err() == nil && work.retryRequest != nil && (selected == nil || id > sessionID) {
			sessionID, selected = id, work
		}
	}
	if p.work != nil && p.work.ctx.Err() == nil && p.work.retryRequest != nil {
		return p.sessionID, p.work
	}
	return sessionID, selected
}

func (p *VoicePlugin) retryLastError() {
	p.mu.Lock()
	if p.shutdown {
		p.mu.Unlock()
		return
	}
	if _, work := p.retryTaskLocked(); work != nil {
		close(work.retryRequest)
		work.retryRequest = nil
		work.retryDetail = ""
		p.publishStatusLocked()
		p.mu.Unlock()
		return
	}
	if p.recording || p.stopping || p.pendingDone > 0 || p.lastError == "" || time.Now().After(p.errorUntil) {
		p.mu.Unlock()
		return
	}
	p.clearErrorLocked()
	p.publishStatusLocked()
	p.mu.Unlock()
	p.startRecording()
}

func (p *VoicePlugin) stopRecordingAsync() {
	p.mu.Lock()
	session := p.detachRecordingLocked()
	p.trackFinishLocked(session)
	p.publishStatusLocked()
	p.mu.Unlock()
	if session != nil {
		go p.finishRecordingSession(session)
	}
}

func (p *VoicePlugin) detachRecordingLocked() *recordingSession {
	if p.stopTimer != nil {
		p.stopTimer.Stop()
		p.stopTimer = nil
	}
	p.stopAt = time.Time{}
	if p.recorder == nil && p.work == nil {
		p.recording, p.stopping, p.userStopped = false, false, false
		return nil
	}
	session := &recordingSession{
		sessionID:   p.sessionID,
		recorder:    p.recorder,
		work:        p.work,
		autoSubmit:  p.autoSubmit,
		userStopped: p.userStopped,
	}
	p.recorder, p.work = nil, nil
	p.recording, p.stopping, p.userStopped = false, false, false
	p.holdReleased = false
	return session
}

func (p *VoicePlugin) trackFinishLocked(session *recordingSession) {
	if session == nil {
		return
	}
	p.pendingDone++
	if p.finishingSessions == nil {
		p.finishingSessions = make(map[uint64]*recognitionTask)
	}
	p.finishingSessions[session.sessionID] = session.work
}

func (p *VoicePlugin) finishRecordingSession(session *recordingSession) {
	if session == nil {
		return
	}
	defer p.recordingSessionFinished(session.sessionID)
	work := session.work
	defer work.cancel()
	p.logger.Debug("finish session: stopping recorder", "session", session.sessionID)
	remaining, err := session.recorder.Stop()
	if err != nil {
		p.logger.Warn("stop recorder failed", "error", err)
	}
	// Stop waits for any in-flight Read. Wait for its cache append as well,
	// then append the tail exactly once before marking the cache complete.
	<-work.captureDone
	work.cache.append(remaining)
	work.cache.seal()
	p.logger.Debug("finish session: recorder stopped; waiting ASR final", "session", session.sessionID, "remaining_bytes", len(remaining))
	if !session.userStopped || work.ctx.Err() != nil {
		return
	}
	select {
	case <-work.ctx.Done():
		return
	case result := <-work.result:
		if result.err != nil || result.text == "" || !p.claimSessionOutput(session.sessionID) {
			return
		}
		// Recognition is complete. Output errors retain the text and retry
		// without reconnecting to ASR or recognizing the audio again.
		err := retryUntilSuccess(work.ctx, func() error {
			return p.dispatchTextOutput(result.text, session.autoSubmit)
		}, func(attempt int, err error) <-chan struct{} {
			return p.setRetryStatus(session.sessionID, work, attempt, err)
		})
		if err == nil {
			recordTUIStats(result.text, work.cache.duration())
		}
	}
}

func (p *VoicePlugin) dispatchTextOutput(text string, autoSubmit bool) error {
	if autoSubmit {
		p.logger.Debug("text output: auto-submit", "text_len", len(text))
		if err := autotype.Paste(text, p.logger); err != nil {
			return fmt.Errorf("上屏失败: %w", err)
		}
		pout("✅ 已上屏")
		return nil
	}
	p.logger.Debug("text output: writing clipboard", "text_len", len(text))
	if err := writeClipboard(text); err != nil {
		return fmt.Errorf("复制到剪贴板失败: %w", err)
	}
	pout("📋 已复制到剪贴板")
	return nil
}

func (p *VoicePlugin) recordingSessionFinished(sessionID uint64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	_, canceled := p.canceledSessions[sessionID]
	delete(p.finishingSessions, sessionID)
	delete(p.canceledSessions, sessionID)
	delete(p.outputSessions, sessionID)
	if !canceled && p.pendingDone > 0 {
		p.pendingDone--
	}
	p.publishStatusLocked()
}

func (p *VoicePlugin) claimSessionOutput(sessionID uint64) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, canceled := p.canceledSessions[sessionID]; p.shutdown || canceled {
		return false
	}
	if p.outputSessions == nil {
		p.outputSessions = make(map[uint64]struct{})
	}
	if _, exists := p.outputSessions[sessionID]; exists {
		return false
	}
	p.outputSessions[sessionID] = struct{}{}
	return true
}

func (p *VoicePlugin) publishStatusLocked() {
	if p.shutdown {
		return
	}
	state, detail := "idle", "等待热键"
	stopAt := time.Time{}
	recording, stopping := p.recording, false

	switch {
	case p.recording && p.stopping:
		state, detail = "stopping_delayed", "等待停止延迟"
		stopping = true
		stopAt = p.stopAt
	case p.recording && p.work != nil && p.work.retryDetail != "":
		state, detail = "waiting_retry", "录音中；"+p.work.retryDetail
	case p.recording && p.work != nil && p.work.recoveryDetail != "":
		state, detail = "connecting", "录音中；"+p.work.recoveryDetail
	case p.recording && p.work != nil && p.work.saveTraffic:
		state, detail = "recording", "录音中；省流量已开，停止后整段识别"
	case p.recording && p.work != nil && !p.work.connected:
		state, detail = "connecting", "录音中，正在连接 ASR"
	case p.recording:
		state, detail = "recording", "录音中"
	case p.pendingDone > 0:
		state, detail = "stopping", "正在停止并等待识别结果"
		stopping = true
		for _, work := range p.finishingSessions {
			if work.ctx.Err() == nil && work.recoveryDetail != "" {
				detail = work.recoveryDetail
			}
		}
		if _, work := p.retryTaskLocked(); work != nil {
			state, detail = "waiting_retry", work.retryDetail
		}
	case p.lastError != "" && time.Now().Before(p.errorUntil):
		state, detail = "error", p.lastError
	}

	p.publishStatusSnapshotLocked(state, detail, recording, stopping, stopAt, p.sessionID)
	errorActive := state == "error" && time.Now().Before(p.errorUntil)
	wasFlowActive := p.flowActive
	p.flowActive = state != "idle"
	if state == "idle" && wasFlowActive {
		p.cleanupTransientHotkeysLocked()
		return
	}
	_, retryWork := p.retryTaskLocked()
	p.syncTransientHotkeysLocked(recording || stopping || p.pendingDone > 0 || errorActive, errorActive || retryWork != nil)
}

func (p *VoicePlugin) publishErrorLocked(detail string, sessionID uint64) {
	p.lastError = detail
	p.errorUntil = time.Now().Add(errorHoldDuration)
	if p.errorTimer != nil {
		p.errorTimer.Stop()
	}
	p.errorTimer = time.AfterFunc(errorHoldDuration, func() {
		p.mu.Lock()
		defer p.mu.Unlock()
		if p.lastError != "" && time.Now().After(p.errorUntil) {
			p.clearErrorLocked()
			p.publishStatusLocked()
		}
	})
	p.publishStatusSnapshotLocked("error", detail, false, false, time.Time{}, sessionID)
	p.syncTransientHotkeysLocked(true, true)
}

func (p *VoicePlugin) publishStatusSnapshotLocked(state, detail string, recording, stopping bool, stopAt time.Time, sessionID uint64) {
	pendingDone := p.pendingDone
	setTUIStatus(func(s *TUIVoiceStatus) {
		s.State, s.Detail = state, detail
		s.Recording, s.Stopping = recording, stopping
		s.StopAt = stopAt
		s.ErrorUntil = p.errorUntil
		s.SessionID = sessionID
		s.PendingFinishes = pendingDone
	})
}

func (p *VoicePlugin) clearErrorLocked() {
	if p.errorTimer != nil {
		p.errorTimer.Stop()
		p.errorTimer = nil
	}
	p.errorUntil = time.Time{}
	p.lastError = ""
}

func (p *VoicePlugin) cleanupTransientHotkeysLocked() {
	p.syncTransientHotkeysLocked(false, false)
}

func validateVoiceHotkey(combo hotkey.Combo) error {
	if combo.Key.IsTextKey() {
		return fmt.Errorf("语音热键不支持普通字符键 %s；请使用适合作为全局快捷键的组合，如 Alt+Super、Alt+F8、F9、Ctrl+Alt+Tab", combo)
	}
	return nil
}

func (p *VoicePlugin) syncTransientHotkeysLocked(needCancel, needRetry bool) {
	if needCancel && !p.cancelHotkeyRegistered {
		if err := p.env.RegisterHotkey(cancelRecordingCombo, p.onCancelHotkey); err != nil {
			p.logger.Warn("register cancel hotkey failed", "combo", cancelRecordingCombo, "error", err)
		} else {
			p.cancelHotkeyRegistered = true
		}
	} else if !needCancel && p.cancelHotkeyRegistered {
		if err := p.env.UnregisterHotkey(cancelRecordingCombo); err != nil {
			p.logger.Warn("unregister cancel hotkey failed", "combo", cancelRecordingCombo, "error", err)
		}
		p.cancelHotkeyRegistered = false
	}

	if needRetry && !p.retryHotkeyRegistered {
		if err := p.env.RegisterHotkey(retryErrorCombo, p.onRetryHotkey); err != nil {
			p.logger.Warn("register retry hotkey failed", "combo", retryErrorCombo, "error", err)
		} else {
			p.retryHotkeyRegistered = true
		}
	} else if !needRetry && p.retryHotkeyRegistered {
		if err := p.env.UnregisterHotkey(retryErrorCombo); err != nil {
			p.logger.Warn("unregister retry hotkey failed", "combo", retryErrorCombo, "error", err)
		}
		p.retryHotkeyRegistered = false
	}
}

func shortError(err error) string {
	msg := strings.TrimSpace(err.Error())
	if msg == "" {
		return "未知错误"
	}
	msg = strings.ReplaceAll(msg, "\n", " ")
	if len([]rune(msg)) <= 120 {
		return msg
	}
	return string([]rune(msg)[:120]) + "..."
}

func asrConnectErrorDetail(err error) string {
	msg := err.Error()
	if strings.Contains(msg, "HTTP 401") || strings.Contains(msg, "HTTP 403") {
		return "认证失败，请检查 App Key 或 Access Key。"
	}
	return shortError(err)
}

func writeClipboard(text string) error {
	cb, err := clipboard.New()
	if err != nil {
		return err
	}
	return cb.Set(text)
}

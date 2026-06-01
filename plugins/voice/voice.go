package voice

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
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
		if home, err := os.UserHomeDir(); err == nil {
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
	userStopped            bool
	stopTimer              *time.Timer
	stopAt                 time.Time
	startedAt              time.Time
	sessionID              uint64
	sessionGen             uint64
	recorder               *Recorder
	asrClient              *ASRClient
	asrCancel              context.CancelFunc
	autoSubmit             bool
	stopDelayMs            int
	pendingDone            int
	finishingSessions      map[uint64]struct{}
	canceledSessions       map[uint64]struct{}
	outputSessions         map[uint64]struct{}
	errorUntil             time.Time
	errorTimer             *time.Timer
	lastError              string
	cancelHotkeyRegistered bool
	retryHotkeyRegistered  bool
}

type recordingSession struct {
	sessionID   uint64
	recorder    *Recorder
	asrClient   *ASRClient
	asrCancel   context.CancelFunc
	autoSubmit  bool
	userStopped bool
	startedAt   time.Time
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
	session := p.detachRecordingLocked()
	p.trackFinishLocked(session)
	p.publishStatusLocked()
	p.mu.Unlock()
	p.finishRecordingSession(session)
	return nil
}

func (p *VoicePlugin) OnConfigReload(cfg *config.Config) error { return p.registerFromConfig(cfg) }

func (p *VoicePlugin) registerFromConfig(cfg *config.Config) error {
	vc := cfg.Voice
	if !vc.Enabled {
		p.mu.Lock()
		oldCombo := p.combo
		p.syncTransientHotkeysLocked(false, false)
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

	p.mu.Lock()
	oldCombo := p.combo
	sameCombo := oldCombo == combo
	p.combo = combo
	p.mode = mode
	p.autoSubmit = vc.AutoSubmit
	p.stopDelayMs = vc.StopDelayMs
	p.mu.Unlock()

	p.logger.Info("config_reloaded", "hotkey", combo, "mode", mode,
		"auto_submit", vc.AutoSubmit, "stop_delay_ms", vc.StopDelayMs)

	if !sameCombo {
		isOld := oldCombo.Key != hotkey.KeyNone || oldCombo.Mods != hotkey.ModNone
		if isOld {
			p.env.UnregisterHotkey(oldCombo)
		}
		if err := p.env.RegisterHotkey(combo, p.onHotkey); err != nil {
			return fmt.Errorf("register hotkey: %w", err)
		}
		p.logger.Info("hotkey_registered", "combo", combo)
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
			if stopping {
				p.cancelStopDelay()
			} else if !rec {
				p.startRecording()
			}
		} else if evt.Type == hotkey.KeyUp {
			p.startStopDelay()
		}
	case "toggle":
		if evt.Type != hotkey.KeyDown {
			return
		}
		if !rec {
			p.startRecording()
		} else if p.stopping {
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
	if p.stopTimer != nil {
		p.stopTimer.Stop()
		p.stopTimer = nil
	}
	if p.stopping {
		pout("🎤 继续录音")
	}
	p.stopping = false
	p.stopAt = time.Time{}
	p.publishStatusLocked()
}

func (p *VoicePlugin) startRecording() {
	p.mu.Lock()
	p.cancelStopDelayLocked()
	if p.recording || p.asrClient != nil {
		p.mu.Unlock()
		pout("⚠️  已经在录音中")
		return
	}
	vc := p.cfg.Voice
	asrCfg := ASRConfig{AppKey: vc.AppKey, AccessKey: vc.AccessKey, ResourceID: vc.ResourceID, Language: vc.Language, Hotwords: vc.Hotwords}
	if asrCfg.ResourceID == "" {
		asrCfg.ResourceID = "volc.bigasr.sauc.duration"
	}
	if asrCfg.Language == "" {
		asrCfg.Language = "zh-CN"
	}
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
	ctx, cancel := context.WithCancel(context.Background())
	p.sessionID++
	p.sessionGen++
	sessionID := p.sessionID
	sessionGen := p.sessionGen
	startedAt := time.Now()
	p.recorder, p.recording, p.userStopped = rec, true, false
	p.startedAt = startedAt
	p.stopping = false
	p.stopAt = time.Time{}
	p.clearErrorLocked()
	p.asrCancel = cancel
	p.publishStatusLocked()
	p.mu.Unlock() // Release lock before slow WebSocket dial

	go p.connectASR(ctx, cancel, sessionID, sessionGen, rec, asrCfg)
}

func (p *VoicePlugin) connectASR(ctx context.Context, cancel context.CancelFunc, sessionID, sessionGen uint64, rec *Recorder, asrCfg ASRConfig) {
	client := NewASRClient(asrCfg, p.logger)
	if err := client.Connect(ctx); err != nil {
		wasCanceled := ctx.Err() != nil
		cancel()
		p.mu.Lock()
		currentSession := p.sessionGen == sessionGen
		if currentSession {
			rec.Stop()
			p.stopping, p.recorder, p.recording = false, nil, false
			p.stopAt = time.Time{}
			p.asrCancel = nil
			if !wasCanceled {
				p.publishErrorLocked("ASR 连接失败: "+asrConnectErrorDetail(err), sessionID)
			} else {
				p.publishStatusLocked()
			}
		}
		p.mu.Unlock()
		if currentSession && !wasCanceled {
			pout("❌ ASR 连接失败: %v", err)
		}
		return
	}
	pout("✅ ASR 已连接")

	p.mu.Lock()
	if p.sessionGen != sessionGen || !p.recording || p.recorder != rec {
		p.mu.Unlock()
		client.Close()
		cancel()
		return
	}
	p.asrClient = client
	p.publishStatusLocked()
	p.mu.Unlock()

	go client.ReceiveLoop(ctx)
	go p.streamAudio(ctx, rec, client)
	go func() {
		for result := range client.Results() {
			if result.Error != nil {
				pout("❌ ASR 错误: %v", result.Error)
				continue
			}
			if result.IsFinal {
				pout("\n🎤 最终: %s", result.Text)
			} else if result.Text != "" {
				pout("\r🎤 %s", result.Text)
			}
		}
	}()
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
	hadError := p.lastError != "" && time.Now().Before(p.errorUntil)
	hadPending := p.pendingDone > 0
	if hadPending {
		if p.canceledSessions == nil {
			p.canceledSessions = make(map[uint64]struct{})
		}
		for id := range p.finishingSessions {
			p.canceledSessions[id] = struct{}{}
		}
		p.pendingDone = 0
	}
	p.clearErrorLocked()
	p.publishStatusLocked()
	p.mu.Unlock()

	if session != nil {
		if session.asrCancel != nil {
			session.asrCancel()
		}
		if session.recorder != nil {
			_, _ = session.recorder.Stop()
		}
		if session.asrClient != nil {
			_ = session.asrClient.Close()
		}
		pout("🎤 已取消本次录音")
	} else if hadPending {
		pout("🎤 已取消等待识别结果")
	} else if hadError {
		pout("⚠️  已关闭错误状态")
	}
}

func (p *VoicePlugin) retryLastError() {
	p.mu.Lock()
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
	if p.recorder == nil && p.asrClient == nil && p.asrCancel == nil {
		p.recording, p.stopping, p.userStopped = false, false, false
		return nil
	}
	session := &recordingSession{
		sessionID:   p.sessionID,
		recorder:    p.recorder,
		asrClient:   p.asrClient,
		asrCancel:   p.asrCancel,
		autoSubmit:  p.autoSubmit,
		userStopped: p.userStopped,
		startedAt:   p.startedAt,
	}
	p.sessionGen++
	p.recorder, p.asrClient, p.asrCancel = nil, nil, nil
	p.startedAt = time.Time{}
	p.recording, p.stopping, p.userStopped = false, false, false
	return session
}

func (p *VoicePlugin) trackFinishLocked(session *recordingSession) {
	if session == nil {
		return
	}
	p.pendingDone++
	if p.finishingSessions == nil {
		p.finishingSessions = make(map[uint64]struct{})
	}
	p.finishingSessions[session.sessionID] = struct{}{}
}

func (p *VoicePlugin) finishRecordingSession(session *recordingSession) {
	if session == nil {
		return
	}
	defer p.recordingSessionFinished(session.sessionID)
	pout("🎤 停止录音")
	if session.asrClient == nil && session.asrCancel != nil {
		session.asrCancel()
	}
	var remaining []byte
	if session.recorder != nil {
		p.logger.Debug("finish session: stopping recorder")
		remaining, _ = session.recorder.Stop()
		p.logger.Debug("finish session: recorder stopped", "remaining_bytes", len(remaining))
	}

	if session.asrClient != nil {
		p.logger.Debug("finish session: sending final audio", "bytes", len(remaining))
		sendCtx, sendCancel := context.WithTimeout(context.Background(), 3*time.Second)
		if len(remaining) > 0 {
			if err := session.asrClient.SendAudio(sendCtx, remaining, true); err != nil {
				p.logger.Warn("send final audio failed", "error", err)
			}
		} else {
			if err := session.asrClient.SendAudio(sendCtx, nil, true); err != nil {
				p.logger.Warn("send final audio marker failed", "error", err)
			}
		}
		sendCancel()
		p.logger.Debug("finish session: waiting ASR final")
		select {
		case <-session.asrClient.Final():
			p.logger.Debug("finish session: ASR final received")
		case <-session.asrClient.Done():
			p.logger.Debug("finish session: ASR done")
		case <-time.After(15 * time.Second):
			if !p.sessionCanceled(session.sessionID) {
				pout("⚠️  识别超时")
				p.publishError("识别超时: 等待 ASR final 超过 15s", session.sessionID)
			}
		}
		if text := session.asrClient.LastText(); text != "" && session.userStopped && p.claimSessionOutput(session.sessionID) {
			audioDuration := time.Duration(0)
			if !session.startedAt.IsZero() {
				audioDuration = time.Since(session.startedAt)
			}
			recordTUIStats(text, audioDuration)
			p.dispatchTextOutput(text, session.autoSubmit)
		}
		p.logger.Debug("finish session: closing ASR client")
		closeDone := make(chan error, 1)
		go func() { closeDone <- session.asrClient.Close() }()
		select {
		case err := <-closeDone:
			if err != nil {
				p.logger.Debug("close ASR client failed", "error", err)
			}
		case <-time.After(2 * time.Second):
			p.logger.Warn("close ASR client timed out")
		}
	}
	if session.asrCancel != nil {
		session.asrCancel()
	}
	p.logger.Debug("finish session: done")
}

func (p *VoicePlugin) dispatchTextOutput(text string, autoSubmit bool) {
	go func() {
		if autoSubmit {
			if err := autotype.Paste(text, p.logger); err != nil {
				pout("❌ 上屏失败: %v", err)
			} else {
				pout("📋 已复制到剪贴板")
				pout("✅ 已上屏")
			}
			return
		}

		p.logger.Debug("text output: writing clipboard", "text_len", len(text))
		if err := writeClipboard(text); err != nil {
			pout("❌ 复制到剪贴板失败: %v", err)
		} else {
			pout("📋 已复制到剪贴板")
		}
	}()
}

func (p *VoicePlugin) recordingSessionFinished(sessionID uint64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.finishingSessions, sessionID)
	delete(p.canceledSessions, sessionID)
	if p.pendingDone > 0 {
		p.pendingDone--
	}
	p.publishStatusLocked()
}

func (p *VoicePlugin) sessionCanceled(sessionID uint64) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	_, ok := p.canceledSessions[sessionID]
	return ok
}

func (p *VoicePlugin) claimSessionOutput(sessionID uint64) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, canceled := p.canceledSessions[sessionID]; canceled {
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
	state, detail := "idle", "等待热键"
	stopAt := time.Time{}
	recording, stopping := p.recording, false

	switch {
	case p.recording && p.stopping:
		state, detail = "stopping_delayed", "等待停止延迟"
		stopping = true
		stopAt = p.stopAt
	case p.recording && p.asrClient == nil && p.asrCancel != nil:
		state, detail = "connecting", "录音中，正在连接 ASR"
	case p.recording:
		state, detail = "recording", "录音中"
	case p.pendingDone > 0:
		state, detail = "stopping", "正在停止并等待识别结果"
		stopping = true
	case p.lastError != "" && time.Now().Before(p.errorUntil):
		state, detail = "error", p.lastError
	}

	p.publishStatusSnapshotLocked(state, detail, recording, stopping, stopAt, p.sessionID)
	errorActive := state == "error" && time.Now().Before(p.errorUntil)
	p.syncTransientHotkeysLocked(recording || stopping || p.pendingDone > 0 || errorActive, errorActive)
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

func (p *VoicePlugin) publishError(detail string, sessionID uint64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.publishErrorLocked(detail, sessionID)
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

func (p *VoicePlugin) streamAudio(ctx context.Context, rec *Recorder, client *ASRClient) {
	buf := make([]byte, 6400)
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		n, err := rec.Read(buf)
		if n > 0 {
			client.SendAudio(ctx, buf[:n], false)
		}
		if err == io.EOF || (err != nil && ctx.Err() != nil) {
			return
		}
		if err != nil {
			p.logger.Error("read audio error", "error", err)
			return
		}
	}
}

func writeClipboard(text string) error {
	cb, err := clipboard.New()
	if err != nil {
		return err
	}
	return cb.Set(text)
}

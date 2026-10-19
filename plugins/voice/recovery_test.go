package voice

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/c/just-talk-go/engine"
	"github.com/c/just-talk-go/hotkey"
)

type scriptedASR struct {
	connectErr error
	failSend   int
	noFinal    bool
	emptyFinal bool
	closeFinal bool
	blockDial  <-chan struct{}
	sent       []byte
	sends      int
	finals     int
	closed     bool
	final      chan struct{}
	done       chan struct{}
	results    chan ASRResult
	doneOnce   sync.Once
}

func newScriptedASR() *scriptedASR {
	return &scriptedASR{final: make(chan struct{}), done: make(chan struct{}), results: make(chan ASRResult)}
}
func (c *scriptedASR) Connect(ctx context.Context) error {
	if c.blockDial != nil {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-c.blockDial:
		}
	}
	return c.connectErr
}
func (c *scriptedASR) SendAudio(ctx context.Context, pcm []byte, last bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	c.sends++
	c.sent = append(c.sent, pcm...)
	if c.sends == c.failSend {
		return errors.New("network write failed")
	}
	if last {
		c.finals++
		if !c.noFinal {
			close(c.final)
		}
		if c.noFinal || c.closeFinal {
			c.doneOnce.Do(func() { close(c.done) })
		}
	}
	return nil
}
func (c *scriptedASR) ReceiveLoop(ctx context.Context) {
	<-ctx.Done()
	c.doneOnce.Do(func() { close(c.done) })
	close(c.results)
}
func (c *scriptedASR) Results() <-chan ASRResult { return c.results }
func (c *scriptedASR) Done() <-chan struct{}     { return c.done }
func (c *scriptedASR) Final() <-chan struct{}    { return c.final }
func (c *scriptedASR) LastText() string {
	if c.noFinal {
		return "partial transcript"
	}
	if c.emptyFinal {
		return ""
	}
	return "complete transcript"
}
func (c *scriptedASR) Err() error   { return nil }
func (c *scriptedASR) Close() error { c.closed = true; return nil }

func TestRetryRecognitionReplaysCompleteAudioUntilSuccess(t *testing.T) {
	cache := newAudioCache()
	pcm := bytes.Repeat([]byte{0x12, 0x34}, 12000)
	cache.append(pcm)
	cache.seal()
	var clients []*scriptedASR
	retries := 0
	factory := func() recognitionClient {
		c := newScriptedASR()
		switch len(clients) {
		case 0:
			c.connectErr = errors.New("offline")
		case 1:
			c.failSend = 2
		case 2:
			c.failSend = 5 // final audio write fails after all PCM was sent
		case 3:
			c.noFinal = true // partial text + Done is not success
		case 4:
			c.closeFinal = true // Final + Done together is success
		default:
			t.Fatal("retried after success")
		}
		clients = append(clients, c)
		return c
	}
	text, err := retryRecognition(context.Background(), cache, factory, func() {},
		func(_ int, _ error) <-chan struct{} { retries++; return pressedRetry() })
	if err != nil || text != "complete transcript" {
		t.Fatalf("result = %q, %v", text, err)
	}
	if len(clients) != 5 || retries != 4 {
		t.Fatalf("attempts=%d, retries=%d", len(clients), retries)
	}
	for i, c := range clients {
		if !c.closed {
			t.Fatalf("attempt %d leaked its connection", i)
		}
	}
	for _, i := range []int{2, 3, 4} {
		if !bytes.Equal(clients[i].sent, pcm) {
			t.Fatalf("attempt %d lost or duplicated cached audio", i)
		}
	}
	if clients[4].finals != 1 {
		t.Fatal("successful attempt did not send exactly one final packet")
	}
}

func TestRecordingContinuesWhileConnectingAndStoppedAudioIsReplayed(t *testing.T) {
	cache := newAudioCache()
	gate := make(chan struct{})
	client := newScriptedASR()
	client.blockDial = gate
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	finished := make(chan error, 1)
	go func() {
		_, err := recognizeAttempt(ctx, cache, client, func() {})
		finished <- err
	}()
	first := []byte{1, 2, 3, 4}
	cache.append(first)
	first[0] = 99 // recording buffers are reused; cache must own its bytes
	cache.append([]byte{5, 6})
	cache.seal() // user can stop before the connection completes
	close(gate)
	if err := <-finished; err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(client.sent, []byte{1, 2, 3, 4, 5, 6}) {
		t.Fatalf("sent %v", client.sent)
	}
}

func TestCancellationInterruptsAudioWait(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := newAudioCache().read(ctx, nil, 0, make([]byte, 6400))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cache read error = %v", err)
	}
}

func pressedRetry() <-chan struct{} {
	request := make(chan struct{})
	close(request)
	return request
}

func TestWaitASRFinalRejectsTimeoutAndRetainsEmptyRecognition(t *testing.T) {
	client := newScriptedASR()
	ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()
	if err := waitASRFinal(ctx, client); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("wait final error = %v", err)
	}
	cache := newAudioCache()
	cache.seal()
	client = newScriptedASR()
	client.emptyFinal = true
	text, err := recognizeAttempt(context.Background(), cache, client, func() {})
	var permanent *permanentASRError
	if text != "" || !errors.As(err, &permanent) {
		t.Fatalf("empty final = %q, %v; want error requiring explicit retry", text, err)
	}
}

// A muted input device produces digital silence, which the server answers with
// an empty transcript. Reporting that as an ASR fault sends the user to the
// wrong place, so the message must name the microphone instead.
func TestEmptyTranscriptBlamesMicrophoneOnlyWhenAudioIsSilent(t *testing.T) {
	for _, tc := range []struct {
		name string
		pcm  []byte
		want string
	}{
		{"muted device", bytes.Repeat([]byte{0x00, 0x00}, 16000), "请检查麦克风"},
		{"noise floor only", bytes.Repeat([]byte{0x20, 0x00}, 16000), "请检查麦克风"},
		{"speech present", bytes.Repeat([]byte{0x00, 0x40}, 16000), "ASR 未返回有效文字"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cache := newAudioCache()
			cache.append(tc.pcm)
			cache.seal()
			client := newScriptedASR()
			client.emptyFinal = true
			text, err := recognizeAttempt(context.Background(), cache, client, func() {})
			if text != "" || err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("text=%q err=%v; want %q", text, err, tc.want)
			}
		})
	}
}

func TestPeakAmplitudeReadsSignedLittleEndianSamples(t *testing.T) {
	for _, tc := range []struct {
		name string
		pcm  []byte
		want int
	}{
		{"empty", nil, 0},
		{"odd trailing byte ignored", []byte{0x00, 0x01, 0x7f}, 256},
		{"negative sample", []byte{0x00, 0x80}, 32768},
		{"max positive", []byte{0xff, 0x7f}, 32767},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := peakAmplitude(tc.pcm); got != tc.want {
				t.Fatalf("peakAmplitude = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestCaptureRetainsAudioWithoutASR(t *testing.T) {
	pcm := bytes.Repeat([]byte{1, 0}, 16000)
	rec := NewRecorder(slog.New(slog.NewTextHandler(io.Discard, nil)), 2)
	rec.stdout = io.NopCloser(bytes.NewReader(pcm))
	work := newRecognitionTask()
	defer work.cancel()
	work.capture(rec)
	work.cache.seal()
	client := newScriptedASR()
	_, err := recognizeAttempt(context.Background(), work.cache, client, func() {})
	if err != nil {
		t.Fatal(err)
	}
	want := bytes.Repeat([]byte{2, 0}, 16000)
	if !bytes.Equal(client.sent, want) {
		t.Fatal("capture lost audio or applied gain twice during replay")
	}
	if work.cache.duration() != time.Second {
		t.Fatalf("audio duration = %v", work.cache.duration())
	}
}

func TestOutputRetryRetainsRecognizedTextAndStopsAfterSuccess(t *testing.T) {
	const text = "the complete recognized text"
	var outputs []string
	err := retryUntilSuccess(context.Background(), func() error {
		outputs = append(outputs, text)
		if len(outputs) < 3 {
			return errors.New("clipboard unavailable")
		}
		return nil
	}, func(int, error) <-chan struct{} { return pressedRetry() })
	if err != nil || len(outputs) != 3 {
		t.Fatalf("output attempts = %d, error = %v", len(outputs), err)
	}
	for _, got := range outputs {
		if got != text {
			t.Fatalf("output lost recognized text: %q", got)
		}
	}
}

func TestFinishCachesRecorderTailExactlyOnce(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	work := newRecognitionTask()
	work.cache.append([]byte{1, 0, 2, 0})
	close(work.captureDone)
	work.cancel() // no output or network needed to check capture shutdown
	rec := NewRecorder(logger, 2)
	rec.started = true
	rec.stdout = io.NopCloser(bytes.NewReader([]byte{3, 0, 4, 0}))
	p := &VoicePlugin{logger: logger, shutdown: true}
	p.finishRecordingSession(&recordingSession{sessionID: 1, recorder: rec, work: work, userStopped: true})
	got := make([]byte, 32)
	n, err := work.cache.read(context.Background(), nil, 0, got)
	if err != nil || !bytes.Equal(got[:n], []byte{1, 0, 2, 0, 6, 0, 8, 0}) {
		t.Fatalf("cached audio = %v, error = %v", got[:n], err)
	}
	if _, err := work.cache.read(context.Background(), nil, n, got); err != io.EOF {
		t.Fatalf("cache was not sealed after stopping: %v", err)
	}
}

func TestOutputClaimRejectsDuplicateCanceledAndShutdownSessions(t *testing.T) {
	p := &VoicePlugin{canceledSessions: map[uint64]struct{}{2: {}}}
	if !p.claimSessionOutput(1) || p.claimSessionOutput(1) {
		t.Fatal("session output was not claimed exactly once")
	}
	if p.claimSessionOutput(2) {
		t.Fatal("canceled session claimed output")
	}
	p.shutdown = true
	if p.claimSessionOutput(3) {
		t.Fatal("session claimed output during shutdown")
	}
}

func TestCanceledSessionCleanupDoesNotDecrementNewSessionCount(t *testing.T) {
	old := newRecognitionTask()
	defer old.cancel()
	newWork := newRecognitionTask()
	defer newWork.cancel()
	p := &VoicePlugin{
		shutdown:          true, // status updates are unrelated to the bookkeeping
		pendingDone:       1,
		finishingSessions: map[uint64]*recognitionTask{1: old, 2: newWork},
		canceledSessions:  map[uint64]struct{}{1: {}},
	}
	p.recordingSessionFinished(1)
	if p.pendingDone != 1 || len(p.finishingSessions) != 1 {
		t.Fatal("canceled session cleanup hid the newer pending recording")
	}
	p.recordingSessionFinished(2)
	if p.pendingDone != 0 || len(p.finishingSessions) != 0 {
		t.Fatal("completed session was not removed")
	}
}

func TestEscCancelsPendingRecovery(t *testing.T) {
	work := newRecognitionTask()
	defer work.cancel()
	p := &VoicePlugin{
		pendingDone:       1,
		finishingSessions: map[uint64]*recognitionTask{1: work},
	}
	p.cancelRecording()
	if work.ctx.Err() != context.Canceled || p.pendingDone != 0 {
		t.Fatal("Esc did not cancel pending recovery")
	}
	if p.claimSessionOutput(1) {
		t.Fatal("canceled recovery could still output text")
	}
}

func TestEscDoesNotWaitForRecorderStop(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	work := newRecognitionTask()
	defer work.cancel()
	close(work.captureDone)
	stopEntered := make(chan struct{})
	stopRelease := make(chan struct{})
	p := &VoicePlugin{
		logger: logger, recording: true, sessionID: 1, work: work,
		recorder: &Recorder{
			logger: logger, started: true,
			stopFunc: func() error {
				close(stopEntered)
				<-stopRelease
				return nil
			},
		},
	}
	returned := make(chan struct{})
	go func() { p.cancelRecording(); close(returned) }()
	defer close(stopRelease)
	select {
	case <-returned:
	case <-time.After(time.Second):
		t.Fatal("Esc blocked on recorder shutdown")
	}
	if work.ctx.Err() != context.Canceled {
		t.Fatal("Esc did not cancel the active recognition task")
	}
	select {
	case <-stopEntered:
	case <-time.After(time.Second):
		t.Fatal("Esc did not schedule recorder shutdown")
	}
}

// Only hotkey registration is needed by these plugin-level tests.
type retryTestEnv struct{ engine.PluginEnv }

func (retryTestEnv) RegisterHotkey(hotkey.Combo, func(hotkey.Event)) error { return nil }
func (retryTestEnv) UnregisterHotkey(hotkey.Combo) error                   { return nil }

func TestRPermitsOneRetryAndFailureWaitsAgain(t *testing.T) {
	work := newRecognitionTask()
	defer work.cancel()
	work.cache.append([]byte{1, 2, 3, 4})
	work.cache.seal()
	p := &VoicePlugin{
		env: retryTestEnv{}, logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		pendingDone: 1, finishingSessions: map[uint64]*recognitionTask{1: work},
	}
	attempts := make(chan int, 8)
	failures := make(chan struct{}, 8)
	done := make(chan recognitionResult, 1)
	var clients []*scriptedASR
	go func() {
		text, err := retryRecognition(work.ctx, work.cache, func() recognitionClient {
			c := newScriptedASR()
			if len(clients) < 2 {
				c.noFinal = true
			}
			clients = append(clients, c)
			attempts <- len(clients)
			return c
		}, func() {}, func(attempt int, err error) <-chan struct{} {
			request := p.setRetryStatus(1, work, attempt, err)
			failures <- struct{}{}
			return request
		})
		done <- recognitionResult{text: text, err: err}
	}()
	awaitFailure := func() {
		t.Helper()
		select {
		case <-failures:
		case <-time.After(time.Second):
			t.Fatal("no failed attempt")
		}
	}
	assertNoAttempt := func() {
		t.Helper()
		select {
		case n := <-attempts:
			t.Fatalf("unrequested attempt %d", n)
		case <-time.After(40 * time.Millisecond):
		}
	}
	awaitFailure()
	<-attempts
	if status := TUIStatus(); status.State != "waiting_retry" || !status.ErrorUntil.IsZero() {
		t.Fatalf("cached error must wait without expiration: %+v", status)
	}
	assertNoAttempt()
	p.onRetryHotkey(hotkey.Event{Type: hotkey.KeyUp})
	assertNoAttempt()
	p.onRetryHotkey(hotkey.Event{Type: hotkey.KeyDown})
	awaitFailure()
	if n := <-attempts; n != 2 {
		t.Fatalf("attempt=%d", n)
	}
	assertNoAttempt()
	p.onRetryHotkey(hotkey.Event{Type: hotkey.KeyDown})
	select {
	case result := <-done:
		if result.err != nil || result.text != "complete transcript" {
			t.Fatalf("result=%+v", result)
		}
	case <-time.After(time.Second):
		t.Fatal("R did not retry")
	}
	if n := <-attempts; n != 3 {
		t.Fatalf("attempt=%d", n)
	}
	for _, c := range clients {
		if !bytes.Equal(c.sent, []byte{1, 2, 3, 4}) {
			t.Fatalf("replay changed audio: %v", c.sent)
		}
	}
	p.onRetryHotkey(hotkey.Event{Type: hotkey.KeyDown})
	assertNoAttempt()
	p.cancelRecording()
}

func TestRWhileBusyCannotQueueFutureRetry(t *testing.T) {
	work := newRecognitionTask()
	defer work.cancel()
	p := &VoicePlugin{env: retryTestEnv{}, logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		pendingDone: 1, finishingSessions: map[uint64]*recognitionTask{1: work}}
	p.retryLastError() // still busy, so R must be ignored
	request := p.setRetryStatus(1, work, 1, errors.New("offline"))
	select {
	case <-request:
		t.Fatal("busy R press was queued for a later failure")
	default:
	}
	p.retryLastError()
	select {
	case <-request:
	default:
		t.Fatal("R did not release waiting retry")
	}
	p.retryLastError() // repeated press before another failure is ignored
	next := p.setRetryStatus(1, work, 2, errors.New("offline"))
	select {
	case <-next:
		t.Fatal("extra R press authorized another retry")
	default:
	}
	p.cancelRecording()
}

func TestEscInterruptsManualRetryWait(t *testing.T) {
	work := newRecognitionTask()
	defer work.cancel()
	p := &VoicePlugin{env: retryTestEnv{}, logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		pendingDone: 1, finishingSessions: map[uint64]*recognitionTask{1: work}}
	failed := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- retryUntilSuccess(work.ctx, func() error { return errors.New("offline") }, func(n int, err error) <-chan struct{} {
			request := p.setRetryStatus(1, work, n, err)
			close(failed)
			return request
		})
	}()
	select {
	case <-failed:
	case <-time.After(time.Second):
		t.Fatal("no failure")
	}
	p.cancelRecording()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("error=%v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Esc did not cancel retry wait")
	}
	p.retryLastError()
	if p.claimSessionOutput(1) {
		t.Fatal("canceled recording can still output")
	}
}

func TestCaptureContinuesWhileNativeStopFlushesAudio(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	reader, writer := io.Pipe()
	defer reader.Close()
	defer writer.Close()
	rec := NewRecorder(logger, 1)
	rec.stdout, rec.started = reader, true
	stopping := make(chan struct{})
	pcm := bytes.Repeat([]byte{1, 0}, 6400)
	rec.stopFunc = func() error {
		close(stopping)
		_, err := writer.Write(pcm)
		writer.Close()
		return err
	}
	work := newRecognitionTask()
	defer work.cancel()
	finished := make(chan []byte, 1)
	go func() { tail, _ := rec.Stop(); finished <- tail }()
	<-stopping
	// Native stop is waiting for a callback to flush its last buffer.
	go work.capture(rec)
	select {
	case tail := <-finished:
		<-work.captureDone
		work.cache.append(tail)
		if !bytes.Equal(work.cache.pcm, pcm) {
			t.Fatal("native stop lost audio")
		}
	case <-time.After(time.Second):
		writer.Close()
		<-finished
		<-work.captureDone
		t.Fatal("native stop blocked its own capture reader")
	}
}

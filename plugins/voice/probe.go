package voice

import (
	"log/slog"
	"sync"
	"time"
)

// ProbeInputLevel records briefly and reports the loudest 16-bit sample the
// input device produced. A muted or wrongly routed microphone records digital
// silence, which otherwise only surfaces as an empty transcript after a full
// recognition round trip.
func ProbeInputLevel(logger *slog.Logger, device string, d time.Duration) (int, error) {
	rec := NewRecorderWithDevice(logger, device, 1)
	if err := rec.Start(); err != nil {
		return 0, err
	}

	var mu sync.Mutex
	peak := 0
	observe := func(pcm []byte) {
		mu.Lock()
		defer mu.Unlock()
		if p := peakAmplitude(pcm); p > peak {
			peak = p
		}
	}

	// Stop unblocks the read below, so the probe cannot outlive a device that
	// never delivers samples.
	done := make(chan struct{})
	go func() {
		defer close(done)
		buf := make([]byte, 6400)
		for {
			n, err := rec.Read(buf)
			if n > 0 {
				observe(buf[:n])
			}
			if err != nil {
				return
			}
		}
	}()

	time.Sleep(d)
	remaining, err := rec.Stop()
	<-done
	observe(remaining)

	mu.Lock()
	defer mu.Unlock()
	return peak, err
}

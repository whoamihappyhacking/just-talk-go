//go:build windows

package overlay

import (
	"image"
	"image/png"
	"os"
	"testing"
	"time"

	"github.com/c/just-talk-go/config"
)

func TestWindowsOverlayIntegration(t *testing.T) {
	if os.Getenv("JUST_TALK_TEST_WINDOWS_OVERLAY") == "" {
		t.Skip("set JUST_TALK_TEST_WINDOWS_OVERLAY=1 to preview the Windows overlay")
	}

	backend, err := newBackend(config.OverlayConfig{Position: "bottom-center", Scale: 1})
	if err != nil {
		t.Fatalf("create Windows overlay: %v", err)
	}
	b := backend.(*windowsOverlayBackend)
	defer b.Close()
	if err := b.Show("REC", statusColor{R: 255 << 8, G: 65 << 8, B: 65 << 8}); err != nil {
		t.Fatalf("show Windows overlay: %v", err)
	}
	time.Sleep(250 * time.Millisecond)
	if err := b.lastRenderError(); err != nil {
		t.Fatalf("render Windows overlay: %v", err)
	}
	if previewPath := os.Getenv("JUST_TALK_WINDOWS_OVERLAY_PREVIEW"); previewPath != "" {
		writeWindowsOverlayPreview(t, b, previewPath)
	}
	time.Sleep(3 * time.Second)
}

func writeWindowsOverlayPreview(t *testing.T, b *windowsOverlayBackend, path string) {
	t.Helper()
	screenDC, _, err := procGetDC.Call(0)
	if screenDC == 0 {
		t.Fatalf("get screen DC for preview: %v", err)
	}
	defer procReleaseDC.Call(0, screenDC)

	pixels := make([]byte, int(b.width*b.height*4))
	b.mu.RLock()
	label, status := b.label, b.color
	b.mu.RUnlock()
	b.composeOverlay(screenDC, pixels, label, status)

	preview := image.NewNRGBA(image.Rect(0, 0, int(b.width), int(b.height)))
	background := [3]int{232, 235, 240}
	for y := int32(0); y < b.height; y++ {
		for x := int32(0); x < b.width; x++ {
			offset := int((y*b.width + x) * 4)
			alpha := int(pixels[offset+3])
			inverse := 255 - alpha
			preview.Pix[offset] = byte(int(pixels[offset+2]) + background[0]*inverse/255)
			preview.Pix[offset+1] = byte(int(pixels[offset+1]) + background[1]*inverse/255)
			preview.Pix[offset+2] = byte(int(pixels[offset]) + background[2]*inverse/255)
			preview.Pix[offset+3] = 255
		}
	}

	file, err := os.Create(path)
	if err != nil {
		t.Fatalf("create overlay preview: %v", err)
	}
	defer file.Close()
	if err := png.Encode(file, preview); err != nil {
		t.Fatalf("encode overlay preview: %v", err)
	}
}

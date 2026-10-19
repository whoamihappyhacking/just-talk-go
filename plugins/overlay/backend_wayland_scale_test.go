//go:build linux && !no_x11

package overlay

import (
	"os"
	"testing"

	"github.com/c/just-talk-go/config"
)

// Needs a live Wayland session, so it only runs where one exists. It proves
// the surface asks for a buffer at the output's scale instead of letting the
// compositor upscale a low-resolution one.
func TestWaylandBackendMatchesOutputScale(t *testing.T) {
	if os.Getenv("WAYLAND_DISPLAY") == "" {
		t.Skip("no Wayland session")
	}
	b, err := newWaylandBackend(config.OverlayConfig{Position: "bottom-center", Scale: 1.0})
	if err != nil {
		t.Skipf("Wayland overlay unavailable: %v", err)
	}
	defer b.Close()

	wb := b.(*waylandBackend)
	t.Logf("bufferScale=%d logical=%dx%d buffer=%dx%d pixelScale=%.2f",
		wb.bufferScale, wb.logicalW, wb.logicalH, wb.w, wb.h, wb.pixelScale())

	if wb.bufferScale < 1 {
		t.Fatalf("bufferScale = %d", wb.bufferScale)
	}
	if wb.w != wb.logicalW*wb.bufferScale || wb.h != wb.logicalH*wb.bufferScale {
		t.Fatalf("buffer %dx%d is not the logical size %dx%d at scale %d",
			wb.w, wb.h, wb.logicalW, wb.logicalH, wb.bufferScale)
	}
	if len(wb.data) != wb.w*wb.h*4 {
		t.Fatalf("buffer holds %d bytes, want %d", len(wb.data), wb.w*wb.h*4)
	}
}

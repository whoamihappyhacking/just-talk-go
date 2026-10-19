//go:build linux && !no_x11

package overlay

import (
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/c/just-talk-go/plugins/voice"
)

// Rendering is hard to review from a description, so this writes the capsule
// for every status to PNGs you can open. Opt-in: it is a design aid, not an
// assertion.
//
//	JUST_TALK_OVERLAY_PREVIEW=/tmp/preview go test ./plugins/overlay -run Preview
func TestCapsulePreview(t *testing.T) {
	dir := os.Getenv("JUST_TALK_OVERLAY_PREVIEW")
	if dir == "" {
		t.Skip("set JUST_TALK_OVERLAY_PREVIEW=<dir> to write capsule previews")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}

	const scale = 2 // match a HiDPI output, where the old path was upscaled
	states := []string{"idle", "connecting", "recording", "stopping_delayed", "stopping", "waiting_retry", "error"}
	for _, state := range states {
		label, sc, _ := displayForStatus(voice.TUIVoiceStatus{State: state}, true)
		dot := rgba{uint8(sc.R >> 8), uint8(sc.G >> 8), uint8(sc.B >> 8), 255}
		w, h := capsuleSize(float64(scale))
		canvas := newARGBCanvas(w, h)
		renderCapsule(canvas, w, h, label, dot)

		img := image.NewNRGBA(image.Rect(0, 0, w, h))
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				i := (y*w + x) * 4
				// The canvas is premultiplied BGRA; undo it for a PNG.
				a := canvas.data[i+3]
				b, g, r := canvas.data[i], canvas.data[i+1], canvas.data[i+2]
				if a > 0 {
					r = uint8(min(255, int(r)*255/int(a)))
					g = uint8(min(255, int(g)*255/int(a)))
					b = uint8(min(255, int(b)*255/int(a)))
				}
				img.SetNRGBA(x, y, color.NRGBA{R: r, G: g, B: b, A: a})
			}
		}
		out := filepath.Join(dir, state+".png")
		file, err := os.Create(out)
		if err != nil {
			t.Fatal(err)
		}
		if err := png.Encode(file, img); err != nil {
			file.Close()
			t.Fatal(err)
		}
		file.Close()
		t.Logf("wrote %s (%dx%d, label=%q)", out, w, h, label)
	}
}

//go:build linux && !no_x11

package overlay

import (
	"image"
	"sync"

	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/gomedium"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"
)

// Capsule geometry in logical pixels. The surface keeps this size at every
// output scale; only the pixel buffer behind it grows.
// Inner metrics are fractions of the pill's height, so proportions survive
// any resize. basePillH and overlay.scale are the only knobs, and the device
// pixel ratio is already folded into the height the renderer receives.
const (
	capsuleTextRatio = 0.44
	capsuleDotRatio  = 0.28
	capsuleGapRatio  = 0.21
	capsulePadRatio  = 0.34
)

var (
	capsuleBackground = rgba{24, 24, 27, 236}
	capsuleText       = rgba{244, 244, 245, 255}
)

// One face per pixel size. Shaping a face is far more expensive than drawing
// with it, and the overlay redraws on every status change.
var (
	faceMu    sync.Mutex
	faceCache = map[float64]font.Face{}
)

func capsuleFace(sizePx float64) font.Face {
	faceMu.Lock()
	defer faceMu.Unlock()
	if face, ok := faceCache[sizePx]; ok {
		return face
	}
	parsed, err := opentype.Parse(gomedium.TTF)
	if err != nil {
		return nil
	}
	face, err := opentype.NewFace(parsed, &opentype.FaceOptions{
		Size: sizePx, DPI: 72, Hinting: font.HintingFull,
	})
	if err != nil {
		return nil
	}
	faceCache[sizePx] = face
	return face
}

// renderCapsule paints the status pill into canvas at the given pixel size.
// scale is the device pixel ratio: every dimension is multiplied by it so the
// result is sharp on HiDPI outputs rather than upscaled by the compositor.
func renderCapsule(canvas *argbCanvas, w, h int, label string, dot rgba) {
	clear(canvas.data)

	radius := h / 2
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			if coverage := roundedRectCoverage(x, y, w, h, radius); coverage > 0 {
				c := capsuleBackground
				c.a = uint8(uint16(c.a) * uint16(coverage) / 255)
				canvas.setPixel(x, y, c)
			}
		}
	}

	dotSize := ratioPx(h, capsuleDotRatio)
	gap := ratioPx(h, capsuleGapRatio)
	face := capsuleFace(float64(h) * capsuleTextRatio)
	textW, textAscent, textDescent := measureCapsuleText(face, label)

	contentW := dotSize + gap + textW
	startX := (w - contentW) / 2
	if pad := ratioPx(h, capsulePadRatio); startX < pad {
		startX = pad
	}

	canvas.fillCircleAA(startX+dotSize/2, h/2, dotSize/2, dot)

	// Centre on the cap/descender band rather than the full line box: the
	// label is short uppercase text, so the line box leaves it sitting high.
	baseline := (h + textAscent - textDescent) / 2
	drawCapsuleText(canvas, face, startX+dotSize+gap, baseline, label, capsuleText)
}

func measureCapsuleText(face font.Face, s string) (width, ascent, descent int) {
	if face == nil {
		return 0, 0, 0
	}
	metrics := face.Metrics()
	return font.MeasureString(face, s).Round(), metrics.Ascent.Round(), metrics.Descent.Round()
}

// drawCapsuleText renders through an alpha mask so glyph antialiasing is
// preserved when compositing onto the translucent pill.
func drawCapsuleText(canvas *argbCanvas, face font.Face, x, baseline int, s string, c rgba) {
	if face == nil || s == "" {
		return
	}
	width, ascent, descent := measureCapsuleText(face, s)
	if width <= 0 {
		return
	}
	mask := image.NewAlpha(image.Rect(0, 0, width, ascent+descent))
	drawer := font.Drawer{
		Dst:  mask,
		Src:  image.White,
		Face: face,
		Dot:  fixed.P(0, ascent),
	}
	drawer.DrawString(s)

	for my := mask.Rect.Min.Y; my < mask.Rect.Max.Y; my++ {
		for mx := mask.Rect.Min.X; mx < mask.Rect.Max.X; mx++ {
			coverage := mask.AlphaAt(mx, my).A
			if coverage == 0 {
				continue
			}
			canvas.blendPixel(x+mx, baseline-ascent+my, c, coverage)
		}
	}
}

// capsuleSize is the pill's pixel buffer size at a given device pixel ratio.
// The logical size stays constant so the pill looks the same physical size on
// every output; only its resolution changes.
func capsuleSize(scale float64) (w, h int) {
	return scalePx(basePillW, scale), scalePx(basePillH, scale)
}

func scalePx(v int, scale float64) int {
	if scale <= 0 {
		scale = 1
	}
	return int(float64(v)*scale + 0.5)
}

func ratioPx(h int, ratio float64) int {
	return int(float64(h)*ratio + 0.5)
}

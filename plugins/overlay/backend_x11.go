//go:build linux && !no_x11

package overlay

// #cgo LDFLAGS: -lX11 -lXext
// #include <X11/Xlib.h>
// #include <X11/Xatom.h>
// #include <X11/extensions/shape.h>
// #include <stdlib.h>
//
// static void set_override_redirect(Display *dpy, Window win) {
//     XSetWindowAttributes attrs;
//     attrs.override_redirect = True;
//     attrs.save_under = True;
//     XChangeWindowAttributes(dpy, win, CWOverrideRedirect | CWSaveUnder, &attrs);
// }
//
// static void set_empty_input_shape(Display *dpy, Window win) {
//     XShapeCombineRectangles(dpy, win, ShapeInput, 0, 0, NULL, 0, ShapeSet, Unsorted);
// }
//
// static void set_window_type_utility(Display *dpy, Window win) {
//     Atom type = XInternAtom(dpy, "_NET_WM_WINDOW_TYPE", False);
//     Atom utility = XInternAtom(dpy, "_NET_WM_WINDOW_TYPE_UTILITY", False);
//     XChangeProperty(dpy, win, type, XA_ATOM, 32, PropModeReplace, (unsigned char*)&utility, 1);
// }
import "C"

import (
	"fmt"
	"os"
	"strings"
	"unsafe"

	"github.com/c/just-talk-go/config"
)

const (
	basePillW  = 122
	basePillH  = 42
	baseMargin = 28
)

type x11Backend struct {
	dpy      *C.Display
	win      C.Window
	gc       C.GC
	mask     C.Pixmap
	screen   C.int
	visible  bool
	position string
	scale    float64
	w        int
	h        int
	margin   int
}

func newX11Backend(cfg config.OverlayConfig) (backend, error) {
	if os.Getenv("DISPLAY") == "" {
		return nil, fmt.Errorf("DISPLAY is not set")
	}
	dpy := C.XOpenDisplay(nil)
	if dpy == nil {
		return nil, fmt.Errorf("cannot open X display")
	}
	scale := cfg.Scale
	if scale <= 0 {
		scale = 1.0
	}
	b := &x11Backend{dpy: dpy, screen: C.XDefaultScreen(dpy), position: cfg.Position, scale: scale}
	b.w = b.scaled(basePillW)
	b.h = b.scaled(basePillH)
	b.margin = b.scaled(baseMargin)
	if b.position == "" {
		b.position = "top-right"
	}
	root := C.XRootWindow(dpy, b.screen)
	black := C.XBlackPixel(dpy, b.screen)
	b.win = C.XCreateSimpleWindow(dpy, root, 0, 0, C.uint(b.w), C.uint(b.h), 0, black, black)
	if b.win == 0 {
		C.XCloseDisplay(dpy)
		return nil, fmt.Errorf("cannot create overlay window")
	}
	C.set_override_redirect(dpy, b.win)
	C.set_window_type_utility(dpy, b.win)
	b.gc = C.XCreateGC(dpy, C.Drawable(b.win), 0, nil)
	b.applyShape()
	C.set_empty_input_shape(dpy, b.win)
	b.move()
	C.XFlush(dpy)
	return b, nil
}

func (b *x11Backend) Show(label string, color statusColor) error {
	if b.dpy == nil {
		return nil
	}
	b.move()
	b.draw(label, color)
	if !b.visible {
		C.XMapRaised(b.dpy, b.win)
		b.visible = true
	} else {
		C.XRaiseWindow(b.dpy, b.win)
	}
	C.XFlush(b.dpy)
	return nil
}

func (b *x11Backend) Hide() error {
	if b.dpy == nil || !b.visible {
		return nil
	}
	C.XUnmapWindow(b.dpy, b.win)
	C.XFlush(b.dpy)
	b.visible = false
	return nil
}

func (b *x11Backend) Close() error {
	if b.dpy == nil {
		return nil
	}
	if b.mask != 0 {
		C.XFreePixmap(b.dpy, b.mask)
	}
	if b.gc != nil {
		C.XFreeGC(b.dpy, b.gc)
	}
	if b.win != 0 {
		C.XDestroyWindow(b.dpy, b.win)
	}
	C.XCloseDisplay(b.dpy)
	b.dpy = nil
	return nil
}

func (b *x11Backend) move() {
	sw := int(C.XDisplayWidth(b.dpy, b.screen))
	sh := int(C.XDisplayHeight(b.dpy, b.screen))
	x, y := sw-b.w-b.margin, b.margin
	switch strings.ToLower(b.position) {
	case "top-left":
		x, y = b.margin, b.margin
	case "top-center":
		x, y = (sw-b.w)/2, b.margin
	case "bottom-left":
		x, y = b.margin, sh-b.h-b.margin
	case "bottom-center":
		x, y = (sw-b.w)/2, sh-b.h-b.margin
	case "bottom-right":
		x, y = sw-b.w-b.margin, sh-b.h-b.margin
	}
	if x < 0 {
		x = 0
	}
	if y < 0 {
		y = 0
	}
	C.XMoveWindow(b.dpy, b.win, C.int(x), C.int(y))
}

func (b *x11Backend) draw(label string, color statusColor) {
	bg := b.alloc(20<<8, 20<<8, 20<<8)
	fg := b.alloc(245<<8, 245<<8, 245<<8)
	dot := b.alloc(color.R, color.G, color.B)

	C.XSetForeground(b.dpy, b.gc, bg)
	C.XFillRectangle(b.dpy, C.Drawable(b.win), b.gc, 0, 0, C.uint(b.w), C.uint(b.h))
	C.XSetForeground(b.dpy, b.gc, dot)
	dotSize := b.scaled(14)
	dotX := b.scaled(20)
	dotY := (b.h - dotSize) / 2
	C.XFillArc(b.dpy, C.Drawable(b.win), b.gc, C.int(dotX), C.int(dotY), C.uint(dotSize), C.uint(dotSize), 0, 360*64)
	C.XSetForeground(b.dpy, b.gc, fg)
	textScale := b.scaled(3)
	textW := bitmapTextWidth(label, textScale)
	textH := 7 * textScale
	textX := dotX + dotSize + b.scaled(14)
	textY := (b.h - textH) / 2
	if maxX := b.w - b.scaled(14) - textW; textX > maxX {
		textX = maxX
	}
	drawBitmapText(b, textX, textY, label, textScale)
}

func (b *x11Backend) alloc(r, g, bl uint16) C.ulong {
	cmap := C.XDefaultColormap(b.dpy, b.screen)
	color := C.XColor{red: C.ushort(r), green: C.ushort(g), blue: C.ushort(bl)}
	if C.XAllocColor(b.dpy, cmap, &color) == 0 {
		return C.XWhitePixel(b.dpy, b.screen)
	}
	return color.pixel
}

func (b *x11Backend) applyShape() {
	data := roundedMask(b.w, b.h, b.h/2)
	ptr := (*C.char)(unsafe.Pointer(&data[0]))
	b.mask = C.XCreateBitmapFromData(b.dpy, C.Drawable(b.win), ptr, C.uint(b.w), C.uint(b.h))
	if b.mask == 0 {
		return
	}
	C.XShapeCombineMask(b.dpy, b.win, C.ShapeBounding, 0, 0, b.mask, C.ShapeSet)
}

func (b *x11Backend) scaled(v int) int {
	n := int(float64(v)*b.scale + 0.5)
	if n < 1 {
		return 1
	}
	return n
}

func drawBitmapText(b *x11Backend, x, y int, s string, scale int) {
	for _, r := range strings.ToUpper(s) {
		glyph, ok := glyphs[r]
		if !ok {
			x += 4 * scale
			continue
		}
		for row, bits := range glyph {
			for col := 0; col < 5; col++ {
				if bits&(1<<(4-col)) == 0 {
					continue
				}
				C.XFillRectangle(
					b.dpy,
					C.Drawable(b.win),
					b.gc,
					C.int(x+col*scale),
					C.int(y+row*scale),
					C.uint(scale),
					C.uint(scale),
				)
			}
		}
		x += 6 * scale
	}
}

func bitmapTextWidth(s string, scale int) int {
	if len(s) == 0 {
		return 0
	}
	return (len(s)*6 - 1) * scale
}

var glyphs = map[rune][7]byte{
	'C': {0b01110, 0b10001, 0b10000, 0b10000, 0b10000, 0b10001, 0b01110},
	'D': {0b11110, 0b10001, 0b10001, 0b10001, 0b10001, 0b10001, 0b11110},
	'E': {0b11111, 0b10000, 0b10000, 0b11110, 0b10000, 0b10000, 0b11111},
	'I': {0b11111, 0b00100, 0b00100, 0b00100, 0b00100, 0b00100, 0b11111},
	'N': {0b10001, 0b11001, 0b10101, 0b10011, 0b10001, 0b10001, 0b10001},
	'O': {0b01110, 0b10001, 0b10001, 0b10001, 0b10001, 0b10001, 0b01110},
	'P': {0b11110, 0b10001, 0b10001, 0b11110, 0b10000, 0b10000, 0b10000},
	'R': {0b11110, 0b10001, 0b10001, 0b11110, 0b10100, 0b10010, 0b10001},
	'S': {0b01111, 0b10000, 0b10000, 0b01110, 0b00001, 0b00001, 0b11110},
	'T': {0b11111, 0b00100, 0b00100, 0b00100, 0b00100, 0b00100, 0b00100},
	'W': {0b10001, 0b10001, 0b10001, 0b10101, 0b10101, 0b10101, 0b01010},
}

func roundedMask(w, h, r int) []byte {
	stride := (w + 7) / 8
	data := make([]byte, stride*h)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			if insideRoundedRect(x, y, w, h, r) {
				data[y*stride+x/8] |= 1 << uint(x%8)
			}
		}
	}
	return data
}

func insideRoundedRect(x, y, w, h, r int) bool {
	if x >= r && x < w-r {
		return true
	}
	cx := r
	if x >= w-r {
		cx = w - r - 1
	}
	cy := r
	if y >= h/2 {
		cy = h - r - 1
	}
	dx, dy := x-cx, y-cy
	return dx*dx+dy*dy <= r*r
}

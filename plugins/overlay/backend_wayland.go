//go:build linux && !no_x11

package overlay

/*
#cgo LDFLAGS: -lwayland-client
#include <stdint.h>
#include <stdlib.h>
#include <string.h>
#include <wayland-client.h>
#include <wayland-client-protocol.h>
#include "xdg-shell-client-protocol.h"
#include "wlr-layer-shell-unstable-v1-client-protocol.h"

extern void goOverlayRegistryGlobal(uintptr_t handle, struct wl_registry *registry, uint32_t name, char *interface, uint32_t version);
extern void goOverlayLayerConfigure(uintptr_t handle, struct zwlr_layer_surface_v1 *surface, uint32_t serial, uint32_t width, uint32_t height);
extern void goOverlayLayerClosed(uintptr_t handle, struct zwlr_layer_surface_v1 *surface);

static void registry_global_cb(void *data, struct wl_registry *registry, uint32_t name, const char *interface, uint32_t version) {
	goOverlayRegistryGlobal((uintptr_t)data, registry, name, (char*)interface, version);
}

static void registry_remove_cb(void *data, struct wl_registry *registry, uint32_t name) {
}

static const struct wl_registry_listener registry_listener = {
	.global = registry_global_cb,
	.global_remove = registry_remove_cb,
};

static void add_registry_listener(struct wl_registry *registry, uintptr_t handle) {
	wl_registry_add_listener(registry, &registry_listener, (void*)handle);
}

static void layer_configure_cb(void *data, struct zwlr_layer_surface_v1 *surface, uint32_t serial, uint32_t width, uint32_t height) {
	goOverlayLayerConfigure((uintptr_t)data, surface, serial, width, height);
}

static void layer_closed_cb(void *data, struct zwlr_layer_surface_v1 *surface) {
	goOverlayLayerClosed((uintptr_t)data, surface);
}

static const struct zwlr_layer_surface_v1_listener layer_surface_listener = {
	.configure = layer_configure_cb,
	.closed = layer_closed_cb,
};

static void add_layer_surface_listener(struct zwlr_layer_surface_v1 *surface, uintptr_t handle) {
	zwlr_layer_surface_v1_add_listener(surface, &layer_surface_listener, (void*)handle);
}

static void *bind_wl_compositor(struct wl_registry *registry, uint32_t name, uint32_t version) {
	if (version > 4) version = 4;
	return wl_registry_bind(registry, name, &wl_compositor_interface, version);
}

static void *bind_wl_shm(struct wl_registry *registry, uint32_t name, uint32_t version) {
	if (version > 1) version = 1;
	return wl_registry_bind(registry, name, &wl_shm_interface, version);
}

static void *bind_layer_shell(struct wl_registry *registry, uint32_t name, uint32_t version) {
	if (version > 4) version = 4;
	return wl_registry_bind(registry, name, &zwlr_layer_shell_v1_interface, version);
}
*/
import "C"

import (
	"fmt"
	"os"
	"runtime/cgo"
	"strings"
	"unsafe"

	"github.com/c/just-talk-go/config"
	"golang.org/x/sys/unix"
)

type waylandBackend struct {
	display      *C.struct_wl_display
	registry     *C.struct_wl_registry
	compositor   *C.struct_wl_compositor
	shm          *C.struct_wl_shm
	layerShell   *C.struct_zwlr_layer_shell_v1
	surface      *C.struct_wl_surface
	layerSurface *C.struct_zwlr_layer_surface_v1
	pool         *C.struct_wl_shm_pool
	buffer       *C.struct_wl_buffer
	data         []byte
	file         *os.File
	handle       cgo.Handle
	configured   bool
	closed       bool
	visible      bool
	position     string
	scale        float64
	w            int
	h            int
	margin       int
}

func newWaylandBackend(cfg config.OverlayConfig) (backend, error) {
	display := C.wl_display_connect(nil)
	if display == nil {
		return nil, fmt.Errorf("cannot connect to Wayland display")
	}
	scale := cfg.Scale
	if scale <= 0 {
		scale = 1.0
	}
	b := &waylandBackend{display: display, position: cfg.Position, scale: scale}
	b.w = b.scaled(basePillW)
	b.h = b.scaled(basePillH)
	b.margin = b.scaled(baseMargin)
	if b.position == "" {
		b.position = "top-right"
	}
	b.handle = cgo.NewHandle(b)
	b.registry = C.wl_display_get_registry(display)
	C.add_registry_listener(b.registry, C.uintptr_t(b.handle))
	C.wl_display_roundtrip(display)
	if b.compositor == nil {
		b.Close()
		return nil, fmt.Errorf("Wayland compositor global not found")
	}
	if b.shm == nil {
		b.Close()
		return nil, fmt.Errorf("Wayland shm global not found")
	}
	if b.layerShell == nil {
		b.Close()
		return nil, fmt.Errorf("wlr-layer-shell protocol is not supported by this compositor")
	}
	if err := b.createSurface(); err != nil {
		b.Close()
		return nil, err
	}
	if err := b.createBuffer(); err != nil {
		b.Close()
		return nil, err
	}
	return b, nil
}

func (b *waylandBackend) Show(label string, color statusColor) error {
	if b.display == nil || b.closed {
		return nil
	}
	b.draw(label, color)
	C.wl_surface_attach(b.surface, b.buffer, 0, 0)
	C.wl_surface_damage_buffer(b.surface, 0, 0, C.int32_t(b.w), C.int32_t(b.h))
	C.wl_surface_commit(b.surface)
	C.wl_display_flush(b.display)
	b.visible = true
	return nil
}

func (b *waylandBackend) Hide() error {
	if b.display == nil || !b.visible {
		return nil
	}
	clear(b.data)
	C.wl_surface_attach(b.surface, b.buffer, 0, 0)
	C.wl_surface_damage_buffer(b.surface, 0, 0, C.int32_t(b.w), C.int32_t(b.h))
	C.wl_surface_commit(b.surface)
	C.wl_display_flush(b.display)
	b.visible = false
	return nil
}

func (b *waylandBackend) Close() error {
	if b.buffer != nil {
		C.wl_buffer_destroy(b.buffer)
		b.buffer = nil
	}
	if b.pool != nil {
		C.wl_shm_pool_destroy(b.pool)
		b.pool = nil
	}
	if b.data != nil {
		_ = unix.Munmap(b.data)
		b.data = nil
	}
	if b.file != nil {
		_ = b.file.Close()
		b.file = nil
	}
	if b.layerSurface != nil {
		C.zwlr_layer_surface_v1_destroy(b.layerSurface)
		b.layerSurface = nil
	}
	if b.surface != nil {
		C.wl_surface_destroy(b.surface)
		b.surface = nil
	}
	if b.layerShell != nil {
		C.zwlr_layer_shell_v1_destroy(b.layerShell)
		b.layerShell = nil
	}
	if b.shm != nil {
		C.wl_shm_destroy(b.shm)
		b.shm = nil
	}
	if b.compositor != nil {
		C.wl_compositor_destroy(b.compositor)
		b.compositor = nil
	}
	if b.registry != nil {
		C.wl_registry_destroy(b.registry)
		b.registry = nil
	}
	if b.display != nil {
		C.wl_display_disconnect(b.display)
		b.display = nil
	}
	if b.handle != 0 {
		b.handle.Delete()
		b.handle = 0
	}
	return nil
}

func (b *waylandBackend) createSurface() error {
	b.surface = C.wl_compositor_create_surface(b.compositor)
	if b.surface == nil {
		return fmt.Errorf("cannot create Wayland surface")
	}
	ns := C.CString("just-talk-overlay")
	defer C.free(unsafe.Pointer(ns))
	b.layerSurface = C.zwlr_layer_shell_v1_get_layer_surface(
		b.layerShell,
		b.surface,
		nil,
		C.ZWLR_LAYER_SHELL_V1_LAYER_OVERLAY,
		ns,
	)
	if b.layerSurface == nil {
		return fmt.Errorf("cannot create layer-shell surface")
	}
	C.add_layer_surface_listener(b.layerSurface, C.uintptr_t(b.handle))
	C.zwlr_layer_surface_v1_set_size(b.layerSurface, C.uint32_t(b.w), C.uint32_t(b.h))
	C.zwlr_layer_surface_v1_set_anchor(b.layerSurface, C.uint32_t(b.anchor()))
	top, right, bottom, left := b.margins()
	C.zwlr_layer_surface_v1_set_margin(b.layerSurface, C.int32_t(top), C.int32_t(right), C.int32_t(bottom), C.int32_t(left))
	C.zwlr_layer_surface_v1_set_keyboard_interactivity(b.layerSurface, C.ZWLR_LAYER_SURFACE_V1_KEYBOARD_INTERACTIVITY_NONE)
	C.zwlr_layer_surface_v1_set_exclusive_zone(b.layerSurface, 0)
	region := C.wl_compositor_create_region(b.compositor)
	C.wl_surface_set_input_region(b.surface, region)
	C.wl_region_destroy(region)
	C.wl_surface_commit(b.surface)
	C.wl_display_roundtrip(b.display)
	if !b.configured {
		return fmt.Errorf("layer-shell surface was not configured")
	}
	return nil
}

func (b *waylandBackend) createBuffer() error {
	size := b.w * b.h * 4
	f, err := os.CreateTemp("", "just-talk-overlay-*.shm")
	if err != nil {
		return err
	}
	_ = os.Remove(f.Name())
	if err := f.Truncate(int64(size)); err != nil {
		f.Close()
		return err
	}
	data, err := unix.Mmap(int(f.Fd()), 0, size, unix.PROT_READ|unix.PROT_WRITE, unix.MAP_SHARED)
	if err != nil {
		f.Close()
		return err
	}
	b.file = f
	b.data = data
	b.pool = C.wl_shm_create_pool(b.shm, C.int32_t(f.Fd()), C.int32_t(size))
	if b.pool == nil {
		return fmt.Errorf("cannot create Wayland shm pool")
	}
	b.buffer = C.wl_shm_pool_create_buffer(b.pool, 0, C.int32_t(b.w), C.int32_t(b.h), C.int32_t(b.w*4), C.WL_SHM_FORMAT_ARGB8888)
	if b.buffer == nil {
		return fmt.Errorf("cannot create Wayland shm buffer")
	}
	return nil
}

func (b *waylandBackend) draw(label string, color statusColor) {
	clear(b.data)
	bg := rgba{20, 20, 20, 215}
	fg := rgba{245, 245, 245, 255}
	dot := rgba{uint8(color.R >> 8), uint8(color.G >> 8), uint8(color.B >> 8), 255}
	radius := b.h / 2
	for y := 0; y < b.h; y++ {
		for x := 0; x < b.w; x++ {
			if insideRoundedRect(x, y, b.w, b.h, radius) {
				b.setPixel(x, y, bg)
			}
		}
	}
	dotSize := b.scaled(14)
	gap := b.scaled(14)
	textScale := b.scaled(3)
	textW := bitmapTextWidth(label, textScale)
	contentW := dotSize + gap + textW
	dotX := (b.w - contentW) / 2
	if dotX < 0 {
		dotX = 0
	}
	dotY := (b.h - dotSize) / 2
	b.fillCircleAA(dotX+dotSize/2, dotY+dotSize/2, dotSize/2, dot)
	textH := 7 * textScale
	textX := dotX + dotSize + gap
	textY := (b.h - textH) / 2
	if maxX := b.w - b.scaled(14) - textW; textX > maxX {
		textX = maxX
	}
	b.drawText(textX, textY, label, textScale, fg)
}

type rgba struct {
	r, g, b, a uint8
}

func (b *waylandBackend) setPixel(x, y int, c rgba) {
	if x < 0 || y < 0 || x >= b.w || y >= b.h {
		return
	}
	i := (y*b.w + x) * 4
	a := uint16(c.a)
	b.data[i+0] = uint8(uint16(c.b) * a / 255)
	b.data[i+1] = uint8(uint16(c.g) * a / 255)
	b.data[i+2] = uint8(uint16(c.r) * a / 255)
	b.data[i+3] = c.a
}

func (b *waylandBackend) blendPixel(x, y int, c rgba, coverage uint8) {
	if x < 0 || y < 0 || x >= b.w || y >= b.h || coverage == 0 {
		return
	}
	i := (y*b.w + x) * 4
	a := uint16(coverage)
	inv := uint16(255 - coverage)
	b.data[i+0] = uint8((uint16(c.b)*a + uint16(b.data[i+0])*inv) / 255)
	b.data[i+1] = uint8((uint16(c.g)*a + uint16(b.data[i+1])*inv) / 255)
	b.data[i+2] = uint8((uint16(c.r)*a + uint16(b.data[i+2])*inv) / 255)
	b.data[i+3] = 255
}

func (b *waylandBackend) fillCircleAA(cx, cy, r int, c rgba) {
	rr := r * r * 16
	for y := cy - r; y <= cy+r; y++ {
		for x := cx - r; x <= cx+r; x++ {
			inside := 0
			for sy := 0; sy < 4; sy++ {
				for sx := 0; sx < 4; sx++ {
					dx := (x-cx)*4 + sx - 1
					dy := (y-cy)*4 + sy - 1
					if dx*dx+dy*dy <= rr {
						inside++
					}
				}
			}
			if inside > 0 {
				b.blendPixel(x, y, c, uint8(inside*255/16))
			}
		}
	}
}

func (b *waylandBackend) drawText(x, y int, s string, scale int, c rgba) {
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
				for yy := 0; yy < scale; yy++ {
					for xx := 0; xx < scale; xx++ {
						b.setPixel(x+col*scale+xx, y+row*scale+yy, c)
					}
				}
			}
		}
		x += 6 * scale
	}
}

func (b *waylandBackend) anchor() int {
	switch strings.ToLower(b.position) {
	case "top-left":
		return C.ZWLR_LAYER_SURFACE_V1_ANCHOR_TOP | C.ZWLR_LAYER_SURFACE_V1_ANCHOR_LEFT
	case "top-center":
		return C.ZWLR_LAYER_SURFACE_V1_ANCHOR_TOP
	case "bottom-left":
		return C.ZWLR_LAYER_SURFACE_V1_ANCHOR_BOTTOM | C.ZWLR_LAYER_SURFACE_V1_ANCHOR_LEFT
	case "bottom-center":
		return C.ZWLR_LAYER_SURFACE_V1_ANCHOR_BOTTOM
	case "bottom-right":
		return C.ZWLR_LAYER_SURFACE_V1_ANCHOR_BOTTOM | C.ZWLR_LAYER_SURFACE_V1_ANCHOR_RIGHT
	default:
		return C.ZWLR_LAYER_SURFACE_V1_ANCHOR_TOP | C.ZWLR_LAYER_SURFACE_V1_ANCHOR_RIGHT
	}
}

func (b *waylandBackend) margins() (top, right, bottom, left int) {
	switch strings.ToLower(b.position) {
	case "top-left":
		return b.margin, 0, 0, b.margin
	case "top-center":
		return b.margin, 0, 0, 0
	case "bottom-left":
		return 0, 0, b.margin, b.margin
	case "bottom-center":
		return 0, 0, b.margin, 0
	case "bottom-right":
		return 0, b.margin, b.margin, 0
	default:
		return b.margin, b.margin, 0, 0
	}
}

func (b *waylandBackend) scaled(v int) int {
	n := int(float64(v)*b.scale + 0.5)
	if n < 1 {
		return 1
	}
	return n
}

//export goOverlayRegistryGlobal
func goOverlayRegistryGlobal(handle C.uintptr_t, registry *C.struct_wl_registry, name C.uint32_t, iface *C.char, version C.uint32_t) {
	h := cgo.Handle(handle)
	b, ok := h.Value().(*waylandBackend)
	if !ok {
		return
	}
	switch C.GoString(iface) {
	case "wl_compositor":
		b.compositor = (*C.struct_wl_compositor)(C.bind_wl_compositor(registry, name, version))
	case "wl_shm":
		b.shm = (*C.struct_wl_shm)(C.bind_wl_shm(registry, name, version))
	case "zwlr_layer_shell_v1":
		b.layerShell = (*C.struct_zwlr_layer_shell_v1)(C.bind_layer_shell(registry, name, version))
	}
}

//export goOverlayLayerConfigure
func goOverlayLayerConfigure(handle C.uintptr_t, surface *C.struct_zwlr_layer_surface_v1, serial C.uint32_t, width C.uint32_t, height C.uint32_t) {
	h := cgo.Handle(handle)
	b, ok := h.Value().(*waylandBackend)
	if !ok {
		return
	}
	C.zwlr_layer_surface_v1_ack_configure(surface, serial)
	b.configured = true
}

//export goOverlayLayerClosed
func goOverlayLayerClosed(handle C.uintptr_t, surface *C.struct_zwlr_layer_surface_v1) {
	h := cgo.Handle(handle)
	b, ok := h.Value().(*waylandBackend)
	if !ok {
		return
	}
	b.closed = true
}

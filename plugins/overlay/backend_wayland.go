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
extern void goOverlayOutputScale(uintptr_t handle, int32_t factor);

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

// The compositor reports each output's scale; the largest one decides the
// buffer resolution so the pill stays sharp wherever it is shown.
static void output_geometry_cb(void *data, struct wl_output *output, int32_t x, int32_t y,
		int32_t pw, int32_t ph, int32_t subpixel, const char *make, const char *model, int32_t transform) {
}

static void output_mode_cb(void *data, struct wl_output *output, uint32_t flags,
		int32_t width, int32_t height, int32_t refresh) {
}

static void output_done_cb(void *data, struct wl_output *output) {
}

static void output_scale_cb(void *data, struct wl_output *output, int32_t factor) {
	goOverlayOutputScale((uintptr_t)data, factor);
}

static const struct wl_output_listener output_listener = {
	.geometry = output_geometry_cb,
	.mode = output_mode_cb,
	.done = output_done_cb,
	.scale = output_scale_cb,
};

static void *bind_wl_output(struct wl_registry *registry, uint32_t name, uint32_t version, uintptr_t handle) {
	if (version > 2) version = 2;
	struct wl_output *output = wl_registry_bind(registry, name, &wl_output_interface, version);
	if (output && version >= 2) wl_output_add_listener(output, &output_listener, (void*)handle);
	return output;
}
*/
import "C"

import (
	"fmt"
	"os"
	"runtime/cgo"
	"strings"
	"sync"
	"unsafe"

	"github.com/c/just-talk-go/config"
	"golang.org/x/sys/unix"
)

type waylandBackend struct {
	mu           sync.Mutex
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
	destroyed    bool
	visible      bool
	position     string
	scale        float64
	// Surface size in logical pixels, and the pixel buffer behind it. They
	// differ by bufferScale on a HiDPI output; without that split the
	// compositor upscales a low-resolution buffer and the pill looks blurry.
	logicalW    int
	logicalH    int
	bufferScale int
	w           int
	h           int
	margin      int
}

// pixelScale is how many buffer pixels the capsule gets per design pixel.
func (b *waylandBackend) pixelScale() float64 {
	return b.scale * float64(b.bufferScale)
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
	b := &waylandBackend{display: display, position: cfg.Position, scale: scale, bufferScale: 1}
	b.logicalW = b.scaled(basePillW)
	b.logicalH = b.scaled(basePillH)
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
	// Output scale events only arrive after wl_output is bound, so the first
	// roundtrip delivers the globals and this one delivers their scales.
	C.wl_display_roundtrip(display)
	b.w = b.logicalW * b.bufferScale
	b.h = b.logicalH * b.bufferScale
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
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.display == nil || b.closed || b.destroyed {
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
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.display == nil || b.closed || b.destroyed || !b.visible {
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
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.destroyed {
		return nil
	}
	b.destroyed = true
	b.closed = true
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
	C.wl_surface_set_buffer_scale(b.surface, C.int32_t(b.bufferScale))
	C.zwlr_layer_surface_v1_set_size(b.layerSurface, C.uint32_t(b.logicalW), C.uint32_t(b.logicalH))
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
	dot := rgba{uint8(color.R >> 8), uint8(color.G >> 8), uint8(color.B >> 8), 255}
	canvas := &argbCanvas{w: b.w, h: b.h, data: b.data}
	renderCapsule(canvas, b.w, b.h, label, dot)
}

type rgba struct {
	r, g, b, a uint8
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
	case "wl_output":
		C.bind_wl_output(registry, name, version, C.uintptr_t(handle))
	}
}

//export goOverlayOutputScale
func goOverlayOutputScale(handle C.uintptr_t, factor C.int32_t) {
	b, ok := cgo.Handle(handle).Value().(*waylandBackend)
	if !ok || factor < 1 {
		return
	}
	// Taking the largest scale keeps the pill sharp on a mixed-DPI setup; a
	// buffer with more detail than an output needs only costs downscaling.
	if int(factor) > b.bufferScale {
		b.bufferScale = int(factor)
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
	b.mu.Lock()
	defer b.mu.Unlock()
	b.configured = true
}

//export goOverlayLayerClosed
func goOverlayLayerClosed(handle C.uintptr_t, surface *C.struct_zwlr_layer_surface_v1) {
	h := cgo.Handle(handle)
	b, ok := h.Value().(*waylandBackend)
	if !ok {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.closed = true
}

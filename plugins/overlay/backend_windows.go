//go:build windows

package overlay

import (
	"fmt"
	"math"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"github.com/c/just-talk-go/config"
	"golang.org/x/sys/windows"
)

var (
	overlayUser32              = windows.NewLazySystemDLL("user32.dll")
	overlayGdi32               = windows.NewLazySystemDLL("gdi32.dll")
	overlayKernel32            = windows.NewLazySystemDLL("kernel32.dll")
	procRegisterClassExW       = overlayUser32.NewProc("RegisterClassExW")
	procCreateWindowExW        = overlayUser32.NewProc("CreateWindowExW")
	procDefWindowProcW         = overlayUser32.NewProc("DefWindowProcW")
	procDestroyWindow          = overlayUser32.NewProc("DestroyWindow")
	procShowWindow             = overlayUser32.NewProc("ShowWindow")
	procSetWindowPos           = overlayUser32.NewProc("SetWindowPos")
	procUpdateLayeredWindow    = overlayUser32.NewProc("UpdateLayeredWindow")
	procPostMessageW           = overlayUser32.NewProc("PostMessageW")
	procGetMessageOverlayW     = overlayUser32.NewProc("GetMessageW")
	procTranslateMessage       = overlayUser32.NewProc("TranslateMessage")
	procDispatchMessageW       = overlayUser32.NewProc("DispatchMessageW")
	procPostQuitMessage        = overlayUser32.NewProc("PostQuitMessage")
	procBeginPaint             = overlayUser32.NewProc("BeginPaint")
	procEndPaint               = overlayUser32.NewProc("EndPaint")
	procSystemParametersInfoW  = overlayUser32.NewProc("SystemParametersInfoW")
	procSetTimer               = overlayUser32.NewProc("SetTimer")
	procKillTimer              = overlayUser32.NewProc("KillTimer")
	procGetDC                  = overlayUser32.NewProc("GetDC")
	procReleaseDC              = overlayUser32.NewProc("ReleaseDC")
	procDrawTextW              = overlayUser32.NewProc("DrawTextW")
	procCreateCompatibleDC     = overlayGdi32.NewProc("CreateCompatibleDC")
	procDeleteDC               = overlayGdi32.NewProc("DeleteDC")
	procCreateDIBSection       = overlayGdi32.NewProc("CreateDIBSection")
	procSelectObject           = overlayGdi32.NewProc("SelectObject")
	procDeleteObject           = overlayGdi32.NewProc("DeleteObject")
	procCreateFontW            = overlayGdi32.NewProc("CreateFontW")
	procSetTextColor           = overlayGdi32.NewProc("SetTextColor")
	procSetBkColor             = overlayGdi32.NewProc("SetBkColor")
	procSetBkMode              = overlayGdi32.NewProc("SetBkMode")
	procGetModuleHandleOverlay = overlayKernel32.NewProc("GetModuleHandleW")
)

const (
	wmDestroy          = 0x0002
	wmPaint            = 0x000F
	wmEraseBkgnd       = 0x0014
	wmNCHitTest        = 0x0084
	wmTimer            = 0x0113
	wmAppShow          = 0x8001
	wmAppHide          = 0x8002
	wmAppClose         = 0x8003
	htTransparent      = ^uintptr(0)
	overlayTimerID     = 1
	overlayTimerMs     = 90
	overlaySupersample = 3
	wsPopup            = 0x80000000
	wsExTopmost        = 0x00000008
	wsExTransparent    = 0x00000020
	wsExToolWindow     = 0x00000080
	wsExLayered        = 0x00080000
	wsExNoActivate     = 0x08000000
	swHide             = 0
	swShowNoActivate   = 8
	swpNoActivate      = 0x0010
	swpShowWindow      = 0x0040
	spiGetWorkArea     = 0x0030
	ulwAlpha           = 0x00000002
	acSrcAlpha         = 0x01
	biRGB              = 0
	dibRGBColors       = 0
	transparentMode    = 1
	dtCenter           = 0x00000001
	dtVCenter          = 0x00000004
	dtSingleLine       = 0x00000020
	dtNoPrefix         = 0x00000800
	fontWeightSemiBold = 600
	defaultCharset     = 1
	antialiasedQuality = 4
)

type overlayRect struct {
	Left, Top, Right, Bottom int32
}

type overlayPaintStruct struct {
	HDC         windows.Handle
	Erase       int32
	Paint       overlayRect
	Restore     int32
	IncUpdate   int32
	RGBReserved [32]byte
}

type overlayWindowClass struct {
	Size       uint32
	Style      uint32
	WndProc    uintptr
	ClsExtra   int32
	WndExtra   int32
	Instance   windows.Handle
	Icon       windows.Handle
	Cursor     windows.Handle
	Background windows.Handle
	MenuName   *uint16
	ClassName  *uint16
	IconSmall  windows.Handle
}

type overlayPoint struct {
	X int32
	Y int32
}

type overlaySize struct {
	CX int32
	CY int32
}

type overlayMessage struct {
	Window  windows.Handle
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	Point   overlayPoint
	Private uint32
}

type overlayBlendFunction struct {
	BlendOp             byte
	BlendFlags          byte
	SourceConstantAlpha byte
	AlphaFormat         byte
}

type overlayBitmapInfoHeader struct {
	Size          uint32
	Width         int32
	Height        int32
	Planes        uint16
	BitCount      uint16
	Compression   uint32
	SizeImage     uint32
	XPelsPerMeter int32
	YPelsPerMeter int32
	ClrUsed       uint32
	ClrImportant  uint32
}

type overlayBitmapInfo struct {
	Header overlayBitmapInfoHeader
	Colors [1]uint32
}

type windowsOverlayBackend struct {
	mu            sync.RWMutex
	hwnd          windows.Handle
	label         string
	color         statusColor
	position      string
	scale         float64
	width         int32
	height        int32
	contentWidth  int32
	contentHeight int32
	shadowPad     int32
	renderErr     error
	closed        bool
	done          chan struct{}
}

var (
	activeWindowsOverlayMu sync.RWMutex
	activeWindowsOverlay   *windowsOverlayBackend
	windowsOverlayWndProc  = syscall.NewCallback(windowsOverlayWindowProc)
)

func newBackend(cfg config.OverlayConfig) (backend, error) {
	scale := cfg.Scale
	if scale <= 0 {
		scale = 1
	}
	contentWidth := overlayScaled(128, scale, 116)
	contentHeight := overlayScaled(42, scale, 38)
	shadowPad := overlayScaled(10, scale, 8)
	b := &windowsOverlayBackend{
		position:      cfg.Position,
		scale:         scale,
		contentWidth:  contentWidth,
		contentHeight: contentHeight,
		shadowPad:     shadowPad,
		width:         contentWidth + shadowPad*2,
		height:        contentHeight + shadowPad*2,
		done:          make(chan struct{}),
	}
	ready := make(chan error, 1)
	go b.run(ready)
	if err := <-ready; err != nil {
		return nil, err
	}
	return b, nil
}

func (b *windowsOverlayBackend) Show(label string, color statusColor) error {
	b.mu.Lock()
	b.label = label
	b.color = color
	hwnd := b.hwnd
	closed := b.closed
	b.mu.Unlock()
	if closed || hwnd == 0 {
		return nil
	}
	return postOverlayMessage(hwnd, wmAppShow)
}

func (b *windowsOverlayBackend) Hide() error {
	b.mu.RLock()
	hwnd := b.hwnd
	closed := b.closed
	b.mu.RUnlock()
	if closed || hwnd == 0 {
		return nil
	}
	return postOverlayMessage(hwnd, wmAppHide)
}

func (b *windowsOverlayBackend) Close() error {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return nil
	}
	b.closed = true
	hwnd := b.hwnd
	b.mu.Unlock()
	if hwnd != 0 {
		if err := postOverlayMessage(hwnd, wmAppClose); err != nil {
			return err
		}
		<-b.done
	}
	return nil
}

func (b *windowsOverlayBackend) run(ready chan<- error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	defer close(b.done)

	instance, _, _ := procGetModuleHandleOverlay.Call(0)
	className, _ := windows.UTF16PtrFromString("JustTalkOverlayWindow")
	class := overlayWindowClass{
		Size:      uint32(unsafe.Sizeof(overlayWindowClass{})),
		WndProc:   windowsOverlayWndProc,
		Instance:  windows.Handle(instance),
		ClassName: className,
	}
	if atom, _, err := procRegisterClassExW.Call(uintptr(unsafe.Pointer(&class))); atom == 0 {
		const classAlreadyExists = syscall.Errno(1410)
		if err != classAlreadyExists {
			ready <- fmt.Errorf("register Windows overlay class: %w", err)
			return
		}
	}

	style := uintptr(wsExTopmost | wsExTransparent | wsExToolWindow | wsExLayered | wsExNoActivate)
	hwnd, _, err := procCreateWindowExW.Call(
		style,
		uintptr(unsafe.Pointer(className)), uintptr(unsafe.Pointer(className)),
		wsPopup, 0, 0, uintptr(b.width), uintptr(b.height),
		0, 0, instance, 0,
	)
	if hwnd == 0 {
		ready <- fmt.Errorf("create Windows overlay: %w", err)
		return
	}
	b.mu.Lock()
	b.hwnd = windows.Handle(hwnd)
	b.mu.Unlock()
	activeWindowsOverlayMu.Lock()
	activeWindowsOverlay = b
	activeWindowsOverlayMu.Unlock()
	ready <- nil

	var msg overlayMessage
	for {
		result, _, _ := procGetMessageOverlayW.Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0)
		if result == 0 || result == ^uintptr(0) {
			break
		}
		procTranslateMessage.Call(uintptr(unsafe.Pointer(&msg)))
		procDispatchMessageW.Call(uintptr(unsafe.Pointer(&msg)))
	}

	activeWindowsOverlayMu.Lock()
	if activeWindowsOverlay == b {
		activeWindowsOverlay = nil
	}
	activeWindowsOverlayMu.Unlock()
	b.mu.Lock()
	b.hwnd = 0
	b.mu.Unlock()
}

func windowsOverlayWindowProc(hwnd uintptr, msg uint32, wParam, lParam uintptr) uintptr {
	activeWindowsOverlayMu.RLock()
	b := activeWindowsOverlay
	activeWindowsOverlayMu.RUnlock()
	switch msg {
	case wmAppShow:
		if b != nil {
			b.positionWindow(hwnd)
			b.setRenderError(b.render(hwnd))
			procSetTimer.Call(hwnd, overlayTimerID, overlayTimerMs, 0)
			procShowWindow.Call(hwnd, swShowNoActivate)
		}
		return 0
	case wmAppHide:
		procKillTimer.Call(hwnd, overlayTimerID)
		procShowWindow.Call(hwnd, swHide)
		return 0
	case wmAppClose:
		procKillTimer.Call(hwnd, overlayTimerID)
		procDestroyWindow.Call(hwnd)
		return 0
	case wmTimer:
		if b != nil && wParam == overlayTimerID {
			b.setRenderError(b.render(hwnd))
		}
		return 0
	case wmPaint:
		var ps overlayPaintStruct
		if hdc, _, _ := procBeginPaint.Call(hwnd, uintptr(unsafe.Pointer(&ps))); hdc != 0 {
			procEndPaint.Call(hwnd, uintptr(unsafe.Pointer(&ps)))
		}
		return 0
	case wmEraseBkgnd:
		return 1
	case wmNCHitTest:
		return htTransparent
	case wmDestroy:
		procPostQuitMessage.Call(0)
		return 0
	default:
		result, _, _ := procDefWindowProcW.Call(hwnd, uintptr(msg), wParam, lParam)
		return result
	}
}

func (b *windowsOverlayBackend) positionWindow(hwnd uintptr) {
	var work overlayRect
	if ok, _, _ := procSystemParametersInfoW.Call(spiGetWorkArea, 0, uintptr(unsafe.Pointer(&work)), 0); ok == 0 {
		work = overlayRect{Right: 1920, Bottom: 1080}
	}
	margin := int32(24 * b.scale)
	x := work.Right - b.width - margin
	y := work.Top + margin
	switch strings.ToLower(strings.TrimSpace(b.position)) {
	case "top-left":
		x = work.Left + margin
	case "top-center":
		x = work.Left + (work.Right-work.Left-b.width)/2
	case "bottom-left":
		x = work.Left + margin
		y = work.Bottom - b.height - margin
	case "bottom-center", "":
		x = work.Left + (work.Right-work.Left-b.width)/2
		y = work.Bottom - b.height - margin
	case "bottom-right":
		y = work.Bottom - b.height - margin
	}
	procSetWindowPos.Call(hwnd, ^uintptr(0), uintptr(x), uintptr(y), uintptr(b.width), uintptr(b.height), swpNoActivate|swpShowWindow)
}

func (b *windowsOverlayBackend) setRenderError(err error) {
	b.mu.Lock()
	b.renderErr = err
	b.mu.Unlock()
}

func (b *windowsOverlayBackend) lastRenderError() error {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.renderErr
}

func (b *windowsOverlayBackend) render(hwnd uintptr) error {
	b.mu.RLock()
	label := b.label
	color := b.color
	width, height := b.width, b.height
	b.mu.RUnlock()

	screenDC, _, err := procGetDC.Call(0)
	if screenDC == 0 {
		return fmt.Errorf("get Windows overlay screen DC: %w", err)
	}
	defer procReleaseDC.Call(0, screenDC)

	memoryDC, _, err := procCreateCompatibleDC.Call(screenDC)
	if memoryDC == 0 {
		return fmt.Errorf("create Windows overlay memory DC: %w", err)
	}
	defer procDeleteDC.Call(memoryDC)

	bitmap, bits, err := createOverlayDIB(screenDC, width, height)
	if err != nil {
		return err
	}
	oldBitmap, _, _ := procSelectObject.Call(memoryDC, bitmap)
	defer func() {
		procSelectObject.Call(memoryDC, oldBitmap)
		procDeleteObject.Call(bitmap)
	}()

	destination := unsafe.Slice((*byte)(unsafe.Pointer(bits)), int(width*height*4))
	b.composeOverlay(screenDC, destination, label, color)

	size := overlaySize{CX: width, CY: height}
	source := overlayPoint{}
	blend := overlayBlendFunction{SourceConstantAlpha: 255, AlphaFormat: acSrcAlpha}
	if ok, _, err := procUpdateLayeredWindow.Call(
		hwnd, screenDC, 0, uintptr(unsafe.Pointer(&size)), memoryDC,
		uintptr(unsafe.Pointer(&source)), 0, uintptr(unsafe.Pointer(&blend)), ulwAlpha,
	); ok == 0 {
		return fmt.Errorf("update Windows overlay: %w", err)
	}
	return nil
}

func createOverlayDIB(hdc uintptr, width, height int32) (uintptr, uintptr, error) {
	bmi := overlayBitmapInfo{Header: overlayBitmapInfoHeader{
		Size:        uint32(unsafe.Sizeof(overlayBitmapInfoHeader{})),
		Width:       width,
		Height:      -height,
		Planes:      1,
		BitCount:    32,
		Compression: biRGB,
	}}
	var bits uintptr
	bitmap, _, err := procCreateDIBSection.Call(
		hdc, uintptr(unsafe.Pointer(&bmi)), dibRGBColors,
		uintptr(unsafe.Pointer(&bits)), 0, 0,
	)
	if bitmap == 0 || bits == 0 {
		return 0, 0, fmt.Errorf("create Windows overlay bitmap: %w", err)
	}
	return bitmap, bits, nil
}

func (b *windowsOverlayBackend) composeOverlay(screenDC uintptr, destination []byte, label string, color statusColor) {
	const ss = int32(overlaySupersample)
	highWidth := b.width * ss
	highHeight := b.height * ss
	high := make([]byte, int(highWidth*highHeight*4))
	b.drawOverlayShapes(high, highWidth, highHeight, color, ss)
	b.drawOverlayText(screenDC, high, highWidth, label, ss)
	downsampleOverlay(destination, b.width, b.height, high, highWidth, ss)
}

func (b *windowsOverlayBackend) drawOverlayShapes(pixels []byte, pixelWidth, pixelHeight int32, color statusColor, ss int32) {
	pad := b.shadowPad * ss
	x, y := pad, pad
	w, h := b.contentWidth*ss, b.contentHeight*ss
	radius := h / 2

	shadowScale := int32(math.Round(b.scale))
	if shadowScale < 1 {
		shadowScale = 1
	}
	shadowLayers := []struct {
		expand int32
		alpha  uint8
	}{
		{8 * shadowScale * ss, 3},
		{6 * shadowScale * ss, 5},
		{4 * shadowScale * ss, 8},
		{2 * shadowScale * ss, 12},
		{1 * shadowScale * ss, 14},
	}
	for _, layer := range shadowLayers {
		expand := layer.expand
		drawRoundedRect(pixels, pixelWidth, pixelHeight,
			x-expand/2, y+2*shadowScale*ss-expand/2,
			w+expand, h+expand, radius+expand/2,
			overlayARGB(layer.alpha, 0, 0, 0),
		)
	}

	drawRoundedRect(pixels, pixelWidth, pixelHeight, x, y, w, h, radius, overlayARGB(246, 20, 23, 29))
	stroke := maxInt32(1, int32(math.Round(b.scale))) * ss
	drawRoundedStroke(pixels, pixelWidth, pixelHeight, x, y, w, h, radius, stroke, overlayARGB(34, 255, 255, 255))

	r := uint8(color.R >> 8)
	g := uint8(color.G >> 8)
	bl := uint8(color.B >> 8)
	waveAreaX := (b.shadowPad + overlayScaled(10, b.scale, 9)) * ss
	waveAreaY := (b.shadowPad + overlayScaled(7, b.scale, 6)) * ss
	waveAreaW := overlayScaled(49, b.scale, 45) * ss
	waveAreaH := (b.contentHeight - overlayScaled(14, b.scale, 12)) * ss
	drawRoundedRect(pixels, pixelWidth, pixelHeight, waveAreaX, waveAreaY, waveAreaW, waveAreaH, waveAreaH/2, overlayARGB(20, r, g, bl))

	centerY := (b.shadowPad + b.contentHeight/2) * ss
	barWidth := overlayScaled(3, b.scale, 3) * ss
	barGap := overlayScaled(4, b.scale, 3) * ss
	barStart := waveAreaX + (waveAreaW-(barWidth*5+barGap*4))/2
	maxHeight := (b.contentHeight - overlayScaled(21, b.scale, 18)) * ss
	minHeight := overlayScaled(7, b.scale, 6) * ss
	phase := float64(time.Now().UnixMilli()%1800) / 1800 * math.Pi * 2
	profiles := []float64{0.40, 0.70, 1.00, 0.72, 0.44}
	for i, profile := range profiles {
		motion := 0.78 + 0.22*math.Sin(phase+float64(i)*0.92)
		barHeight := minHeight + int32(float64(maxHeight-minHeight)*profile*motion)
		if barHeight < barWidth {
			barHeight = barWidth
		}
		barX := barStart + int32(i)*(barWidth+barGap)
		alpha := uint8(205 + 12*i)
		if i > 2 {
			alpha = uint8(245 - 12*(i-2))
		}
		drawRoundedRect(pixels, pixelWidth, pixelHeight, barX, centerY-barHeight/2, barWidth, barHeight, barWidth/2, overlayARGB(alpha, r, g, bl))
	}

	dividerX := (b.shadowPad + overlayScaled(10, b.scale, 9) + overlayScaled(49, b.scale, 45) + overlayScaled(7, b.scale, 6)) * ss
	dividerH := overlayScaled(14, b.scale, 12) * ss
	dividerW := maxInt32(1, int32(math.Round(b.scale))) * ss
	drawRoundedRect(pixels, pixelWidth, pixelHeight, dividerX, centerY-dividerH/2, dividerW, dividerH, dividerW/2, overlayARGB(24, 255, 255, 255))
}

func (b *windowsOverlayBackend) drawOverlayText(screenDC uintptr, pixels []byte, pixelWidth int32, label string, ss int32) {
	textLeft := (b.shadowPad + overlayScaled(10, b.scale, 9) + overlayScaled(49, b.scale, 45) + overlayScaled(14, b.scale, 12)) * ss
	textRight := (b.shadowPad + b.contentWidth - overlayScaled(9, b.scale, 8)) * ss
	textTop := b.shadowPad * ss
	textWidth := textRight - textLeft
	textHeight := b.contentHeight * ss
	if textWidth <= 0 || textHeight <= 0 {
		return
	}

	textDC, _, _ := procCreateCompatibleDC.Call(screenDC)
	if textDC == 0 {
		return
	}
	defer procDeleteDC.Call(textDC)
	bitmap, bits, err := createOverlayDIB(screenDC, textWidth, textHeight)
	if err != nil {
		return
	}
	oldBitmap, _, _ := procSelectObject.Call(textDC, bitmap)
	defer func() {
		procSelectObject.Call(textDC, oldBitmap)
		procDeleteObject.Call(bitmap)
	}()
	mask := unsafe.Slice((*byte)(unsafe.Pointer(bits)), int(textWidth*textHeight*4))
	clear(mask)

	fontName, _ := windows.UTF16PtrFromString("Segoe UI Semibold")
	fontHeight := overlayScaled(11, b.scale, 10) * ss
	font, _, _ := procCreateFontW.Call(
		uintptr(uint32(-fontHeight)), 0, 0, 0, fontWeightSemiBold,
		0, 0, 0, defaultCharset, 0, 0, antialiasedQuality, 0,
		uintptr(unsafe.Pointer(fontName)),
	)
	if font == 0 {
		return
	}
	oldFont, _, _ := procSelectObject.Call(textDC, font)
	defer func() {
		procSelectObject.Call(textDC, oldFont)
		procDeleteObject.Call(font)
	}()
	procSetBkMode.Call(textDC, transparentMode)
	procSetBkColor.Call(textDC, overlayColorRef(0, 0, 0))
	procSetTextColor.Call(textDC, overlayColorRef(255, 255, 255))
	textRect := overlayRect{Right: textWidth, Bottom: textHeight}
	text, _ := windows.UTF16FromString(windowsOverlayLabel(label))
	procDrawTextW.Call(
		textDC, uintptr(unsafe.Pointer(&text[0])), uintptr(len(text)-1),
		uintptr(unsafe.Pointer(&textRect)), dtCenter|dtVCenter|dtSingleLine|dtNoPrefix,
	)

	textColor := overlayARGB(232, 244, 246, 250)
	for py := int32(0); py < textHeight; py++ {
		for px := int32(0); px < textWidth; px++ {
			maskOffset := int((py*textWidth + px) * 4)
			coverage := maxByte(mask[maskOffset], maxByte(mask[maskOffset+1], mask[maskOffset+2]))
			if coverage == 0 {
				continue
			}
			targetX := textLeft + px
			targetY := textTop + py
			targetOffset := int((targetY*pixelWidth + targetX) * 4)
			blendOverlayPixel(pixels, targetOffset, textColor, coverage)
		}
	}
}

func drawRoundedRect(pixels []byte, pixelWidth, pixelHeight, x, y, width, height, radius int32, color uint32) {
	left := maxInt32(0, x)
	top := maxInt32(0, y)
	right := minInt32(pixelWidth, x+width)
	bottom := minInt32(pixelHeight, y+height)
	for py := top; py < bottom; py++ {
		for px := left; px < right; px++ {
			if !insideRoundedRect(px, py, x, y, width, height, radius) {
				continue
			}
			offset := int((py*pixelWidth + px) * 4)
			blendOverlayPixel(pixels, offset, color, 255)
		}
	}
}

func drawRoundedStroke(pixels []byte, pixelWidth, pixelHeight, x, y, width, height, radius, stroke int32, color uint32) {
	if stroke < 1 {
		return
	}
	innerX, innerY := x+stroke, y+stroke
	innerWidth, innerHeight := width-stroke*2, height-stroke*2
	innerRadius := radius - stroke
	if innerRadius < 0 {
		innerRadius = 0
	}
	left := maxInt32(0, x)
	top := maxInt32(0, y)
	right := minInt32(pixelWidth, x+width)
	bottom := minInt32(pixelHeight, y+height)
	for py := top; py < bottom; py++ {
		for px := left; px < right; px++ {
			if !insideRoundedRect(px, py, x, y, width, height, radius) {
				continue
			}
			if innerWidth > 0 && innerHeight > 0 && insideRoundedRect(px, py, innerX, innerY, innerWidth, innerHeight, innerRadius) {
				continue
			}
			offset := int((py*pixelWidth + px) * 4)
			blendOverlayPixel(pixels, offset, color, 255)
		}
	}
}

func insideRoundedRect(px, py, x, y, width, height, radius int32) bool {
	if px < x || py < y || px >= x+width || py >= y+height {
		return false
	}
	maxRadius := minInt32(width, height) / 2
	if radius > maxRadius {
		radius = maxRadius
	}
	if radius <= 0 {
		return true
	}
	px2 := int64(px*2 + 1)
	py2 := int64(py*2 + 1)
	leftCenter := int64((x + radius) * 2)
	rightCenter := int64((x + width - radius) * 2)
	topCenter := int64((y + radius) * 2)
	bottomCenter := int64((y + height - radius) * 2)
	nearestX := clampInt64(px2, leftCenter, rightCenter)
	nearestY := clampInt64(py2, topCenter, bottomCenter)
	dx, dy := px2-nearestX, py2-nearestY
	diameter := int64(radius * 2)
	return dx*dx+dy*dy <= diameter*diameter
}

func blendOverlayPixel(pixels []byte, offset int, color uint32, coverage uint8) {
	alpha := int(uint8(color>>24)) * int(coverage) / 255
	if alpha <= 0 {
		return
	}
	red := int(uint8(color >> 16))
	green := int(uint8(color >> 8))
	blue := int(uint8(color))
	inverse := 255 - alpha
	pixels[offset] = byte(minInt(255, blue*alpha/255+int(pixels[offset])*inverse/255))
	pixels[offset+1] = byte(minInt(255, green*alpha/255+int(pixels[offset+1])*inverse/255))
	pixels[offset+2] = byte(minInt(255, red*alpha/255+int(pixels[offset+2])*inverse/255))
	pixels[offset+3] = byte(minInt(255, alpha+int(pixels[offset+3])*inverse/255))
}

func downsampleOverlay(destination []byte, width, height int32, source []byte, sourceWidth, scale int32) {
	count := int(scale * scale)
	for y := int32(0); y < height; y++ {
		for x := int32(0); x < width; x++ {
			var sums [4]int
			for sy := int32(0); sy < scale; sy++ {
				for sx := int32(0); sx < scale; sx++ {
					sourceOffset := int((((y*scale + sy) * sourceWidth) + (x*scale + sx)) * 4)
					for channel := 0; channel < 4; channel++ {
						sums[channel] += int(source[sourceOffset+channel])
					}
				}
			}
			destinationOffset := int((y*width + x) * 4)
			for channel := 0; channel < 4; channel++ {
				destination[destinationOffset+channel] = byte(sums[channel] / count)
			}
		}
	}
}

func windowsOverlayLabel(label string) string {
	switch strings.ToUpper(strings.TrimSpace(label)) {
	case "CON":
		return "LINK"
	case "REC":
		return "REC"
	case "STP":
		return "STOP"
	case "WAI":
		return "WAIT"
	case "ERR":
		return "ERR"
	case "IDL":
		return "IDLE"
	default:
		return label
	}
}

func overlayScaled(value int32, scale float64, minimum int32) int32 {
	scaled := int32(math.Round(float64(value) * scale))
	if scaled < minimum {
		return minimum
	}
	return scaled
}

func overlayARGB(alpha, red, green, blue uint8) uint32 {
	return uint32(alpha)<<24 | uint32(red)<<16 | uint32(green)<<8 | uint32(blue)
}

func overlayColorRef(red, green, blue uint8) uintptr {
	return uintptr(uint32(red) | uint32(green)<<8 | uint32(blue)<<16)
}

func clampInt64(value, minimum, maximum int64) int64 {
	if value < minimum {
		return minimum
	}
	if value > maximum {
		return maximum
	}
	return value
}

func minInt32(a, b int32) int32 {
	if a < b {
		return a
	}
	return b
}

func maxInt32(a, b int32) int32 {
	if a > b {
		return a
	}
	return b
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func maxByte(a, b byte) byte {
	if a > b {
		return a
	}
	return b
}

func postOverlayMessage(hwnd windows.Handle, message uint32) error {
	if ok, _, err := procPostMessageW.Call(uintptr(hwnd), uintptr(message), 0, 0); ok == 0 {
		return fmt.Errorf("post Windows overlay message: %w", err)
	}
	return nil
}

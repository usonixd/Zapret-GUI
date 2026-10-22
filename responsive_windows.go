//go:build windows

package main

import (
	"fmt"
	"math"
	"unsafe"
)

const (
	WM_SIZE        = 0x0005
	WM_LBUTTONDOWN = 0x0201
	WM_DPICHANGED  = 0x02E0
)

var (
	controlRect    = make(map[uintptr]rect)
	baseFonts      = make(map[uintptr]fontSpec)
	controlFonts   = make(map[uintptr]fontSpec)
	generatedFonts = make(map[string]uintptr)
	priorScale     = 0.0
	pMoveWindow    = u32.NewProc("MoveWindow")
	pSetProcessDpi = u32.NewProc("SetProcessDPIAware")
)

type fontSpec struct {
	size, weight int32
	name         string
}

func registerControlRect(h uintptr, x, y, w, hgt int32) {
	if h != 0 {
		controlRect[h] = rect{x, y, w, hgt}
	}
}
func registerBaseFont(h uintptr, size, weight int32, name string) {
	if h != 0 {
		baseFonts[h] = fontSpec{size, weight, name}
	}
}
func noteControlFont(h, f uintptr) {
	if h != 0 {
		if spec, exists := baseFonts[f]; exists {
			controlFonts[h] = spec
		}
	}
}
func setDPIAwareness() {
	// Per-monitor V2 when supported. Fallback for older Windows.
	p := u32.NewProc("SetProcessDpiAwarenessContext")
	if err := p.Find(); err == nil {
		if call(p, ^uintptr(3)) != 0 {
			return
		} // DPI_AWARENESS_CONTEXT_PER_MONITOR_AWARE_V2 (-4)
	}
	call(pSetProcessDpi)
}
func layoutFont(s fontSpec, factor float64) uintptr {
	sz := int32(math.Round(float64(s.size) * factor))
	if sz < 10 {
		sz = 10
	}
	key := fmt.Sprintf("%s:%d:%d", s.name, s.weight, sz)
	if h := generatedFonts[key]; h != 0 {
		return h
	}
	h := call(pFont, uintptr(uint32(-sz)), 0, 0, 0, uintptr(s.weight), 0, 0, 0, 1, 0, 0, 5, 0, uintptr(unsafe.Pointer(ptr(s.name))))
	if h != 0 {
		generatedFonts[key] = h
	}
	return h
}
func applyResponsiveLayout(clientW, clientH int32) {
	if clientW <= 0 || clientH <= 0 || len(controlRect) == 0 {
		return
	}
	const baseW, baseH = 944.0, 751.0
	scale := math.Min(float64(clientW)/baseW, float64(clientH)/baseH)
	if scale > 1.55 {
		scale = 1.55
	}
	if scale < 0.69 {
		scale = 0.69
	}
	offsetX := (float64(clientW) - baseW*scale) / 2
	offsetY := (float64(clientH) - baseH*scale) / 2
	// Center the entire panel, rather than leaving controls stranded in a corner.
	for h, r := range controlRect {
		// Offscreen hidden native combo is only used internally for storing selections.
		if r.left < -1000 {
			continue
		}
		x := int32(math.Round(offsetX + float64(r.left)*scale))
		y := int32(math.Round(offsetY + float64(r.top)*scale))
		w := int32(math.Round(float64(r.right) * scale))
		hh := int32(math.Round(float64(r.bottom) * scale))
		if w <= 0 || hh <= 0 {
			continue
		}
		call(pMoveWindow, h, uintptr(int32(x)), uintptr(int32(y)), uintptr(int32(w)), uintptr(int32(hh)), 1)
	}
	if math.Abs(scale-priorScale) >= 0.035 {
		priorScale = scale
		for h, r := range controlRect {
			if r.left < -1000 {
				continue
			}
			spec, ok := controlFonts[h]
			if !ok {
				spec, ok = baseFonts[font]
			}
			if !ok {
				continue
			}
			f := layoutFont(spec, scale)
			if f != 0 {
				send(h, WM_SETFONT, f, 1)
			}
		}
	}
}

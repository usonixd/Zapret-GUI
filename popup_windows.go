//go:build windows

package main

import (
	"math"
	"strings"
	"syscall"
	"unsafe"
)

const (
	WM_PAINT          = 0x000F
	WM_ACTIVATEAPP    = 0x001C
	WS_POPUP          = 0x80000000
	WS_EX_TOOLWINDOW  = 0x00000080
	SW_SHOWNOACTIVATE = 4
	SWP_NOACTIVATE    = 0x0010
	SWP_SHOWWINDOW    = 0x0040
)

type popHelp struct {
	hwnd  uintptr
	text  string
	title string
	id    int
}
type winPaint struct {
	hdc                uintptr
	erase              int32
	rc                 rect
	restore, incUpdate int32
	reserved           [32]byte
}

var (
	helpPop        popHelp
	helpControls   = map[uintptr]string{}
	helpActiveID   int
	popupClassName *uint16
	pBeginPaint    = u32.NewProc("BeginPaint")
	pEndPaint      = u32.NewProc("EndPaint")
	pSetWindowPos  = u32.NewProc("SetWindowPos")
	pWindowRect    = u32.NewProc("GetWindowRect")
	pGetCtlID      = u32.NewProc("GetDlgCtrlID")
	pIsVisible     = u32.NewProc("IsWindowVisible")
	pPaintLineTo   = gdi.NewProc("LineTo")
	pPaintMove     = gdi.NewProc("MoveToEx")
)

func registerHelpButton(hwnd uintptr, text string) { helpControls[hwnd] = text }
func helpTitle(id int) string {
	switch id {
	case ID_HELP_GAME:
		return "Game Filter"
	case ID_HELP_PORTS:
		return "TCP / UDP порты"
	case ID_HELP_STRATEGY:
		return "Стратегия обхода"
	case ID_HELP_AUTOTEST:
		return "Автоподбор"
	case ID_HELP_SITES:
		return "Списки сайтов"
	case ID_HELP_AUTORUN:
		return "Автозапуск"
	case ID_HELP_UPDATES:
		return "Обновление ядра"
	}
	return "Подсказка"
}
func initContextPopup() {
	popupClassName = ptr("ZapretDeskDarkHelpPopupV07")
	cls := wndClass{size: uint32(unsafe.Sizeof(wndClass{})), wndProc: syscall.NewCallback(popupWndProc), instance: hinst, cursor: call(pLoadCursor, 0, 32512), background: pageBrush, className: popupClassName}
	if call(pRegisterClass, uintptr(unsafe.Pointer(&cls))) == 0 {
		return
	}
	// Popup is owned by the main window, so it never gets its own taskbar button.
	helpPop.hwnd = call(pCreateWindow, WS_EX_TOOLWINDOW, uintptr(unsafe.Pointer(popupClassName)), 0,
		WS_POPUP, 0, 0, 414, 162, hwndMain, 0, hinst, 0)
	call(pShow, helpPop.hwnd, SW_HIDE)
}
func hideContextHint() {
	if helpPop.hwnd != 0 {
		call(pShow, helpPop.hwnd, SW_HIDE)
	}
	helpActiveID = 0
}
func showContextHint(fallback string) {
	if helpPop.hwnd == 0 {
		return
	}
	id := helpActiveID
	if id == 0 {
		return
	}
	var anchor uintptr
	for h := range helpControls {
		if int(call(pGetCtlID, h)) == id && call(pIsVisible, h) != 0 {
			anchor = h
			break
		}
	}
	if anchor == 0 {
		return
	}
	// Clicking the same help button toggles the popover.
	if helpPop.id == id && call(pIsVisible, helpPop.hwnd) != 0 {
		hideContextHint()
		return
	}
	txt := helpControls[anchor]
	if strings.TrimSpace(txt) == "" {
		txt = fallback
	}
	helpPop.id = id
	helpPop.title = helpTitle(id)
	helpPop.text = txt
	var hrect, mrect rect
	call(pWindowRect, anchor, uintptr(unsafe.Pointer(&hrect)))
	call(pWindowRect, hwndMain, uintptr(unsafe.Pointer(&mrect)))
	scale := math.Max(.90, math.Min(1.35, priorScale))
	width := int32(420 * scale)
	height := int32(170 * scale)
	// Align near the help button and prevent clipping by the main window.
	x := hrect.right - width + 45
	y := hrect.bottom + 7
	if x < mrect.left+10 {
		x = mrect.left + 10
	}
	if x+width > mrect.right-10 {
		x = mrect.right - width - 10
	}
	if y+height > mrect.bottom-20 {
		y = hrect.top - height - 7
	}
	if y < mrect.top+35 {
		y = mrect.top + 35
	}
	call(pSetWindowPos, helpPop.hwnd, 0, uintptr(x), uintptr(y), uintptr(width), uintptr(height), SWP_NOACTIVATE|SWP_SHOWWINDOW)
	call(pInvalidate, helpPop.hwnd, 0, 1)
}
func popupWndProc(h uintptr, m uint32, w, l uintptr) uintptr {
	switch m {
	case WM_ERASEBKGND:
		return 1
	case WM_PAINT:
		var ps winPaint
		dc := call(pBeginPaint, h, uintptr(unsafe.Pointer(&ps)))
		if dc != 0 {
			var r rect
			call(pGetClientRect, h, uintptr(unsafe.Pointer(&r)))
			call(pFillRect, dc, uintptr(unsafe.Pointer(&r)), pageBrush)
			s := math.Max(.9, math.Min(1.35, priorScale))
			pad := int32(8 * s)
			drawRound(dc, rect{pad, pad + 3, r.right - pad, r.bottom - pad}, colInput, colBorder, int32(17*s))
			left := int32(25 * s)
			title := rect{left, int32(20 * s), r.right - left, int32(53 * s)}
			paintText(dc, helpPop.title, title, layoutFont(fontSpec{size: 18, weight: 700, name: "Segoe UI"}, s), colText, 0x0)
			content := rect{left, int32(61 * s), r.right - left, int32(155 * s)}
			paintText(dc, helpPop.text, content, layoutFont(fontSpec{size: 15, weight: 400, name: "Segoe UI"}, s), colMuted, 0x10) // DT_WORDBREAK
			call(pEndPaint, h, uintptr(unsafe.Pointer(&ps)))
		}
		return 0
	case WM_LBUTTONDOWN:
		hideContextHint()
		return 0
	}
	return call(pDefWnd, h, uintptr(m), w, l)
}

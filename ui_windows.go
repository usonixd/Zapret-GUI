//go:build windows

package main

import (
	"errors"
	"fmt"
	"log"
	"math"
	"os"
	"strings"
	"syscall"
	"time"
	"unsafe"
)

const (
	ID_POWER           = 120
	ID_TEST_FULL       = 121
	ID_APPLY_BEST      = 122
	ID_OPEN_REPORT     = 123
	ID_NAV_HOME        = 124
	ID_NAV_TEST        = 125
	ID_NAV_SITES       = 126
	ID_NAV_SETTINGS    = 127
	ID_CHOOSE_STRATEGY = 128
	ID_MODE_BOTH       = 129
	ID_MODE_DISCORD    = 130
	ID_MODE_YOUTUBE    = 131
	ID_SCOPE_QUICK     = 132
	ID_SCOPE_FULL      = 133
	ID_STRATEGY_ITEM   = 300
	ID_PICKER_ROW      = 350
	ID_PICKER_PREV     = 357
	ID_PICKER_NEXT     = 358
	ID_HELP_STRATEGY   = 400
	ID_HELP_AUTOTEST   = 401
	ID_HELP_SITES      = 402
	ID_HELP_GAME       = 403
	ID_HELP_PORTS      = 404
	ID_HELP_AUTORUN    = 405
	ID_HELP_UPDATES    = 406
	ID_GAME_PAGE       = 407
	ID_SETTINGS_BACK   = 408
	ID_CANCEL_TEST     = 409
	SS_CENTER          = 0x00000001
	WM_DRAWITEM        = 0x002B
	WM_MEASUREITEM     = 0x002C
	WM_MOUSEWHEEL      = 0x020A
	WM_ERASEBKGND      = 0x0014
	WM_CTLCOLORLISTBOX = 0x0134
	BM_OWNERDRAW       = 0x0000000B
	CBS_OWNERDRAWFIXED = 0x0010
	ES_READONLY        = 0x0800
	EM_LIMITTEXT       = 0x00C5
	EM_SETSEL          = 0x00B1
	EM_SCROLLCARET     = 0x00B7
	SW_HIDE            = 0
	SW_SHOW            = 5
	pageHome           = 0
	pageTest           = 1
	pageSites          = 2
	pageSettings       = 3
	pageGame           = 4
)

const (
	darkBackground uint32 = 0x191A20 // #201A19? packed COLORREF values supplied through rgb() below
)

func rgb(r, g, b int) uintptr { return uintptr(r | (g << 8) | (b << 16)) }

var (
	colBg      = rgb(20, 21, 26)
	colCard    = rgb(31, 33, 40)
	colInput   = rgb(37, 39, 48)
	colActive  = rgb(66, 79, 143)
	colPrimary = rgb(78, 91, 165)
	colGreen   = rgb(54, 138, 113)
	colRed     = rgb(158, 64, 75)
	colText    = rgb(235, 236, 242)
	colMuted   = rgb(151, 157, 171)
	colBorder  = rgb(53, 57, 68)
)
var (
	hReport, hTestProgress, hTestHelp                             uintptr
	hApplyBest, hOpenReports                                      uintptr
	hHomeGame, hHomeHelp, hHomeSelection                          uintptr
	hTestLeader, hModeBoth, hModeDiscord, hModeYouTube            uintptr
	hRunTest, hCancelTest                                         uintptr
	hHomeAutopick, hGameInfo, hTipPanel, hHelpStatus              uintptr
	hPickerRows                                                   [5]uintptr
	hPickerPrev, hPickerNext, hPickerCount                        uintptr
	hCopyright                                                    uintptr
	pickerOpen                                                    bool
	pickerOffset                                                  int
	selectedTestMode                                              = modeBoth
	hNav                                                          [4]uintptr
	pageElements                                                  [5][]uintptr
	currentPage                                                   = pageHome
	uiTitleFont, uiMainFont, uiSmallFont, uiButtonFont, powerFont uintptr
	pageBrush, fieldBrush, cardBrush                              uintptr
	pFillRect                                                     = u32.NewProc("FillRect")
	pGetClientRect                                                = u32.NewProc("GetClientRect")
	pInvalidate                                                   = u32.NewProc("InvalidateRect")
	pDrawText                                                     = u32.NewProc("DrawTextW")
	pTheme                                                        = syscall.NewLazyDLL("uxtheme.dll").NewProc("SetWindowTheme")
	pRoundRect                                                    = gdi.NewProc("RoundRect")
	pEllipse                                                      = gdi.NewProc("Ellipse")
	gdip                                                          = syscall.NewLazyDLL("gdiplus.dll")
	pGdipStartup                                                  = gdip.NewProc("GdiplusStartup")
	pGdipCreate                                                   = gdip.NewProc("GdipCreateFromHDC")
	pGdipSmooth                                                   = gdip.NewProc("GdipSetSmoothingMode")
	pGdipSolid                                                    = gdip.NewProc("GdipCreateSolidFill")
	pGdipPen                                                      = gdip.NewProc("GdipCreatePen1")
	pGdipFillEllipse                                              = gdip.NewProc("GdipFillEllipse")
	pGdipEllipse                                                  = gdip.NewProc("GdipDrawEllipse")
	pGdipArc                                                      = gdip.NewProc("GdipDrawArc")
	pGdipLine                                                     = gdip.NewProc("GdipDrawLine")
	pGdipPenCap                                                   = gdip.NewProc("GdipSetPenStartCap")
	pGdipPenEnd                                                   = gdip.NewProc("GdipSetPenEndCap")
	pGdipDelPen                                                   = gdip.NewProc("GdipDeletePen")
	pGdipDelBrush                                                 = gdip.NewProc("GdipDeleteBrush")
	pGdipDelGraph                                                 = gdip.NewProc("GdipDeleteGraphics")
	gdipToken                                                     uintptr
	pCreatePen                                                    = gdi.NewProc("CreatePen")
	pSelectObject                                                 = gdi.NewProc("SelectObject")
	pDeleteObject                                                 = gdi.NewProc("DeleteObject")
	pSetBkMode                                                    = gdi.NewProc("SetBkMode")
)

type rect struct{ left, top, right, bottom int32 }
type drawItem struct {
	ctlType, ctlID, itemID, itemAction, itemState uint32
	hwndItem, hdc                                 uintptr
	rcItem                                        rect
	itemData                                      uintptr
}
type measureItem struct {
	ctlType, ctlID, itemID, itemWidth, itemHeight uint32
	itemData                                      uintptr
}

func addPageControl(pg int, hwnd uintptr) uintptr {
	pageElements[pg] = append(pageElements[pg], hwnd)
	return hwnd
}
func pageCenter(pg int, s string, x, y, w, h int32) uintptr {
	return addPageControl(pg, create("STATIC", s, 0, x, y, w, h, SS_CENTER))
}
func pageLabel(pg int, s string, x, y, w, h int32) uintptr {
	return addPageControl(pg, create("STATIC", s, 0, x, y, w, h, 0))
}
func pageButton(pg int, name string, id int32, x, y, w, h int32) uintptr {
	return addPageControl(pg, create("BUTTON", name, id, x, y, w, h, BM_OWNERDRAW|WS_TABSTOP))
}
func setFont(hwnd, which uintptr) {
	if hwnd != 0 && which != 0 {
		send(hwnd, WM_SETFONT, which, 1)
		noteControlFont(hwnd, which)
	}
}
func darkControl(hwnd uintptr) {
	if hwnd != 0 {
		call(pTheme, hwnd, uintptr(unsafe.Pointer(ptr("DarkMode_Explorer"))), 0)
	}
}
func showPage(pg int) {
	if pg < 0 || pg >= len(pageElements) {
		return
	}
	currentPage = pg
	for i, items := range pageElements {
		for _, h := range items {
			if i == pg {
				call(pShow, h, SW_SHOW)
			} else {
				call(pShow, h, SW_HIDE)
			}
		}
	}
	for _, h := range hNav {
		call(pInvalidate, h, 0, 1)
	}
	if pg != pageHome {
		pickerOpen = false
	}
	if pg == pageHome {
		refreshHomeCaption()
		refreshPicker()
	}
	if pg == pageTest {
		refreshReport()
	}
	hideContextHint()
}
func refreshTestHighlights() {
	for _, h := range []uintptr{hModeBoth, hModeDiscord, hModeYouTube} {
		if h != 0 {
			call(pInvalidate, h, 0, 1)
		}
	}
}
func refreshHomeCaption() {
	if hHomeSelection != 0 {
		name := strings.TrimSuffix(cfg.Strategy, ".bat")
		setText(hHomeSelection, "Стратегия: "+name+"   ▾")
	}
	if hHomeGame != 0 {
		name := []string{"Выключен", "TCP + UDP", "TCP", "UDP"}
		gi := selCombo(hGame)
		if gi < 0 || gi >= len(name) {
			gi = 0
		}
		setText(hHomeGame, "Game Filter: "+name[gi]+" · настройка в разделе «Настройки»")
	}
}
func refreshPicker() {
	if currentPage != pageHome {
		return
	}
	show := func(h uintptr, yes bool) {
		if h != 0 {
			if yes {
				call(pShow, h, SW_SHOW)
			} else {
				call(pShow, h, SW_HIDE)
			}
		}
	}
	for i, h := range hPickerRows {
		n := pickerOffset + i
		visible := pickerOpen && n < len(strategyNames)
		show(h, visible)
		if visible {
			name := strings.TrimSuffix(strategyNames[n], ".bat")
			if strategyNames[n] == cfg.Strategy {
				name = "✓  " + name
			}
			setText(h, name)
			call(pInvalidate, h, 0, 1)
		}
	}
	show(hPickerPrev, pickerOpen)
	show(hPickerNext, pickerOpen)
	show(hPickerCount, pickerOpen)
	show(hHomeAutopick, !pickerOpen)
	show(hHomeGame, !pickerOpen)
	show(hHomeHelp, !pickerOpen)
	show(hHelpStatus, !pickerOpen)
	if pickerOpen {
		first := pickerOffset + 1
		last := pickerOffset + len(hPickerRows)
		if last > len(strategyNames) {
			last = len(strategyNames)
		}
		setText(hPickerCount, fmt.Sprintf("%d–%d из %d", first, last, len(strategyNames)))
	}
}
func togglePicker() {
	pickerOpen = !pickerOpen
	if pickerOpen {
		pickerOffset = 0
		for i, n := range strategyNames {
			if n == cfg.Strategy {
				pickerOffset = i
				break
			}
		}
		if pickerOffset > len(strategyNames)-len(hPickerRows) {
			pickerOffset = len(strategyNames) - len(hPickerRows)
		}
		if pickerOffset < 0 {
			pickerOffset = 0
		}
	}
	refreshPicker()
}
func refreshReport() {
	reportMu.Lock()
	p, r, choice, leader := testProgress, lastReport, suggestedStrategy, testLeaderText
	reportMu.Unlock()
	if hTestProgress != 0 {
		setText(hTestProgress, p)
	}
	if hTestLeader != 0 {
		setText(hTestLeader, leader)
	}
	if hReport != 0 && getText(hReport) != r {
		setText(hReport, r)
		send(hReport, EM_SETSEL, 0, 0)
		send(hReport, EM_SCROLLCARET, 0, 0)
	}
	if hApplyBest != 0 {
		call(pEnable, hApplyBest, boolToUintptr(choice != "" && !testActive.Load()))
	}
	if hCancelTest != 0 {
		if testActive.Load() {
			call(pShow, hCancelTest, SW_SHOW)
		} else {
			call(pShow, hCancelTest, SW_HIDE)
		}
	}
}
func paintText(hdc uintptr, text string, r rect, font uintptr, clr uintptr, format uintptr) {
	if font != 0 {
		old := call(pSelectObject, hdc, font)
		defer call(pSelectObject, hdc, old)
	}
	call(pSetBkMode, hdc, 1)
	call(pFg, hdc, clr)
	call(pDrawText, hdc, uintptr(unsafe.Pointer(ptr(text))), ^uintptr(0), uintptr(unsafe.Pointer(&r)), format)
}
func drawRound(hdc uintptr, r rect, fill, border uintptr, radius int32) {
	brush := call(pSolid, fill)
	pen := call(pCreatePen, 0, 1, border)
	oldB := call(pSelectObject, hdc, brush)
	oldP := call(pSelectObject, hdc, pen)
	call(pRoundRect, hdc, uintptr(r.left), uintptr(r.top), uintptr(r.right), uintptr(r.bottom), uintptr(radius), uintptr(radius))
	call(pSelectObject, hdc, oldB)
	call(pSelectObject, hdc, oldP)
	call(pDeleteObject, brush)
	call(pDeleteObject, pen)
}
func f32(v float32) uintptr { return uintptr(math.Float32bits(v)) }

type gdipStartupInput struct {
	Version        uint32
	DebugCallback  uintptr
	SuppressBg     uint32
	SuppressCodecs uint32
}

func startGdip() {
	input := gdipStartupInput{Version: 1}
	if call(pGdipStartup, uintptr(unsafe.Pointer(&gdipToken)), uintptr(unsafe.Pointer(&input)), 0) != 0 {
		gdipToken = 0
	}
}
func drawPower(di *drawItem) {
	r := di.rcItem
	running := engineVisualState == "on" || engineVisualState == "starting"
	fill, border := uint32(0xFF20222A), uint32(0xFF444854)
	if running {
		fill, border = 0xFF213631, 0xFF41947D
	}
	if engineVisualState == "failed" {
		border = 0xFFB85D65
	}
	if gdipToken != 0 {
		var g, brush, pen, powerPen uintptr
		if call(pGdipCreate, di.hdc, uintptr(unsafe.Pointer(&g))) == 0 {
			call(pGdipSmooth, g, 4) // anti-alias geometry: no jagged pixels
			call(pGdipSolid, uintptr(fill), uintptr(unsafe.Pointer(&brush)))
			call(pGdipPen, uintptr(border), f32(2), 2, uintptr(unsafe.Pointer(&pen)))
			x, y := float32(r.left+5), float32(r.top+5)
			d := float32(r.right - r.left - 10)
			call(pGdipFillEllipse, g, brush, f32(x), f32(y), f32(d), f32(d))
			call(pGdipEllipse, g, pen, f32(x), f32(y), f32(d), f32(d))
			cx, cy := x+d/2, y+d/2-12
			call(pGdipPen, 0xFFF3F4F8, f32(5), 2, uintptr(unsafe.Pointer(&powerPen)))
			call(pGdipPenCap, powerPen, 2)
			call(pGdipPenEnd, powerPen, 2)
			// Open top of circle, straight power stem; both vectors antialiased.
			call(pGdipArc, g, powerPen, f32(cx-23), f32(cy-17), f32(46), f32(46), f32(-45), f32(270))
			call(pGdipLine, g, powerPen, f32(cx), f32(cy-25), f32(cx), f32(cy+4))
			call(pGdipDelPen, powerPen)
			call(pGdipDelPen, pen)
			call(pGdipDelBrush, brush)
			call(pGdipDelGraph, g)
		}
	} else {
		// Fallback if GDI+ unavailable.
		drawRound(di.hdc, rect{r.left + 3, r.top + 3, r.right - 3, r.bottom - 3}, colCard, colBorder, 110)
	}
	label := "ВКЛЮЧИТЬ"
	if running {
		label = "ОСТАНОВИТЬ"
	}
	if engineVisualState == "starting" {
		label = "ЗАПУСК..."
	}
	txt := rect{r.left + 4, r.bottom - 59, r.right - 4, r.bottom - 35}
	paintText(di.hdc, label, txt, uiSmallFont, colText, 0x25)
}
func drawButton(di *drawItem) {
	if int(di.ctlID) == ID_POWER {
		drawPower(di)
		return
	}
	id := int(di.ctlID)
	fill := colCard
	edge := colBorder
	fg := colText
	if id == ID_TEST || id == ID_SAVE || id == ID_APPLY_BEST {
		fill = colPrimary
		edge = colPrimary
	}
	if id == ID_NAV_HOME || id == ID_NAV_TEST || id == ID_NAV_SITES || id == ID_NAV_SETTINGS {
		selected := (id == ID_NAV_HOME && currentPage == pageHome) || (id == ID_NAV_TEST && currentPage == pageTest) || (id == ID_NAV_SITES && currentPage == pageSites) || (id == ID_NAV_SETTINGS && (currentPage == pageSettings || currentPage == pageGame))
		if selected {
			fill = colActive
			edge = colActive
		}
	}
	selected := (id == ID_MODE_BOTH && selectedTestMode == modeBoth) ||
		(id == ID_MODE_DISCORD && selectedTestMode == modeDiscord) ||
		(id == ID_MODE_YOUTUBE && selectedTestMode == modeYouTube)
	if id >= ID_PICKER_ROW && id < ID_PICKER_ROW+len(hPickerRows) {
		n := pickerOffset + id - ID_PICKER_ROW
		if n < len(strategyNames) {
			selected = strategyNames[n] == cfg.Strategy
		}
	}
	if selected {
		fill = colActive
		edge = colActive
	}
	if di.itemState&0x0004 != 0 {
		fill = rgb(49, 52, 64)
	} // ODS_DISABLED
	drawRound(di.hdc, di.rcItem, fill, edge, 12)
	paintText(di.hdc, getText(di.hwndItem), di.rcItem, uiButtonFont, fg, 0x25|0x8000)
}
func drawCombo(di *drawItem) {
	r := di.rcItem
	selected := di.itemState&0x0001 != 0
	brush := fieldBrush
	if selected {
		brush = cardBrush
	}
	call(pFillRect, di.hdc, uintptr(unsafe.Pointer(&r)), brush)
	if di.itemID == ^uint32(0) {
		return
	}
	n := send(di.hwndItem, 0x149, uintptr(di.itemID), 0)
	if n == ^uintptr(0) || n > 8192 {
		return
	}
	buffer := make([]uint16, int(n)+2)
	send(di.hwndItem, 0x148, uintptr(di.itemID), uintptr(unsafe.Pointer(&buffer[0])))
	text := syscall.UTF16ToString(buffer)
	paintText(di.hdc, text, rect{r.left + 10, r.top, r.right - 6, r.bottom}, uiMainFont, colText, 0x24)
}

// Tooltips are registered by their help-button ID; the custom popup is drawn
// in popup_windows.go, so Windows will not display its native white tooltip.
func attachTooltip(h uintptr, message string) { registerHelpButton(h, message) }
func showHint(message string)                 { showContextHint(message) }
func wndProc(hwnd uintptr, msg uint32, w, l uintptr) uintptr {
	switch msg {
	case WM_ERASEBKGND:
		var r rect
		call(pGetClientRect, hwnd, uintptr(unsafe.Pointer(&r)))
		call(pFillRect, w, uintptr(unsafe.Pointer(&r)), pageBrush)
		return 1
	case WM_DRAWITEM:
		if l != 0 {
			di := (*drawItem)(unsafe.Pointer(l))
			if di.ctlType == 4 {
				drawButton(di)
				return 1
			}
			if di.ctlType == 3 {
				drawCombo(di)
				return 1
			}
		}
	case WM_MEASUREITEM:
		if l != 0 {
			mi := (*measureItem)(unsafe.Pointer(l))
			if mi.ctlType == 3 {
				mi.itemHeight = 27
				return 1
			}
		}
	case WM_CTLCOLORSTATIC, WM_CTLCOLORBTN:
		call(pBk, w, colBg)
		c := colText
		if l == hStatusHint || l == hVersion || l == hFeedback || l == hCopyright || l == hTestHelp || l == hHomeGame || l == hHomeHelp {
			c = colMuted
		}
		if l == hStatus {
			switch engineVisualState {
			case "on":
				c = rgb(98, 189, 156)
			case "failed":
				c = rgb(235, 113, 122)
			case "starting":
				c = rgb(227, 179, 106)
			}
		}
		call(pFg, w, c)
		return pageBrush
	case WM_CTLCOLORLISTBOX, WM_CTLCOLOREDIT:
		call(pBk, w, colInput)
		call(pFg, w, colText)
		return fieldBrush
	case WM_MOUSEWHEEL:
		if pickerOpen && currentPage == pageHome {
			delta := int16((w >> 16) & 0xFFFF)
			if delta > 0 && pickerOffset > 0 {
				pickerOffset--
			}
			if delta < 0 && pickerOffset+len(hPickerRows) < len(strategyNames) {
				pickerOffset++
			}
			refreshPicker()
			return 0
		}
	case WM_SIZE:
		applyResponsiveLayout(int32(uint16(l)), int32(uint16(l>>16)))
		hideContextHint()
		return 0
	case WM_LBUTTONDOWN:
		hideContextHint()
	case WM_ACTIVATEAPP:
		if w == 0 {
			hideContextHint()
		}
	case WM_DPICHANGED:
		// Move to Windows' suggested monitor-DPI rectangle; WM_SIZE reapplies layout.
		if l != 0 {
			suggested := (*rect)(unsafe.Pointer(l))
			call(pSetWindowPos, hwnd, 0, uintptr(suggested.left), uintptr(suggested.top), uintptr(suggested.right-suggested.left), uintptr(suggested.bottom-suggested.top), 0x0004|0x0010)
		}
		hideContextHint()
		return 0
	case WM_TIMER:
		if w == ENGINE_TIMER_ID {
			pollEngine()
			return 0
		}
	case WM_CREATE:
		return 0
	case WM_COMMAND:
		id := int(w & 0xffff)
		if id == 0 {
			return 0
		}
		if id == ID_GAME {
			if hHomeGame != 0 {
				refreshHomeCaption()
			}
			return 0
		}
		if id == ID_STRATEGY {
			return 0
		}
		// BN_CLICKED == 0. Ignore focus-change notifications from buttons.
		if (w >> 16) != 0 {
			return 0
		}
		if id >= ID_HELP_STRATEGY && id <= ID_HELP_UPDATES {
			helpActiveID = id
		} else {
			hideContextHint()
		}
		cmd(id)
		return 0
	case WM_APP_NOTICE:
		newsMutex.Lock()
		t, p := newsText, newsDialog
		newsMutex.Unlock()
		status(t)
		if p {
			alert("Zapret Desk", t, MB_ICONINFORMATION)
		}
		return 0
	case WM_APP_TEST:
		refreshUI()
		refreshHomeCaption()
		refreshReport()
		return 0
	case WM_APP_REPORT:
		refreshReport()
		return 0
	case WM_APP_AUTO:
		log.Print("startup UI message received")
		if len(os.Args) > 1 {
			switch os.Args[1] {
			case "--run":
				handleStart()
			case "--stop":
				handleStop()
			case "--autotest":
				showPage(pageTest)
				go autoTest(true, modeBoth)
			case "--autotest-full":
				showPage(pageTest)
				refreshTestHighlights()
				go autoTest(true, modeBoth)
			case "--autotest-discord":
				selectedTestMode = modeDiscord
				showPage(pageTest)
				refreshTestHighlights()
				go autoTest(true, modeDiscord)
			case "--autotest-full-discord":
				selectedTestMode = modeDiscord
				showPage(pageTest)
				refreshTestHighlights()
				go autoTest(true, modeDiscord)
			case "--autotest-youtube":
				selectedTestMode = modeYouTube
				showPage(pageTest)
				refreshTestHighlights()
				go autoTest(true, modeYouTube)
			case "--autotest-full-youtube":
				selectedTestMode = modeYouTube
				showPage(pageTest)
				refreshTestHighlights()
				go autoTest(true, modeYouTube)
			case "--auto-enable":
				checkSet(hAuto, true)
				setAutoStart()
			case "--auto-disable":
				checkSet(hAuto, false)
				setAutoStart()
			case "--autostart":
				if cfg.AutoStart && isAdmin() {
					if e := startEngine(cfg.Strategy); e != nil {
						engineVisualState = "failed"
						status("Ошибка автозапуска: " + e.Error())
					} else {
						engineLastPID = cfg.PID
						engineVisualState = "starting"
						engineStartAt = time.Now()
						status("Запущено автоматически. Проверяю процесс...")
					}
					renderEngineStatus()
				}
			}
		}
		if cfg.AutoCheck {
			go checkUpdates(false)
		}
		return 0
	case WM_CLOSE:
		newsMutex.Lock()
		busy := isBusy
		newsMutex.Unlock()
		if busy {
			alert("Операция выполняется", "Дождись окончания тестирования или обновления перед закрытием окна, чтобы не оставить тестовый winws.exe запущенным.", MB_ICONINFORMATION)
			return 0
		}
		call(pDestroy, hwnd)
		return 0
	case WM_DESTROY:
		hideContextHint()
		call(pPostQuit, 0)
		return 0
	}
	return call(pDefWnd, hwnd, uintptr(msg), w, l)
}
func makeFont(size int32, weight int32, face string) uintptr {
	h := call(pFont, uintptr(uint32(-size)), 0, 0, 0, uintptr(weight), 0, 0, 0, 1, 0, 0, 5, 0, uintptr(unsafe.Pointer(ptr(face))))
	registerBaseFont(h, size, weight, face)
	return h
}
func initGUI() error {
	inst, _, _ := k32.NewProc("GetModuleHandleW").Call(0)
	hinst = inst
	pageBrush = call(pSolid, colBg)
	fieldBrush = call(pSolid, colInput)
	cardBrush = call(pSolid, colActive)
	backgroundBrush = pageBrush
	editBrush = fieldBrush
	font = makeFont(18, 400, "Segoe UI")
	uiMainFont = font
	uiSmallFont = makeFont(15, 400, "Segoe UI")
	uiTitleFont = makeFont(27, 700, "Segoe UI")
	uiButtonFont = makeFont(16, 600, "Segoe UI")
	statusFont = makeFont(23, 700, "Segoe UI")
	startGdip()
	cls := ptr("ZapretDeskNativeV07")
	wc := wndClass{size: uint32(unsafe.Sizeof(wndClass{})), wndProc: syscall.NewCallback(wndProc), instance: hinst, cursor: call(pLoadCursor, 0, 32512), background: pageBrush, className: cls, icon: appIconLarge(), iconSmall: appIconSmall()}
	if call(pRegisterClass, uintptr(unsafe.Pointer(&wc))) == 0 {
		return errors.New("RegisterClassExW failed")
	}
	// Respect the monitor's initial display scale without opening off-screen.
	initialDpi := call(u32.NewProc("GetDpiForSystem"))
	if initialDpi < 96 || initialDpi > 384 {
		initialDpi = 96
	}
	initialScale := float64(initialDpi) / 96.0
	initialW := int32(math.Round(960 * initialScale))
	initialH := int32(math.Round(790 * initialScale))
	metrics := u32.NewProc("GetSystemMetrics")
	screenW := int32(call(metrics, 0))
	screenH := int32(call(metrics, 1))
	if screenW > 0 && initialW > screenW-60 {
		initialW = screenW - 60
	}
	if screenH > 0 && initialH > screenH-85 {
		initialH = screenH - 85
	}
	hwndMain = call(pCreateWindow, 0, uintptr(unsafe.Pointer(cls)), uintptr(unsafe.Pointer(ptr("Zapret Desk 0.7 — test build by usonixd"))), WS_OVERLAPPEDWINDOW|WS_VISIBLE, 0x80000000, 0x80000000, uintptr(initialW), uintptr(initialH), 0, 0, hinst, 0)
	if hwndMain == 0 {
		return errors.New("CreateWindow failed")
	}
	setAppWindowIcons(hwndMain)
	var dark uint32 = 1
	call(pDwm, hwndMain, 20, uintptr(unsafe.Pointer(&dark)), uintptr(unsafe.Sizeof(dark)))
	call(pDwm, hwndMain, 19, uintptr(unsafe.Pointer(&dark)), uintptr(unsafe.Sizeof(dark)))
	// Context help uses a custom dark popover instead of the native tooltip.
	initContextPopup()

	hTitle := create("STATIC", "zapret desk", 0, 34, 17, 490, 39, 0)
	setFont(hTitle, uiTitleFont)
	hVersion = create("STATIC", "Flowseal 1.10.4 · v0.7 beta", 0, 640, 28, 288, 23, 0)
	setFont(hVersion, uiSmallFont)
	for i, t := range []string{"Главная", "Автоподбор", "Сайты", "Настройки"} {
		ids := []int32{ID_NAV_HOME, ID_NAV_TEST, ID_NAV_SITES, ID_NAV_SETTINGS}
		hNav[i] = create("BUTTON", t, ids[i], 32+int32(i)*230, 72, 211, 44, BM_OWNERDRAW|WS_TABSTOP)
	}

	ht := pageCenter(pageHome, "Управление подключением", 210, 142, 530, 39)
	setFont(ht, uiTitleFont)
	hStatus = pageCenter(pageHome, "ВЫКЛЮЧЕН", 299, 189, 350, 35)
	setFont(hStatus, statusFont)
	hStart = pageButton(pageHome, "", ID_POWER, 395, 232, 160, 160)
	hStatusHint = pageCenter(pageHome, "winws.exe не запущен", 100, 404, 750, 26)
	setFont(hStatusHint, uiSmallFont)
	sl := pageLabel(pageHome, "Стратегия обхода", 210, 446, 425, 24)
	setFont(sl, uiMainFont)
	hs := pageButton(pageHome, "?", ID_HELP_STRATEGY, 730, 442, 35, 31)
	attachTooltip(hs, "Стратегия — набор настроек winws. Подбирается под твоего провайдера. Смена требует остановить zapret.")
	hStrategy = create("COMBOBOX", "", ID_STRATEGY, -2000, -2000, 400, 240, CBS_DROPDOWNLIST|CBS_HASSTRINGS)
	hHomeSelection = pageButton(pageHome, "Стратегия: general   ▾", ID_CHOOSE_STRATEGY, 210, 478, 530, 46)
	hHomeAutopick = pageButton(pageHome, "Автоподбор (необязательно)  →", ID_NAV_TEST, 210, 541, 530, 46)
	hHomeGame = pageCenter(pageHome, "Game Filter: Выключен", 170, 613, 610, 27)
	setFont(hHomeGame, uiSmallFont)
	hHomeHelp = pageCenter(pageHome, "Не уверен в стратегии? Проверяй Discord и YouTube вручную.", 135, 645, 680, 26)
	setFont(hHomeHelp, uiSmallFont)
	hHelpStatus = pageCenter(pageHome, "Выбор стратегии не перезапускает уже работающий zapret", 130, 672, 690, 25)
	setFont(hHelpStatus, uiSmallFont)
	// Custom dark strategy dropdown with six rows and two dark navigation buttons;
	// avoids the bright native Win32 ComboBox scrollbar.
	for i := range hPickerRows {
		hPickerRows[i] = pageButton(pageHome, "", int32(ID_PICKER_ROW+i), 210, int32(526+i*30), 530, 28)
	}
	hPickerPrev = pageButton(pageHome, "↑ Ранее", ID_PICKER_PREV, 210, 673, 156, 32)
	hPickerCount = pageCenter(pageHome, "1–6 из 22", 374, 673, 202, 32)
	setFont(hPickerCount, uiSmallFont)
	hPickerNext = pageButton(pageHome, "Позже ↓", ID_PICKER_NEXT, 584, 673, 156, 32)
	// Reposition picker rows to end at 693 and hide footer beneath when open.
	for i, h := range hPickerRows {
		_ = i
		call(pShow, h, SW_HIDE)
	}

	tt := pageLabel(pageTest, "Автоподбор стратегий", 32, 140, 690, 39)
	setFont(tt, uiTitleFont)
	hTestHelp = pageLabel(pageTest, "Проверка HTTPS. Пинг в голосе Discord и видеопоток YouTube оценивай вручную.", 32, 181, 860, 25)
	setFont(hTestHelp, uiSmallFont)
	htHelp := pageButton(pageTest, "?", ID_HELP_AUTOTEST, 887, 173, 36, 31)
	attachTooltip(htHelp, "Все 22 стратегии проверяются по очереди. Результат приблизительный: не проверяет голосовой UDP Discord и реальную скорость YouTube-видео.")
	hModeBoth = pageButton(pageTest, "Баланс", ID_MODE_BOTH, 32, 224, 288, 44)
	hModeDiscord = pageButton(pageTest, "Discord", ID_MODE_DISCORD, 332, 224, 289, 44)
	hModeYouTube = pageButton(pageTest, "YouTube", ID_MODE_YOUTUBE, 632, 224, 292, 44)
	hRunTest = pageButton(pageTest, "Начать тест", ID_TEST, 32, 290, 442, 51)
	hApplyBest = pageButton(pageTest, "Выбрать лидера", ID_APPLY_BEST, 484, 290, 228, 51)
	hOpenReports = pageButton(pageTest, "Полный отчёт ↗", ID_OPEN_REPORT, 723, 290, 201, 51)
	hTestProgress = pageLabel(pageTest, "Нажми «Начать тест»: проверятся все стратегии", 32, 363, 667, 30)
	hCancelTest = pageButton(pageTest, "Остановить тест", ID_CANCEL_TEST, 723, 352, 201, 36)
	setFont(hTestProgress, uiSmallFont)
	hTestLeader = pageLabel(pageTest, "Лидер пока не определён", 32, 408, 886, 61)
	setFont(hTestLeader, uiButtonFont)
	section := pageLabel(pageTest, "Рейтинг · короткие результаты", 32, 478, 720, 27)
	setFont(section, uiMainFont)
	hReport = addPageControl(pageTest, create("EDIT", "", 0, 32, 515, 892, 183, WS_BORDER|WS_VSCROLL|ES_MULTILINE|ES_AUTOVSCROLL|ES_READONLY|WS_TABSTOP))
	setFont(hReport, makeFont(16, 400, "Consolas"))
	darkControl(hReport)
	send(hReport, EM_LIMITTEXT, 250000, 0)

	txt := pageLabel(pageSites, "Списки сайтов", 32, 140, 620, 38)
	setFont(txt, uiTitleFont)
	hint := pageLabel(pageSites, "Домены без https:// и пути. Списки применяются после перезапуска zapret.", 32, 186, 834, 24)
	setFont(hint, uiSmallFont)
	hsite := pageButton(pageSites, "?", ID_HELP_SITES, 885, 177, 37, 31)
	attachTooltip(hsite, "Для обхода: дополнительный список доменов; исключения: адреса, которые zapret должен пропускать. Некоторые BAT-стратегии используют свои отдельные списки.")
	pageLabel(pageSites, "Домены для обхода", 32, 228, 395, 25)
	pageLabel(pageSites, "Исключения", 487, 228, 395, 25)
	hDomains = addPageControl(pageSites, create("EDIT", "", 0, 32, 260, 436, 337, WS_BORDER|WS_VSCROLL|ES_MULTILINE|ES_AUTOVSCROLL|ES_WANTRETURN|WS_TABSTOP))
	hExclude = addPageControl(pageSites, create("EDIT", "", 0, 487, 260, 437, 337, WS_BORDER|WS_VSCROLL|ES_MULTILINE|ES_AUTOVSCROLL|ES_WANTRETURN|WS_TABSTOP))
	darkControl(hDomains)
	darkControl(hExclude)
	pageButton(pageSites, "Сохранить списки", ID_SAVE, 32, 615, 436, 47)
	note := pageLabel(pageSites, "Важно: не все стратегии автоматически используют пользовательские списки.", 32, 673, 850, 25)
	setFont(note, uiSmallFont)

	txt = pageLabel(pageSettings, "Настройки", 32, 140, 650, 40)
	setFont(txt, uiTitleFont)
	pageLabel(pageSettings, "Game Filter · обработка игрового трафика", 32, 207, 715, 26)
	hg := pageButton(pageSettings, "?", ID_HELP_GAME, 876, 197, 39, 31)
	attachTooltip(hg, "Game Filter влияет на диапазоны TCP/UDP, которые обрабатывает winws. Избыточные диапазоны могут повлиять на работу игр. Это не ускоритель интернета.")
	pageButton(pageSettings, "Настроить Game Filter  →", ID_GAME_PAGE, 32, 244, 891, 51)
	hAuto = addPageControl(pageSettings, create("BUTTON", "Автозапуск с Windows", ID_AUTO, 32, 342, 360, 34, BS_AUTOCHECKBOX|WS_TABSTOP))
	ha := pageButton(pageSettings, "?", ID_HELP_AUTORUN, 401, 340, 36, 32)
	attachTooltip(ha, "Через Планировщик заданий Windows с повышенными правами. Запускает сохранённую стратегию после входа в Windows.")
	hAutoCheck = addPageControl(pageSettings, create("BUTTON", "Проверять обновления при запуске", ID_AUTOCHECK, 447, 342, 450, 34, BS_AUTOCHECKBOX|WS_TABSTOP))
	pageButton(pageSettings, "Проверить обновления", ID_CHECK_UPDATE, 32, 424, 273, 45)
	pageButton(pageSettings, "Обновить ядро", ID_APPLY_UPDATE, 319, 424, 254, 45)
	pageButton(pageSettings, "GitHub Flowseal ↗", ID_OPEN, 587, 424, 337, 45)
	hu := pageButton(pageSettings, "?", ID_HELP_UPDATES, 886, 384, 36, 31)
	attachTooltip(hu, "Обновление ядра скачивает новый zapret с GitHub Flowseal. Интерфейс Zapret Desk и пользовательские настройки отдельно.")
	note = pageLabel(pageSettings, "Обновление ядра не обновляет интерфейс Zapret Desk.", 32, 485, 845, 30)
	setFont(note, uiSmallFont)
	pageButton(pageSettings, "Сохранить настройки", ID_SAVE, 32, 539, 438, 46)

	txt = pageLabel(pageGame, "Game Filter", 32, 140, 650, 40)
	setFont(txt, uiTitleFont)
	pageButton(pageGame, "← Назад к настройкам", ID_SETTINGS_BACK, 640, 142, 280, 43)
	inf := pageLabel(pageGame, "Game Filter задаёт, какие дополнительные TCP/UDP-порты обрабатывать через winws.", 32, 196, 870, 38)
	setFont(inf, uiMainFont)
	pageLabel(pageGame, "Режим", 32, 262, 265, 28)
	help := pageButton(pageGame, "?", ID_HELP_GAME, 257, 258, 36, 31)
	attachTooltip(help, "TCP: надёжные соединения, игровые лаунчеры и часть сетевых функций. UDP: голос и игровой трафик. Обычно отключено, пока конкретной игре это не нужно.")
	hGame = addPageControl(pageGame, create("COMBOBOX", "", ID_GAME, 32, 303, 370, 202, CBS_DROPDOWNLIST|CBS_HASSTRINGS|CBS_OWNERDRAWFIXED|WS_TABSTOP))
	for _, t := range []string{"Выключен", "TCP + UDP", "Только TCP", "Только UDP"} {
		addCombo(hGame, t)
	}
	darkControl(hGame)
	pageLabel(pageGame, "Порты TCP", 32, 376, 400, 26)
	pageLabel(pageGame, "Порты UDP", 485, 376, 405, 26)
	hp := pageButton(pageGame, "?", ID_HELP_PORTS, 870, 369, 39, 30)
	attachTooltip(hp, "Можно указывать один порт (25565), список (27015,27016) или диапазон (27000-27100). Сохраняется отдельно для TCP и UDP.")
	hTCP = addPageControl(pageGame, create("EDIT", "", 0, 32, 412, 420, 37, WS_BORDER|WS_TABSTOP))
	hUDP = addPageControl(pageGame, create("EDIT", "", 0, 485, 412, 438, 37, WS_BORDER|WS_TABSTOP))
	darkControl(hTCP)
	darkControl(hUDP)
	expl := pageLabel(pageGame, "TCP — соединения приложений. UDP — голосовые чаты и многие игровые соединения.\nШирокий диапазон 1024–65535 захватывает много трафика — меняй только при необходимости.\nИзменения применяются после сохранения и перезапуска zapret.", 32, 486, 885, 109)
	setFont(expl, uiSmallFont)
	hGameInfo = pageLabel(pageGame, "Совет: сначала оставь Game Filter выключенным. Включай его для конкретной проблемы.", 32, 598, 888, 50)
	setFont(hGameInfo, uiSmallFont)
	pageButton(pageGame, "Сохранить Game Filter", ID_SAVE, 32, 655, 420, 44)

	hFeedback = create("STATIC", "Готов к работе", 0, 32, 716, 510, 21, 0)
	setFont(hFeedback, uiSmallFont)

	hCopyright = create("STATIC", "test build by usonixd · core: Flowseal", 0, 600, 716, 323, 21, 0)
	setFont(hCopyright, uiSmallFont)
	refreshUI()
	showPage(pageHome)
	call(pSetTimer, hwndMain, ENGINE_TIMER_ID, 1400, 0)
	// Apply geometry once all controls have been created.
	var clientRect rect
	call(pGetClientRect, hwndMain, uintptr(unsafe.Pointer(&clientRect)))
	applyResponsiveLayout(clientRect.right, clientRect.bottom)
	call(pShow, hwndMain, SW_SHOW)
	call(pUpdate, hwndMain)
	call(pPost, hwndMain, WM_APP_AUTO, 0, 0)
	return nil
}

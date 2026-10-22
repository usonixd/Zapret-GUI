//go:build windows

package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

var (
	u32            = syscall.NewLazyDLL("user32.dll")
	gdi            = syscall.NewLazyDLL("gdi32.dll")
	k32            = syscall.NewLazyDLL("kernel32.dll")
	sh             = syscall.NewLazyDLL("shell32.dll")
	pCreateWindow  = u32.NewProc("CreateWindowExW")
	pRegisterClass = u32.NewProc("RegisterClassExW")
	pDefWnd        = u32.NewProc("DefWindowProcW")
	pSend          = u32.NewProc("SendMessageW")
	pGetMsg        = u32.NewProc("GetMessageW")
	pSetTimer      = u32.NewProc("SetTimer")
	pEnable        = u32.NewProc("EnableWindow")
	pTranslate     = u32.NewProc("TranslateMessage")
	pDispatch      = u32.NewProc("DispatchMessageW")
	pPostQuit      = u32.NewProc("PostQuitMessage")
	pShow          = u32.NewProc("ShowWindow")
	pUpdate        = u32.NewProc("UpdateWindow")
	pSetText       = u32.NewProc("SetWindowTextW")
	pGetText       = u32.NewProc("GetWindowTextW")
	pGetLength     = u32.NewProc("GetWindowTextLengthW")
	pMessageBox    = u32.NewProc("MessageBoxW")
	pPost          = u32.NewProc("PostMessageW")
	pDestroy       = u32.NewProc("DestroyWindow")
	pLoadCursor    = u32.NewProc("LoadCursorW")
	pFont          = gdi.NewProc("CreateFontW")
	pSolid         = gdi.NewProc("CreateSolidBrush")
	pBk            = gdi.NewProc("SetBkColor")
	pFg            = gdi.NewProc("SetTextColor")
	pShell         = sh.NewProc("ShellExecuteW")
	pAdmin         = sh.NewProc("IsUserAnAdmin")
	pQueryImage    = k32.NewProc("QueryFullProcessImageNameW")
	pClose         = k32.NewProc("CloseHandle")
	pDwm           = syscall.NewLazyDLL("dwmapi.dll").NewProc("DwmSetWindowAttribute")
)

const (
	WS_VISIBLE          = 0x10000000
	WS_CHILD            = 0x40000000
	WS_OVERLAPPEDWINDOW = 0x00CF0000
	WS_BORDER           = 0x00800000
	WS_VSCROLL          = 0x00200000
	WS_TABSTOP          = 0x00010000
	ES_MULTILINE        = 4
	ES_AUTOVSCROLL      = 0x40
	ES_WANTRETURN       = 0x1000
	CBS_DROPDOWNLIST    = 3
	CBS_HASSTRINGS      = 0x200
	BS_AUTOCHECKBOX     = 3
	WM_CREATE           = 1
	WM_DESTROY          = 2
	WM_CLOSE            = 16
	WM_TIMER            = 0x0113
	WM_COMMAND          = 0x111
	WM_SETFONT          = 0x30
	WM_CTLCOLORSTATIC   = 0x138
	WM_CTLCOLOREDIT     = 0x133
	WM_CTLCOLORBTN      = 0x135
	WM_APP_NOTICE       = 0x8001
	WM_APP_TEST         = 0x8002
	WM_APP_AUTO         = 0x8003
	CB_ADDSTRING        = 0x143
	CB_GETCURSEL        = 0x147
	CB_SETCURSEL        = 0x14e
	BM_GETCHECK         = 0x00F0
	BM_SETCHECK         = 0x00F1
	MB_YESNO            = 4
	MB_ICONQUESTION     = 32
	MB_ICONERROR        = 16
	MB_ICONINFORMATION  = 64
	IDYES               = 6
	ID_START            = 101
	ID_STOP             = 102
	ID_TEST             = 103
	ID_CHECK_UPDATE     = 104
	ID_APPLY_UPDATE     = 105
	ID_SAVE             = 106
	ID_AUTO             = 107
	ID_AUTOCHECK        = 108
	ID_STRATEGY         = 109
	ID_GAME             = 110
	ID_OPEN             = 111
	ENGINE_TIMER_ID     = 1
)

type point struct{ X, Y int32 }
type winMsg struct {
	hwnd           uintptr
	message        uint32
	_              uint32
	wparam, lparam uintptr
	time           uint32
	pt             point
	private        uint32
}
type wndClass struct {
	size, style                        uint32
	wndProc                            uintptr
	clsExtra, wndExtra                 int32
	instance, icon, cursor, background uintptr
	menuName, className                *uint16
	iconSmall                          uintptr
}

func ptr(s string) *uint16                                 { p, _ := syscall.UTF16PtrFromString(s); return p }
func call(proc *syscall.LazyProc, args ...uintptr) uintptr { r, _, _ := proc.Call(args...); return r }
func send(hwnd uintptr, msg uint32, w, l uintptr) uintptr {
	return call(pSend, hwnd, uintptr(msg), w, l)
}
func setText(hwnd uintptr, s string) { call(pSetText, hwnd, uintptr(unsafe.Pointer(ptr(s)))) }
func getText(hwnd uintptr) string {
	n := int(call(pGetLength, hwnd))
	if n < 0 || n > 250000 {
		return ""
	}
	buf := make([]uint16, n+2)
	call(pGetText, hwnd, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	return syscall.UTF16ToString(buf)
}
func alert(title, s string, flags uintptr) int {
	return int(call(pMessageBox, hwndMain, uintptr(unsafe.Pointer(ptr(s))), uintptr(unsafe.Pointer(ptr(title))), flags))
}
func create(cls, text string, id, x, y, w, h int32, style uint32) uintptr {
	r := call(pCreateWindow, 0, uintptr(unsafe.Pointer(ptr(cls))), uintptr(unsafe.Pointer(ptr(text))), uintptr(style|WS_CHILD|WS_VISIBLE), uintptr(x), uintptr(y), uintptr(w), uintptr(h), hwndMain, uintptr(id), hinst, 0)
	if r != 0 && font != 0 {
		send(r, WM_SETFONT, font, 1)
		noteControlFont(r, font)
	}
	if r != 0 {
		registerControlRect(r, x, y, w, h)
	}
	return r
}
func addCombo(h uintptr, text string) { send(h, CB_ADDSTRING, 0, uintptr(unsafe.Pointer(ptr(text)))) }
func selCombo(h uintptr) int {
	v := send(h, CB_GETCURSEL, 0, 0)
	if int64(v) < 0 || v == ^uintptr(0) {
		return -1
	}
	return int(v)
}
func checkButton(h uintptr) bool { return send(h, BM_GETCHECK, 0, 0) == 1 }
func checkSet(h uintptr, v bool) {
	b := uintptr(0)
	if v {
		b = 1
	}
	send(h, BM_SETCHECK, b, 0)
}

var (
	root, core, settingsFile                                                                                                                string
	cfg                                                                                                                                     Config
	hwndMain, hStatus, hStatusHint, hFeedback, hStart, hStop, hVersion, hStrategy, hGame, hTCP, hUDP, hDomains, hExclude, hAuto, hAutoCheck uintptr
	font, statusFont, hinst, backgroundBrush, editBrush                                                                                     uintptr
	strategyNames                                                                                                                           []string
	newsMutex                                                                                                                               sync.Mutex
	newsText                                                                                                                                string
	newsDialog                                                                                                                              bool
	isBusy                                                                                                                                  bool
	engineVisualState                                                                                                                       = "off"
	engineStartAt                                                                                                                           time.Time
	engineLastPID                                                                                                                           int
)

func setNotice(msg string, popup bool) {
	newsMutex.Lock()
	newsText = msg
	newsDialog = popup
	newsMutex.Unlock()
	call(pPost, hwndMain, WM_APP_NOTICE, 0, 0)
}

// Operational notices are separate from the permanent process indicator.
func status(msg string) {
	if hFeedback != 0 {
		setText(hFeedback, msg)
	}
}

func renderEngineStatus() {
	if hStatus == 0 {
		return
	}
	switch engineVisualState {
	case "starting":
		setText(hStatus, "ЗАПУСКАЕТСЯ...")
		setText(hStatusHint, "Проверяем, что winws.exe не завершится сразу после запуска")
	case "on":
		setText(hStatus, "ЗАПУЩЕН")
		setText(hStatusHint, fmt.Sprintf("winws.exe работает · PID %d · Доступность сайтов проверяй отдельно", cfg.PID))
	case "failed":
		setText(hStatus, "ОШИБКА ЗАПУСКА")
		setText(hStatusHint, "winws.exe завершился. Причина может быть в файле winws.log")
	default:
		setText(hStatus, "ВЫКЛЮЧЕН")
		setText(hStatusHint, "winws.exe не запущен")
	}
	if hStart != 0 {
		call(u32.NewProc("InvalidateRect"), hStart, 0, 1)
	}
}
func boolToUintptr(b bool) uintptr {
	if b {
		return 1
	}
	return 0
}

// Checks the actual executable path, not just whether a PID exists.
// We wait briefly after starting before declaring the process stable.
func pollEngine() {
	newsMutex.Lock()
	busy := isBusy
	newsMutex.Unlock()
	if busy {
		return
	} // autotest starts/stops winws repeatedly in the background
	alive := pidMatches(cfg.PID)
	if alive {
		if engineVisualState == "starting" && time.Since(engineStartAt) < 2*time.Second {
			return
		}
		if engineVisualState != "on" {
			engineVisualState = "on"
			renderEngineStatus()
		}
		return
	}
	if cfg.PID > 0 {
		engineLastPID = cfg.PID
		cfg.PID = 0
		if e := saveConfig(settingsFile, cfg); e != nil {
			log.Printf("save PID after exit: %v", e)
		}
	}
	if engineVisualState == "on" || engineVisualState == "starting" {
		engineVisualState = "failed"
		status("Неожиданное завершение PID " + strconv.Itoa(engineLastPID) + ". Проверь: %LOCALAPPDATA%\\ZapretDesk\\winws.log")
		log.Printf("engine unexpectedly exited: PID %d", engineLastPID)
	} else if engineVisualState != "failed" {
		engineVisualState = "off"
	}
	renderEngineStatus()
}
func isAdmin() bool { return call(pAdmin) != 0 }
func executable() string {
	p, e := os.Executable()
	if e != nil {
		return ""
	}
	return p
}
func relaunchAsAdmin(args string) bool {
	exe := executable()
	r := call(pShell, hwndMain, uintptr(unsafe.Pointer(ptr("runas"))), uintptr(unsafe.Pointer(ptr(exe))), uintptr(unsafe.Pointer(ptr(args))), 0, 1)
	if r <= 32 {
		alert("Права администратора", "Запуск отменён. Для WinDivert нужны права администратора.", MB_ICONERROR)
		return false
	}
	call(pDestroy, hwndMain)
	return true
}
func hiddenCommand(name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
	b, e := cmd.CombinedOutput()
	return strings.TrimSpace(string(b)), e
}
func pidMatches(pid int) bool {
	if pid < 1 {
		return false
	}
	h, e := syscall.OpenProcess(0x1000, false, uint32(pid))
	if e != nil {
		return false
	}
	defer syscall.CloseHandle(h)
	buf := make([]uint16, 32768)
	length := uint32(len(buf))
	r := call(pQueryImage, uintptr(h), 0, uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&length)))
	if r == 0 {
		return false
	}
	actual := syscall.UTF16ToString(buf[:length])
	expected := filepath.Join(core, "bin", "winws.exe")
	return strings.EqualFold(filepath.Clean(actual), filepath.Clean(expected))
}
func otherWinwsExists() bool {
	out, _ := hiddenCommand("tasklist.exe", "/NH", "/FI", "IMAGENAME eq winws.exe")
	return strings.Contains(strings.ToLower(out), "winws.exe")
}
func stopEngine() error {
	if cfg.PID < 1 {
		return nil
	}
	if !pidMatches(cfg.PID) {
		cfg.PID = 0
		return saveConfig(settingsFile, cfg)
	}
	p, e := os.FindProcess(cfg.PID)
	if e != nil {
		return e
	}
	if e = p.Kill(); e != nil {
		return e
	}
	cfg.PID = 0
	return saveConfig(settingsFile, cfg)
}
func startEngine(strategy string) error {
	if pidMatches(cfg.PID) {
		return errors.New("zapret уже запущен через эту программу")
	}
	if otherWinwsExists() {
		return errors.New("обнаружен другой winws.exe. Сначала выключи запущенный zapret / службу, чтобы избежать конфликта")
	}
	if !isAdmin() {
		return errors.New("нужны права администратора")
	}
	if err := ensureUserFiles(core); err != nil {
		return err
	}
	if err := saveGameFilter(core, cfg); err != nil {
		return err
	}
	args, err := readArgsFromBat(filepath.Join(core, strategy), core, cfg)
	if err != nil {
		return err
	}
	cmd := exec.Command(filepath.Join(core, "bin", "winws.exe"), args...)
	cmd.Dir = filepath.Join(core, "bin")
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
	// Child stdout/stderr are needed to diagnose WinDivert failures or invalid args.
	engineLog, logErr := os.OpenFile(filepath.Join(root, "winws.log"), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if logErr == nil {
		cmd.Stdout = engineLog
		cmd.Stderr = engineLog
	} else {
		log.Printf("winws.log unavailable: %v", logErr)
	}
	err = cmd.Start()
	if engineLog != nil {
		_ = engineLog.Close()
	}
	if err != nil {
		return fmt.Errorf("не удалось запустить winws: %w", err)
	}
	cfg.PID = cmd.Process.Pid
	_ = cmd.Process.Release()
	if err = saveConfig(settingsFile, cfg); err != nil {
		return err
	}
	return nil
}
func pullUI() error {
	idx := selCombo(hStrategy)
	if idx >= 0 && idx < len(strategyNames) {
		cfg.Strategy = strategyNames[idx]
	}
	switch selCombo(hGame) {
	case 0:
		cfg.Game = "disabled"
	case 1:
		cfg.Game = "all"
	case 2:
		cfg.Game = "tcp"
	case 3:
		cfg.Game = "udp"
	}
	cfg.TCP = strings.ReplaceAll(strings.TrimSpace(getText(hTCP)), " ", "")
	cfg.UDP = strings.ReplaceAll(strings.TrimSpace(getText(hUDP)), " ", "")
	if !validatePorts(cfg.TCP) || !validatePorts(cfg.UDP) {
		return errors.New("ошибка диапазонов портов (пример: 1024-65535 или 1200,1300-1500)")
	}
	cfg.AutoCheck = checkButton(hAutoCheck)
	return saveConfig(settingsFile, cfg)
}
func writeDomains() error {
	a, e := normalizeDomains(getText(hDomains))
	if e != nil {
		return e
	}
	b, e := normalizeDomains(getText(hExclude))
	if e != nil {
		return e
	}
	if e := os.WriteFile(filepath.Join(core, "lists", "list-general-user.txt"), []byte(a), 0644); e != nil {
		return e
	}
	if e := os.WriteFile(filepath.Join(core, "lists", "list-exclude-user.txt"), []byte(b), 0644); e != nil {
		return e
	}
	return nil
}
func startupTask(enabled bool) error {
	exe := executable()
	if strings.Contains(exe, "\"") {
		return errors.New("кавычки в пути программы недопустимы")
	}
	if enabled {
		taskPath := fmt.Sprintf("\"%s\" --autostart", exe)
		out, e := hiddenCommand("schtasks.exe", "/Create", "/F", "/SC", "ONLOGON", "/TN", "ZapretDesk", "/TR", taskPath, "/RL", "HIGHEST")
		if e != nil {
			return fmt.Errorf("не удалось создать задачу автозапуска: %v, %s", e, out)
		}
	} else {
		out, e := hiddenCommand("schtasks.exe", "/Delete", "/F", "/TN", "ZapretDesk")
		if e != nil && !strings.Contains(strings.ToUpper(out), "ERROR:") {
			return fmt.Errorf("не удалось удалить задачу: %v, %s", e, out)
		}
	}
	return nil
}
func setAutoStart() {
	if !isAdmin() {
		wanted := checkButton(hAuto)
		checkSet(hAuto, cfg.AutoStart)
		if wanted {
			relaunchAsAdmin("--auto-enable")
		} else {
			relaunchAsAdmin("--auto-disable")
		}
		return
	}
	wanted := checkButton(hAuto)
	if e := startupTask(wanted); e != nil {
		checkSet(hAuto, cfg.AutoStart)
		alert("Автозапуск", e.Error(), MB_ICONERROR)
		return
	}
	cfg.AutoStart = wanted
	_ = saveConfig(settingsFile, cfg)
	status("Автозапуск Windows: настройки сохранены")
}
func currentSelection(name string) {
	for i, s := range strategyNames {
		if s == name {
			send(hStrategy, CB_SETCURSEL, uintptr(i), 0)
			return
		}
	}
	if len(strategyNames) > 0 {
		send(hStrategy, CB_SETCURSEL, 0, 0)
	}
}
func refreshUI() {
	strategyNames = enumerateStrategies(core)
	send(hStrategy, 0x14B, 0, 0) // CB_RESETCONTENT
	for _, n := range strategyNames {
		addCombo(hStrategy, strings.TrimSuffix(n, ".bat"))
	}
	currentSelection(cfg.Strategy)
	gm := 0
	switch cfg.Game {
	case "all":
		gm = 1
	case "tcp":
		gm = 2
	case "udp":
		gm = 3
	}
	send(hGame, CB_SETCURSEL, uintptr(gm), 0)
	setText(hTCP, cfg.TCP)
	setText(hUDP, cfg.UDP)
	checkSet(hAuto, cfg.AutoStart)
	checkSet(hAutoCheck, cfg.AutoCheck)
	txt, _ := os.ReadFile(filepath.Join(core, "lists", "list-general-user.txt"))
	setText(hDomains, filterPlaceholder(string(txt)))
	exc, _ := os.ReadFile(filepath.Join(core, "lists", "list-exclude-user.txt"))
	setText(hExclude, filterPlaceholder(string(exc)))
	setText(hVersion, "Flowseal "+currentCoreVersion(core)+"  ·  v0.7 beta")
	if pidMatches(cfg.PID) {
		engineVisualState = "on"
	} else if engineVisualState != "failed" {
		engineVisualState = "off"
	}
	renderEngineStatus()
}
func filterPlaceholder(s string) string {
	var lines []string
	for _, l := range strings.Split(strings.ReplaceAll(s, "\r", ""), "\n") {
		l = strings.TrimSpace(l)
		if l == "" || strings.HasPrefix(l, "#") || l == "domain.example.abc" {
			continue
		}
		lines = append(lines, l)
	}
	return strings.Join(lines, "\r\n")
}
func handleStart() {
	if e := pullUI(); e != nil {
		alert("Настройки", e.Error(), MB_ICONERROR)
		return
	}
	if !isAdmin() {
		relaunchAsAdmin("--run")
		return
	}
	if e := startEngine(cfg.Strategy); e != nil {
		engineVisualState = "failed"
		renderEngineStatus()
		status("Запуск не удался: " + e.Error())
		alert("Запуск zapret", e.Error(), MB_ICONERROR)
		return
	}
	engineLastPID = cfg.PID
	engineVisualState = "starting"
	engineStartAt = time.Now()
	status("Запуск стратегии: " + strings.TrimSuffix(cfg.Strategy, ".bat") + ". Проверяю процесс...")
	renderEngineStatus()
}
func handleStop() {
	if !isAdmin() && pidMatches(cfg.PID) {
		relaunchAsAdmin("--stop")
		return
	}
	if e := stopEngine(); e != nil {
		status("Ошибка остановки: " + e.Error())
		alert("Остановка", e.Error(), MB_ICONERROR)
		return
	}
	engineVisualState = "off"
	renderEngineStatus()
	status("zapret остановлен")
}
func client() *http.Client {
	return &http.Client{Timeout: 5 * time.Second, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 3 {
			return http.ErrUseLastResponse
		}
		return nil
	}, Transport: &http.Transport{Proxy: nil, TLSHandshakeTimeout: 4 * time.Second}}
}
func checkUpdates(notify bool) {
	c := client()
	r, e := c.Get(githubVersionURL)
	if e != nil {
		if notify {
			setNotice("Не удалось проверить обновление: "+e.Error(), true)
		}
		return
	}
	defer r.Body.Close()
	b, e := io.ReadAll(io.LimitReader(r.Body, 200))
	if e != nil || r.StatusCode != 200 {
		if notify {
			setNotice("Не удалось получить версию с GitHub", true)
		}
		return
	}
	latest := strings.TrimSpace(string(b))
	current := currentCoreVersion(core)
	if latest != "" && latest != current {
		setNotice(fmt.Sprintf("Доступно обновление ядра: %s → %s. Кнопка «Обновить» скачает релиз после подтверждения.", current, latest), notify)
	} else if notify {
		setNotice("Новых версий ядра не обнаружено (локальная "+current+")", true)
	}
}

type releaseInfo struct {
	TagName string `json:"tag_name"`
	Assets  []struct {
		Name string `json:"name"`
		URL  string `json:"browser_download_url"`
	} `json:"assets"`
}

func doUpdate() {
	newsMutex.Lock()
	if isBusy {
		newsMutex.Unlock()
		return
	}
	isBusy = true
	newsMutex.Unlock()
	defer func() { newsMutex.Lock(); isBusy = false; newsMutex.Unlock() }()
	if pidMatches(cfg.PID) {
		setNotice("Сначала останови zapret перед обновлением.", true)
		return
	}
	setNotice("Загружаю сведения о релизе Flowseal...", false)
	c := &http.Client{Timeout: 120 * time.Second}
	req, _ := http.NewRequest("GET", "https://api.github.com/repos/Flowseal/zapret-discord-youtube/releases/latest", nil)
	req.Header.Set("User-Agent", "ZapretDesk/0.7")
	r, e := c.Do(req)
	if e != nil {
		setNotice("GitHub API: "+e.Error(), true)
		return
	}
	var rel releaseInfo
	if r.StatusCode != 200 {
		r.Body.Close()
		setNotice("GitHub API вернул HTTP "+strconv.Itoa(r.StatusCode), true)
		return
	}
	e = json.NewDecoder(io.LimitReader(r.Body, 500000)).Decode(&rel)
	r.Body.Close()
	if e != nil {
		setNotice(e.Error(), true)
		return
	}
	var url string
	for _, a := range rel.Assets {
		if strings.HasSuffix(strings.ToLower(a.Name), ".zip") && strings.Contains(strings.ToLower(a.Name), "zapret") {
			url = a.URL
			break
		}
	}
	if url == "" {
		setNotice("В последнем релизе не найден ZIP с ядром. Скачай его вручную с GitHub.", true)
		return
	}
	if !strings.HasPrefix(url, "https://github.com/Flowseal/zapret-discord-youtube/releases/download/") {
		setNotice("Неожиданный адрес релиза. Обновление отклонено.", true)
		return
	}
	setNotice("Скачиваю ZIP с GitHub...", false)
	resp, e := c.Get(url)
	if e != nil {
		setNotice(e.Error(), true)
		return
	}
	if resp.StatusCode != 200 {
		resp.Body.Close()
		setNotice("Ошибка загрузки: HTTP "+strconv.Itoa(resp.StatusCode), true)
		return
	}
	zipBytes, e := io.ReadAll(io.LimitReader(resp.Body, 35*1024*1024+1))
	resp.Body.Close()
	if e != nil || len(zipBytes) > 35*1024*1024 {
		setNotice("Не удалось скачать ZIP (лимит 35 МБ)", true)
		return
	}
	stage := filepath.Join(root, "core_next")
	os.RemoveAll(stage)
	os.MkdirAll(stage, 0755)
	if e = unpackZip(zipBytes, stage); e != nil {
		setNotice("Неверный архив обновления: "+e.Error(), true)
		return
	}
	if e = backupUserLists(core, stage); e != nil {
		setNotice("Ошибка сохранения пользовательских списков: "+e.Error(), true)
		return
	}
	previous := filepath.Join(root, "core_backup_"+time.Now().Format("20060102_150405"))
	if e = os.Rename(core, previous); e != nil {
		setNotice("Не удалось сохранить резервную копию: "+e.Error(), true)
		return
	}
	if e = os.Rename(stage, core); e != nil {
		_ = os.Rename(previous, core)
		setNotice("Ошибка обновления, предыдущая сборка восстановлена: "+e.Error(), true)
		return
	}
	setNotice("Обновление установлено: "+currentCoreVersion(core)+". Резервная копия: "+previous, true)
	call(pPost, hwndMain, WM_APP_TEST, 0, 0)
}
func openURL(url string) {
	call(pShell, hwndMain, uintptr(unsafe.Pointer(ptr("open"))), uintptr(unsafe.Pointer(ptr(url))), 0, 0, 1)
}
func cmd(id int) {
	newsMutex.Lock()
	busy := isBusy
	newsMutex.Unlock()
	if busy && (id == ID_POWER || id == ID_START || id == ID_STOP || id == ID_SAVE || id == ID_TEST || id == ID_TEST_FULL || id == ID_APPLY_BEST || id == ID_APPLY_UPDATE || id == ID_AUTO) {
		alert("Операция выполняется", "Дождись окончания автоподбора или обновления.", MB_ICONINFORMATION)
		return
	}
	switch id {
	case ID_START:
		handleStart()
	case ID_STOP:
		handleStop()
	case ID_TEST:
		mode := selectedTestMode
		if e := pullUI(); e != nil {
			alert("Настройки", e.Error(), MB_ICONERROR)
			return
		}
		if !isAdmin() {
			arg := "--autotest"
			if mode == modeDiscord {
				arg += "-discord"
			}
			if mode == modeYouTube {
				arg += "-youtube"
			}
			relaunchAsAdmin(arg)
			return
		}
		if alert("Автоподбор", "Программа по очереди запустит стратегии для проверки: "+modeName(mode)+". Соединения могут прерываться. Пинг голоса и скорость видеопотока не измеряются. Запустить?", MB_YESNO|MB_ICONQUESTION) == IDYES {
			go autoTest(true, mode)
		}
	case ID_SAVE:
		if e := pullUI(); e != nil {
			alert("Настройки", e.Error(), MB_ICONERROR)
			return
		}
		if e := writeDomains(); e != nil {
			alert("Списки", e.Error(), MB_ICONERROR)
			return
		}
		if e := saveGameFilter(core, cfg); e != nil {
			alert("Game Filter", e.Error(), MB_ICONERROR)
			return
		}
		status("Сохранено. Если запущен zapret, перезапусти для применения изменений.")
	case ID_CHECK_UPDATE:
		go checkUpdates(true)
	case ID_APPLY_UPDATE:
		if alert("Обновление ядра", "Скачать и установить последнюю сборку с GitHub Flowseal? Старое ядро сохранится в резервной папке. Приложение не обновляется автоматически.", MB_YESNO|MB_ICONQUESTION) == IDYES {
			go doUpdate()
		}
	case ID_AUTO:
		setAutoStart()
	case ID_AUTOCHECK:
		cfg.AutoCheck = checkButton(hAutoCheck)
		_ = saveConfig(settingsFile, cfg)
	case ID_APPLY_BEST:
		applySuggestedStrategy()
	case ID_OPEN_REPORT:
		openReportFolder()
	case ID_CANCEL_TEST:
		if testActive.Load() {
			testCancel.Store(true)
			status("Тест остановится после текущей проверки. Тестовая стратегия будет выключена.")
		}
	case ID_NAV_HOME:
		showPage(pageHome)
	case ID_NAV_TEST:
		showPage(pageTest)
	case ID_NAV_SITES:
		showPage(pageSites)
	case ID_NAV_SETTINGS:
		showPage(pageSettings)
	case ID_CHOOSE_STRATEGY:
		togglePicker()
	case ID_PICKER_PREV:
		pickerOffset -= len(hPickerRows)
		if pickerOffset < 0 {
			pickerOffset = 0
		}
		refreshPicker()
	case ID_PICKER_NEXT:
		pickerOffset += len(hPickerRows)
		max := len(strategyNames) - len(hPickerRows)
		if max < 0 {
			max = 0
		}
		if pickerOffset > max {
			pickerOffset = max
		}
		refreshPicker()
	case ID_GAME_PAGE:
		showPage(pageGame)
	case ID_SETTINGS_BACK:
		showPage(pageSettings)
	case ID_HELP_GAME:
		showHint("Game Filter расширяет набор TCP/UDP портов, обрабатываемых winws. Это не снижает пинг само по себе. Без необходимости оставляй выключенным.")
	case ID_HELP_PORTS:
		showHint("Порты: 25565 или 27015,27016 или 27000-27100. Широкие диапазоны обрабатывают много трафика. Не меняй без нужды.")
	case ID_HELP_STRATEGY:
		showHint("Выбери стратегию и проверь Discord/YouTube вручную. Для переключения останови текущий winws.exe.")
	case ID_HELP_AUTOTEST:
		showHint("Тестируются все 22 стратегии. Это HTTPS-проверки, а не настоящий пинг голосового сервера Discord или потоковая скорость YouTube.")
	case ID_HELP_SITES:
		showHint("Добавляй только домены, например example.com. Исключения не обрабатываются zapret. Сначала сохрани списки.")
	case ID_HELP_AUTORUN:
		showHint("Автозапуск использует Планировщик заданий Windows и требует права администратора. Он включает выбранную стратегию при входе.")
	case ID_HELP_UPDATES:
		showHint("Обновляется ядро Flowseal, не интерфейс. Перед обновлением запущенный zapret нужно остановить.")
	case ID_MODE_BOTH, ID_MODE_DISCORD, ID_MODE_YOUTUBE:
		newsMutex.Lock()
		busy := isBusy
		newsMutex.Unlock()
		if busy {
			return
		}
		selectedTestMode = modeBoth
		if id == ID_MODE_DISCORD {
			selectedTestMode = modeDiscord
		}
		if id == ID_MODE_YOUTUBE {
			selectedTestMode = modeYouTube
		}
		refreshTestHighlights()
		reportMu.Lock()
		suggestedStrategy = ""
		testLeaderText = "Тест для выбранного режима ещё не запускался"
		testProgress = "Нажми «Начать тест»: будут проверены все 22 стратегии"
		lastReport = ""
		reportMu.Unlock()
		refreshReport()
	case ID_POWER:
		if engineVisualState == "on" || engineVisualState == "starting" {
			handleStop()
		} else {
			handleStart()
		}
	case ID_OPEN:
		openURL(githubLatestURL)
	default:
		if id >= ID_PICKER_ROW && id < ID_PICKER_ROW+len(hPickerRows) {
			newsMutex.Lock()
			busy := isBusy
			newsMutex.Unlock()
			if busy {
				alert("Выбор стратегии", "Дождись окончания теста.", MB_ICONINFORMATION)
				return
			}
			if pidMatches(cfg.PID) {
				alert("Стратегия", "Сначала останови zapret, затем выбери новую стратегию.", MB_ICONINFORMATION)
				return
			}
			n := pickerOffset + id - ID_PICKER_ROW
			if n < 0 || n >= len(strategyNames) {
				return
			}
			name := strategyNames[n]
			if !isStrategyAllowed(core, name) {
				return
			}
			cfg.Strategy = name
			if err := saveConfig(settingsFile, cfg); err != nil {
				alert("Ошибка сохранения", err.Error(), MB_ICONERROR)
				return
			}
			currentSelection(name)
			pickerOpen = false
			refreshHomeCaption()
			refreshPicker()
			status("Выбрана стратегия " + strings.TrimSuffix(name, ".bat") + ". Проверь её вручную после запуска.")
		}

	}
}
func fatalStartup(err error) {
	call(pMessageBox, 0, uintptr(unsafe.Pointer(ptr("Не удалось запустить Zapret Desk:\n"+err.Error()))), uintptr(unsafe.Pointer(ptr("Zapret Desk"))), MB_ICONERROR)
}
func main() {
	// Use native pixel coordinates and responsive layout, including DPI scaling.
	setDPIAwareness()
	// Win32 windows receive messages only on the OS thread that created them.
	// Go goroutines can migrate between threads unless we explicitly lock one.
	// Without this, CreateWindow may work but the GetMessage loop can run on
	// another thread, leaving the original window permanently unresponsive.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	root = filepath.Join(os.Getenv("LOCALAPPDATA"), "ZapretDesk")
	if os.Getenv("LOCALAPPDATA") == "" {
		root = filepath.Join(os.TempDir(), "ZapretDesk")
	}
	if e := os.MkdirAll(root, 0755); e != nil {
		fatalStartup(e)
		return
	}
	// Diagnostic log stays available even when the GUI message loop stalls.
	logFile, logErr := os.OpenFile(filepath.Join(root, "app.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if logErr == nil {
		defer logFile.Close()
		log.SetOutput(logFile)
		log.SetFlags(log.Ldate | log.Ltime | log.Lmicroseconds)
	}
	log.Printf("Zapret Desk %s: startup, UI OS thread locked", toolVersion)
	settingsFile = filepath.Join(root, "settings.json")
	var err error
	core, err = ensureCore(root)
	if err != nil {
		log.Printf("ensureCore failed: %v", err)
		fatalStartup(err)
		return
	}
	log.Print("core ready")
	if err = ensureUserFiles(core); err != nil {
		fatalStartup(err)
		return
	}
	cfg = loadConfig(settingsFile)
	if !isStrategyAllowed(core, cfg.Strategy) {
		cfg.Strategy = "general.bat"
	}
	if err = initGUI(); err != nil {
		log.Printf("initGUI failed: %v", err)
		fatalStartup(err)
		return
	}
	log.Print("GUI initialized; starting Windows message loop")
	var m winMsg
	for {
		r := call(pGetMsg, uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		if r == 0 || r == ^uintptr(0) {
			log.Printf("message loop stopped: %d", r)
			break
		}
		call(pTranslate, uintptr(unsafe.Pointer(&m)))
		call(pDispatch, uintptr(unsafe.Pointer(&m)))
	}
}

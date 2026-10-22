//go:build windows

package main

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type probeTarget struct {
	Label, URL string
	Download   bool
}

var discordTargets = []probeTarget{
	{"Discord web", "https://discord.com/app", false},
	{"Discord API", "https://discord.com/api/v9/gateway", false},
}
var youtubeTargets = []probeTarget{
	{"YouTube web", "https://www.youtube.com/", false},
	// Small static sample from YouTube image CDN: only a throughput proxy.
	// It is NOT a googlevideo.com media-stream measurement.
	{"YouTube CDN", "https://i.ytimg.com/vi/jfKfPfyJRdk/maxresdefault.jpg", true},
}

var (
	reportMu          sync.Mutex
	testCancel        atomic.Bool
	testActive        atomic.Bool
	testProgress      = "Выбери сервис и нажми «Начать тест» — проверятся все 22 стратегии."
	lastReport        string
	lastReportPath    string
	suggestedStrategy string
	testLeaderText    = "Результатов пока нет"
)

const WM_APP_REPORT = 0x8004

func targetsForMode(mode testMode) []probeTarget {
	if mode == modeDiscord {
		return append([]probeTarget(nil), discordTargets...)
	}
	if mode == modeYouTube {
		return append([]probeTarget(nil), youtubeTargets...)
	}
	return append(append([]probeTarget(nil), discordTargets...), youtubeTargets...)
}
func setTestReport(progress, body string) {
	reportMu.Lock()
	if progress != "" {
		testProgress = progress
	}
	if body != "" {
		lastReport = body
	}
	reportMu.Unlock()
	if hwndMain != 0 {
		call(pPost, hwndMain, WM_APP_REPORT, 0, 0)
	}
}
func shortError(s string) string {
	s = strings.ReplaceAll(s, "https://www.youtube.com/", "YouTube: ")
	s = strings.ReplaceAll(s, "https://discord.com/", "Discord: ")
	s = strings.ReplaceAll(s, "https://i.ytimg.com/", "YT CDN: ")
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) > 160 {
		s = s[:160] + "..."
	}
	return s
}
func probeOne(t probeTarget) probeOutcome {
	start := time.Now()
	out := probeOutcome{Label: t.Label}
	transport := &http.Transport{Proxy: nil, DisableKeepAlives: true,
		TLSHandshakeTimeout: 3500 * time.Millisecond, ResponseHeaderTimeout: 4 * time.Second,
		MaxIdleConns: 0, ForceAttemptHTTP2: false}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 7 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 3 {
				return http.ErrUseLastResponse
			}
			return nil
		}}
	req, err := http.NewRequest(http.MethodGet, t.URL, nil)
	if err != nil {
		out.Error = err.Error()
		return out
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 ZapretDesk/0.6")
	req.Header.Set("Cache-Control", "no-cache")
	req.Header.Set("Accept-Encoding", "identity")
	if !t.Download {
		req.Header.Set("Range", "bytes=0-255")
	}
	resp, err := client.Do(req)
	if err != nil {
		out.Took = time.Since(start)
		out.Error = shortError(err.Error())
		return out
	}
	defer resp.Body.Close()
	out.Code = resp.StatusCode
	if t.Download {
		bytes, e := io.CopyN(io.Discard, resp.Body, 2*1024*1024)
		out.Bytes = bytes
		out.Took = time.Since(start)
		if e != nil && e != io.EOF {
			out.Error = shortError(e.Error())
			return out
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			out.Error = fmt.Sprintf("HTTP %d", resp.StatusCode)
			return out
		}
		if bytes < 16*1024 {
			out.Error = fmt.Sprintf("слишком мало данных (%d байт)", bytes)
			return out
		}
		if out.Took > 0 {
			out.Mbps = float64(bytes) * 8 / out.Took.Seconds() / 1e6
		}
	} else {
		_, e := io.Copy(io.Discard, io.LimitReader(resp.Body, 128))
		out.Took = time.Since(start)
		if e != nil {
			out.Error = shortError(e.Error())
			return out
		}
		if resp.StatusCode >= 500 {
			out.Error = fmt.Sprintf("HTTP %d", resp.StatusCode)
			return out
		}
	}
	out.OK = true
	return out
}
func probeBatch(name string, targets []probeTarget) strategyOutcome {
	out := strategyOutcome{Name: name}
	results := make([]probeOutcome, len(targets)*2)
	// Two rounds are sequential so the two CDN downloads do not compete
	// with each other for bandwidth. Endpoints in a round may run concurrently.
	for retry := 0; retry < 2; retry++ {
		var wg sync.WaitGroup
		for i, t := range targets {
			pos := retry*len(targets) + i
			wg.Add(1)
			go func(idx int, t probeTarget) { defer wg.Done(); results[idx] = probeOne(t) }(pos, t)
		}
		wg.Wait()
	}
	out.Probes = results
	for _, p := range results {
		if p.OK {
			out.OK++
		} else {
			out.Errors++
		}
	}
	return out
}

func candidateNames(full bool) []string {
	if full {
		return enumerateStrategies(core)
	}
	return []string{"general.bat", "general (ALT).bat", "general (ALT2).bat", "general (ALT3).bat", "general (ALT7).bat", "general (FAKE TLS AUTO).bat", "general (SIMPLE FAKE).bat"}
}
func autoTest(full bool, mode testMode) {
	newsMutex.Lock()
	if isBusy {
		newsMutex.Unlock()
		return
	}
	isBusy = true
	newsMutex.Unlock()
	testCancel.Store(false)
	testActive.Store(true)
	call(pPost, hwndMain, WM_APP_REPORT, 0, 0)
	defer func() {
		testActive.Store(false)
		if pidMatches(cfg.PID) {
			if err := stopEngine(); err != nil {
				log.Printf("autotest cleanup failed: %v", err)
			}
		}
		newsMutex.Lock()
		isBusy = false
		newsMutex.Unlock()
		call(pPost, hwndMain, WM_APP_TEST, 0, 0)
	}()
	if pidMatches(cfg.PID) || otherWinwsExists() {
		setTestReport("Останови zapret перед тестированием.", "Обнаружен работающий winws.exe. Сначала останови его.")
		return
	}
	if !isAdmin() {
		setTestReport("Нужны права администратора", "WinDivert запускается с правами администратора.")
		return
	}
	targets := targetsForMode(mode)
	candidates := candidateNames(full)
	filtered := make([]string, 0, len(candidates))
	for _, s := range candidates {
		if isStrategyAllowed(core, s) {
			filtered = append(filtered, s)
		}
	}
	reportMu.Lock()
	suggestedStrategy = ""
	lastReport = ""
	testLeaderText = "Проверка выполняется…"
	reportMu.Unlock()
	setTestReport("Измерение без zapret…", "Сначала измеряется сеть без zapret.\r\nЗатем стратегии запускаются по очереди. Тест может временно прерывать соединения.\r\nГолосовой RTT Discord и настоящая скорость YouTube-видео не измеряются.")
	baseline := probeBatch("Без zapret", targets)
	completed := make([]strategyOutcome, 0, len(filtered))
	for i, s := range filtered {
		if testCancel.Load() {
			break
		}
		name := strings.TrimSuffix(s, ".bat")
		setTestReport(fmt.Sprintf("Проверяется %d/%d: %s", i+1, len(filtered), name), " ")
		result := strategyOutcome{Name: name}
		err := startEngine(s)
		if err != nil {
			result.StartupError = err.Error()
			result.Errors = len(targets) * 2
		} else {
			time.Sleep(1750 * time.Millisecond)
			if !pidMatches(cfg.PID) {
				result.StartupError = "winws.exe завершился сразу после запуска. Смотри winws.log"
				result.Errors = len(targets) * 2
			} else {
				result = probeBatch(name, targets)
			}
			if e := stopEngine(); e != nil {
				result.StartupError += "; остановка: " + e.Error()
				completed = append(completed, result)
				setTestReport("Остановлено: ошибка завершения winws", result.StartupError)
				break
			}
		}
		completed = append(completed, result)
		leader := bestOutcome(completed, mode)
		reportMu.Lock()
		if leader != nil {
			testLeaderText = "Пока лидирует: " + leader.Name + " · " + shortMetric(*leader, mode)
		}
		reportMu.Unlock()
		setTestReport(fmt.Sprintf("Проверено %d/%d · %s · успешно %d/%d", i+1, len(filtered), name, result.OK, len(targets)*2), buildCompactSummary(baseline, completed, mode, false))
		time.Sleep(650 * time.Millisecond)
	}
	text, choice, leader := buildFullReport(baseline, completed, mode)
	if testCancel.Load() {
		choice = ""
		leader = "Тест прерван. Рекомендация не сформирована."
	}
	dir := filepath.Join(root, "reports")
	_ = os.MkdirAll(dir, 0755)
	file := "autotest-" + string(mode) + "-" + time.Now().Format("20060102-150405") + ".txt"
	path := filepath.Join(dir, file)
	e := os.WriteFile(path, []byte(text), 0600)
	reportMu.Lock()
	if e == nil {
		lastReportPath = path
	}
	suggestedStrategy = choice
	testLeaderText = leader
	reportMu.Unlock()
	finalStatus := "Завершено"
	if testCancel.Load() {
		finalStatus = "Остановлено пользователем"
	}
	setTestReport(fmt.Sprintf("%s: %d/%d стратегий · %s", finalStatus, len(completed), len(filtered), modeName(mode)), buildCompactSummary(baseline, completed, mode, true))
	if e != nil {
		setNotice("Не удалось сохранить отчёт: "+e.Error(), false)
	}
}

// On-screen result is intentionally a short overview. Technical HTTP diagnostics
// remain in the separate report file for troubleshooting.
func buildCompactSummary(base strategyOutcome, all []strategyOutcome, mode testMode, finished bool) string {
	var b strings.Builder
	fmt.Fprintf(&b, "БЕЗ ZAPRET  ·  %s\r\n\r\n", shortMetric(base, mode))
	sorted := append([]strategyOutcome(nil), all...)
	sort.SliceStable(sorted, func(i, j int) bool { return score(sorted[i], mode) > score(sorted[j], mode) })
	b.WriteString("СТРАТЕГИИ · по результатам HTTPS-теста\r\n")
	for i, x := range sorted {
		outcome := "НЕ РАБОТАЕТ"
		if x.StartupError != "" {
			outcome = "ОШИБКА ЗАПУСКА"
		} else if x.OK > 0 && x.Errors == 0 {
			outcome = "ОК"
		} else if x.OK > 0 {
			outcome = "ЧАСТИЧНО"
		}
		fmt.Fprintf(&b, "%02d. %-34s %-12s %s\r\n", i+1, x.Name, outcome, shortMetric(x, mode))
	}
	if !finished {
		b.WriteString("\r\nПроверка продолжается…")
	}
	return b.String()
}
func shortMetric(out strategyOutcome, mode testMode) string {
	m := getMetrics(out)
	if out.StartupError != "" {
		return "процесс winws не запустился"
	}
	switch mode {
	case modeDiscord:
		return fmt.Sprintf("Discord %d/%d · HTTPS %s", m.DiscordOK, m.DiscordTotal, formatMs(m.HTTPSmedian))
	case modeYouTube:
		return fmt.Sprintf("YouTube %d/%d · CDN %d/%d", m.YouTubeOK, m.YouTubeTotal, m.CDNOK, m.CDNTotal)
	default:
		return fmt.Sprintf("Discord %d/%d · YouTube %d/%d · CDN %d/%d", m.DiscordOK, m.DiscordTotal, m.YouTubeOK, m.YouTubeTotal, m.CDNOK, m.CDNTotal)
	}
}
func buildSummary(base strategyOutcome, all []strategyOutcome, mode testMode, complete bool) string {
	var b strings.Builder
	fmt.Fprintf(&b, "БЕЗ ZAPRET: %s\r\n\r\n", formatMetric(base, mode))
	sorted := append([]strategyOutcome(nil), all...)
	sort.SliceStable(sorted, func(i, j int) bool { return score(sorted[i], mode) > score(sorted[j], mode) })
	b.WriteString("РЕЙТИНГ ПРОВЕРЕННЫХ СТРАТЕГИЙ\r\n")
	for i, x := range sorted {
		fmt.Fprintf(&b, "%02d. %-38s  %s\r\n", i+1, x.Name, formatMetric(x, mode))
		if x.StartupError != "" {
			fmt.Fprintf(&b, "     ОШИБКА: %s\r\n", x.StartupError)
		}
	}
	if !complete {
		b.WriteString("\r\nИдёт тестирование…\r\n")
	}
	return b.String()
}

func fullLeader(base strategyOutcome, all []strategyOutcome, mode testMode) (choice, description string) {
	best := bestOutcome(all, mode)
	if best == nil {
		return "", "Рабочая стратегия в этом тесте не обнаружена"
	}
	for _, s := range enumerateStrategies(core) {
		if strings.TrimSuffix(s, ".bat") == best.Name {
			choice = s
			break
		}
	}
	if choice == "" {
		return "", "Не удалось сопоставить стратегию с BAT-файлом"
	}
	m := getMetrics(*best)
	reliable := false
	switch mode {
	case modeDiscord:
		reliable = m.DiscordTotal > 0 && m.DiscordOK >= 3 &&
			hasSuccessfulProbe(*best, "Discord web") && hasSuccessfulProbe(*best, "Discord API")
	case modeYouTube:
		reliable = m.YouTubeOK >= 1 && m.CDNOK >= 1 && best.OK >= 3
	default:
		reliable = m.DiscordOK >= 3 && m.YouTubeOK >= 1 && m.CDNOK >= 1 && best.OK >= 6
	}
	if !reliable {
		return "", "Лидер: " + best.Name + " · недостаточно данных для рекомендации. Сверь вручную."
	}
	improvement := ""
	if score(*best, mode) > score(base, mode) {
		improvement = " · лучше исходного замера"
	} else {
		improvement = " · убедительного улучшения относительно исходного замера нет"
	}
	return choice, "Лидер: " + best.Name + " · " + shortMetric(*best, mode) + improvement
}
func buildFullReport(base strategyOutcome, all []strategyOutcome, mode testMode) (string, string, string) {
	choice, leader := fullLeader(base, all, mode)
	var b strings.Builder
	fmt.Fprintf(&b, "ZAPRET DESK 0.6 — ОТЧЁТ\r\n%s\r\nРежим: %s\r\nПроверено стратегий: %d\r\n\r\n", time.Now().Format("02.01.2006 15:04:05"), modeName(mode), len(all))
	b.WriteString("ЛИДЕР ТЕСТА\r\n" + leader + "\r\n\r\n")
	b.WriteString("Важно: HTTPS-задержка ≠ RTT голосового Discord. Проверяй фактический ping в голосовом канале.\r\n")
	b.WriteString("Загрузка небольшого изображения с i.ytimg.com ≠ скорость потокового видео с googlevideo.com.\r\n")
	b.WriteString("Проверь воспроизведение YouTube и голос Discord вручную перед окончательным выбором.\r\n\r\n")
	b.WriteString(buildSummary(base, all, mode, true))
	b.WriteString("\r\nПОДРОБНЫЕ ПОПЫТКИ (HTTP-коды, задержки, ошибки)\r\n")
	for _, s := range append([]strategyOutcome{base}, all...) {
		fmt.Fprintf(&b, "\r\n[%s] успешно %d/%d; ошибок %d\r\n", s.Name, s.OK, len(s.Probes), s.Errors)
		if s.StartupError != "" {
			fmt.Fprintf(&b, "Ошибка запуска: %s\r\n", s.StartupError)
		}
		for i, p := range s.Probes {
			mark := "OK"
			if !p.OK {
				mark = "FAIL"
			}
			code := "—"
			if p.Code != 0 {
				code = fmt.Sprintf("HTTP %d", p.Code)
			}
			fmt.Fprintf(&b, "  %d. %-12s %-4s %-8s %s", i+1, p.Label, mark, code, formatMs(p.Took))
			if p.Mbps > 0 {
				fmt.Fprintf(&b, " · %d байт · %.2f Мбит/с", p.Bytes, p.Mbps)
			}
			if p.Error != "" {
				fmt.Fprintf(&b, " · %s", p.Error)
			}
			b.WriteString("\r\n")
		}
	}
	return b.String(), choice, leader
}
func applySuggestedStrategy() {
	reportMu.Lock()
	choice := suggestedStrategy
	reportMu.Unlock()
	if choice == "" {
		alert("Автоподбор", "Нет стратегии для применения. Проверь результаты теста.", MB_ICONINFORMATION)
		return
	}
	if pidMatches(cfg.PID) {
		alert("Автоподбор", "Останови zapret перед сменой стратегии.", MB_ICONINFORMATION)
		return
	}
	cfg.Strategy = choice
	if err := saveConfig(settingsFile, cfg); err != nil {
		alert("Настройки", err.Error(), MB_ICONERROR)
		return
	}
	refreshUI()
	showPage(pageHome)
	status("Выбрана стратегия " + strings.TrimSuffix(choice, ".bat") + ". Чтобы запустить её, нажми центральную кнопку.")
}
func openReportFolder() {
	dir := filepath.Join(root, "reports")
	_ = os.MkdirAll(dir, 0755)
	hiddenCommand("explorer.exe", dir)
}

package main

import (
	"archive/zip"
	"bytes"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

const toolVersion = "0.7.0-beta"
const bundledCoreVersion = "1.10.4"
const githubVersionURL = "https://raw.githubusercontent.com/Flowseal/zapret-discord-youtube/main/.service/version.txt"
const githubLatestURL = "https://github.com/Flowseal/zapret-discord-youtube/releases/latest"

//go:embed core.zip
var embeddedCore []byte

type Config struct {
	Strategy  string `json:"strategy"`
	Game      string `json:"game"`
	TCP       string `json:"tcp"`
	UDP       string `json:"udp"`
	AutoStart bool   `json:"autostart"`
	AutoCheck bool   `json:"autocheck"`
	PID       int    `json:"pid"`
}

func defaultConfig() Config {
	return Config{Strategy: "general.bat", Game: "disabled", TCP: "1024-65535", UDP: "1024-65535", AutoCheck: true}
}
func loadConfig(path string) Config {
	c := defaultConfig()
	b, err := os.ReadFile(path)
	if err == nil {
		_ = json.Unmarshal(b, &c)
	}
	if c.Strategy == "" {
		c.Strategy = "general.bat"
	}
	if !validatePorts(c.TCP) {
		c.TCP = "1024-65535"
	}
	if !validatePorts(c.UDP) {
		c.UDP = "1024-65535"
	}
	if c.Game != "all" && c.Game != "tcp" && c.Game != "udp" {
		c.Game = "disabled"
	}
	return c
}
func saveConfig(path string, c Config) error {
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0600)
}
func validatePorts(s string) bool {
	if s == "" || len(s) > 150 {
		return false
	}
	for _, part := range strings.Split(s, ",") {
		sides := strings.Split(part, "-")
		if len(sides) > 2 {
			return false
		}
		prev := 0
		for i, v := range sides {
			if v == "" || len(v) > 5 {
				return false
			}
			for _, ch := range v {
				if ch < '0' || ch > '9' {
					return false
				}
			}
			n, e := strconv.Atoi(v)
			if e != nil || n < 1 || n > 65535 {
				return false
			}
			if i == 1 && n < prev {
				return false
			}
			prev = n
		}
	}
	return true
}

var domainRx = regexp.MustCompile(`(?i)^(?:[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)

func normalizeDomains(s string) (string, error) {
	out := []string{}
	found := map[string]bool{}
	for _, line := range strings.Split(strings.ReplaceAll(s, "\r", ""), "\n") {
		d := strings.ToLower(strings.TrimSpace(line))
		if d == "" || strings.HasPrefix(d, "#") {
			continue
		}
		if !domainRx.MatchString(d) || len(d) > 253 {
			return "", fmt.Errorf("неверный домен: %q (без https://, путей и масок *)", d)
		}
		if !found[d] {
			out = append(out, d)
			found[d] = true
		}
	}
	if len(out) == 0 {
		return "# empty user list\ndomain.example.abc\n", nil
	} // winws should not receive truly empty hostlists
	return strings.Join(out, "\n") + "\n", nil
}
func enumerateStrategies(core string) []string {
	entries, e := os.ReadDir(core)
	if e != nil {
		return nil
	}
	names := []string{}
	for _, f := range entries {
		n := f.Name()
		if !f.IsDir() && strings.HasPrefix(strings.ToLower(n), "general") && strings.HasSuffix(strings.ToLower(n), ".bat") {
			names = append(names, n)
		}
	}
	sort.Slice(names, func(i, j int) bool {
		if names[i] == "general.bat" {
			return true
		}
		if names[j] == "general.bat" {
			return false
		}
		return strings.ToLower(names[i]) < strings.ToLower(names[j])
	})
	return names
}
func isStrategyAllowed(core, name string) bool {
	for _, s := range enumerateStrategies(core) {
		if name == s {
			return true
		}
	}
	return false
}
func readArgsFromBat(file string, core string, c Config) ([]string, error) {
	if !isStrategyAllowed(core, filepath.Base(file)) {
		return nil, errors.New("стратегия отсутствует в сборке")
	}
	b, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	content := strings.ReplaceAll(string(bytes.TrimPrefix(b, []byte{0xef, 0xbb, 0xbf})), "\r", "")
	lines := strings.Split(content, "\n")
	var raw strings.Builder
	active := false
	for _, line := range lines {
		t := strings.TrimSpace(line)
		if !active {
			idx := strings.Index(strings.ToLower(t), "winws.exe\"")
			if idx < 0 {
				continue
			}
			active = true
			t = strings.TrimSpace(t[idx+len("winws.exe\""):])
		}
		cont := strings.HasSuffix(t, "^")
		t = strings.TrimSpace(strings.TrimSuffix(t, "^"))
		raw.WriteString(t)
		raw.WriteByte(' ')
		if !cont {
			break
		}
	}
	if !active {
		return nil, errors.New("winws.exe не найден в BAT")
	}
	tcp, udp := "12", "12"
	if c.Game == "all" || c.Game == "tcp" {
		tcp = c.TCP
	}
	if c.Game == "all" || c.Game == "udp" {
		udp = c.UDP
	}
	argstr := raw.String()
	rep := strings.NewReplacer("%BIN%", filepath.Join(core, "bin")+string(os.PathSeparator), "%LISTS%", filepath.Join(core, "lists")+string(os.PathSeparator), "%GameFilterTCP%", tcp, "%GameFilterUDP%", udp)
	argstr = rep.Replace(argstr)
	if strings.Contains(argstr, "%") {
		return nil, fmt.Errorf("неизвестная переменная в BAT: %s", argstr)
	}
	args, err := parseQuotedArgs(argstr)
	if err != nil {
		return nil, err
	}
	if len(args) < 8 {
		return nil, errors.New("подозрительно мало аргументов стратегии")
	}
	return args, nil
}

// winws bat arguments are simple quoted paths (no Windows shell metacharacter evaluation).
func parseQuotedArgs(s string) ([]string, error) {
	var out []string
	var b strings.Builder
	quoted := false
	started := false
	for _, ch := range s {
		switch {
		case ch == '"':
			quoted = !quoted
			started = true
		case ch == ' ' || ch == '\t' || ch == '\n':
			if quoted {
				b.WriteRune(ch)
			} else if started {
				out = append(out, b.String())
				b.Reset()
				started = false
			}
		default:
			b.WriteRune(ch)
			started = true
		}
	}
	if quoted {
		return nil, errors.New("непарные кавычки в стратегии")
	}
	if started {
		out = append(out, b.String())
	}
	return out, nil
}
func unpackZip(blob []byte, dest string) error {
	r, e := zip.NewReader(bytes.NewReader(blob), int64(len(blob)))
	if e != nil {
		return e
	}
	for _, f := range r.File {
		name := strings.ReplaceAll(f.Name, "\\", "/")
		// Official archives contain exactly one top directory. Protect against zip-slip.
		pieces := strings.SplitN(name, "/", 2)
		if len(pieces) < 2 || pieces[1] == "" {
			continue
		}
		rel := filepath.Clean(filepath.FromSlash(pieces[1]))
		if rel == "." || rel == ".." || filepath.IsAbs(rel) || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
			return fmt.Errorf("unsafe ZIP path: %q", name)
		}
		target := filepath.Join(dest, rel)
		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0755); err != nil {
				return err
			}
			continue
		}
		if f.UncompressedSize64 > 70*1024*1024 {
			return fmt.Errorf("ZIP file too large: %q", name)
		}
		if e := os.MkdirAll(filepath.Dir(target), 0755); e != nil {
			return e
		}
		rc, e := f.Open()
		if e != nil {
			return e
		}
		fd, e := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
		if e != nil {
			rc.Close()
			return e
		}
		_, copyErr := io.CopyN(fd, rc, 71*1024*1024)
		c1 := fd.Close()
		c2 := rc.Close()
		if copyErr != nil && copyErr != io.EOF {
			return copyErr
		}
		if c1 != nil {
			return c1
		}
		if c2 != nil {
			return c2
		}
	}
	if _, err := os.Stat(filepath.Join(dest, "bin", "winws.exe")); err != nil {
		return fmt.Errorf("в архиве нет winws.exe: %w", err)
	}
	if _, err := os.Stat(filepath.Join(dest, "general.bat")); err != nil {
		return fmt.Errorf("в архиве нет general.bat: %w", err)
	}
	return nil
}
func ensureCore(root string) (string, error) {
	core := filepath.Join(root, "core")
	if _, e := os.Stat(filepath.Join(core, "bin", "winws.exe")); e == nil {
		return core, nil
	}
	stage := filepath.Join(root, "stage_first")
	os.RemoveAll(stage)
	if err := os.MkdirAll(stage, 0755); err != nil {
		return "", err
	}
	if err := unpackZip(embeddedCore, stage); err != nil {
		return "", err
	}
	if err := os.Rename(stage, core); err != nil {
		return "", err
	}
	return core, nil
}
func ensureUserFiles(core string) error {
	for _, f := range []string{"list-general-user.txt", "list-exclude-user.txt", "ipset-exclude-user.txt"} {
		p := filepath.Join(core, "lists", f)
		if _, e := os.Stat(p); os.IsNotExist(e) {
			value := "domain.example.abc\n"
			if f == "ipset-exclude-user.txt" {
				value = "203.0.113.113/32\n"
			}
			if e := os.WriteFile(p, []byte(value), 0644); e != nil {
				return e
			}
		}
	}
	return nil
}
func saveGameFilter(core string, c Config) error {
	if !validatePorts(c.TCP) || !validatePorts(c.UDP) {
		return errors.New("неправильные порты")
	}
	s := fmt.Sprintf("mode=%s\ntcp=%s\nudp=%s\n", c.Game, c.TCP, c.UDP)
	return os.WriteFile(filepath.Join(core, "utils", "game_filter.enabled"), []byte(s), 0644)
}
func currentCoreVersion(core string) string {
	b, e := os.ReadFile(filepath.Join(core, "service.bat"))
	if e != nil {
		return bundledCoreVersion
	}
	r := regexp.MustCompile(`(?m)^set "LOCAL_VERSION=([0-9A-Za-z.\-]+)"`)
	m := r.FindSubmatch(b)
	if len(m) > 1 {
		return string(m[1])
	}
	return bundledCoreVersion
}
func backupUserLists(oldCore, newCore string) error {
	for _, f := range []string{"list-general-user.txt", "list-exclude-user.txt", "ipset-exclude-user.txt", "ipset-all.txt", "utils/game_filter.enabled"} {
		b, e := os.ReadFile(filepath.Join(oldCore, filepath.FromSlash(f)))
		if os.IsNotExist(e) {
			continue
		}
		if e != nil {
			return e
		}
		p := filepath.Join(newCore, filepath.FromSlash(f))
		if e := os.MkdirAll(filepath.Dir(p), 0755); e != nil {
			return e
		}
		if e := os.WriteFile(p, b, 0644); e != nil {
			return e
		}
	}
	return nil
}

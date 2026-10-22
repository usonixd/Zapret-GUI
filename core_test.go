package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBundledStrategies(t *testing.T) {
	root := t.TempDir()
	core, err := ensureCore(root)
	if err != nil {
		t.Fatal(err)
	}
	if err = ensureUserFiles(core); err != nil {
		t.Fatal(err)
	}
	strategies := enumerateStrategies(core)
	if len(strategies) != 22 {
		t.Fatalf("want 22 strategies, got %d", len(strategies))
	}
	c := defaultConfig()
	for _, name := range strategies {
		args, e := readArgsFromBat(filepath.Join(core, name), core, c)
		if e != nil {
			t.Fatalf("%s: %v", name, e)
		}
		joined := strings.Join(args, " ")
		if !strings.Contains(joined, "--wf-tcp=") || !strings.Contains(joined, "--wf-udp=") {
			t.Fatalf("%s: missing filter args", name)
		}
		if strings.Contains(joined, "%BIN%") || strings.Contains(joined, "%LISTS%") {
			t.Fatalf("%s: unresolved variables", name)
		}
		if !strings.Contains(joined, "list-general-user.txt") {
			t.Fatalf("%s missing user domain list", name)
		}
	}
}
func TestGameFilter(t *testing.T) {
	root := t.TempDir()
	core, err := ensureCore(root)
	if err != nil {
		t.Fatal(err)
	}
	c := defaultConfig()
	c.Game = "udp"
	c.UDP = "10000-19999"
	c.TCP = "3000-3999"
	a, e := readArgsFromBat(filepath.Join(core, "general.bat"), core, c)
	if e != nil {
		t.Fatal(e)
	}
	all := strings.Join(a, " ")
	if !strings.Contains(all, "10000-19999") || strings.Contains(all, "3000-3999") {
		t.Fatal("UDP-only filter invalid")
	}
	if e = saveGameFilter(core, c); e != nil {
		t.Fatal(e)
	}
	txt, e := os.ReadFile(filepath.Join(core, "utils", "game_filter.enabled"))
	if e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(string(txt), "mode=udp") {
		t.Fatal(string(txt))
	}
}
func TestDomainValidation(t *testing.T) {
	got, e := normalizeDomains("Discord.com\nexample.org\ndiscord.com\n")
	if e != nil {
		t.Fatal(e)
	}
	if strings.Count(got, "discord.com") != 1 {
		t.Fatalf("duplicates not removed: %q", got)
	}
	for _, s := range []string{"https://example.org", "../bad", "*.discord.com", "example.com/path", "bad domain"} {
		if _, e = normalizeDomains(s); e == nil {
			t.Fatalf("accepted invalid %q", s)
		}
	}
}
func TestPortValidation(t *testing.T) {
	for _, s := range []string{"1024-65535", "443", "1024,3000-5000"} {
		if !validatePorts(s) {
			t.Fatalf("rejected %q", s)
		}
	}
	for _, s := range []string{"0", "65536", "100-10", "1--2", "", "12&whoami"} {
		if validatePorts(s) {
			t.Fatalf("accepted invalid %q", s)
		}
	}
}

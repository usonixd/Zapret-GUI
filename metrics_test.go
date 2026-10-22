package main

import (
	"strings"
	"testing"
	"time"
)

func sampleDiscord(name string, outcomes []bool, durations []int) strategyOutcome {
	x := strategyOutcome{Name: name}
	labels := []string{"Discord web", "Discord API", "Discord web", "Discord API"}
	for i, ok := range outcomes {
		p := probeOutcome{Label: labels[i], OK: ok, Took: time.Duration(durations[i]) * time.Millisecond}
		x.Probes = append(x.Probes, p)
		if ok {
			x.OK++
		} else {
			x.Errors++
		}
	}
	return x
}
func sampleYouTube(name string, outcomes []bool, speeds []float64) strategyOutcome {
	x := strategyOutcome{Name: name}
	labels := []string{"YouTube web", "YouTube CDN", "YouTube web", "YouTube CDN"}
	for i, ok := range outcomes {
		p := probeOutcome{Label: labels[i], OK: ok}
		if strings.Contains(labels[i], "CDN") {
			p.Mbps = speeds[i/2]
		}
		x.Probes = append(x.Probes, p)
		if ok {
			x.OK++
		} else {
			x.Errors++
		}
	}
	return x
}
func TestDiscordReliabilityBeforeLatency(t *testing.T) {
	reliable := sampleDiscord("stable", []bool{true, true, true, true}, []int{150, 180, 160, 170})
	fastFlaky := sampleDiscord("flaky", []bool{true, false, true, false}, []int{7, 0, 6, 0})
	got := bestOutcome([]strategyOutcome{fastFlaky, reliable}, modeDiscord)
	if got == nil || got.Name != "stable" {
		t.Fatalf("stable endpoint should outrank flaky, got %#v", got)
	}
}
func TestDiscordLowerMedianWinsOnEqualReliability(t *testing.T) {
	slow := sampleDiscord("slow", []bool{true, true, true, true}, []int{260, 260, 270, 280})
	fast := sampleDiscord("fast", []bool{true, true, true, true}, []int{110, 115, 120, 125})
	got := bestOutcome([]strategyOutcome{slow, fast}, modeDiscord)
	if got == nil || got.Name != "fast" {
		t.Fatalf("unexpected result: %#v", got)
	}
	if getMetrics(fast).HTTPSmedian != 117*time.Millisecond+500*time.Microsecond {
		t.Fatalf("wrong median: %v", getMetrics(fast).HTTPSmedian)
	}
}
func TestYouTubeSuccessfulConnectionsBeforeThroughput(t *testing.T) {
	stable := sampleYouTube("stable", []bool{true, true, true, true}, []float64{6, 7})
	flaky := sampleYouTube("flaky", []bool{true, true, false, true}, []float64{90, 110})
	got := bestOutcome([]strategyOutcome{flaky, stable}, modeYouTube)
	if got == nil || got.Name != "stable" {
		t.Fatalf("incorrect ranking %#v", got)
	}
	faster := sampleYouTube("faster", []bool{true, true, true, true}, []float64{40, 42})
	got = bestOutcome([]strategyOutcome{stable, faster}, modeYouTube)
	if got == nil || got.Name != "faster" {
		t.Fatalf("speed should break reliability ties: %#v", got)
	}
}
func TestFailedStartupNeverRecommended(t *testing.T) {
	bad := sampleDiscord("bad", []bool{true, true, true, true}, []int{1, 1, 1, 1})
	bad.StartupError = "cannot launch"
	if got := bestOutcome([]strategyOutcome{bad}, modeDiscord); got != nil {
		t.Fatalf("failed startup returned %#v", got)
	}
}
func TestMetricLabelsClarifyProxies(t *testing.T) {
	d := sampleDiscord("test", []bool{true, true, true, true}, []int{100, 120, 110, 130})
	if !strings.Contains(formatMetric(d, modeDiscord), "не пинг голоса") {
		t.Fatal("Discord latency caveat missing")
	}
	y := sampleYouTube("test", []bool{true, true, true, true}, []float64{6, 10})
	if !strings.Contains(formatMetric(y, modeYouTube), "Мбит/с") {
		t.Fatal("YouTube speed unit missing")
	}
}

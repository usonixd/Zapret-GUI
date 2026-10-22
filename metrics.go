package main

import (
	"fmt"
	"math"
	"sort"
	"time"
)

type testMode string

const (
	modeBoth    testMode = "both"
	modeDiscord testMode = "discord"
	modeYouTube testMode = "youtube"
)

type probeOutcome struct {
	Label string
	Code  int
	Took  time.Duration
	Bytes int64
	Mbps  float64
	Error string
	OK    bool
}
type strategyOutcome struct {
	Name         string
	Probes       []probeOutcome
	OK           int
	Errors       int
	StartupError string
}
type metrics struct {
	DiscordOK, DiscordTotal int
	YouTubeOK, YouTubeTotal int
	CDNOK, CDNTotal         int
	HTTPSmedian             time.Duration
	CDNmedianMbps           float64
}

func medianDuration(ds []time.Duration) time.Duration {
	if len(ds) == 0 {
		return 0
	}
	sort.Slice(ds, func(i, j int) bool { return ds[i] < ds[j] })
	if len(ds)%2 == 1 {
		return ds[len(ds)/2]
	}
	return (ds[len(ds)/2-1] + ds[len(ds)/2]) / 2
}

func medianFloat(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	sort.Float64s(xs)
	if len(xs)%2 == 1 {
		return xs[len(xs)/2]
	}
	return (xs[len(xs)/2-1] + xs[len(xs)/2]) / 2
}

func getMetrics(out strategyOutcome) metrics {
	var m metrics
	var d []time.Duration
	var speeds []float64
	for _, p := range out.Probes {
		switch p.Label {
		case "Discord web", "Discord API":
			m.DiscordTotal++
			if p.OK {
				m.DiscordOK++
				d = append(d, p.Took)
			}
		case "YouTube web":
			m.YouTubeTotal++
			if p.OK {
				m.YouTubeOK++
			}
		case "YouTube CDN":
			m.CDNTotal++
			if p.OK {
				m.CDNOK++
				speeds = append(speeds, p.Mbps)
			}
		}
	}
	m.HTTPSmedian = medianDuration(d)
	m.CDNmedianMbps = medianFloat(speeds)
	return m
}

func score(out strategyOutcome, mode testMode) float64 {
	if out.StartupError != "" || len(out.Probes) == 0 {
		return -1
	}
	m := getMetrics(out)
	if mode == modeDiscord {
		if m.DiscordTotal == 0 {
			return -1
		}
		// Reliable connectivity first; median HTTPS delay second. No voice RTT claim.
		ratio := float64(m.DiscordOK) / float64(m.DiscordTotal)
		if m.DiscordOK == 0 {
			return 0
		}
		delay := math.Min(float64(m.HTTPSmedian.Milliseconds()), 5000)
		return ratio*100000 + (5000 - delay)
	}
	if mode == modeYouTube {
		if m.YouTubeTotal+m.CDNTotal == 0 {
			return -1
		}
		total := m.YouTubeTotal + m.CDNTotal
		ok := m.YouTubeOK + m.CDNOK
		if ok == 0 {
			return 0
		}
		ratio := float64(ok) / float64(total)
		// CDN success is required for best score; modest sample speed used for ties.
		bonus := math.Min(m.CDNmedianMbps, 200)
		return ratio*100000 + bonus
	}
	if m.DiscordTotal+m.YouTubeTotal+m.CDNTotal == 0 {
		return -1
	}
	total := m.DiscordTotal + m.YouTubeTotal + m.CDNTotal
	ok := m.DiscordOK + m.YouTubeOK + m.CDNOK
	if ok == 0 {
		return 0
	}
	bonus := math.Min(m.CDNmedianMbps, 200)
	if m.HTTPSmedian > 0 {
		bonus += (5000 - math.Min(float64(m.HTTPSmedian.Milliseconds()), 5000)) / 100
	}
	return float64(ok)/float64(total)*100000 + bonus
}

func bestOutcome(all []strategyOutcome, mode testMode) *strategyOutcome {
	var winner *strategyOutcome
	for i := range all {
		c := &all[i]
		if score(*c, mode) <= 0 {
			continue
		}
		if winner == nil || score(*c, mode) > score(*winner, mode) {
			winner = c
		}
	}
	return winner
}

func formatMetric(out strategyOutcome, mode testMode) string {
	m := getMetrics(out)
	switch mode {
	case modeDiscord:
		return fmt.Sprintf("HTTPS %d/%d · медиана %s (не пинг голоса)", m.DiscordOK, m.DiscordTotal, formatMs(m.HTTPSmedian))
	case modeYouTube:
		speed := "н/д"
		if m.CDNOK > 0 {
			speed = fmt.Sprintf("%.1f Мбит/с", m.CDNmedianMbps)
		}
		return fmt.Sprintf("сайт %d/%d · CDN %d/%d · тестовая загрузка %s", m.YouTubeOK, m.YouTubeTotal, m.CDNOK, m.CDNTotal, speed)
	default:
		speed := "н/д"
		if m.CDNOK > 0 {
			speed = fmt.Sprintf("%.1f Мбит/с", m.CDNmedianMbps)
		}
		return fmt.Sprintf("Discord %d/%d (%s) · YouTube %d/%d, CDN %d/%d (%s)", m.DiscordOK, m.DiscordTotal, formatMs(m.HTTPSmedian), m.YouTubeOK, m.YouTubeTotal, m.CDNOK, m.CDNTotal, speed)
	}
}

func formatMs(d time.Duration) string {
	if d <= 0 {
		return "—"
	}
	return fmt.Sprintf("%d мс", d.Milliseconds())
}

func modeName(mode testMode) string {
	switch mode {
	case modeDiscord:
		return "Discord · минимальная HTTPS-задержка"
	case modeYouTube:
		return "YouTube · скорость тестовой загрузки"
	default:
		return "Универсальный · обе платформы"
	}
}

func hasSuccessfulProbe(out strategyOutcome, label string) bool {
	for _, p := range out.Probes {
		if p.Label == label && p.OK {
			return true
		}
	}
	return false
}

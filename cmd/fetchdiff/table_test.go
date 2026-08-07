package main

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/BehiSecc/fetchdiff/internal/model"
)

func TestRenderTargetTableIsCompactAndAligned(t *testing.T) {
	targets := []model.Target{
		{
			Name: "SoundOn-ClientHomeBundle", ResourceType: model.ResourceJavaScript, Every: 24 * time.Hour,
			URL: "https://sf-soundon-fe.soundoncdn-us.com/obj/soundon-fe-tx/soundon/client-home/static/js/main.2a7cc22d.js",
		},
		{
			Name: "failing-target", ResourceType: model.ResourceHTML, Every: 30 * time.Minute,
			URL: "https://example.com/pages/index.html", ConsecutiveFailures: 2,
		},
	}
	var output bytes.Buffer
	renderTargetTable(&output, targets, false)
	text := output.String()
	for _, expected := range []string{"┌", "NAME", "SoundOn-ClientHomeBundle", "JS", "24h", "● Healthy", "● Failing (2)", "main.2a7cc22d.js"} {
		if !strings.Contains(text, expected) {
			t.Fatalf("table is missing %q:\n%s", expected, text)
		}
	}
	if strings.Contains(text, targets[0].URL) || strings.Contains(text, "24h0m0s") || strings.Contains(text, "\x1b[") {
		t.Fatalf("table is not compact or color-free:\n%s", text)
	}
	lines := strings.Split(strings.TrimSpace(text), "\n")
	wantWidth := runeWidth(lines[0])
	for _, line := range lines[1:] {
		if got := runeWidth(line); got != wantWidth {
			t.Fatalf("line width = %d, want %d:\n%s", got, wantWidth, text)
		}
	}
}

func TestRenderTargetTableColorsWhenEnabled(t *testing.T) {
	var output bytes.Buffer
	renderTargetTable(&output, []model.Target{{Name: "app", ResourceType: model.ResourceJavaScript, Every: time.Hour, URL: "https://example.com/app.js"}}, true)
	if !strings.Contains(output.String(), ansiBoldCyan) || !strings.Contains(output.String(), ansiGreen) || !strings.Contains(output.String(), ansiDim) {
		t.Fatalf("colored table:\n%q", output.String())
	}
	if colorEnabled(&output) {
		t.Fatal("non-terminal writer unexpectedly enabled colors")
	}
}

func TestRenderTargetDetailsIncludesFullMetadata(t *testing.T) {
	target := model.Target{
		Name: "production-js", URL: "https://cdn.example.com/assets/app.js", ResourceType: model.ResourceJavaScript,
		Every: 24 * time.Hour, StatusCode: 200, SnapshotSize: 188416,
		SnapshotHash: strings.Repeat("a", 64), CreatedAt: time.Date(2026, 8, 7, 10, 0, 0, 0, time.UTC),
		LastCheckedAt: time.Date(2026, 8, 7, 11, 0, 0, 0, time.UTC), NextCheckAt: time.Date(2026, 8, 8, 11, 0, 0, 0, time.UTC),
	}
	var output bytes.Buffer
	renderTargetDetails(&output, target, false)
	text := output.String()
	for _, expected := range []string{target.Name, target.URL, "● Healthy", "JavaScript", "24h", "200 OK", "184.0 KB", target.SnapshotHash, "2026-08-08 11:00 UTC"} {
		if !strings.Contains(text, expected) {
			t.Fatalf("details are missing %q:\n%s", expected, text)
		}
	}
}

func TestCompactSourceAndTruncation(t *testing.T) {
	if got := compactSource("https://www.example.com/a/very/long/path/app.123.js?x=1"); got != "example.com/…/app.123.js" {
		t.Fatalf("source = %q", got)
	}
	if got := truncateMiddle("abcdefghijklmnopqrstuvwxyz", 9); runeWidth(got) != 9 || !strings.Contains(got, "…") {
		t.Fatalf("truncated = %q", got)
	}
}

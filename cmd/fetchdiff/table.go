package main

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"strings"
	"time"

	"github.com/BehiSecc/fetchdiff/internal/model"
	"golang.org/x/net/publicsuffix"
)

const (
	ansiReset       = "\x1b[0m"
	ansiBoldCyan    = "\x1b[1;36m"
	ansiBrightWhite = "\x1b[1;97m"
	ansiBlue        = "\x1b[94m"
	ansiYellow      = "\x1b[33m"
	ansiGreen       = "\x1b[32m"
	ansiRed         = "\x1b[31m"
	ansiDim         = "\x1b[2m"
)

type palette struct {
	enabled bool
}

type targetTableRow struct {
	name         string
	resourceType string
	every        string
	health       string
	source       string
	failing      bool
}

func colorEnabled(out io.Writer) bool {
	if _, disabled := os.LookupEnv("NO_COLOR"); disabled {
		return false
	}
	if strings.EqualFold(os.Getenv("TERM"), "dumb") {
		return false
	}
	file, ok := out.(*os.File)
	if !ok {
		return false
	}
	info, err := file.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

func (p palette) paint(code, value string) string {
	if !p.enabled {
		return value
	}
	return code + value + ansiReset
}

func renderTargetTable(out io.Writer, targets []model.Target, colors bool) {
	rows := make([]targetTableRow, 0, len(targets))
	for _, target := range targets {
		rows = append(rows, targetTableRow{
			name: truncateEnd(target.Name, 30), resourceType: shortResourceType(target.ResourceType),
			every: compactDuration(target.Every), health: tableHealth(target),
			source: truncateMiddle(compactSource(target.URL), 44), failing: target.ConsecutiveFailures > 0,
		})
	}
	headers := []string{"NAME", "TYPE", "EVERY", "HEALTH", "SOURCE"}
	widths := []int{runeWidth(headers[0]), runeWidth(headers[1]), runeWidth(headers[2]), runeWidth(headers[3]), runeWidth(headers[4])}
	for _, row := range rows {
		for index, value := range []string{row.name, row.resourceType, row.every, row.health, row.source} {
			if width := runeWidth(value); width > widths[index] {
				widths[index] = width
			}
		}
	}
	p := palette{enabled: colors}
	fmt.Fprintln(out, p.paint(ansiDim, tableBorder("┌", "┬", "┐", widths)))
	writeTableRow(out, p, headers, widths, -1, true)
	fmt.Fprintln(out, p.paint(ansiDim, tableBorder("├", "┼", "┤", widths)))
	for _, row := range rows {
		failingColumn := -1
		if row.failing {
			failingColumn = 3
		}
		writeTableRow(out, p, []string{row.name, row.resourceType, row.every, row.health, row.source}, widths, failingColumn, false)
	}
	fmt.Fprintln(out, p.paint(ansiDim, tableBorder("└", "┴", "┘", widths)))
}

func writeTableRow(out io.Writer, p palette, values []string, widths []int, failingColumn int, header bool) {
	fmt.Fprint(out, p.paint(ansiDim, "│"))
	for index, value := range values {
		value = padRight(value, widths[index])
		code := ansiBrightWhite
		if header {
			code = ansiBoldCyan
		} else {
			switch index {
			case 1:
				code = ansiBlue
			case 2:
				code = ansiYellow
			case 3:
				code = ansiGreen
				if failingColumn == index {
					code = ansiRed
				}
			case 4:
				code = ansiDim
			}
		}
		fmt.Fprintf(out, " %s %s", p.paint(code, value), p.paint(ansiDim, "│"))
	}
	fmt.Fprintln(out)
}

func tableBorder(left, middle, right string, widths []int) string {
	parts := make([]string, len(widths))
	for index, width := range widths {
		parts[index] = strings.Repeat("─", width+2)
	}
	return left + strings.Join(parts, middle) + right
}

func renderTargetDetails(out io.Writer, target model.Target, colors bool) {
	p := palette{enabled: colors}
	fmt.Fprintf(out, "%s\n\n", p.paint(ansiBrightWhite, target.Name))
	writeDetail(out, p, "Health", p.paint(healthColor(target), detailHealth(target)))
	writeDetail(out, p, "Type", target.ResourceType)
	writeDetail(out, p, "Interval", compactDuration(target.Every))
	writeDetail(out, p, "URL", target.URL)
	if target.EffectiveURL != "" && target.EffectiveURL != target.URL {
		writeDetail(out, p, "Effective URL", target.EffectiveURL)
	}
	status := fmt.Sprintf("%d", target.StatusCode)
	if text := http.StatusText(target.StatusCode); text != "" {
		status += " " + text
	}
	writeDetail(out, p, "Status", status)
	writeDetail(out, p, "Size", formatBytes(target.SnapshotSize))
	writeDetail(out, p, "SHA-256", target.SnapshotHash)
	if target.ETag != "" {
		writeDetail(out, p, "ETag", target.ETag)
	}
	if target.LastModified != "" {
		writeDetail(out, p, "Last modified", target.LastModified)
	}
	writeDetail(out, p, "Created", formatTime(target.CreatedAt))
	writeDetail(out, p, "Last check", formatTime(target.LastCheckedAt))
	writeDetail(out, p, "Last change", formatTime(target.LastChangedAt))
	writeDetail(out, p, "Next check", formatTime(target.NextCheckAt))
	if target.LastError != "" {
		writeDetail(out, p, "Last error", target.LastError)
	}
}

func writeDetail(out io.Writer, p palette, label, value string) {
	fmt.Fprintf(out, "%s  %s\n", p.paint(ansiBoldCyan, padRight(label+":", 14)), value)
}

func healthColor(target model.Target) string {
	if target.ConsecutiveFailures > 0 {
		return ansiRed
	}
	return ansiGreen
}

func tableHealth(target model.Target) string {
	if target.ConsecutiveFailures > 0 {
		return fmt.Sprintf("● Failing (%d)", target.ConsecutiveFailures)
	}
	return "● Healthy"
}

func detailHealth(target model.Target) string {
	if target.ConsecutiveFailures > 0 {
		return fmt.Sprintf("● Failing (%d consecutive)", target.ConsecutiveFailures)
	}
	return "● Healthy"
}

func shortResourceType(resourceType string) string {
	switch resourceType {
	case model.ResourceJavaScript:
		return "JS"
	case model.ResourceHTML:
		return "HTML"
	default:
		return resourceType
	}
}

func compactDuration(value time.Duration) string {
	if value > 0 && value%time.Hour == 0 {
		return fmt.Sprintf("%dh", int64(value/time.Hour))
	}
	if value > 0 && value%time.Minute == 0 {
		return fmt.Sprintf("%dm", int64(value/time.Minute))
	}
	return value.String()
}

func compactSource(rawURL string) string {
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Host == "" {
		return rawURL
	}
	host := strings.TrimPrefix(parsed.Hostname(), "www.")
	if domain, domainErr := publicsuffix.EffectiveTLDPlusOne(host); domainErr == nil {
		host = domain
	}
	if port := parsed.Port(); port != "" {
		host += ":" + port
	}
	filename := path.Base(parsed.Path)
	if filename == "." || filename == "/" || filename == "" {
		return host
	}
	return host + "/…/" + filename
}

func truncateEnd(value string, maximum int) string {
	runes := []rune(value)
	if len(runes) <= maximum {
		return value
	}
	if maximum <= 1 {
		return "…"
	}
	return string(runes[:maximum-1]) + "…"
}

func truncateMiddle(value string, maximum int) string {
	runes := []rune(value)
	if len(runes) <= maximum {
		return value
	}
	if maximum <= 1 {
		return "…"
	}
	left := (maximum - 1) / 2
	right := maximum - 1 - left
	return string(runes[:left]) + "…" + string(runes[len(runes)-right:])
}

func padRight(value string, width int) string {
	return value + strings.Repeat(" ", width-runeWidth(value))
}

func runeWidth(value string) int {
	return len([]rune(value))
}

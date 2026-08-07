package main

import (
	"testing"
	"time"
)

func TestParseInterval(t *testing.T) {
	tests := map[string]time.Duration{
		"1m":       time.Minute,
		"24h":      24 * time.Hour,
		"7d":       7 * 24 * time.Hour,
		"2w":       2 * 7 * 24 * time.Hour,
		"1w2d6h":   (7*24 + 2*24 + 6) * time.Hour,
		"1d12h30m": 36*time.Hour + 30*time.Minute,
		"1.5d":     36 * time.Hour,
		"1d0h":     24 * time.Hour,
	}
	for input, expected := range tests {
		t.Run(input, func(t *testing.T) {
			actual, err := parseInterval(input)
			if err != nil {
				t.Fatal(err)
			}
			if actual != expected {
				t.Fatalf("interval = %s, want %s", actual, expected)
			}
		})
	}
}

func TestParseIntervalRejectsInvalidValues(t *testing.T) {
	for _, input := range []string{"", "0", "0d", "-1d", "1x", "1d 2h", ".", "999999999999999w"} {
		t.Run(input, func(t *testing.T) {
			if _, err := parseInterval(input); err == nil {
				t.Fatalf("expected %q to fail", input)
			}
		})
	}
}

func TestCompactDurationUsesDaysAndWeeks(t *testing.T) {
	tests := map[time.Duration]string{
		24 * time.Hour:                "24h",
		36 * time.Hour:                "1d12h",
		48 * time.Hour:                "2d",
		7 * 24 * time.Hour:            "1w",
		(7*24 + 2*24 + 6) * time.Hour: "1w2d6h",
	}
	for input, expected := range tests {
		if actual := compactDuration(input); actual != expected {
			t.Fatalf("compactDuration(%s) = %q, want %q", input, actual, expected)
		}
	}
}

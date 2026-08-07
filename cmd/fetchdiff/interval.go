package main

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

var intervalUnits = []string{"ms", "us", "µs", "μs", "ns", "w", "d", "h", "m", "s"}

func parseInterval(raw string) (time.Duration, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return 0, invalidInterval(raw)
	}
	if !strings.ContainsAny(value, "wd") {
		interval, err := time.ParseDuration(value)
		if err != nil || interval <= 0 {
			return 0, invalidInterval(raw)
		}
		return interval, nil
	}

	var total time.Duration
	for position := 0; position < len(value); {
		numberStart := position
		digits := 0
		dots := 0
		for position < len(value) {
			character := value[position]
			if character >= '0' && character <= '9' {
				digits++
				position++
				continue
			}
			if character == '.' {
				dots++
				position++
				continue
			}
			break
		}
		if digits == 0 || dots > 1 || position == len(value) {
			return 0, invalidInterval(raw)
		}
		number := value[numberStart:position]
		unit := ""
		for _, candidate := range intervalUnits {
			if strings.HasPrefix(value[position:], candidate) {
				unit = candidate
				position += len(candidate)
				break
			}
		}
		if unit == "" {
			return 0, invalidInterval(raw)
		}

		component, err := intervalComponent(number, unit)
		if err != nil || component < 0 || component > time.Duration(1<<63-1)-total {
			return 0, invalidInterval(raw)
		}
		total += component
	}
	if total <= 0 {
		return 0, invalidInterval(raw)
	}
	return total, nil
}

func intervalComponent(number, unit string) (time.Duration, error) {
	if unit != "w" && unit != "d" {
		return time.ParseDuration(number + unit)
	}
	hours, err := time.ParseDuration(number + "h")
	if err != nil {
		return 0, err
	}
	factor := int64(24)
	if unit == "w" {
		factor = 7 * 24
	}
	if hours > time.Duration(1<<63-1)/time.Duration(factor) {
		return 0, errors.New("interval overflows time.Duration")
	}
	return hours * time.Duration(factor), nil
}

func invalidInterval(value string) error {
	return fmt.Errorf("invalid interval %q; use values like 30m, 24h, 7d, or 2w", value)
}

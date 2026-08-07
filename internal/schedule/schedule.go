package schedule

import (
	"time"

	"github.com/BehiSecc/fetchdiff/internal/model"
)

func Due(target model.Target, now time.Time) bool {
	return !target.NextCheckAt.After(now)
}

func NextWake(targets []model.Target, now time.Time, maximum time.Duration) time.Duration {
	if maximum <= 0 {
		maximum = 30 * time.Second
	}
	wait := maximum
	for _, target := range targets {
		until := target.NextCheckAt.Sub(now)
		if until <= 0 {
			return 0
		}
		if until < wait {
			wait = until
		}
	}
	return wait
}

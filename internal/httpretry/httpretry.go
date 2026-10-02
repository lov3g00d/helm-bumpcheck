package httpretry

import (
	"context"
	"math/rand/v2"
	"net/http"
	"strconv"
	"time"
)

const jitter = 0.25

func Backoff(base, ceiling time.Duration, attempt int) time.Duration {
	d := base
	for range attempt {
		d *= 2
		if d >= ceiling {
			d = ceiling
			break
		}
	}
	return time.Duration(float64(d) * (1 - jitter*rand.Float64()))
}

func After(h http.Header, now time.Time) (time.Duration, bool) {
	if ms, err := strconv.Atoi(h.Get("Retry-After-Ms")); err == nil && ms >= 0 {
		return time.Duration(ms) * time.Millisecond, true
	}
	v := h.Get("Retry-After")
	if v == "" {
		return 0, false
	}
	if s, err := strconv.Atoi(v); err == nil && s >= 0 {
		return time.Duration(s) * time.Second, true
	}
	if t, err := http.ParseTime(v); err == nil {
		return max(t.Sub(now), 0), true
	}
	return 0, false
}

func Sleep(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

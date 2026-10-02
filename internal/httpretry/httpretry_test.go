package httpretry

import (
	"context"
	"net/http"
	"testing"
	"time"
)

func TestBackoffDoublesWithCapAndJitter(t *testing.T) {
	base, ceiling := 500*time.Millisecond, 5*time.Second
	for attempt, full := range []time.Duration{500 * time.Millisecond, time.Second, 2 * time.Second, 4 * time.Second, 5 * time.Second, 5 * time.Second} {
		for range 50 {
			d := Backoff(base, ceiling, attempt)
			if d > full || d < time.Duration(float64(full)*0.75) {
				t.Fatalf("attempt %d: backoff %s outside [%s, %s]", attempt, d, time.Duration(float64(full)*0.75), full)
			}
		}
	}
}

func TestAfter(t *testing.T) {
	now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name   string
		header http.Header
		want   time.Duration
		ok     bool
	}{
		{"none", http.Header{}, 0, false},
		{"seconds", http.Header{"Retry-After": {"3"}}, 3 * time.Second, true},
		{"milliseconds win", http.Header{"Retry-After": {"3"}, "Retry-After-Ms": {"250"}}, 250 * time.Millisecond, true},
		{"http date", http.Header{"Retry-After": {now.Add(7 * time.Second).Format(http.TimeFormat)}}, 7 * time.Second, true},
		{"date in the past", http.Header{"Retry-After": {now.Add(-time.Minute).Format(http.TimeFormat)}}, 0, true},
		{"garbage", http.Header{"Retry-After": {"soon"}}, 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := After(tt.header, now)
			if got != tt.want || ok != tt.ok {
				t.Errorf("After = %s, %v; want %s, %v", got, ok, tt.want, tt.ok)
			}
		})
	}
}

func TestSleepStopsOnCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	start := time.Now()
	if err := Sleep(ctx, time.Minute); err == nil || time.Since(start) > time.Second {
		t.Errorf("Sleep ignored cancellation: %v after %s", err, time.Since(start))
	}
}

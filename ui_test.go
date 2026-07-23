package main

import (
	"testing"
	"time"
)

func TestBucket(t *testing.T) {
	// fixed "now" late in the day so today-boundary math is visible
	now := time.Date(2026, 7, 23, 18, 30, 0, 0, time.Local)
	day := func(d int, hour int) time.Time {
		return time.Date(2026, 7, 23+d, hour, 0, 0, 0, time.Local)
	}
	tests := []struct {
		t    time.Time
		want string
	}{
		{day(0, 9), "TODAY"},
		{day(0, 0), "TODAY"},        // exactly midnight
		{day(-1, 23), "YESTERDAY"},  // late yesterday
		{day(-1, 1), "YESTERDAY"},   // early yesterday
		{day(-2, 12), "LAST 7 DAYS"},
		{day(-6, 12), "LAST 7 DAYS"},
		{day(-7, 12), "LAST 30 DAYS"},
		{day(-29, 12), "LAST 30 DAYS"},
		{day(-30, 12), "OLDER"},
	}
	for _, tt := range tests {
		if got := bucket(tt.t, now); got != tt.want {
			t.Errorf("bucket(%v): got %q, want %q", tt.t, got, tt.want)
		}
	}
}

func TestRelAge(t *testing.T) {
	now := time.Date(2026, 7, 23, 18, 30, 0, 0, time.Local)
	tests := []struct {
		ago  time.Duration
		want string
	}{
		{30 * time.Second, "just now"},
		{5 * time.Minute, "5min ago"},
		{63 * time.Minute, "1h 3min ago"},
		{2 * time.Hour, "2h ago"},
		{26 * time.Hour, "1d 2h ago"},
		{48 * time.Hour, "2d ago"},
		{40 * 24 * time.Hour, "1mo 10d ago"},
		{60 * 24 * time.Hour, "2mo ago"},
		{400 * 24 * time.Hour, "1y 1mo ago"},
		{730 * 24 * time.Hour, "2y ago"},
	}
	for _, tt := range tests {
		if got := relAge(now.Add(-tt.ago), now); got != tt.want {
			t.Errorf("relAge(-%v): got %q, want %q", tt.ago, got, tt.want)
		}
	}
	if got := relAge(time.Time{}, now); got != "" {
		t.Errorf("zero time must give empty age, got %q", got)
	}
}

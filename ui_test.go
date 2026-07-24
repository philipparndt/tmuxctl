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
		{day(0, 0), "TODAY"},       // exactly midnight
		{day(-1, 23), "YESTERDAY"}, // late yesterday
		{day(-1, 1), "YESTERDAY"},  // early yesterday
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

func TestActivityRankedFilterMostRecentlyUsedFirst(t *testing.T) {
	now := time.Date(2026, 7, 23, 18, 30, 0, 0, time.Local)
	targets := []string{
		"hue-old ~/dev/old/hue-old",           // substring match, used long ago
		"unrelated ~/dev/unrelated",           // no match — must be filtered out
		"hue-fresh ~/dev/smarthome/hue-fresh", // substring match, used recently
		"hue-ws 2 windows",                    // workspace: no activity entry
		// fuzzy-only match ("hue" scattered over smart_h_ome/_u_nifi-acc_e_ss),
		// recently used — recency must NOT lift it above real substring matches
		"unifi-access ~/dev/smarthome/unifi-access",
	}
	activity := map[string]time.Time{
		targets[0]: now.AddDate(-2, 0, 0),
		targets[1]: now,
		targets[2]: now.Add(-time.Hour),
		targets[4]: now.Add(-time.Minute),
	}

	ranks := activityRankedFilter(activity)("hue", targets)

	var got []string
	for _, r := range ranks {
		got = append(got, targets[r.Index])
	}
	// workspaces/templates (no activity entry) first, then substring-matched
	// projects by date, then fuzzy-only matches
	want := []string{targets[3], targets[2], targets[0], targets[4]}
	if len(got) != len(want) {
		t.Fatalf("want %v, got %v", want, got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("want order %v, got %v", want, got)
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

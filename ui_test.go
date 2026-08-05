package main

import (
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
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

// Every selectable kind needs a tag: filtered results have no section
// headers, so the tag is the only thing left that names the row's type.
func TestTagPerSelectableKind(t *testing.T) {
	kinds := map[itemKind]string{
		kindWorkspace:      "ws",
		kindTemplate:       "tpl",
		kindTemplateChoice: "tpl",
		kindWindow:         "win",
		kindProject:        "prj",
	}
	for kind, want := range kinds {
		it := pickItem{kind: kind, name: "x"}
		if got := it.tag(); got != want {
			t.Errorf("kind %d: tag %q, want %q", kind, got, want)
		}
		// names must line up across kinds, so every column is tagWidth wide
		if got := lipgloss.Width(it.tagColumn()); got != tagWidth {
			t.Errorf("kind %d: tagColumn width %d, want %d", kind, got, tagWidth)
		}
	}
	for _, kind := range []itemKind{kindHeader, kindHint} {
		if got := (pickItem{kind: kind}).tag(); got != "" {
			t.Errorf("kind %d: tag %q, want empty", kind, got)
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

func TestChromeForHeight(t *testing.T) {
	if c := chromeFor(compactHeight + 1); c != fullChrome {
		t.Errorf("height %d: got %+v, want the full chrome", compactHeight+1, c)
	}
	for _, h := range []int{compactHeight, 12, 6, 1} {
		if c := chromeFor(h); c != (chrome{}) {
			t.Errorf("height %d: got %+v, want no chrome", h, c)
		}
	}
	// the header View draws must match what the list's size is reduced by
	if got, want := fullChrome.reservedRows(), 4; got != want {
		t.Errorf("full chrome reserves %d rows, want %d", got, want)
	}
	if got, want := (chrome{}).reservedRows(), 1; got != want {
		t.Errorf("compact chrome reserves %d rows, want %d", got, want)
	}
}

// dense drops every section header and spacer — the tag column and the age
// say what they said — but keeps the hints, which nothing else repeats.
func TestDenseDropsHeadersKeepsHints(t *testing.T) {
	items := []list.Item{
		header("TEMPLATES"),
		pickItem{kind: kindTemplate, name: "dev"},
		header(""), // spacer between sections
		header("PROJECTS"),
		pickItem{kind: kindProject, name: "tmuxctl"},
		pickItem{kind: kindHint, name: "… 3 older"},
	}
	want := []list.Item{items[1], items[4], items[5]}
	got := dense(items)
	if len(got) != len(want) {
		t.Fatalf("dense kept %d items, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("dense item %d: got %v, want %v", i, got[i], want[i])
		}
	}
}

// The view must never be taller than the terminal: an overflowing view pushes
// the title and the filter line off the top of the screen.
func TestViewFitsTerminalHeight(t *testing.T) {
	cfg := &Config{
		RecentDays:      14,
		DefaultTemplate: "dev",
		Templates: map[string]Template{
			"dev":  {Panes: []Pane{{}, {}}},
			"home": {Dir: "/tmp", Panes: []Pane{{}}},
		},
		Workspaces: map[string]Workspace{
			"work": {Windows: []WorkspaceWindow{{Project: "a"}, {Project: "b"}}},
		},
	}
	// narrow widths included: a header or row that wraps would cost an extra
	// screen row the height math knows nothing about
	for _, w := range []int{24, 40, 80, 200} {
		for _, h := range []int{6, 10, 12, 20, 21, 30, 50} {
			m, _ := newUIModel(cfg).Update(tea.WindowSizeMsg{Width: w, Height: h})
			if got := lipgloss.Height(m.View()); got > h {
				t.Errorf("terminal %dx%d: view is %d rows tall", w, h, got)
			}
		}
	}
}

// A resize switches the chrome live: tmux sends SIGWINCH to the popup when
// the client's terminal changes size, which bubbletea delivers as a
// WindowSizeMsg. The section headers are part of the backing item set, so
// the swap has to happen there too — in both directions.
func TestResizeSwapsChrome(t *testing.T) {
	cfg := &Config{RecentDays: 14, DefaultTemplate: "dev",
		Templates: map[string]Template{"dev": {Panes: []Pane{{}}}, "home": {Dir: "/tmp"}}}
	headers := func(m tea.Model) int {
		n := 0
		for _, it := range m.(uiModel).picker.Items() {
			if p, ok := it.(pickItem); ok && p.kind == kindHeader {
				n++
			}
		}
		return n
	}
	m, _ := newUIModel(cfg).Update(tea.WindowSizeMsg{Width: 80, Height: 40})
	if headers(m) == 0 {
		t.Fatal("full chrome: expected section headers")
	}
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 12})
	if n := headers(m); n != 0 {
		t.Errorf("after shrinking: %d section headers left", n)
	}
	if got := lipgloss.Height(m.View()); got > 12 {
		t.Errorf("after shrinking: view is %d rows tall, want at most 12", got)
	}
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 40})
	if headers(m) == 0 {
		t.Error("after growing back: section headers did not return")
	}
}

// A short terminal must spend its rows on items, not on decoration.
func TestCompactChromeShowsMoreItems(t *testing.T) {
	cfg := &Config{RecentDays: 14, DefaultTemplate: "dev",
		Templates: map[string]Template{"dev": {Panes: []Pane{{}}}}}
	rows := func(height int) int {
		m, _ := newUIModel(cfg).Update(tea.WindowSizeMsg{Width: 80, Height: height})
		return m.(uiModel).picker.Height()
	}
	compact, full := rows(compactHeight), rows(compactHeight+1)
	if compact <= full {
		t.Errorf("compact list height %d, full-chrome list height %d at nearly the same terminal height", compact, full)
	}
}

package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/cursor"
	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

var (
	docStyle    = lipgloss.NewStyle().Margin(1, 2)
	headerStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("39"))
	selStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("212")).Bold(true)
	detailStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("241"))
	titleStyle  = lipgloss.NewStyle().Background(lipgloss.Color("62")).Foreground(lipgloss.Color("230")).Bold(true).Padding(0, 1)
	// filterPromptStyle is the "Filter: " label — plain yellow text, no badge.
	filterPromptStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("220")).Bold(true)
)

// reservedRows is the height of the header we render above the list ourselves
// (title, a blank line, the filter line, and a blank separator). The list's
// own size must be reduced by this much or the combined view overflows.
const reservedRows = 4

type itemKind int

const (
	kindHeader itemKind = iota // non-selectable section separator
	kindWorkspace
	kindTemplate // template with a fixed dir
	kindProject
	kindTemplateChoice // step 2: template for a chosen project
	kindHint           // non-selectable dim note, e.g. "N older projects hidden"
)

type pickItem struct {
	kind   itemKind
	name   string
	detail string // path or summary
	age    string // relative last-activity time (kindProject), display only
	dir    string // project path (kindProject)
}

// detailText is the dim right-hand part of the row: detail plus the
// project's age. The age stays out of FilterValue on purpose — "2d ago"
// would make filter text like "2d" match nearly everything.
func (i pickItem) detailText() string {
	switch {
	case i.detail != "" && i.age != "":
		return i.detail + " · " + i.age
	case i.age != "":
		return i.age
	}
	return i.detail
}

func (i pickItem) label() string {
	switch i.kind {
	case kindWorkspace:
		return "⊞ " + i.name
	case kindTemplate:
		return "⊡ " + i.name
	default:
		return i.name
	}
}

func (i pickItem) selectable() bool {
	return i.kind != kindHeader && i.kind != kindHint
}

// FilterValue is empty for headers and hints so they drop out during filtering.
func (i pickItem) FilterValue() string {
	if !i.selectable() {
		return ""
	}
	return i.name + " " + i.detail
}

func header(name string) pickItem { return pickItem{kind: kindHeader, name: name} }

// projGroup is one titled section of the project list.
type projGroup struct {
	title string
	items []list.Item
}

// relAge renders how long ago t was as a compact duration like
// "1h 3min ago", using the two largest non-zero units. Empty for the
// zero time (no .git metadata readable).
func relAge(t, now time.Time) string {
	if t.IsZero() {
		return ""
	}
	min := int(now.Sub(t).Minutes())
	if min < 1 {
		return "just now"
	}
	h, day := min/60, min/(60*24)
	mo, y := day/30, day/365
	switch {
	case y > 0:
		if mo := (day % 365) / 30; mo > 0 {
			return fmt.Sprintf("%dy %dmo ago", y, mo)
		}
		return fmt.Sprintf("%dy ago", y)
	case mo > 0:
		if d := day % 30; d > 0 {
			return fmt.Sprintf("%dmo %dd ago", mo, d)
		}
		return fmt.Sprintf("%dmo ago", mo)
	case day > 0:
		if h := h % 24; h > 0 {
			return fmt.Sprintf("%dd %dh ago", day, h)
		}
		return fmt.Sprintf("%dd ago", day)
	case h > 0:
		if min := min % 60; min > 0 {
			return fmt.Sprintf("%dh %dmin ago", h, min)
		}
		return fmt.Sprintf("%dh ago", h)
	}
	return fmt.Sprintf("%dmin ago", min)
}

// bucket names the time section a project's last activity falls into,
// following browser-history conventions. Day boundaries are local midnights.
func bucket(t, now time.Time) string {
	midnight := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	switch {
	case !t.Before(midnight):
		return "TODAY"
	case !t.Before(midnight.AddDate(0, 0, -1)):
		return "YESTERDAY"
	case !t.Before(midnight.AddDate(0, 0, -6)):
		return "LAST 7 DAYS"
	case !t.Before(midnight.AddDate(0, 0, -29)):
		return "LAST 30 DAYS"
	default:
		return "OLDER"
	}
}

// compactDelegate renders each row on a single line, with styled,
// non-selectable section headers.
type compactDelegate struct{}

func (compactDelegate) Height() int                         { return 1 }
func (compactDelegate) Spacing() int                        { return 0 }
func (compactDelegate) Update(tea.Msg, *list.Model) tea.Cmd { return nil }
func (compactDelegate) Render(w io.Writer, m list.Model, index int, it list.Item) {
	p, ok := it.(pickItem)
	if !ok {
		return
	}
	if p.kind == kindHeader {
		fmt.Fprint(w, headerStyle.Render(p.name))
		return
	}
	if p.kind == kindHint {
		fmt.Fprint(w, "  "+detailStyle.Render(p.name))
		return
	}
	detail := p.detailText()
	if index == m.Index() {
		fmt.Fprint(w, selStyle.Render("▸ "+p.label()))
		if detail != "" {
			fmt.Fprint(w, "  "+detailStyle.Render(detail))
		}
		return
	}
	line := p.label()
	if detail != "" {
		line += "  " + detailStyle.Render(detail)
	}
	fmt.Fprint(w, "  "+line)
}

// uiModel is a two-step picker: choose a workspace/template/project, and for
// a project additionally the template to apply.
type uiModel struct {
	cfg       *Config
	picker    list.Model
	tplPicker list.Model
	step      int

	// The picker initially shows only recently active projects
	// (itemsRecent); "a" toggles the full list, and starting a filter
	// swaps in itemsAll so the search always covers every project.
	itemsRecent []list.Item
	itemsAll    []list.Item
	showAll     bool // "a" pressed: keep the full list
	expanded    bool // itemsAll swapped in for a running filter

	// result, evaluated after the program quits
	selWorkspace string
	selTemplate  string
	selDir       string
	err          error
}

func newUIModel(cfg *Config) uiModel {
	home, _ := os.UserHomeDir()
	tilde := func(p string) string {
		if home != "" && strings.HasPrefix(p, home) {
			return "~" + p[len(home):]
		}
		return p
	}

	var workspaces []list.Item
	for _, name := range sorted(mapKeys(cfg.Workspaces)) {
		workspaces = append(workspaces, pickItem{
			kind:   kindWorkspace,
			name:   name,
			detail: fmt.Sprintf("%d windows", len(cfg.Workspaces[name].Windows)),
		})
	}
	var templates []list.Item
	for _, name := range sorted(mapKeys(cfg.Templates)) {
		if dir := cfg.Templates[name].Dir; dir != "" {
			templates = append(templates, pickItem{
				kind:   kindTemplate,
				name:   name,
				detail: tilde(expandHome(dir)),
			})
		}
	}
	seen := map[string]bool{}
	type projEntry struct {
		item pickItem
		t    time.Time
	}
	var projects []projEntry
	for _, root := range cfg.DevDirs {
		walk(root, 0, cfg.SearchDepth, func(path, name string, isRoot bool) {
			// only repository roots are offered as projects; grouping
			// folders like ~/dev/acme stay searchable via `add` but
			// would clutter the picker
			if isRoot && !seen[path] {
				seen[path] = true
				projects = append(projects, projEntry{
					item: pickItem{kind: kindProject, name: name, detail: tilde(path), dir: path},
					t:    lastActivity(path),
				})
			}
		})
	}
	now := time.Now()
	activity := make(map[string]time.Time, len(projects)) // FilterValue → last activity
	for i := range projects {
		projects[i].item.age = relAge(projects[i].t, now)
		activity[projects[i].item.FilterValue()] = projects[i].t
	}
	sort.Slice(projects, func(i, j int) bool { return projects[i].item.name < projects[j].item.name })
	var allProjects []list.Item
	for _, p := range projects {
		allProjects = append(allProjects, p.item)
	}

	// the recent view: projects with git activity in the last recent_days,
	// most recent first, grouped into time sections
	var recent []projEntry
	if cfg.RecentDays > 0 {
		cutoff := now.AddDate(0, 0, -cfg.RecentDays)
		for _, p := range projects {
			if p.t.After(cutoff) {
				recent = append(recent, p)
			}
		}
	}
	sort.SliceStable(recent, func(i, j int) bool { return recent[i].t.After(recent[j].t) })
	// newest-first, so each project belongs to the current (= last) section
	var recentGroups []projGroup
	for _, p := range recent {
		title := bucket(p.t, now)
		if n := len(recentGroups); n == 0 || recentGroups[n-1].title != title {
			recentGroups = append(recentGroups, projGroup{title: title})
		}
		g := &recentGroups[len(recentGroups)-1]
		g.items = append(g.items, p.item)
	}

	// Each non-empty group is preceded by a header row.
	buildItems := func(groups []projGroup, hint string) []list.Item {
		var items []list.Item
		addGroup := func(title string, group []list.Item) {
			if len(group) == 0 {
				return
			}
			// A blank spacer between groups. It must be a real list item (not a
			// style margin) so the list's height math counts it — otherwise the
			// view renders taller than its allotted height and the title/filter
			// bar scrolls off the top of the screen.
			if len(items) > 0 {
				items = append(items, header(""))
			}
			items = append(items, header(title))
			items = append(items, group...)
		}
		addGroup("WORKSPACES", workspaces)
		addGroup("TEMPLATES", templates)
		for _, g := range groups {
			addGroup(g.title, g.items)
		}
		if hint != "" {
			items = append(items, pickItem{kind: kindHint, name: hint})
		}
		return items
	}
	itemsAll := buildItems([]projGroup{{title: "PROJECTS", items: allProjects}}, "")
	itemsRecent := itemsAll
	if len(recent) > 0 {
		hint := ""
		if hidden := len(allProjects) - len(recent); hidden > 0 {
			hint = fmt.Sprintf("… %d older — type to search all", hidden)
		}
		itemsRecent = buildItems(recentGroups, hint)
	}

	// templates with a fixed dir are complete on their own and make no
	// sense applied to a picked project — only offer the dir-less ones
	var tplItems []list.Item
	defaultIdx := 0
	for _, name := range sorted(mapKeys(cfg.Templates)) {
		if cfg.Templates[name].Dir != "" {
			continue
		}
		detail := fmt.Sprintf("%d panes", max(len(cfg.Templates[name].Panes), 1))
		if name == cfg.DefaultTemplate {
			detail += " · default"
			defaultIdx = len(tplItems)
		}
		tplItems = append(tplItems, pickItem{kind: kindTemplateChoice, name: name, detail: detail})
	}

	picker := list.New(itemsRecent, compactDelegate{}, 0, 0)
	picker.Title = "tmuxctl"
	picker.SetShowStatusBar(false)
	picker.SetShowHelp(true)
	picker.AdditionalShortHelpKeys = func() []key.Binding {
		return []key.Binding{key.NewBinding(key.WithKeys("a"), key.WithHelp("a", "all projects"))}
	}
	// We draw the title and filter on our own lines above the list, so the
	// list must not also render them in its title bar.
	picker.SetShowTitle(false)
	picker.SetShowFilter(false)
	picker.Filter = activityRankedFilter(activity)
	styleFilter(&picker)
	// The picker opens already in filter mode — typing narrows immediately,
	// no "/" needed. An empty filter still shows the recent-projects view.
	focusFilter(&picker)

	tplPicker := list.New(tplItems, compactDelegate{}, 0, 0)
	tplPicker.Title = "template"
	tplPicker.SetShowStatusBar(false)
	tplPicker.SetShowTitle(false)
	tplPicker.SetShowFilter(false)
	styleFilter(&tplPicker)
	tplPicker.Select(defaultIdx)

	return uiModel{cfg: cfg, picker: picker, tplPicker: tplPicker,
		itemsRecent: itemsRecent, itemsAll: itemsAll}
}

// activityRankedFilter orders filter matches by the projects' last activity,
// most recently used first, instead of the default fuzzy-match quality —
// when a filter hits several locations, the one last worked on is almost
// always the wanted one.
//
// Recency only competes among items containing the term as a literal
// substring. The fuzzy filter also matches scattered subsequences (e.g.
// "hue" hits unifi-access via smart_h_ome/_u_nifi-acc_e_ss), and date order
// must not let such a barely-matching item overtake a real match just
// because it was used recently — fuzzy-only matches stay below all
// substring matches, in match-quality order.
//
// activity maps an item's FilterValue to its last activity; entries missing
// from the map (workspaces, templates) sort before all projects, and ties
// keep the fuzzy filter's order.
func activityRankedFilter(activity map[string]time.Time) list.FilterFunc {
	return func(term string, targets []string) []list.Rank {
		ranks := list.DefaultFilter(term, targets)
		q := strings.ToLower(term)
		substr := make([]bool, len(targets))
		for _, r := range ranks {
			substr[r.Index] = strings.Contains(strings.ToLower(targets[r.Index]), q)
		}
		sort.SliceStable(ranks, func(i, j int) bool {
			si, sj := substr[ranks[i].Index], substr[ranks[j].Index]
			if si != sj {
				return si
			}
			if !si {
				return false // both fuzzy-only: keep match-quality order
			}
			ti, iProj := activity[targets[ranks[i].Index]]
			tj, jProj := activity[targets[ranks[j].Index]]
			if iProj != jProj {
				return !iProj
			}
			return ti.After(tj)
		})
		return ranks
	}
}

// styleFilter makes entering filter mode obvious: a yellow "Filter: " label
// and no placeholder, so the empty input shows a solid, visible cursor block
// rather than dim hint text.
func styleFilter(l *list.Model) {
	l.FilterInput.Prompt = "Filter: "
	l.FilterInput.PromptStyle = filterPromptStyle
	l.FilterInput.Placeholder = ""
	l.FilterInput.TextStyle = lipgloss.NewStyle().Bold(true)
	// A static cursor is a solid, always-on block. The default blinking cursor
	// renders as a bare space on its off phase, so on an empty filter (nothing
	// typed yet) there is nothing visible next to the label half the time.
	l.FilterInput.Cursor.SetMode(cursor.CursorStatic)
}

// skipHeader advances the cursor off non-selectable rows (section headers,
// hints), preferring the given direction and reversing at a boundary.
// Guarded against empty/all-header lists so it always terminates.
func skipHeader(l *list.Model, down bool) {
	isHeader := func() bool {
		it, ok := l.SelectedItem().(pickItem)
		return ok && !it.selectable()
	}
	n := len(l.Items())
	for i := 0; i < n && isHeader(); i++ {
		if down {
			l.CursorDown()
		} else {
			l.CursorUp()
		}
	}
	for i := 0; i < n && isHeader(); i++ { // hit a boundary — reverse
		if down {
			l.CursorUp()
		} else {
			l.CursorDown()
		}
	}
}

// focusFilter opens the list already in filter mode with an empty query, so
// the user can type straight away without first pressing "/". An empty filter
// shows every current item (section headers included), so the initial view is
// the same recent-projects list — just ready to receive typing.
func focusFilter(l *list.Model) {
	l.SetFilterText("")
	l.SetFilterState(list.Filtering)
	l.Select(0)
	skipHeader(l, true)
}

// leaveFilter moves from typing into navigating the results: a non-empty query
// stays applied (the narrowed list is kept and becomes browsable), an empty one
// drops back to the plain unfiltered view.
func leaveFilter(l *list.Model) {
	if l.FilterInput.Value() == "" {
		l.ResetFilter()
		return
	}
	l.SetFilterState(list.FilterApplied)
}

// reapplyFilter swaps the backing item set while preserving the live query and
// filter state, recomputing matches synchronously so the view never blanks.
func reapplyFilter(l *list.Model, items []list.Item) {
	state := l.FilterState()
	val := l.FilterInput.Value()
	l.SetItems(items)
	if state != list.Unfiltered {
		l.SetFilterText(val)    // recompute matches over the new items
		l.SetFilterState(state) // SetFilterText leaves FilterApplied; restore
	}
	l.Select(0)
}

// clearFilter leaves filter mode entirely: the query is dropped and the picker
// returns to the recent-projects view (or the full list when "show all" is on).
func (m *uiModel) clearFilter() {
	m.picker.ResetFilter()
	m.expanded = false
	items := m.itemsRecent
	if m.showAll {
		items = m.itemsAll
	}
	m.picker.SetItems(items)
	m.picker.Select(0)
	skipHeader(&m.picker, true)
}

func (m uiModel) Init() tea.Cmd { return nil }

// active returns the list for the current step. Pointer receiver so the
// returned pointer aims at the caller's model, not a copy.
func (m *uiModel) active() *list.Model {
	if m.step == 1 {
		return &m.tplPicker
	}
	return &m.picker
}

func (m uiModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		h, v := docStyle.GetFrameSize()
		m.picker.SetSize(msg.Width-h, msg.Height-v-reservedRows)
		m.tplPicker.SetSize(msg.Width-h, msg.Height-v-reservedRows)
	case tea.KeyMsg:
		filtering := m.active().FilterState() == list.Filtering
		switch msg.String() {
		case "ctrl+c":
			return m, tea.Quit
		case "q":
			// while typing, "q" is filter text, not a quit
			if !filtering {
				return m, tea.Quit
			}
		case "a":
			// toggle between the recent-only and the full project list
			if m.step == 0 && m.picker.FilterState() == list.Unfiltered {
				m.showAll = !m.showAll
				items := m.itemsRecent
				if m.showAll {
					items = m.itemsAll
				}
				m.picker.SetItems(items)
				m.picker.Select(0)
				skipHeader(&m.picker, true)
				return m, nil
			}
		case "down", "up", "pgdown", "pgup", "ctrl+n", "ctrl+p":
			// leave the filter and let the same keystroke move the selection,
			// so one press both exits and moves (not two)
			if filtering {
				leaveFilter(m.active())
			}
		case "esc":
			if m.step == 1 {
				m.step = 0
				return m, nil
			}
			// step 0: the first esc leaves the filter, a second one quits
			if m.picker.FilterState() != list.Unfiltered {
				m.clearFilter()
				return m, nil
			}
			return m, tea.Quit
		case "enter":
			it, ok := m.active().SelectedItem().(pickItem)
			if !ok {
				break
			}
			switch it.kind {
			case kindWorkspace:
				m.selWorkspace = it.name
				return m, tea.Quit
			case kindTemplate:
				m.selTemplate = it.name
				return m, tea.Quit
			case kindProject:
				m.selDir = it.dir
				switch items := m.tplPicker.Items(); len(items) {
				case 0:
					m.err = fmt.Errorf("no template without a fixed dir configured")
					return m, tea.Quit
				case 1: // nothing to choose — apply the only candidate
					m.selTemplate = items[0].(pickItem).name
					return m, tea.Quit
				}
				m.tplPicker.Title = "template for " + it.name
				m.step = 1
				return m, nil
			case kindTemplateChoice:
				m.selTemplate = it.name
				return m, tea.Quit
			}
		}
	}
	al := m.active()
	var cmd tea.Cmd
	*al, cmd = al.Update(msg)

	// Keep the backing set in step with the filter: a live query searches every
	// project, while an empty query (browsing, or just cleared) shows the
	// curated recent view. Skipped when "a" has pinned the full list.
	//
	// reapplyFilter recomputes matches synchronously, so the async filter cmd
	// the list just queued over the *old* items is stale — drop it, or the
	// FilterMatchesMsg it produces would clobber the freshly swapped results.
	if m.step == 0 && !m.showAll {
		typed := m.picker.FilterState() != list.Unfiltered && m.picker.FilterInput.Value() != ""
		switch {
		case typed && !m.expanded:
			m.expanded = true
			reapplyFilter(&m.picker, m.itemsAll)
			cmd = nil
		case !typed && m.expanded:
			m.expanded = false
			reapplyFilter(&m.picker, m.itemsRecent)
			cmd = nil
		}
	}

	// Keep the cursor off non-selectable section headers.
	//
	// While filtering, headers drop out of the results (empty FilterValue)
	// and the list cursor never moves on its own — keystrokes go to the
	// filter input, and a narrowing FilterMatchesMsg replaces the item slice
	// without touching the cursor. That would strand the selection on a stale
	// row (typically the second match), so pin it to the top result instead.
	if al.FilterState() == list.Filtering {
		al.Select(0)
		skipHeader(al, true)
	} else {
		// moving the way the user asked
		down := true
		if k, ok := msg.(tea.KeyMsg); ok {
			switch k.String() {
			case "up", "k", "pgup", "home":
				down = false
			}
		}
		skipHeader(al, down)
	}
	return m, cmd
}

// filterLine is the dedicated filter row shown above the list: a dim hint
// while browsing, and the live "Filter" input (label + typed text + cursor)
// once filtering has started.
func (m uiModel) filterLine() string {
	l := m.active()
	if l.FilterState() == list.Unfiltered {
		return filterPromptStyle.Render("Filter: ") + detailStyle.Render("press / to filter")
	}
	return l.FilterInput.View()
}

func (m uiModel) View() string {
	l := m.active()
	header := lipgloss.JoinVertical(lipgloss.Left,
		titleStyle.Render(l.Title),
		"", // blank line above the filter
		m.filterLine(),
	)
	// title · blank · filter · blank separator · list body — reservedRows tall.
	return docStyle.Render(lipgloss.JoinVertical(lipgloss.Left, header, "", l.View()))
}

// cmdUI runs the interactive picker and applies the selection with the same
// code paths as the add/workspace commands.
//
// -s matters for popups: a display-popup has no pane context, so tmux would
// resolve an untargeted new-window to whatever session was most recently
// active server-wide — which is not necessarily the session the popup was
// opened from. The tmux binding passes -s '#{session_name}' to pin it.
func cmdUI(cfg *Config, args []string) {
	fs := flag.NewFlagSet("ui", flag.ExitOnError)
	session := fs.String("s", "", "target session (default: current, or the configured fallback session)")
	bg := fs.Bool("b", false, "create windows in the background")
	fs.Parse(args)

	p := tea.NewProgram(newUIModel(cfg), tea.WithAltScreen())
	final, err := p.Run()
	if err != nil {
		fatal(err)
	}
	m := final.(uiModel)
	if m.err != nil {
		fatal(m.err)
	}

	switch {
	case m.selWorkspace != "":
		if err := applyWorkspace(cfg, m.selWorkspace, *session, *bg); err != nil {
			fatal(err)
		}
	case m.selTemplate != "":
		tpl := cfg.Templates[m.selTemplate]
		dir := m.selDir
		if dir == "" {
			d, err := templateDir(tpl, m.selTemplate)
			if err != nil {
				fatal(err)
			}
			dir = d
		}
		if err := addWindow(cfg, tpl, dir, *session, *bg); err != nil {
			fatal(err)
		}
	default:
		return // cancelled
	}
	attachHint(cfg, *session)
}

func sorted(names []string) []string {
	sort.Strings(names)
	return names
}

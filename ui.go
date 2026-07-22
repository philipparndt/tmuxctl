package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/charmbracelet/bubbles/cursor"
	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

var (
	docStyle    = lipgloss.NewStyle().Margin(1, 2)
	headerStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("39"))
	selStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("212")).Bold(true)
	detailStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("241"))
	titleStyle = lipgloss.NewStyle().Background(lipgloss.Color("62")).Foreground(lipgloss.Color("230")).Bold(true).Padding(0, 1)
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
)

type pickItem struct {
	kind   itemKind
	name   string
	detail string // path or summary
	dir    string // project path (kindProject)
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

// FilterValue is empty for headers so they drop out during filtering.
func (i pickItem) FilterValue() string {
	if i.kind == kindHeader {
		return ""
	}
	return i.name + " " + i.detail
}

func header(name string) pickItem { return pickItem{kind: kindHeader, name: name} }

// compactDelegate renders each row on a single line, with styled,
// non-selectable section headers.
type compactDelegate struct{}

func (compactDelegate) Height() int                             { return 1 }
func (compactDelegate) Spacing() int                            { return 0 }
func (compactDelegate) Update(tea.Msg, *list.Model) tea.Cmd     { return nil }
func (compactDelegate) Render(w io.Writer, m list.Model, index int, it list.Item) {
	p, ok := it.(pickItem)
	if !ok {
		return
	}
	if p.kind == kindHeader {
		fmt.Fprint(w, headerStyle.Render(p.name))
		return
	}
	line := p.label()
	if p.detail != "" {
		line += "  " + detailStyle.Render(p.detail)
	}
	if index == m.Index() {
		fmt.Fprint(w, selStyle.Render("▸ "+p.label()))
		if p.detail != "" {
			fmt.Fprint(w, "  "+detailStyle.Render(p.detail))
		}
		return
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
	var projects []pickItem
	for _, root := range cfg.DevDirs {
		walk(root, 0, cfg.SearchDepth, func(path, name string, isRoot bool) {
			// only repository roots are offered as projects; grouping
			// folders like ~/dev/acme stay searchable via `add` but
			// would clutter the picker
			if isRoot && !seen[path] {
				seen[path] = true
				projects = append(projects, pickItem{kind: kindProject, name: name, detail: tilde(path), dir: path})
			}
		})
	}
	sort.Slice(projects, func(i, j int) bool { return projects[i].name < projects[j].name })
	var projectItems []list.Item
	for _, p := range projects {
		projectItems = append(projectItems, p)
	}

	// Each non-empty group is preceded by a header row.
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
	addGroup("PROJECTS", projectItems)

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

	picker := list.New(items, compactDelegate{}, 0, 0)
	picker.Title = "tmuxctl"
	picker.SetShowStatusBar(false)
	picker.SetShowHelp(true)
	// We draw the title and filter on our own lines above the list, so the
	// list must not also render them in its title bar.
	picker.SetShowTitle(false)
	picker.SetShowFilter(false)
	styleFilter(&picker)
	skipHeader(&picker, true)

	tplPicker := list.New(tplItems, compactDelegate{}, 0, 0)
	tplPicker.Title = "template"
	tplPicker.SetShowStatusBar(false)
	tplPicker.SetShowTitle(false)
	tplPicker.SetShowFilter(false)
	styleFilter(&tplPicker)
	tplPicker.Select(defaultIdx)

	return uiModel{cfg: cfg, picker: picker, tplPicker: tplPicker}
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

// skipHeader advances the cursor off a section header, preferring the given
// direction and reversing at a boundary. Guarded against empty/all-header
// lists so it always terminates.
func skipHeader(l *list.Model, down bool) {
	isHeader := func() bool {
		it, ok := l.SelectedItem().(pickItem)
		return ok && it.kind == kindHeader
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
		if m.active().FilterState() == list.Filtering {
			break
		}
		switch msg.String() {
		case "ctrl+c", "q":
			return m, tea.Quit
		case "esc":
			if m.step == 1 {
				m.step = 0
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

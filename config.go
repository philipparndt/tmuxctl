package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// SendItem is keystrokes typed into a pane after its Run command started,
// e.g. a slash command entered into an interactive app.
type SendItem struct {
	// Keys is the text typed into the pane, followed by Enter. tmux key
	// names like "BTab" (shift-tab) or "C-c" are sent as those keys.
	Keys string `yaml:"keys"`
	// Enter controls whether Enter is pressed after Keys. Default true;
	// set false for bare key presses like mode toggles.
	Enter *bool `yaml:"enter"`
	// Await is a regex polled against the visible pane content; typing
	// starts once it matches, e.g. await: "❯" for claude's prompt.
	// More reliable than Wait for slow-starting apps.
	Await string `yaml:"await"`
	// Timeout bounds Await polling (Go duration). Default 30s.
	Timeout string `yaml:"timeout"`
	// Wait is a fixed delay before typing (Go duration, e.g. "3s").
	// Default 1s when no Await is set, otherwise 0.
	Wait string `yaml:"wait"`
	// Escape sends an Escape key between typing and Enter — needed for
	// TUIs whose autocomplete popup would swallow the Enter (e.g. claude
	// code slash commands).
	Escape bool `yaml:"escape"`
}

// ClaudePane is first-class support for claude code: mode and initial
// prompt are passed as CLI arguments instead of fragile key sending.
type ClaudePane struct {
	// Prompt is the initial prompt or slash command, e.g. "/tar-monitor".
	Prompt string `yaml:"prompt"`
	// Mode is claude's --permission-mode: acceptEdits, auto,
	// bypassPermissions, manual, dontAsk, or plan.
	Mode string `yaml:"mode"`
	// Args are extra claude CLI arguments, e.g. ["--continue"].
	Args []string `yaml:"args"`
}

// command renders the claude invocation typed into the pane.
func (c ClaudePane) command() string {
	parts := []string{"claude"}
	if c.Mode != "" {
		parts = append(parts, "--permission-mode", c.Mode)
	}
	parts = append(parts, c.Args...)
	if c.Prompt != "" {
		parts = append(parts, c.Prompt)
	}
	for i, p := range parts {
		parts[i] = shellQuote(p)
	}
	return strings.Join(parts, " ")
}

var shellSafe = regexp.MustCompile(`^[a-zA-Z0-9_@%+=:,./-]+$`)

func shellQuote(s string) string {
	if shellSafe.MatchString(s) {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// Pane describes a single pane in a template. The first pane in a template
// is the window's initial pane; every following pane is created by splitting.
type Pane struct {
	// Run is the command typed into the pane after it is created.
	// Empty means the pane is just a shell in the project folder.
	Run string `yaml:"run"`
	// Claude launches claude code in the pane; mutually exclusive with Run.
	Claude *ClaudePane `yaml:"claude"`
	// Send is typed into the pane after Run, item by item — for driving
	// interactive apps (e.g. run claude, then send "/tar-monitor").
	Send []SendItem `yaml:"send"`
	// Split is the direction the pane is split off from the current pane:
	// "right" (horizontal split) or "bottom" (vertical split).
	// Ignored for the first pane. Defaults to "right".
	Split string `yaml:"split"`
	// Size is passed to tmux split-window -l, e.g. "50%" or "20".
	Size string `yaml:"size"`
}

// Template describes how a window is laid out for a project.
type Template struct {
	Panes []Pane `yaml:"panes"`
	// Dir pins the template to a fixed folder; `tmuxctl add -t <name>` then
	// needs no project argument. A given argument still wins.
	Dir string `yaml:"dir"`
	// Name overrides the window title (default: trimmed folder name).
	Name string `yaml:"name"`
	// Background creates the window without switching to it, so the
	// current window stays focused.
	Background bool `yaml:"background"`
	// TrimPrefix/TrimSuffix are stripped from the folder name (first match
	// each) to form the window title, e.g. trim_prefix ["acme-"] names the
	// window for acme-apigateway-go just "apigateway-go".
	TrimPrefix []string `yaml:"trim_prefix"`
	TrimSuffix []string `yaml:"trim_suffix"`
}

// WorkspaceWindow is one window of a workspace: either a template reference,
// an inline template definition, or a template reference with inline
// overrides. `project` resolves the folder like the `add` argument does.
type WorkspaceWindow struct {
	// TemplateRef names a template from `templates` to base this window on.
	TemplateRef string `yaml:"template"`
	// Project is a folder name or substring searched below dev_dirs,
	// an alternative to a fixed dir.
	Project  string `yaml:"project"`
	Template `yaml:",inline"`
}

// Workspace is a named set of windows applied together.
type Workspace struct {
	Windows []WorkspaceWindow `yaml:"windows"`
}

// resolve merges the referenced template (if any) with the inline overrides.
func (w WorkspaceWindow) resolve(cfg *Config) (Template, error) {
	tpl := w.Template
	if w.TemplateRef == "" {
		return tpl, nil
	}
	base, ok := cfg.Templates[w.TemplateRef]
	if !ok {
		return tpl, fmt.Errorf("template %q not found in config", w.TemplateRef)
	}
	if len(tpl.Panes) > 0 {
		base.Panes = tpl.Panes
	}
	if tpl.Dir != "" {
		base.Dir = tpl.Dir
	}
	if tpl.Name != "" {
		base.Name = tpl.Name
	}
	if tpl.Background {
		base.Background = true
	}
	if len(tpl.TrimPrefix) > 0 {
		base.TrimPrefix = tpl.TrimPrefix
	}
	if len(tpl.TrimSuffix) > 0 {
		base.TrimSuffix = tpl.TrimSuffix
	}
	return base, nil
}

// windowName derives the window title from the project folder name.
func (t Template) windowName(folder string) string {
	name := folder
	for _, p := range t.TrimPrefix {
		if rest, ok := strings.CutPrefix(name, p); ok {
			name = rest
			break
		}
	}
	for _, s := range t.TrimSuffix {
		if rest, ok := strings.CutSuffix(name, s); ok {
			name = rest
			break
		}
	}
	if name == "" {
		return folder
	}
	return name
}

// Config is the on-disk configuration at ~/.config/tmuxctl/config.yaml.
type Config struct {
	// DevDirs are the roots searched for projects, e.g. ~/dev.
	DevDirs []string `yaml:"dev_dirs"`
	// SearchDepth is how many directory levels below each dev dir are
	// searched (1 = direct children only). Default 3.
	SearchDepth int `yaml:"search_depth"`
	// RecentDays: the ui initially lists only projects with git activity in
	// the last N days; "a" or filtering reaches all of them. Default 14;
	// -1 always shows all projects.
	RecentDays int `yaml:"recent_days"`
	// Session is the tmux session used when running outside tmux. Default "dev".
	Session string `yaml:"session"`
	// DefaultTemplate is the template used by `tmuxctl add` unless -t is given.
	DefaultTemplate string               `yaml:"default_template"`
	Templates       map[string]Template  `yaml:"templates"`
	Workspaces      map[string]Workspace `yaml:"workspaces"`
}

const defaultConfig = `# tmuxctl configuration
dev_dirs:
  - ~/dev
search_depth: 3
recent_days: 14   # ui: initially only projects active in the last N days (-1: all)
session: dev
default_template: dev
templates:
  dev:
    trim_prefix: ["acme-"]   # window title: folder name without this prefix
    panes:
      - run: claude   # left: claude code in the project folder
      - run: ""       # right: plain shell in the project folder
        split: right
        size: 50%
  # Template pinned to a fixed folder, launching claude with a slash
  # command: "tmuxctl add -t support" (no project argument needed)
  # support:
  #   dir: ~/dev/acme/refactorings/complete
  #   name: support
  #   background: true    # don't switch to the window
  #   panes:
  #     - claude:
  #         mode: auto           # --permission-mode auto
  #         prompt: /tar-monitor # initial prompt or slash command

# A workspace is a set of windows applied together: "tmuxctl workspace acme"
# workspaces:
#   acme:
#     windows:
#       - template: support           # reference a template ...
#       - template: dev               # ... optionally overriding fields
#         project: acme-cluster-ctl  # folder search, like the add argument
#       - name: system tests          # ... or define the window inline
#         dir: ~/dev/acme/acme-test
#         panes:
#           - run: claude
`

func configPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "tmuxctl", "config.yaml"), nil
}

// loadConfig reads the config, writing a commented default file on first run.
func loadConfig() (*Config, error) {
	path, err := configPath()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		if mkErr := os.MkdirAll(filepath.Dir(path), 0o755); mkErr != nil {
			return nil, mkErr
		}
		if wrErr := os.WriteFile(path, []byte(defaultConfig), 0o644); wrErr != nil {
			return nil, wrErr
		}
		fmt.Fprintf(os.Stderr, "tmuxctl: wrote default config to %s\n", path)
		data = []byte(defaultConfig)
	} else if err != nil {
		return nil, err
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	if len(cfg.DevDirs) == 0 {
		cfg.DevDirs = []string{"~/dev"}
	}
	if cfg.SearchDepth <= 0 {
		cfg.SearchDepth = 3
	}
	if cfg.RecentDays == 0 {
		cfg.RecentDays = 14
	}
	if cfg.Session == "" {
		cfg.Session = "dev"
	}
	if cfg.DefaultTemplate == "" {
		cfg.DefaultTemplate = "dev"
	}
	for i, d := range cfg.DevDirs {
		cfg.DevDirs[i] = expandHome(d)
	}
	return &cfg, nil
}

func expandHome(p string) string {
	if p == "~" || len(p) >= 2 && p[:2] == "~/" {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, p[1:])
		}
	}
	return p
}

package main

import (
	"testing"

	"gopkg.in/yaml.v3"
)

// The shipped default is written verbatim on first run, so a YAML slip in it
// would only surface as a parse error on a fresh install.
func TestDefaultConfigParses(t *testing.T) {
	var cfg Config
	if err := yaml.Unmarshal([]byte(defaultConfig), &cfg); err != nil {
		t.Fatalf("defaultConfig: %v", err)
	}
	dev, ok := cfg.Templates[cfg.DefaultTemplate]
	if !ok {
		t.Fatalf("default template %q missing", cfg.DefaultTemplate)
	}
	c := dev.Panes[0].Claude
	if c == nil || c.Mode != "auto" {
		t.Errorf("first pane: got %+v, want a claude pane in auto mode", dev.Panes[0])
	}
}

func TestClaudePaneCommand(t *testing.T) {
	tests := []struct {
		name string
		pane ClaudePane
		want string
	}{
		{"plain", ClaudePane{}, "claude"},
		{"mode and slash command", ClaudePane{Mode: "auto", Prompt: "/tar-monitor"},
			"claude --permission-mode auto /tar-monitor"},
		{"prompt with spaces is quoted", ClaudePane{Prompt: "fix the build"},
			"claude 'fix the build'"},
		{"extra args", ClaudePane{Args: []string{"--continue"}, Prompt: "/monitor-ci"},
			"claude --continue /monitor-ci"},
		{"single quote in prompt", ClaudePane{Prompt: "don't break"},
			`claude 'don'\''t break'`},
	}
	for _, tt := range tests {
		if got := tt.pane.command(); got != tt.want {
			t.Errorf("%s: got %q, want %q", tt.name, got, tt.want)
		}
	}
}

func TestWindowName(t *testing.T) {
	tpl := Template{TrimPrefix: []string{"acme-"}, TrimSuffix: []string{"-go"}}
	if got := tpl.windowName("acme-apigateway-go"); got != "apigateway" {
		t.Errorf("got %q, want apigateway", got)
	}
	if got := tpl.windowName("acme-"); got != "acme-" {
		t.Errorf("trimming to empty must fall back to folder name, got %q", got)
	}
}

func TestWorkspaceWindowResolve(t *testing.T) {
	cfg := &Config{Templates: map[string]Template{
		"dev": {Panes: []Pane{{Run: "claude"}, {}}, TrimPrefix: []string{"acme-"}},
	}}
	w := WorkspaceWindow{
		TemplateRef: "dev",
		Template:    Template{Dir: "~/x", Name: "custom", Background: true},
	}
	got, err := w.resolve(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Panes) != 2 || got.Dir != "~/x" || got.Name != "custom" || !got.Background {
		t.Errorf("merge wrong: %+v", got)
	}
	if _, err := (WorkspaceWindow{TemplateRef: "nope"}).resolve(cfg); err == nil {
		t.Error("unknown template ref must error")
	}
}

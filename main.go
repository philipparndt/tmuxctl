package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

const usage = `tmuxctl - open project windows in tmux from templates

Usage:
  tmuxctl add [-t template] [-s session] [-b] [project]
                        add a window for a project
                        -s: target session, -b: create in background
  tmuxctl workspace [-s session] [-b] <name>
                        apply a whole workspace (all its windows)
  tmuxctl ui [-s session] [-b]
                        interactive picker for all of the above
  tmuxctl templates     list configured templates
  tmuxctl workspaces    list configured workspaces

[project] is a folder name or substring, searched below the configured
dev_dirs (e.g. "apigateway-go" matches ~/dev/acme/acme-apigateway-go).
It can be omitted when the template configures a fixed dir.

Config: ~/.config/tmuxctl/config.yaml (created on first run)
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}

	cfg, err := loadConfig()
	if err != nil {
		fatal(err)
	}

	switch os.Args[1] {
	case "add":
		cmdAdd(cfg, os.Args[2:])
	case "workspace", "ws":
		cmdWorkspace(cfg, os.Args[2:])
	case "ui":
		cmdUI(cfg, os.Args[2:])
	case "templates":
		printSorted(mapKeys(cfg.Templates))
	case "workspaces":
		printSorted(mapKeys(cfg.Workspaces))
	case "-h", "--help", "help":
		fmt.Print(usage)
	default:
		fmt.Fprintf(os.Stderr, "tmuxctl: unknown command %q\n\n%s", os.Args[1], usage)
		os.Exit(2)
	}
}

func cmdAdd(cfg *Config, args []string) {
	fs := flag.NewFlagSet("add", flag.ExitOnError)
	tplName := fs.String("t", cfg.DefaultTemplate, "template to apply")
	session := fs.String("s", "", "target session (default: current, or the configured fallback session)")
	bg := fs.Bool("b", false, "create the window in the background (don't switch to it)")
	fs.Parse(args)
	if fs.NArg() > 1 {
		fatal(fmt.Errorf("usage: tmuxctl add [-t template] [project]"))
	}

	tpl, ok := cfg.Templates[*tplName]
	if !ok {
		fatal(fmt.Errorf("template %q not found in config", *tplName))
	}

	var dir string
	switch {
	case fs.NArg() == 1:
		found, err := findProject(cfg, fs.Arg(0))
		if err != nil {
			fatal(err)
		}
		dir = found
	case tpl.Dir != "":
		d, err := templateDir(tpl, *tplName)
		if err != nil {
			fatal(err)
		}
		dir = d
	default:
		fatal(fmt.Errorf("usage: tmuxctl add [-t template] <project> (template %q has no dir configured)", *tplName))
	}

	if err := addWindow(cfg, tpl, dir, *session, *bg); err != nil {
		fatal(err)
	}
	attachHint(cfg, *session)
}

// addWindow applies tpl to dir as a single new window.
func addWindow(cfg *Config, tpl Template, dir, session string, forceBg bool) error {
	name := tpl.Name
	if name == "" {
		name = tpl.windowName(filepath.Base(dir))
	}
	windowID, err := applyTemplate(cfg, tpl, name, dir, session, tpl.Background || forceBg)
	if err != nil {
		return err
	}
	fmt.Printf("added window %s (%s) for %s\n", name, windowID, dir)
	return nil
}

// templateDir validates and expands a template's fixed dir.
func templateDir(tpl Template, tplName string) (string, error) {
	dir := expandHome(tpl.Dir)
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		return "", fmt.Errorf("template %q dir %s does not exist", tplName, dir)
	}
	return dir, nil
}

func attachHint(cfg *Config, session string) {
	if !insideTmux() && session == "" {
		fmt.Printf("attach with: tmux attach -t %s\n", cfg.Session)
	}
}

// cmdWorkspace applies every window of a workspace. All windows are created
// first so their apps boot in parallel; the send items run afterwards.
func cmdWorkspace(cfg *Config, args []string) {
	fs := flag.NewFlagSet("workspace", flag.ExitOnError)
	session := fs.String("s", "", "target session (default: current, or the configured fallback session)")
	bg := fs.Bool("b", false, "create all windows in the background")
	fs.Parse(args)
	if fs.NArg() != 1 {
		fatal(fmt.Errorf("usage: tmuxctl workspace [-s session] [-b] <name>"))
	}

	if err := applyWorkspace(cfg, fs.Arg(0), *session, *bg); err != nil {
		fatal(err)
	}
	attachHint(cfg, *session)
}

// applyWorkspace creates every window of a workspace first, so the apps
// boot in parallel, and then processes the windows' send items.
func applyWorkspace(cfg *Config, wsName, session string, forceBg bool) error {
	ws, ok := cfg.Workspaces[wsName]
	if !ok {
		return fmt.Errorf("workspace %q not found in config", wsName)
	}
	if len(ws.Windows) == 0 {
		return fmt.Errorf("workspace %q has no windows", wsName)
	}

	var created []appliedWindow
	for i, w := range ws.Windows {
		tpl, err := w.resolve(cfg)
		if err != nil {
			return fmt.Errorf("window %d: %w", i+1, err)
		}

		var dir string
		switch {
		case w.Project != "":
			dir, err = findProject(cfg, w.Project)
			if err != nil {
				return fmt.Errorf("window %d: %w", i+1, err)
			}
		case tpl.Dir != "":
			dir, err = templateDir(tpl, w.TemplateRef)
			if err != nil {
				return fmt.Errorf("window %d: %w", i+1, err)
			}
		default:
			return fmt.Errorf("window %d needs a dir, project, or a template with a dir", i+1)
		}

		name := tpl.Name
		if name == "" {
			name = tpl.windowName(filepath.Base(dir))
		}
		win, err := createWindow(cfg, tpl, name, dir, session, tpl.Background || forceBg)
		if err != nil {
			return fmt.Errorf("window %q: %w", name, err)
		}
		fmt.Printf("added window %s (%s) for %s\n", name, win.windowID, dir)
		created = append(created, win)
	}

	for _, win := range created {
		if err := win.runSends(); err != nil {
			return fmt.Errorf("window %s: %w", win.windowID, err)
		}
	}
	return nil
}

func printSorted(names []string) {
	sort.Strings(names)
	for _, name := range names {
		fmt.Println(name)
	}
}

func mapKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

func fatal(err error) {
	fmt.Fprintf(os.Stderr, "tmuxctl: %v\n", err)
	os.Exit(1)
}

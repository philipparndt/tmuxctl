package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// The tmuxctl block in the tmux config is delimited by these markers, so
// setup can be re-run any time: it replaces its own block and never
// touches anything around it.
const (
	setupBegin = "# >>> tmuxctl >>>"
	setupEnd   = "# <<< tmuxctl <<<"
)

func setupBlock(bin, key string) string {
	return fmt.Sprintf(`%s
# Open the tmuxctl picker (workspaces, templates, projects).
# The || branch keeps the popup open on failure so errors stay readable.
# tmuxctl reads the session from $TMUX itself, so no -s is needed here.
bind %s display-popup -E -w 80%% -h 70%% "%s ui || { echo; echo '-- failed, press any key --'; read -r _; }"
%s`, setupBegin, key, bin, setupEnd)
}

// upsertBlock replaces the marker-delimited tmuxctl block in conf, or
// appends it (separated by a blank line). Reports whether conf changed.
func upsertBlock(conf, block string) (string, bool) {
	begin := strings.Index(conf, setupBegin)
	end := strings.Index(conf, setupEnd)
	if begin >= 0 && end > begin {
		updated := conf[:begin] + block + conf[end+len(setupEnd):]
		return updated, updated != conf
	}
	sep := ""
	if conf != "" {
		if !strings.HasSuffix(conf, "\n") {
			sep = "\n"
		}
		sep += "\n"
	}
	return conf + sep + block + "\n", true
}

// tmuxConfPath prefers an existing config (classic over XDG); with neither
// present the classic path is created.
func tmuxConfPath(home string) string {
	classic := filepath.Join(home, ".tmux.conf")
	if _, err := os.Stat(classic); err == nil {
		return classic
	}
	xdg := filepath.Join(home, ".config", "tmux", "tmux.conf")
	if _, err := os.Stat(xdg); err == nil {
		return xdg
	}
	return classic
}

// cmdSetup installs or updates the picker popup binding in the tmux config
// and reloads a running tmux server so it takes effect immediately.
func cmdSetup(args []string) {
	fs := flag.NewFlagSet("setup", flag.ExitOnError)
	bindKey := fs.String("key", "-", "prefix key bound to the picker popup")
	dryRun := fs.Bool("n", false, "print the config block instead of writing it")
	fs.Parse(args)

	bin, err := os.Executable()
	if err != nil {
		fatal(err)
	}
	if resolved, err := filepath.EvalSymlinks(bin); err == nil {
		bin = resolved
	}
	block := setupBlock(bin, *bindKey)

	if *dryRun {
		fmt.Println(block)
		return
	}

	home, err := os.UserHomeDir()
	if err != nil {
		fatal(err)
	}
	path := tmuxConfPath(home)
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		fatal(err)
	}
	mode := os.FileMode(0o644)
	if info, err := os.Stat(path); err == nil {
		mode = info.Mode()
	}

	updated, changed := upsertBlock(string(data), block)
	if !changed {
		fmt.Printf("%s is already up to date\n", path)
		return
	}
	if err := os.WriteFile(path, []byte(updated), mode); err != nil {
		fatal(err)
	}
	fmt.Printf("updated %s: prefix + %s opens the picker\n", path, *bindKey)

	if exec.Command("tmux", "source-file", path).Run() == nil {
		fmt.Println("reloaded tmux config")
	} else {
		fmt.Println("no running tmux server reloaded - the binding is active once tmux starts")
	}
}

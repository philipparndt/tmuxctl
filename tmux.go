package main

import (
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

func tmux(args ...string) (string, error) {
	out, err := exec.Command("tmux", args...).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("tmux %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}

func insideTmux() bool {
	return os.Getenv("TMUX") != ""
}

// currentSessionTarget returns a target for the session tmuxctl was invoked
// from, read from the session id in $TMUX ("socket,pid,session-id").
//
// This matters inside a display-popup: a popup has no pane context
// ($TMUX_PANE is empty), so an untargeted new-window lets tmux fall back to
// whatever session was most recently active server-wide — which can be a
// different session than the one the popup was opened from.
func currentSessionTarget() string {
	parts := strings.Split(os.Getenv("TMUX"), ",")
	if len(parts) < 3 || parts[2] == "" {
		return ""
	}
	return "$" + parts[2]
}

// ensureSession makes sure the target session exists. When it creates the
// session it returns the id of the session's initial placeholder window, so
// the caller can remove it once a real window has been added.
func ensureSession(name, dir string) (string, error) {
	if err := exec.Command("tmux", "has-session", "-t", "="+name).Run(); err == nil {
		return "", nil
	}
	return tmux("new-session", "-d", "-s", name, "-c", dir, "-P", "-F", "#{window_id}")
}

// appliedWindow is a created window whose send items may still be pending.
type appliedWindow struct {
	windowID string
	paneIDs  []string
	tpl      Template
}

// applyTemplate creates a new window for dir, lays out the template's panes,
// and drives the panes' send items. session overrides the target session;
// background creates the window without switching to it.
func applyTemplate(cfg *Config, tpl Template, name, dir, session string, background bool) (string, error) {
	win, err := createWindow(cfg, tpl, name, dir, session, background)
	if err != nil {
		return "", err
	}
	return win.windowID, win.runSends()
}

// createWindow creates the window and panes and starts the Run commands,
// but does not process send items yet — so a workspace can start all its
// windows first and let the apps boot in parallel.
func createWindow(cfg *Config, tpl Template, name, dir, session string, background bool) (appliedWindow, error) {
	none := appliedWindow{}
	if len(tpl.Panes) == 0 {
		tpl.Panes = []Pane{{}}
	}

	newWindow := []string{"new-window", "-P", "-F", "#{window_id}", "-n", name, "-c", dir}
	if background {
		newWindow = append(newWindow, "-d")
	}
	placeholder := ""
	switch {
	case session != "":
		w, err := ensureSession(session, dir)
		if err != nil {
			return none, err
		}
		placeholder = w
		newWindow = append(newWindow, "-t", session+":")
	case insideTmux():
		if target := currentSessionTarget(); target != "" {
			newWindow = append(newWindow, "-t", target+":")
		}
	default:
		w, err := ensureSession(cfg.Session, dir)
		if err != nil {
			return none, err
		}
		placeholder = w
		newWindow = append(newWindow, "-t", cfg.Session+":")
	}
	windowID, err := tmux(newWindow...)
	if err != nil {
		return none, err
	}
	if placeholder != "" {
		// drop the placeholder window a fresh session starts with
		if _, err := tmux("kill-window", "-t", placeholder); err != nil {
			return none, err
		}
	}

	firstPane, err := tmux("display-message", "-p", "-t", windowID, "#{pane_id}")
	if err != nil {
		return none, err
	}
	paneIDs := []string{firstPane}

	for _, p := range tpl.Panes[1:] {
		args := []string{"split-window", "-P", "-F", "#{pane_id}", "-t", paneIDs[len(paneIDs)-1], "-c", dir}
		switch p.Split {
		case "", "right":
			args = append(args, "-h")
		case "bottom":
			args = append(args, "-v")
		default:
			return none, fmt.Errorf("invalid split %q (want \"right\" or \"bottom\")", p.Split)
		}
		if p.Size != "" {
			args = append(args, "-l", p.Size)
		}
		id, err := tmux(args...)
		if err != nil {
			return none, err
		}
		paneIDs = append(paneIDs, id)
	}

	for i, p := range tpl.Panes {
		run := p.Run
		if p.Claude != nil {
			if run != "" {
				return none, fmt.Errorf("pane %d: run and claude are mutually exclusive", i+1)
			}
			run = p.Claude.command()
		}
		if run == "" {
			continue
		}
		if _, err := tmux("send-keys", "-t", paneIDs[i], run, "C-m"); err != nil {
			return none, err
		}
	}

	if _, err := tmux("select-pane", "-t", paneIDs[0]); err != nil {
		return none, err
	}
	return appliedWindow{windowID: windowID, paneIDs: paneIDs, tpl: tpl}, nil
}

// runSends drives the panes' send items, observing the pane content rather
// than sleeping blindly: an `await` pattern gates typing on the app being
// ready, and Enter is only pressed once the typed text is visible.
func (w appliedWindow) runSends() error {
	for i, p := range w.tpl.Panes {
		pane := w.paneIDs[i]
		for _, s := range p.Send {
			if s.Await != "" {
				timeout := 30 * time.Second
				if s.Timeout != "" {
					d, err := time.ParseDuration(s.Timeout)
					if err != nil {
						return fmt.Errorf("pane %d: invalid timeout %q: %w", i+1, s.Timeout, err)
					}
					timeout = d
				}
				if err := awaitContent(pane, s.Await, timeout); err != nil {
					return fmt.Errorf("pane %d: %w", i+1, err)
				}
			}
			wait := time.Duration(0)
			if s.Wait != "" {
				d, err := time.ParseDuration(s.Wait)
				if err != nil {
					return fmt.Errorf("pane %d: invalid wait %q: %w", i+1, s.Wait, err)
				}
				wait = d
			} else if s.Await == "" {
				wait = time.Second
			}
			time.Sleep(wait)

			if s.Keys != "" {
				if _, err := tmux("send-keys", "-t", pane, "--", s.Keys); err != nil {
					return err
				}
			}
			if s.Enter != nil && !*s.Enter {
				continue
			}
			// Only press Enter once the typed text shows up in the pane,
			// so a still-starting app can't lose it. Best effort: apps
			// that don't echo fall through after the poll times out.
			if s.Keys != "" {
				awaitContent(pane, regexp.QuoteMeta(s.Keys), 5*time.Second)
			}
			time.Sleep(150 * time.Millisecond)
			if s.Escape {
				if _, err := tmux("send-keys", "-t", pane, "Escape"); err != nil {
					return err
				}
				time.Sleep(300 * time.Millisecond)
			}
			if _, err := tmux("send-keys", "-t", pane, "C-m"); err != nil {
				return err
			}
		}
	}
	return nil
}

// awaitContent polls the pane's visible content until it matches the regex
// pattern or the timeout elapses.
func awaitContent(pane, pattern string, timeout time.Duration) error {
	re, err := regexp.Compile(pattern)
	if err != nil {
		return fmt.Errorf("invalid await pattern %q: %w", pattern, err)
	}
	deadline := time.Now().Add(timeout)
	for {
		content, err := tmux("capture-pane", "-p", "-t", pane)
		if err != nil {
			return err
		}
		if re.MatchString(content) {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timed out after %s waiting for %q in pane", timeout, pattern)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

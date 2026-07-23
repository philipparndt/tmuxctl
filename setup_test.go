package main

import (
	"strings"
	"testing"
)

func TestUpsertBlock(t *testing.T) {
	block := setupBlock("/usr/local/bin/tmuxctl", "-")

	got, changed := upsertBlock("", block)
	if !changed || got != block+"\n" {
		t.Errorf("empty conf must become just the block, got %q", got)
	}

	conf := "set -g mouse on" // no trailing newline
	got, changed = upsertBlock(conf, block)
	if !changed || got != conf+"\n\n"+block+"\n" {
		t.Errorf("append must separate with a blank line, got %q", got)
	}

	// replace an outdated block, preserving everything around it
	old := "before\n\n" + setupBegin + "\nold junk\n" + setupEnd + "\n\nafter\n"
	got, changed = upsertBlock(old, block)
	want := "before\n\n" + block + "\n\nafter\n"
	if !changed || got != want {
		t.Errorf("replace: got %q, want %q", got, want)
	}
	if strings.Contains(got, "old junk") {
		t.Error("old block content must be gone")
	}

	// idempotent: a second run must not change anything
	if again, changed := upsertBlock(got, block); changed {
		t.Errorf("second run must be a no-op, got %q", again)
	}
}

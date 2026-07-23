package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestWalkReportsProjectRootsAndSkipsNestedRepos(t *testing.T) {
	dev := t.TempDir()
	mk := func(parts ...string) {
		if err := os.MkdirAll(filepath.Join(append([]string{dev}, parts...)...), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	mk("acme", "acme-apigateway-go", ".git") // repo under a grouping dir
	mk("acme", "acme-apigateway-go", "cmd")  // subdir of a repo
	mk("standalone", ".git")                   // repo directly in dev dir
	mk("standalone", "nested-repo", ".git")    // repo nested inside a repo
	mk("empty-group")                          // grouping dir without repos

	roots := map[string]bool{}
	all := map[string]bool{}
	walk(dev, 0, 3, func(path, name string, isRoot bool) {
		all[name] = true
		if isRoot {
			roots[name] = true
		}
	})

	for _, want := range []string{"acme-apigateway-go", "standalone"} {
		if !roots[want] {
			t.Errorf("expected %s to be reported as project root", want)
		}
	}
	for _, hidden := range []string{"nested-repo", "cmd"} {
		if all[hidden] {
			t.Errorf("%s must not be visited (inside a repo)", hidden)
		}
	}
	if roots["acme"] || roots["empty-group"] {
		t.Error("grouping dirs must not be project roots")
	}
	if !all["acme"] {
		t.Error("grouping dirs must still be visited for name search")
	}
}

func TestLastActivity(t *testing.T) {
	repo := t.TempDir()
	if !lastActivity(repo).IsZero() {
		t.Error("dir without .git must report zero time")
	}

	git := filepath.Join(repo, ".git")
	if err := os.Mkdir(git, 0o755); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-90 * 24 * time.Hour)
	recent := time.Now().Add(-1 * time.Hour)
	for f, mtime := range map[string]time.Time{"HEAD": old, "index": recent} {
		p := filepath.Join(git, f)
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(p, mtime, mtime); err != nil {
			t.Fatal(err)
		}
	}
	// the .git dir itself just changed (files were created) — pin it old so
	// the newest tracked file (the index) must win
	if err := os.Chtimes(git, old, old); err != nil {
		t.Fatal(err)
	}

	got := lastActivity(repo)
	if got.Sub(recent).Abs() > time.Second {
		t.Errorf("want index mtime %v, got %v", recent, got)
	}
}

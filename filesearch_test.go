package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestFileSearch(t *testing.T) {
	if _, err := exec.LookPath("rg"); err != nil {
		t.Skip("ripgrep not installed")
	}
	dev := t.TempDir()
	write := func(rel, content string) {
		p := filepath.Join(dev, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// proj-a matches by file name (and would not by content)
	write("proj-a/docker-compose.yaml", "services: {}")
	// proj-b matches by content only
	write("proj-b/notes.txt", "restart the Docker daemon first")
	// proj-c must not match: term only inside excluded dirs
	write("proj-c/node_modules/docker.js", "docker")
	write("proj-c/.git/docker-hook", "docker")
	write("proj-c/readme.md", "nothing to see")

	roots := []string{
		filepath.Join(dev, "proj-a"),
		filepath.Join(dev, "proj-b"),
		filepath.Join(dev, "proj-c"),
	}
	hits, err := fileSearch(roots, "docker")
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 2 {
		t.Fatalf("want 2 hits, got %+v", hits)
	}
	// hits follow the roots' order
	if hits[0].dir != roots[0] || hits[0].file != "docker-compose.yaml" || hits[0].content {
		t.Errorf("proj-a: want name match on docker-compose.yaml, got %+v", hits[0])
	}
	if hits[1].dir != roots[1] || hits[1].file != "notes.txt" || !hits[1].content {
		t.Errorf("proj-b: want content match on notes.txt, got %+v", hits[1])
	}

	if hits, err := fileSearch(roots, "no-such-term-anywhere"); err != nil || len(hits) != 0 {
		t.Errorf("want no hits and no error, got %+v, %v", hits, err)
	}
}

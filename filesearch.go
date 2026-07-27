package main

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// fileHit is one project matched by a file search. The first matching file
// is kept as evidence to show next to the project; name matches win over
// content matches for that spot.
type fileHit struct {
	dir     string // project root
	file    string // first matched file, relative to dir
	content bool   // evidence file matched by content (false: by name)
	n       int    // total matching files in this project
}

// searchTimeout bounds one file search run. rg sweeps typical dev trees in
// well under a second; the bound only guards pathological cases.
const searchTimeout = 5 * time.Second

// fileSearch finds projects containing term in a file name or in file
// content. Both scans are ripgrep runs — parallel, .gitignore-aware and
// binary-skipping, which makes it the fastest way to sweep a dev tree
// without maintaining an index:
//
//	rg --files          list files; name matching happens here in Go
//	rg -il -F <term>    files whose content contains term (stops per file
//	                    at the first match)
//
// Hidden files are included (dotfiles are worth finding) but .git and the
// usual dependency dirs are not. A hit is attributed to the deepest root
// containing it; results follow the order of roots, so callers passing
// them most-recently-used first get hits in that order.
func fileSearch(roots []string, term string) ([]fileHit, error) {
	if len(roots) == 0 {
		return nil, nil
	}
	if _, err := exec.LookPath("rg"); err != nil {
		return nil, fmt.Errorf("file search needs ripgrep (rg) on the PATH")
	}
	ctx, cancel := context.WithTimeout(context.Background(), searchTimeout)
	defer cancel()

	common := []string{"--hidden", "--glob", "!.git"}
	for _, d := range sorted(mapKeys(skipDirs)) {
		common = append(common, "--glob", "!"+d)
	}
	nameArgs := append([]string{"--files"}, common...)
	nameArgs = append(nameArgs, roots...)
	contentArgs := append([]string{"-il", "--fixed-strings"}, common...)
	contentArgs = append(contentArgs, "--", term)
	contentArgs = append(contentArgs, roots...)

	rg := func(args []string) ([]string, error) {
		out, err := exec.CommandContext(ctx, "rg", args...).Output()
		var lines []string
		if text := strings.TrimRight(string(out), "\n"); text != "" {
			lines = strings.Split(text, "\n")
		}
		if err != nil && len(lines) == 0 {
			// exit status 1 is rg for "no matches", not a failure
			if ee, ok := err.(*exec.ExitError); ok && ee.ExitCode() == 1 {
				return nil, nil
			}
			return nil, fmt.Errorf("rg: %w", err)
		}
		// Partial output — the timeout killed rg mid-run (a pathological
		// tree, e.g. 150k decompiled files in one repo), or exit status 2
		// for unreadable corners — still names real matches: use what we
		// got instead of failing the whole batch.
		return lines, nil
	}

	var nameFiles, contentFiles []string
	var nameErr, contentErr error
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); nameFiles, nameErr = rg(nameArgs) }()
	go func() { defer wg.Done(); contentFiles, contentErr = rg(contentArgs) }()
	wg.Wait()
	if nameErr != nil {
		return nil, nameErr
	}
	if contentErr != nil {
		return nil, contentErr
	}

	q := strings.ToLower(term)
	var named []string
	for _, f := range nameFiles {
		if strings.Contains(strings.ToLower(filepath.Base(f)), q) {
			named = append(named, f)
		}
	}

	// deepest root first, so nested roots win the prefix match
	byLen := append([]string(nil), roots...)
	sort.Slice(byLen, func(i, j int) bool { return len(byLen[i]) > len(byLen[j]) })
	rootOf := func(path string) string {
		for _, r := range byLen {
			if strings.HasPrefix(path, r+string(filepath.Separator)) {
				return r
			}
		}
		return ""
	}

	hitByRoot := map[string]*fileHit{}
	seen := map[string]bool{} // a file matching by name and content counts once
	add := func(path string, content bool) {
		if seen[path] {
			return
		}
		seen[path] = true
		root := rootOf(path)
		if root == "" {
			return
		}
		if h := hitByRoot[root]; h != nil {
			h.n++
			return
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			rel = path
		}
		hitByRoot[root] = &fileHit{dir: root, file: rel, content: content, n: 1}
	}
	for _, f := range named {
		add(f, false)
	}
	for _, f := range contentFiles {
		add(f, true)
	}

	var hits []fileHit
	for _, r := range roots {
		if h := hitByRoot[r]; h != nil {
			hits = append(hits, *h)
		}
	}
	return hits, nil
}

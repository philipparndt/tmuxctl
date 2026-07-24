package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

var skipDirs = map[string]bool{
	"node_modules": true,
	"vendor":       true,
	"dist":         true,
	"build":        true,
	"target":       true,
}

// findProject resolves a query like "acme-apigateway-go" or "apigateway-go"
// to a project directory below one of the configured dev dirs.
// Exact folder-name matches win over substring matches; among matches of the
// same kind, shallower paths win. Ambiguous matches open an interactive
// selector on a terminal and are an error otherwise.
func findProject(cfg *Config, query string) (string, error) {
	q := strings.ToLower(query)
	var exact, partial []string

	for _, root := range cfg.DevDirs {
		walk(root, 0, cfg.SearchDepth, func(path, name string, isRoot bool) {
			lower := strings.ToLower(name)
			switch {
			case lower == q:
				exact = append(exact, path)
			case strings.Contains(lower, q):
				partial = append(partial, path)
			}
		})
	}

	pick := func(matches []string) (string, error) {
		sortByActivity(matches)
		if len(matches) > 1 {
			return chooseMatch(query, matches)
		}
		return matches[0], nil
	}

	switch {
	case len(exact) > 0:
		return pick(exact)
	case len(partial) > 0:
		return pick(partial)
	}
	return "", fmt.Errorf("no project matching %q found under %s", query, strings.Join(cfg.DevDirs, ", "))
}

// sortByActivity orders candidate locations by when they were last worked
// on, most recent first — among several matches the one last used is almost
// always the wanted one. Ties (typically folders without .git metadata,
// which all report the zero time) fall back to shallower paths, then name.
func sortByActivity(matches []string) {
	times := make(map[string]time.Time, len(matches))
	for _, m := range matches {
		times[m] = lastActivity(m)
	}
	sort.Slice(matches, func(i, j int) bool {
		ti, tj := times[matches[i]], times[matches[j]]
		if !ti.Equal(tj) {
			return ti.After(tj)
		}
		di, dj := strings.Count(matches[i], string(filepath.Separator)), strings.Count(matches[j], string(filepath.Separator))
		if di != dj {
			return di < dj
		}
		return matches[i] < matches[j]
	})
}

// walk visits directories below root up to maxDepth levels, calling fn for
// each candidate directory. isRoot marks project roots (a .git is present);
// walk does not descend into those — a repository's subdirectories are
// never projects themselves. Hidden and dependency directories are skipped.
func walk(root string, depth, maxDepth int, fn func(path, name string, isRoot bool)) {
	if depth >= maxDepth {
		return
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		if strings.HasPrefix(name, ".") || skipDirs[name] {
			continue
		}
		path := filepath.Join(root, name)
		isRoot := isProjectRoot(path)
		fn(path, name, isRoot)
		if !isRoot {
			walk(path, depth+1, maxDepth, fn)
		}
	}
}

// isProjectRoot reports whether dir is the root of a repository checkout.
// A .git file (not just a directory) also counts, for git worktrees.
func isProjectRoot(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, ".git"))
	return err == nil
}

// lastActivity estimates when a project was last worked on, from the mtimes
// of .git files that change on commits, checkouts, staging, and even
// `git status` (which rewrites the index) — a cheap stat-only proxy that
// avoids running git per project. Zero time when nothing is readable.
func lastActivity(dir string) time.Time {
	var t time.Time
	for _, p := range []string{
		filepath.Join(".git", "index"),
		filepath.Join(".git", "HEAD"),
		filepath.Join(".git", "FETCH_HEAD"),
		".git", // worktrees: .git is a file
	} {
		if fi, err := os.Stat(filepath.Join(dir, p)); err == nil && fi.ModTime().After(t) {
			t = fi.ModTime()
		}
	}
	return t
}

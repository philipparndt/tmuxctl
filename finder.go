package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
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
		sort.Slice(matches, func(i, j int) bool {
			di, dj := strings.Count(matches[i], string(filepath.Separator)), strings.Count(matches[j], string(filepath.Separator))
			if di != dj {
				return di < dj
			}
			return matches[i] < matches[j]
		})
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

package main

import (
	"fmt"
	"os"
	"strings"
	"time"

	"golang.org/x/term"
)

// chooseMatch resolves an ambiguous query. On a terminal it shows an
// interactive selector; otherwise it fails listing the candidates.
func chooseMatch(query string, matches []string) (string, error) {
	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil || !term.IsTerminal(int(tty.Fd())) {
		return "", ambiguousError(query, matches)
	}
	defer tty.Close()

	title := fmt.Sprintf("%d matches for %q — pick one (↑/↓ or j/k, enter to select, q to cancel):", len(matches), query)
	labels := make([]string, len(matches))
	now := time.Now()
	for i, m := range matches {
		labels[i] = m
		if age := relAge(lastActivity(m), now); age != "" {
			labels[i] += "  · " + age
		}
	}
	idx, err := selectFrom(tty, title, labels)
	if err != nil {
		return "", err
	}
	return matches[idx], nil
}

func ambiguousError(query string, matches []string) error {
	return fmt.Errorf("%q is ambiguous, matches:\n  %s", query, strings.Join(matches, "\n  "))
}

// selectFrom renders an arrow-key list selector on tty and returns the
// chosen index.
func selectFrom(tty *os.File, title string, options []string) (int, error) {
	fd := int(tty.Fd())
	old, err := term.MakeRaw(fd)
	if err != nil {
		return -1, err
	}
	defer term.Restore(fd, old)

	fmt.Fprint(tty, "\x1b[?25l")       // hide cursor
	defer fmt.Fprint(tty, "\x1b[?25h") // show cursor

	fmt.Fprintf(tty, "%s\r\n", title)
	cur := 0
	first := true
	render := func() {
		if !first {
			fmt.Fprintf(tty, "\x1b[%dA", len(options)) // back to top of list
		}
		first = false
		for i, o := range options {
			if i == cur {
				fmt.Fprintf(tty, "\r\x1b[K\x1b[7m> %s\x1b[0m\r\n", o)
			} else {
				fmt.Fprintf(tty, "\r\x1b[K  %s\r\n", o)
			}
		}
	}
	// clear the selector before returning so normal output follows cleanly
	defer fmt.Fprintf(tty, "\x1b[%dA\x1b[J", len(options)+1)

	buf := make([]byte, 8)
	for {
		render()
		n, err := tty.Read(buf)
		if err != nil {
			return -1, err
		}
		key := buf[:n]
		switch {
		case n == 1 && (key[0] == '\r' || key[0] == '\n'):
			return cur, nil
		case n == 1 && (key[0] == 'q' || key[0] == 0x03 || key[0] == 0x1b): // q, ctrl-c, esc
			return -1, fmt.Errorf("selection cancelled")
		case n == 1 && (key[0] == 'j') || n >= 3 && key[0] == 0x1b && key[1] == '[' && key[2] == 'B':
			cur = (cur + 1) % len(options)
		case n == 1 && (key[0] == 'k') || n >= 3 && key[0] == 0x1b && key[1] == '[' && key[2] == 'A':
			cur = (cur + len(options) - 1) % len(options)
		}
	}
}

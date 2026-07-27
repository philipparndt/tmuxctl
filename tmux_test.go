package main

import "testing"

func TestParseWindows(t *testing.T) {
	out := "dev\t@0\tshell\t/private/tmp\n" +
		"dev\t@1\tapigateway-go\t/Users/x/dev/acme-apigateway-go\n" +
		"other\t@2\tvarwin\t/private/var"
	got := parseWindows(out)
	want := []openWindow{
		{session: "dev", id: "@0", name: "shell", path: "/private/tmp"},
		{session: "dev", id: "@1", name: "apigateway-go", path: "/Users/x/dev/acme-apigateway-go"},
		{session: "other", id: "@2", name: "varwin", path: "/private/var"},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d windows, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("window %d: got %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestParseWindowsSkipsMalformed(t *testing.T) {
	// blank lines and lines with too few fields are dropped, not panicked on
	out := "\ndev\t@0\tshell\t/tmp\nbroken-line-no-tabs\ndev\t@1"
	got := parseWindows(out)
	if len(got) != 1 {
		t.Fatalf("want 1 valid window, got %d: %+v", len(got), got)
	}
	if got[0] != (openWindow{session: "dev", id: "@0", name: "shell", path: "/tmp"}) {
		t.Errorf("unexpected window: %+v", got[0])
	}
}

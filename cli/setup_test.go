package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUsableProject(t *testing.T) {
	home := t.TempDir()
	project := filepath.Join(home, "web")
	if err := os.Mkdir(project, 0o755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(home, "notes.txt")
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := usableProject(project, home); err != nil {
		t.Errorf("project: %v", err)
	}
	for _, bad := range []string{home, home + "/", "/", file, filepath.Join(home, "missing")} {
		if usableProject(bad, home) == nil {
			t.Errorf("%s was accepted", bad)
		}
	}
}

func TestTilde(t *testing.T) {
	home := "/Users/ana"
	cases := map[string]string{"/Users/ana/web": "~/web", "/Users/ana": "~", "/Users/anabel": "/Users/anabel", "/tmp": "/tmp"}
	for in, want := range cases {
		if got := tilde(in, home); got != want {
			t.Errorf("tilde(%q) = %q, want %q", in, got, want)
		}
	}
	if got := untilde("~/web", home); got != "/Users/ana/web" {
		t.Errorf("untilde: %q", got)
	}
	if got := shellPath("~/my code"); got != "'~/my code'" {
		t.Errorf("shellPath: %q", got)
	}
}

func TestBareCommandShowsHelpWithoutATerminal(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	var out bytes.Buffer
	if code := cli(nil, strings.NewReader(""), &out, &out); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(out.String(), "setup") || strings.Contains(out.String(), "This machine") {
		t.Fatalf("expected help, got:\n%s", out.String())
	}
}

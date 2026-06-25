package main

import (
	"strings"
	"testing"
)

func TestRun_Version(t *testing.T) {
	var sb strings.Builder
	oldStdout := stdout
	stdout = &sb
	defer func() { stdout = oldStdout }()

	if err := run([]string{"version"}); err != nil {
		t.Fatalf("run(version): %v", err)
	}
	if got := strings.TrimSpace(sb.String()); got == "" {
		t.Fatal("expected version output")
	}
}

func TestRun_Help(t *testing.T) {
	var sb strings.Builder
	oldStdout := stdout
	stdout = &sb
	defer func() { stdout = oldStdout }()

	if err := run([]string{"help"}); err != nil {
		t.Fatalf("run(help): %v", err)
	}
	out := sb.String()
	for _, want := range []string{"ingest", "mcp", "version"} {
		if !strings.Contains(out, want) {
			t.Fatalf("help output missing %q", want)
		}
	}
}

func TestRun_UnknownCommand(t *testing.T) {
	err := run([]string{"nope"})
	if err == nil {
		t.Fatal("expected error for unknown command")
	}
}

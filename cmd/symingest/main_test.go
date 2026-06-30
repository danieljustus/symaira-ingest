package main

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/danieljustus/symaira-ingest/internal/config"
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
	for _, want := range []string{"ingest", "mcp", "version", "watch", "jobs", "retry"} {
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

func TestRun_JobsEmpty(t *testing.T) {
	tempDB := filepath.Join(t.TempDir(), "test.db")
	var sb strings.Builder
	oldStdout := stdout
	stdout = &sb
	defer func() { stdout = oldStdout }()

	if err := run([]string{"jobs", "-db", tempDB}); err != nil {
		t.Fatalf("run(jobs): %v", err)
	}
	if got := strings.TrimSpace(sb.String()); got != "No jobs in queue." {
		t.Fatalf("expected 'No jobs in queue.', got %q", got)
	}
}

func TestRun_JobsJSON(t *testing.T) {
	tempDB := filepath.Join(t.TempDir(), "test.db")
	var sb strings.Builder
	oldStdout := stdout
	stdout = &sb
	defer func() { stdout = oldStdout }()

	if err := run([]string{"jobs", "-db", tempDB, "-json"}); err != nil {
		t.Fatalf("run(jobs -json): %v", err)
	}
	if got := strings.TrimSpace(sb.String()); got != "[]" {
		t.Fatalf("expected '[]', got %q", got)
	}
}

func TestResolveConfig_FlagOverridesEnv(t *testing.T) {
	config.Loader.ResetCache()
	save := setEnv(t, map[string]string{
		"SYMINGEST_VAULT":        "/env/vault",
		"SYMINGEST_ARCHIVE_PATH": "/env/archive",
		"SYMINGEST_DB_PATH":      "/env/test.db",
		"SYMINGEST_OCR_LANG":     "deu",
	})
	defer save()

	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	ocrLang, vault, archive, db := registerSharedFlags(fs)
	args := []string{
		"-vault", "/flag/vault",
		"-archive", "/flag/archive",
		"-db", "/flag/test.db",
		"-ocr-lang", "fra",
	}
	if err := fs.Parse(args); err != nil {
		t.Fatalf("parse: %v", err)
	}

	cfg, err := resolveConfig(fs, ocrLang, vault, archive, db)
	if err != nil {
		t.Fatalf("resolveConfig: %v", err)
	}

	if cfg.vault != "/flag/vault" {
		t.Errorf("vault = %q, want /flag/vault", cfg.vault)
	}
	if cfg.archive != "/flag/archive" {
		t.Errorf("archive = %q, want /flag/archive", cfg.archive)
	}
	if cfg.db != "/flag/test.db" {
		t.Errorf("db = %q, want /flag/test.db", cfg.db)
	}
	if cfg.ocrLang != "fra" {
		t.Errorf("ocrLang = %q, want fra", cfg.ocrLang)
	}
}

func TestResolveConfig_EnvUsedWhenFlagsEmpty(t *testing.T) {
	config.Loader.ResetCache()
	save := setEnv(t, map[string]string{
		"SYMINGEST_VAULT":        "/env/vault",
		"SYMINGEST_ARCHIVE_PATH": "/env/archive",
		"SYMINGEST_DB_PATH":      "/env/test.db",
		"SYMINGEST_OCR_LANG":     "deu",
	})
	defer save()

	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	ocrLang, vault, archive, db := registerSharedFlags(fs)
	if err := fs.Parse([]string{}); err != nil {
		t.Fatalf("parse: %v", err)
	}

	cfg, err := resolveConfig(fs, ocrLang, vault, archive, db)
	if err != nil {
		t.Fatalf("resolveConfig: %v", err)
	}

	if cfg.vault != "/env/vault" {
		t.Errorf("vault = %q, want /env/vault", cfg.vault)
	}
	if cfg.archive != "/env/archive" {
		t.Errorf("archive = %q, want /env/archive", cfg.archive)
	}
	if cfg.db != "/env/test.db" {
		t.Errorf("db = %q, want /env/test.db", cfg.db)
	}
	if cfg.ocrLang != "deu" {
		t.Errorf("ocrLang = %q, want deu", cfg.ocrLang)
	}
}

func TestResolveConfig_DefaultsWhenNothingSet(t *testing.T) {
	config.Loader.ResetCache()
	save := setEnv(t, map[string]string{
		"SYMINGEST_VAULT":        "",
		"SYMINGEST_ARCHIVE_PATH": "",
		"SYMINGEST_DB_PATH":      "",
		"SYMINGEST_OCR_LANG":     "",
	})
	defer save()

	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	ocrLang, vault, archive, db := registerSharedFlags(fs)
	if err := fs.Parse([]string{}); err != nil {
		t.Fatalf("parse: %v", err)
	}

	cfg, err := resolveConfig(fs, ocrLang, vault, archive, db)
	if err != nil {
		t.Fatalf("resolveConfig: %v", err)
	}

	if cfg.ocrLang != "eng" {
		t.Errorf("ocrLang = %q, want eng (hardcoded default)", cfg.ocrLang)
	}
	if cfg.vault != "" {
		t.Errorf("vault = %q, want empty (no default)", cfg.vault)
	}
	if cfg.archive == "" {
		t.Error("archive should have XDG default, got empty")
	}
	if cfg.db == "" {
		t.Error("db should have XDG default, got empty")
	}
}

func setEnv(t *testing.T, vars map[string]string) func() {
	t.Helper()
	origins := make(map[string]string, len(vars))
	for k, v := range vars {
		origins[k] = os.Getenv(k)
		os.Setenv(k, v)
	}
	return func() {
		for k, orig := range origins {
			if orig == "" {
				os.Unsetenv(k)
			} else {
				os.Setenv(k, orig)
			}
		}
	}
}

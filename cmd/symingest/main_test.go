package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/danieljustus/symaira-corekit/exitcodes"

	"github.com/danieljustus/symaira-ingest/internal/config"
	"github.com/danieljustus/symaira-ingest/internal/store"
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

func TestRun_ImportPaperless_StatusEmpty(t *testing.T) {
	tempDB := filepath.Join(t.TempDir(), "test.db")
	var sb strings.Builder
	oldStdout := stdout
	stdout = &sb
	defer func() { stdout = oldStdout }()

	err := run([]string{
		"import", "paperless",
		"-db", tempDB,
		"-base-url", "https://paperless.example",
		"-status",
	})
	if err != nil {
		t.Fatalf("run(import paperless -status): %v", err)
	}
	if got := strings.TrimSpace(sb.String()); !strings.Contains(got, "No recorded import status") {
		t.Fatalf("expected no-status message, got %q", got)
	}
}

func TestRun_ImportPaperless_StatusUsesConfigBaseURL(t *testing.T) {
	home := t.TempDir()
	oldHome := os.Getenv("HOME")
	os.Setenv("HOME", home)
	defer os.Setenv("HOME", oldHome)
	config.Loader.ResetCache()
	defer config.Loader.ResetCache()

	cfgDir := filepath.Join(home, ".config", "symingest")
	if err := os.MkdirAll(cfgDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfgDir, "config.toml"), []byte("paperless_base_url = \"https://paperless.from-config\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	var sb strings.Builder
	oldStdout := stdout
	stdout = &sb
	defer func() { stdout = oldStdout }()

	err := run([]string{
		"import", "paperless",
		"-db", filepath.Join(home, "test.db"),
		"-status",
	})
	if err != nil {
		t.Fatalf("run(import paperless -status): %v", err)
	}
	if got := sb.String(); !strings.Contains(got, "https://paperless.from-config") {
		t.Fatalf("status output did not use config base URL: %q", got)
	}
}

func TestRun_ImportPaperless_StatusJSONAfterUpsert(t *testing.T) {
	tempDB := filepath.Join(t.TempDir(), "test.db")
	st, err := store.Open(tempDB)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	if err := st.UpsertPaperlessImportState(context.Background(), "https://paperless.example", 42, "failed", "boom"); err != nil {
		t.Fatalf("UpsertPaperlessImportState: %v", err)
	}
	if err := st.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	var sb strings.Builder
	oldStdout := stdout
	stdout = &sb
	defer func() { stdout = oldStdout }()

	err = run([]string{
		"import", "paperless",
		"-db", tempDB,
		"-base-url", "https://paperless.example",
		"-status", "-json",
	})
	if err != nil {
		t.Fatalf("run(import paperless -status -json): %v", err)
	}
	out := sb.String()
	for _, want := range []string{`"paperless_document_id": 42`, `"status": "failed"`, `"last_error": "boom"`} {
		if !strings.Contains(out, want) {
			t.Fatalf("output missing %q:\n%s", want, out)
		}
	}
}

func TestRun_SetupWritesConfigWithoutTokenAndDoctorPasses(t *testing.T) {
	home := t.TempDir()
	oldHome := os.Getenv("HOME")
	os.Setenv("HOME", home)
	defer os.Setenv("HOME", oldHome)
	config.Loader.ResetCache()
	defer config.Loader.ResetCache()

	toolsDir := t.TempDir()
	writeTestBin(t, toolsDir, "tesseract", `#!/bin/sh
if [ "$1" = "--list-langs" ]; then
  echo "List of available languages (2):"
  echo "eng"
  echo "deu"
  exit 0
fi
exit 0
`)
	writeTestBin(t, toolsDir, "pdftoppm", "#!/bin/sh\nexit 0\n")
	writeTestBin(t, toolsDir, "sips", "#!/bin/sh\nexit 0\n")
	oldPath := os.Getenv("PATH")
	os.Setenv("PATH", toolsDir+string(os.PathListSeparator)+oldPath)
	defer os.Setenv("PATH", oldPath)
	oldToken := os.Getenv("PAPERLESS_TOKEN")
	os.Setenv("PAPERLESS_TOKEN", "super-secret-token")
	defer os.Setenv("PAPERLESS_TOKEN", oldToken)

	vault := filepath.Join(home, "vault")
	archive := filepath.Join(home, "archive")
	db := filepath.Join(home, "data", "symingest.db")
	inbox := filepath.Join(home, "inbox")
	var sb strings.Builder
	oldStdout := stdout
	stdout = &sb
	defer func() { stdout = oldStdout }()

	if err := run([]string{
		"setup",
		"--vault", vault,
		"--archive", archive,
		"--db", db,
		"--inbox", inbox,
		"--ocr-lang", "eng",
		"--paperless-base-url", "https://paperless.example",
	}); err != nil {
		t.Fatalf("run(setup): %v", err)
	}
	configPath := filepath.Join(home, ".config", "symingest", "config.toml")
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	content := string(data)
	for _, want := range []string{fmt.Sprintf("vault = %q", vault), fmt.Sprintf("archive_path = %q", archive), fmt.Sprintf("db_path = %q", db), fmt.Sprintf("inbox = %q", inbox), `ocr_lang = "eng"`, `paperless_base_url = "https://paperless.example"`} {
		if !strings.Contains(content, want) {
			t.Fatalf("config missing %q:\n%s", want, content)
		}
	}
	if strings.Contains(content, "super-secret-token") || strings.Contains(sb.String(), "super-secret-token") {
		t.Fatalf("setup leaked Paperless token")
	}

	sb.Reset()
	if err := run([]string{"setup", "--vault", vault, "--archive", archive, "--db", db, "--inbox", inbox, "--ocr-lang", "eng", "--paperless-base-url", "https://paperless.example"}); err != nil {
		t.Fatalf("idempotent setup: %v", err)
	}
	if !strings.Contains(sb.String(), "already up to date") {
		t.Fatalf("expected idempotent message, got %q", sb.String())
	}

	sb.Reset()
	if err := run([]string{"doctor", "--json"}); err != nil {
		t.Fatalf("doctor should pass generated config: %v\n%s", err, sb.String())
	}
	var report doctorReport
	if err := json.Unmarshal([]byte(sb.String()), &report); err != nil {
		t.Fatalf("doctor JSON: %v\n%s", err, sb.String())
	}
	if report.Status != doctorOK || report.Failures != 0 || report.Warnings != 0 {
		t.Fatalf("doctor report = %+v", report)
	}
}

func TestRun_SetupDryRunShowsDiffAndDoesNotWrite(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.toml")
	var sb strings.Builder
	oldStdout := stdout
	stdout = &sb
	defer func() { stdout = oldStdout }()

	if err := run([]string{
		"setup",
		"--config", configPath,
		"--vault", filepath.Join(dir, "vault"),
		"--inbox", filepath.Join(dir, "inbox"),
		"--paperless-base-url", "https://paperless.example",
		"--dry-run",
	}); err != nil {
		t.Fatalf("dry-run setup: %v", err)
	}
	if _, err := os.Stat(configPath); !os.IsNotExist(err) {
		t.Fatalf("dry-run should not write config, stat err=%v", err)
	}
	if out := sb.String(); !strings.Contains(out, "+++ proposed") || !strings.Contains(out, "+ vault =") {
		t.Fatalf("dry-run output missing diff:\n%s", out)
	}
}

func TestRun_DoctorPaperlessJSONDoesNotLeakToken(t *testing.T) {
	toolsDir := t.TempDir()
	writeTestBin(t, toolsDir, "tesseract", `#!/bin/sh
if [ "$1" = "--list-langs" ]; then
  echo "List of available languages (1):"
  echo "eng"
  exit 0
fi
exit 0
`)
	writeTestBin(t, toolsDir, "pdftoppm", "#!/bin/sh\nexit 0\n")
	writeTestBin(t, toolsDir, "sips", "#!/bin/sh\nexit 0\n")
	oldPath := os.Getenv("PATH")
	os.Setenv("PATH", toolsDir+string(os.PathListSeparator)+oldPath)
	defer os.Setenv("PATH", oldPath)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Token secret-token" {
			t.Fatalf("Authorization = %q", got)
		}
		if r.URL.Path != "/api/documents/" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		fmt.Fprintln(w, `{"count": 12, "results": []}`)
	}))
	defer srv.Close()

	dir := t.TempDir()
	var sb strings.Builder
	oldStdout := stdout
	stdout = &sb
	defer func() { stdout = oldStdout }()
	err := run([]string{
		"doctor", "--paperless", "--json",
		"--vault", filepath.Join(dir, "vault"),
		"--archive", filepath.Join(dir, "archive"),
		"--db", filepath.Join(dir, "db.sqlite"),
		"--inbox", filepath.Join(dir, "inbox"),
		"--ocr-lang", "eng",
		"--base-url", srv.URL,
		"--token", "secret-token",
	})
	if err != nil {
		t.Fatalf("doctor paperless: %v\n%s", err, sb.String())
	}
	if strings.Contains(sb.String(), "secret-token") {
		t.Fatalf("doctor leaked token:\n%s", sb.String())
	}
	var report doctorReport
	if err := json.Unmarshal([]byte(sb.String()), &report); err != nil {
		t.Fatalf("doctor JSON: %v", err)
	}
	if report.Status != doctorOK {
		t.Fatalf("report status = %s, want ok: %+v", report.Status, report)
	}
}

func TestRun_DoctorExitCodesForWarningsAndFailures(t *testing.T) {
	toolsDir := t.TempDir()
	writeTestBin(t, toolsDir, "tesseract", `#!/bin/sh
if [ "$1" = "--list-langs" ]; then
  echo "eng"
  exit 0
fi
exit 0
`)
	writeTestBin(t, toolsDir, "pdftoppm", "#!/bin/sh\nexit 0\n")
	writeTestBin(t, toolsDir, "sips", "#!/bin/sh\nexit 0\n")
	oldPath := os.Getenv("PATH")
	os.Setenv("PATH", toolsDir+string(os.PathListSeparator)+oldPath)
	defer os.Setenv("PATH", oldPath)

	dir := t.TempDir()
	oldStdout := stdout
	stdout = &strings.Builder{}
	defer func() { stdout = oldStdout }()

	warnErr := run([]string{"doctor", "--vault", filepath.Join(dir, "vault"), "--archive", filepath.Join(dir, "archive"), "--db", filepath.Join(dir, "db.sqlite"), "--ocr-lang", "eng"})
	if warnErr == nil || exitcodes.ExitCodeFromError(warnErr) != exitcodes.ExitNoInput {
		t.Fatalf("warning-only doctor error = %v, code=%d", warnErr, exitcodes.ExitCodeFromError(warnErr))
	}

	oldPath = os.Getenv("PATH")
	os.Setenv("PATH", t.TempDir())
	defer os.Setenv("PATH", oldPath)
	failErr := run([]string{"doctor", "--vault", filepath.Join(dir, "vault2"), "--archive", filepath.Join(dir, "archive2"), "--db", filepath.Join(dir, "db2.sqlite"), "--inbox", filepath.Join(dir, "inbox"), "--ocr-lang", "eng"})
	if failErr == nil || exitcodes.ExitCodeFromError(failErr) != exitcodes.ExitGeneric {
		t.Fatalf("failure doctor error = %v, code=%d", failErr, exitcodes.ExitCodeFromError(failErr))
	}
}

func writeTestBin(t *testing.T, dir, name, script string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake %s: %v", name, err)
	}
	return path
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

func TestParseDocumentIDs(t *testing.T) {
	t.Run("valid list tolerates whitespace and empty entries", func(t *testing.T) {
		ids, err := parseDocumentIDs(" 123, 456 ,,789 ")
		if err != nil {
			t.Fatalf("parseDocumentIDs: %v", err)
		}
		want := []int{123, 456, 789}
		if len(ids) != len(want) {
			t.Fatalf("ids = %v, want %v", ids, want)
		}
		for i, v := range want {
			if ids[i] != v {
				t.Errorf("ids[%d] = %d, want %d", i, ids[i], v)
			}
		}
	})
	t.Run("empty input yields no ids", func(t *testing.T) {
		ids, err := parseDocumentIDs("   ")
		if err != nil {
			t.Fatalf("parseDocumentIDs: %v", err)
		}
		if ids != nil {
			t.Errorf("ids = %v, want nil", ids)
		}
	})
	t.Run("non-numeric entry errors", func(t *testing.T) {
		if _, err := parseDocumentIDs("123,abc"); err == nil {
			t.Error("expected error for non-numeric ID")
		}
	})
	t.Run("non-positive entry errors", func(t *testing.T) {
		if _, err := parseDocumentIDs("123,0"); err == nil {
			t.Error("expected error for zero ID")
		}
		if _, err := parseDocumentIDs("-5"); err == nil {
			t.Error("expected error for negative ID")
		}
	})
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

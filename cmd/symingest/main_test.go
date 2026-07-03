package main

import (
	"context"
	"encoding/json"
	"flag"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

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

func TestRun_ImportPaperless_PlanWritesReportWithoutVault(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/documents/":
			json.NewEncoder(w).Encode(map[string]any{
				"count":   1,
				"results": []map[string]any{{"id": 5, "title": "Plan Doc", "created_date": "2026-01-15", "file_type": ".pdf", "mime_type": "application/pdf"}},
				"next":    nil,
			})
		case "/api/tags/", "/api/correspondents/", "/api/document_types/", "/api/storage_paths/":
			json.NewEncoder(w).Encode(map[string]any{"count": 0, "results": []any{}, "next": nil})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	dir := t.TempDir()
	reportPath := filepath.Join(dir, "plan.json")
	var sb strings.Builder
	oldStdout := stdout
	stdout = &sb
	defer func() { stdout = oldStdout }()

	err := run([]string{
		"import", "paperless",
		"-db", filepath.Join(dir, "test.db"),
		"-base-url", srv.URL,
		"-token", "test-token",
		"-plan",
		"-report", reportPath,
	})
	if err != nil {
		t.Fatalf("run(import paperless -plan): %v", err)
	}
	if !strings.Contains(sb.String(), "Import plan complete") {
		t.Fatalf("plan output missing completion message: %s", sb.String())
	}
	data, err := os.ReadFile(reportPath)
	if err != nil {
		t.Fatalf("read plan report: %v", err)
	}
	var report struct {
		Mode      string `json:"mode"`
		Documents []struct {
			Status    string `json:"status"`
			SourceURI string `json:"source_uri"`
		} `json:"documents"`
		RequiredTools []string `json:"required_tools"`
	}
	if err := json.Unmarshal(data, &report); err != nil {
		t.Fatalf("plan report JSON: %v", err)
	}
	if report.Mode != "plan" || len(report.Documents) != 1 || report.Documents[0].Status != "planned" || report.Documents[0].SourceURI != "paperless://documents/5" {
		t.Fatalf("unexpected plan report: %+v", report)
	}
	if len(report.RequiredTools) == 0 {
		t.Fatalf("plan report should identify required tools for PDF/OCR workload: %+v", report)
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

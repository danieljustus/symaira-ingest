package vaultreview

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testNote(t *testing.T, vault, name, archive, sum string, paperlessID int, tags []string) string {
	t.Helper()
	var b strings.Builder
	b.WriteString("---\n")
	b.WriteString("source_path: paperless://documents/")
	b.WriteString(strings.TrimSuffix(name, ".md"))
	b.WriteString("\ningested_at: 2026-07-03T10:00:00Z\n")
	b.WriteString("sha256: ")
	b.WriteString(sum)
	b.WriteString("\nmime: text/plain\n")
	b.WriteString("archive_path: ")
	b.WriteString(archive)
	b.WriteString("\ntags:\n")
	for _, tag := range tags {
		b.WriteString("  - ")
		b.WriteString(tag)
		b.WriteByte('\n')
	}
	b.WriteString("paperless:\n  document_id: ")
	b.WriteString(string(rune('0' + paperlessID)))
	b.WriteString("\n---\nbody\n")
	path := filepath.Join(vault, name)
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func archiveFile(t *testing.T, dir, name, content string) (string, string) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(content))
	return path, hex.EncodeToString(sum[:])
}

func hasFailure(r *ValidationReport, check string) bool {
	for _, f := range r.Failures {
		if f.Check == check {
			return true
		}
	}
	return false
}

func TestValidateVaultReportsArchiveBadYAMLAndDuplicatePaperlessID(t *testing.T) {
	dir := t.TempDir()
	vault := filepath.Join(dir, "vault")
	archive := filepath.Join(dir, "archive")
	if err := os.MkdirAll(vault, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(archive, 0o700); err != nil {
		t.Fatal(err)
	}
	archivePath, sum := archiveFile(t, archive, "one.txt", "hello")
	testNote(t, vault, "one.md", archivePath, sum, 1, []string{"inbox"})
	testNote(t, vault, "dupe.md", archivePath, sum, 1, []string{"review"})
	testNote(t, vault, "missing-archive.md", filepath.Join(archive, "missing.txt"), sum, 2, []string{"review"})
	if err := os.WriteFile(filepath.Join(vault, "bad.md"), []byte("---\n: bad: yaml\n---\nbody"), 0o600); err != nil {
		t.Fatal(err)
	}

	report, err := ValidateVault(vault)
	if err != nil {
		t.Fatal(err)
	}
	for _, check := range []string{"archive.exists", "frontmatter", "paperless.document_id.unique"} {
		if !hasFailure(report, check) {
			t.Fatalf("missing failure %s in %+v", check, report.Failures)
		}
	}
}

func TestApplyCorrectionAndBulkUpdateProtectInbox(t *testing.T) {
	dir := t.TempDir()
	vault := filepath.Join(dir, "vault")
	archive := filepath.Join(dir, "archive")
	if err := os.MkdirAll(vault, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(archive, 0o700); err != nil {
		t.Fatal(err)
	}
	archivePath, sum := archiveFile(t, archive, "one.txt", "hello")
	note := testNote(t, vault, "one.md", archivePath, sum, 1, []string{"inbox", "old"})
	testNote(t, vault, "two.md", archivePath, sum, 2, []string{"old"})

	if _, err := ApplyCorrection(vault, Correction{PaperlessID: 1, RemoveTags: []string{"inbox"}}, true); err == nil {
		t.Fatal("expected inbox removal to be refused")
	}
	corr := "Acme"
	res, err := ApplyCorrection(vault, Correction{PaperlessID: 1, AddTags: []string{"new"}, Correspondent: &corr}, true)
	if err != nil {
		t.Fatal(err)
	}
	if !res.DryRun || res.Written {
		t.Fatalf("dry run result wrong: %+v", res)
	}
	data, _ := os.ReadFile(note)
	if strings.Contains(string(data), "new") {
		t.Fatal("dry run wrote changes")
	}
	res, err = ApplyCorrection(vault, Correction{PaperlessID: 1, AddTags: []string{"new"}, Correspondent: &corr}, false)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Written {
		t.Fatalf("expected write: %+v", res)
	}
	data, _ = os.ReadFile(note)
	if !strings.Contains(string(data), "new") || !strings.Contains(string(data), "Acme") {
		t.Fatalf("update missing changes:\n%s", data)
	}

	results, err := BulkUpdateByTag(vault, "old", Correction{AddTags: []string{"bulk"}}, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 {
		t.Fatalf("bulk matched %d, want 2", len(results))
	}
}

func TestBuildReviewReportAndHTML(t *testing.T) {
	dir := t.TempDir()
	jsonPath := filepath.Join(dir, "migration.json")
	htmlPath := filepath.Join(dir, "review.html")
	data := `{"run_id":"r1","documents":[{"id":1,"status":"imported","mime":"text/plain","vault_path":"v","archive_path":"a"},{"id":2,"status":"failed","error":"boom","warnings":["w"]}]}`
	if err := os.WriteFile(jsonPath, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	report, err := BuildReviewReport(jsonPath, ReviewFilters{Failed: true})
	if err != nil {
		t.Fatal(err)
	}
	if report.Total != 1 || report.Documents[0].ID != 2 {
		t.Fatalf("unexpected report: %+v", report)
	}
	if err := WriteReviewHTML(htmlPath, report); err != nil {
		t.Fatal(err)
	}
	html, _ := os.ReadFile(htmlPath)
	if !strings.Contains(string(html), "boom") || !strings.Contains(string(html), "Document body text is intentionally not included") {
		t.Fatalf("bad html: %s", html)
	}
}

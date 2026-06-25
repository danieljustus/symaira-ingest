package writer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestWriteNote(t *testing.T) {
	vault := t.TempDir()
	source := "/tmp/scans/invoice.pdf"
	w := &NoteWriter{Vault: vault}
	path, err := w.WriteNote(source, "deadbeef", "application/pdf", "tesseract", "hello", time.Unix(0, 0).UTC())
	if err != nil {
		t.Fatalf("WriteNote: %v", err)
	}
	want := filepath.Join(vault, "invoice.pdf.md")
	if path != want {
		t.Fatalf("path = %q, want %q", path, want)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	out := string(data)
	if !strings.HasPrefix(out, "---\n") {
		t.Fatal("missing frontmatter start")
	}
	for _, needle := range []string{"source_path:", "sha256: deadbeef", "ocr_engine: tesseract", "hello"} {
		if !strings.Contains(out, needle) {
			t.Fatalf("output missing %q", needle)
		}
	}
}

func TestWriteNote_Atomic(t *testing.T) {
	vault := t.TempDir()
	w := &NoteWriter{Vault: vault}
	path, err := w.WriteNote("/tmp/a.txt", "abc", "text/plain", "", "body", time.Now().UTC())
	if err != nil {
		t.Fatalf("WriteNote: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("note not written: %v", err)
	}
	matches, err := filepath.Glob(vault + "/a.txt.tmp-*")
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 0 {
		t.Fatalf("leftover temp files: %v", matches)
	}
}

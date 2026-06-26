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
	path, err := w.WriteNote(source, "deadbeef", "application/pdf", "tesseract", "hello", "", time.Unix(0, 0).UTC(), "invoice", []string{"financial"}, "Acme Corp", "Invoice")
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
	for _, needle := range []string{"source_path:", "sha256: deadbeef", "ocr_engine: tesseract", "category: invoice", "tags:", "- financial", "correspondent: Acme Corp", "document_type: Invoice", "hello"} {
		if !strings.Contains(out, needle) {
			t.Fatalf("output missing %q", needle)
		}
	}
}

func TestWriteNote_Atomic(t *testing.T) {
	vault := t.TempDir()
	w := &NoteWriter{Vault: vault}
	path, err := w.WriteNote("/tmp/a.txt", "abc", "text/plain", "", "body", "", time.Now().UTC(), "", nil, "", "")
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

func TestWriteNote_Golden(t *testing.T) {
	vault := t.TempDir()
	w := &NoteWriter{Vault: vault}

	fixedTime := time.Date(2026, 6, 26, 15, 0, 0, 0, time.UTC)
	cases := []struct {
		name       string
		sourcePath string
		sha256     string
		mime       string
		ocrEngine  string
		text       string
	}{
		{
			name:       "text",
			sourcePath: "/tmp/invoice.txt",
			sha256:     "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
			mime:       "text/plain",
			ocrEngine:  "",
			text:       "Hello World",
		},
		{
			name:       "image",
			sourcePath: "/tmp/invoice.png",
			sha256:     "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
			mime:       "image/png",
			ocrEngine:  "tesseract",
			text:       "Extracted text from image",
		},
		{
			name:       "pdf",
			sourcePath: "/tmp/invoice.pdf",
			sha256:     "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
			mime:       "application/pdf",
			ocrEngine:  "pdftoppm+tesseract",
			text:       "Extracted text from PDF",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path, err := w.WriteNote(tc.sourcePath, tc.sha256, tc.mime, tc.ocrEngine, tc.text, "", fixedTime, "", nil, "", "")
			if err != nil {
				t.Fatalf("WriteNote failed: %v", err)
			}

			got, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("failed to read generated note: %v", err)
			}

			goldenPath := filepath.Join("testdata", tc.name+".golden")
			if os.Getenv("UPDATE_GOLDEN") == "true" {
				if err := os.MkdirAll("testdata", 0o755); err != nil {
					t.Fatalf("failed to create testdata dir: %v", err)
				}
				if err := os.WriteFile(goldenPath, got, 0o644); err != nil {
					t.Fatalf("failed to write golden file: %v", err)
				}
			}

			want, err := os.ReadFile(goldenPath)
			if err != nil {
				t.Fatalf("failed to read golden file (run UPDATE_GOLDEN=true go test ./internal/writer to generate): %v", err)
			}

			if string(got) != string(want) {
				t.Errorf("generated note does not match golden file %s\nGOT:\n%s\nWANT:\n%s", goldenPath, string(got), string(want))
			}
		})
	}
}

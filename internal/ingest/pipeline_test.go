package ingest

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/danieljustus/symaira-ingest/internal/extract"
	"github.com/danieljustus/symaira-ingest/internal/store"
	"github.com/danieljustus/symaira-ingest/internal/writer"
)

func TestPipeline_Deduplicates(t *testing.T) {
	dir := t.TempDir()
	s, err := store.Open(filepath.Join(dir, "docs.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	vault := filepath.Join(dir, "vault")
	p := &Pipeline{
		Engine: nil,
		Store:  s,
		Writer: &writer.NoteWriter{Vault: vault},
	}

	path := filepath.Join(dir, "note.txt")
	if err := os.WriteFile(path, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := p.Ingest(context.Background(), path); err != nil {
		t.Fatalf("first ingest: %v", err)
	}
	if _, err := p.Ingest(context.Background(), path); !errors.Is(err, ErrDuplicate) {
		t.Fatalf("second ingest error = %v, want ErrDuplicate", err)
	}

	matches, err := filepath.Glob(vault + "/*.md")
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 {
		t.Fatalf("expected 1 note, got %d", len(matches))
	}
}

func TestPipeline_ExtractsWithEngine(t *testing.T) {
	dir := t.TempDir()
	s, err := store.Open(filepath.Join(dir, "docs.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	vault := filepath.Join(dir, "vault")
	eng := extract.Engine(&fakeEngine{result: &extract.Result{Text: "ocr text"}})
	p := &Pipeline{
		Engine: eng,
		Store:  s,
		Writer: &writer.NoteWriter{Vault: vault},
	}

	path := filepath.Join(dir, "scan.png")
	if err := os.WriteFile(path, []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A}, 0o644); err != nil {
		t.Fatal(err)
	}

	res, err := p.Ingest(context.Background(), path)
	if err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	if res.Extract.Text != "ocr text" {
		t.Fatalf("text = %q, want %q", res.Extract.Text, "ocr text")
	}
}

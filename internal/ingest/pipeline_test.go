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

type fakePipelineEngine struct {
	result *extract.Result
	err    error
}

func (f *fakePipelineEngine) Extract(ctx context.Context, source string, kind extract.Kind) (*extract.Result, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.result, nil
}

func TestPipeline_Deduplicates(t *testing.T) {
	dir := t.TempDir()
	s, err := store.Open(filepath.Join(dir, "docs.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	vault := filepath.Join(dir, "vault")
	archive := filepath.Join(dir, "archive")
	p := &Pipeline{
		Engine:     nil,
		Store:      s,
		Writer:     &writer.NoteWriter{Vault: vault},
		ArchiveDir: archive,
	}

	path := filepath.Join(dir, "note.txt")
	if err := os.WriteFile(path, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}

	res, err := p.Ingest(context.Background(), path)
	if err != nil {
		t.Fatalf("first ingest: %v", err)
	}
	if res.ArchivePath == "" {
		t.Fatal("expected ArchivePath to be populated in result")
	}
	if _, err := os.Stat(res.ArchivePath); err != nil {
		t.Fatalf("expected archived file to exist: %v", err)
	}

	// Test re-ingesting the exact same file path
	_, err = p.Ingest(context.Background(), path)
	if err == nil {
		t.Fatal("expected duplicate error, got nil")
	}
	var dupErr *DuplicateError
	if !errors.As(err, &dupErr) {
		t.Fatalf("expected DuplicateError, got %T: %v", err, err)
	}
	if dupErr.VaultPath != res.VaultPath {
		t.Errorf("dupErr.VaultPath = %q, want %q", dupErr.VaultPath, res.VaultPath)
	}
	if dupErr.ArchivePath != res.ArchivePath {
		t.Errorf("dupErr.ArchivePath = %q, want %q", dupErr.ArchivePath, res.ArchivePath)
	}

	// Test duplicate from a different source path
	otherPath := filepath.Join(dir, "other.txt")
	if err := os.WriteFile(otherPath, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err = p.Ingest(context.Background(), otherPath)
	if err == nil {
		t.Fatal("expected duplicate error for different path, got nil")
	}
	if !errors.As(err, &dupErr) {
		t.Fatalf("expected DuplicateError, got %T: %v", err, err)
	}
	if dupErr.VaultPath != res.VaultPath {
		t.Errorf("dupErr.VaultPath = %q, want %q", dupErr.VaultPath, res.VaultPath)
	}
	if dupErr.ArchivePath != res.ArchivePath {
		t.Errorf("dupErr.ArchivePath = %q, want %q", dupErr.ArchivePath, res.ArchivePath)
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
	eng := extract.Engine(&fakePipelineEngine{result: &extract.Result{Text: "ocr text"}})
	p := &Pipeline{
		Engine:     eng,
		Store:      s,
		Writer:     &writer.NoteWriter{Vault: vault},
		ArchiveDir: filepath.Join(dir, "archive"),
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

package ingest

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/danieljustus/symaira-ingest/internal/extract"
)

type fakeEngine struct {
	result *extract.Result
}

func (f *fakeEngine) Extract(ctx context.Context, path string, kind extract.Kind) (*extract.Result, error) {
	return f.result, nil
}

func TestOneShot_TextFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "note.txt")
	if err := os.WriteFile(path, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := OneShot(context.Background(), path, nil)
	if err != nil {
		t.Fatalf("OneShot: %v", err)
	}
	if res.Extract.Text != "hello" {
		t.Fatalf("text = %q, want hello", res.Extract.Text)
	}
}

func TestOneShot_Engine(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "scan.png")
	if err := os.WriteFile(path, []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A}, 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := OneShot(context.Background(), path, &fakeEngine{result: &extract.Result{Text: "ocr"}})
	if err != nil {
		t.Fatalf("OneShot: %v", err)
	}
	if res.Extract.Text != "ocr" {
		t.Fatalf("text = %q, want ocr", res.Extract.Text)
	}
}

func TestOneShot_Directory(t *testing.T) {
	dir := t.TempDir()
	if _, err := OneShot(context.Background(), dir, nil); err == nil {
		t.Fatal("expected error for directory")
	}
}

package store

import (
	"context"
	"path/filepath"
	"testing"
)

func TestCreateOrGet_Deduplicates(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	ctx := context.Background()
	d1, err := s.CreateOrGet(ctx, "/tmp/a.pdf", "abc123", "application/pdf")
	if err != nil {
		t.Fatalf("CreateOrGet first: %v", err)
	}
	d2, err := s.CreateOrGet(ctx, "/tmp/b.pdf", "abc123", "application/pdf")
	if err != nil {
		t.Fatalf("CreateOrGet second: %v", err)
	}
	if d1.ID != d2.ID {
		t.Fatal("expected duplicate hash to return same document")
	}
}

func TestSetVaultPath(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	ctx := context.Background()
	d, err := s.CreateOrGet(ctx, "/tmp/a.pdf", "abc", "application/pdf")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetVaultPath(ctx, d.ID, "/vault/a.pdf.md"); err != nil {
		t.Fatalf("SetVaultPath: %v", err)
	}
	got, err := s.ByHash(ctx, "abc")
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "done" {
		t.Fatalf("status = %q, want done", got.Status)
	}
	if got.VaultPath == nil || *got.VaultPath != "/vault/a.pdf.md" {
		t.Fatalf("vault_path mismatch")
	}
}

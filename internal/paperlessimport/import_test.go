package paperlessimport

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/danieljustus/symaira-ingest/internal/extract"
	"github.com/danieljustus/symaira-ingest/internal/ingest"
	"github.com/danieljustus/symaira-ingest/internal/store"
	"github.com/danieljustus/symaira-ingest/internal/writer"
)

type fakeEngine struct{}

func (fakeEngine) Extract(ctx context.Context, path string, kind extract.Kind) (*extract.Result, error) {
	return &extract.Result{Text: "extracted text", MIME: string(kind), Engine: "fake"}, nil
}

// handleEmptyLookups serves an empty paginated result for the four
// Paperless lookup endpoints that Run() always loads before importing.
func handleEmptyLookups(w http.ResponseWriter, r *http.Request) bool {
	switch r.URL.Path {
	case "/api/tags/", "/api/correspondents/", "/api/document_types/", "/api/storage_paths/":
		json.NewEncoder(w).Encode(map[string]any{"count": 0, "results": []any{}, "next": nil})
		return true
	default:
		return false
	}
}

func TestRun_DryRun(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if handleEmptyLookups(w, r) {
			return
		}
		switch r.URL.Path {
		case "/api/documents/":
			json.NewEncoder(w).Encode(map[string]any{
				"count": 1,
				"results": []map[string]any{
					{"id": 1, "title": "Test Doc", "created_date": "2026-01-15T00:00:00Z", "file_type": ".pdf"},
				},
				"next": nil,
			})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	dir := t.TempDir()
	s, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	pipeline := &ingest.Pipeline{
		Engine:     fakeEngine{},
		Store:      s,
		Writer:     &writer.NoteWriter{Vault: filepath.Join(dir, "vault")},
		ArchiveDir: filepath.Join(dir, "archive"),
	}

	stats, err := Run(context.Background(), Options{
		BaseURL: srv.URL,
		Token:   "test-token",
		DryRun:  true,
	}, pipeline)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if stats.Total != 1 {
		t.Errorf("stats.Total = %d, want 1", stats.Total)
	}
	if stats.Skipped != 1 {
		t.Errorf("stats.Skipped = %d, want 1", stats.Skipped)
	}
	if stats.Imported != 0 {
		t.Errorf("stats.Imported = %d, want 0", stats.Imported)
	}
}

func TestRun_Import(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if handleEmptyLookups(w, r) {
			return
		}
		switch {
		case r.URL.Path == "/api/documents/":
			json.NewEncoder(w).Encode(map[string]any{
				"count": 1,
				"results": []map[string]any{
					{
						"id": 1, "title": "Invoice", "created_date": "2026-01-15T00:00:00Z",
						"file_type":     ".txt",
						"tags":          []map[string]any{{"id": 1, "name": "financial"}},
						"correspondent": map[string]any{"id": 1, "name": "Acme Corp"},
						"document_type": map[string]any{"id": 1, "name": "Invoice"},
					},
				},
				"next": nil,
			})
		case r.URL.Path == "/api/documents/1/download/":
			w.Write([]byte("invoice content"))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	dir := t.TempDir()
	s, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	pipeline := &ingest.Pipeline{
		Engine:     fakeEngine{},
		Store:      s,
		Writer:     &writer.NoteWriter{Vault: filepath.Join(dir, "vault")},
		ArchiveDir: filepath.Join(dir, "archive"),
	}

	stats, err := Run(context.Background(), Options{
		BaseURL: srv.URL,
		Token:   "test-token",
	}, pipeline)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if stats.Imported != 1 {
		t.Errorf("stats.Imported = %d, want 1", stats.Imported)
	}
	if stats.Failed != 0 {
		t.Errorf("stats.Failed = %d, want 0", stats.Failed)
	}

	matches, _ := filepath.Glob(filepath.Join(dir, "vault") + "/*.md")
	if len(matches) != 1 {
		t.Fatalf("expected 1 note, got %d", len(matches))
	}
}

func TestRun_ResolvesIDsViaLookups(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/documents/":
			json.NewEncoder(w).Encode(map[string]any{
				"count": 1,
				"results": []map[string]any{
					{
						"id": 1, "title": "Invoice", "created_date": "2026-01-15",
						"file_type":     ".txt",
						"tags":          []int{1, 99},
						"correspondent": 2,
						"document_type": 3,
						"storage_path":  7,
					},
				},
				"next": nil,
			})
		case "/api/documents/1/download/":
			w.Write([]byte("invoice content"))
		case "/api/tags/":
			json.NewEncoder(w).Encode(map[string]any{
				"count": 1, "results": []map[string]any{{"id": 1, "name": "financial"}}, "next": nil,
			})
		case "/api/correspondents/":
			json.NewEncoder(w).Encode(map[string]any{
				"count": 1, "results": []map[string]any{{"id": 2, "name": "Acme Corp"}}, "next": nil,
			})
		case "/api/document_types/":
			json.NewEncoder(w).Encode(map[string]any{
				"count": 1, "results": []map[string]any{{"id": 3, "name": "Invoice"}}, "next": nil,
			})
		case "/api/storage_paths/":
			json.NewEncoder(w).Encode(map[string]any{"count": 0, "results": []any{}, "next": nil})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	dir := t.TempDir()
	s, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	pipeline := &ingest.Pipeline{
		Engine:     fakeEngine{},
		Store:      s,
		Writer:     &writer.NoteWriter{Vault: filepath.Join(dir, "vault")},
		ArchiveDir: filepath.Join(dir, "archive"),
	}

	stats, err := Run(context.Background(), Options{
		BaseURL: srv.URL,
		Token:   "test-token",
	}, pipeline)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if stats.Imported != 1 {
		t.Errorf("stats.Imported = %d, want 1", stats.Imported)
	}

	// Tag 99, correspondent and document type resolve via lookups; tag 99
	// and the storage path have no matching entry and must be flagged.
	if len(stats.Warnings) != 2 {
		t.Fatalf("stats.Warnings = %v, want 2 entries (unresolved tag 99, storage path 7)", stats.Warnings)
	}

	matches, _ := filepath.Glob(filepath.Join(dir, "vault") + "/*.md")
	if len(matches) != 1 {
		t.Fatalf("expected 1 note, got %d", len(matches))
	}
	contentBytes, err := os.ReadFile(matches[0])
	if err != nil {
		t.Fatal(err)
	}
	content := string(contentBytes)
	if !contains(content, "financial") {
		t.Errorf("note content missing resolved tag name 'financial':\n%s", content)
	}
	if !contains(content, "Acme Corp") {
		t.Errorf("note content missing resolved correspondent name 'Acme Corp':\n%s", content)
	}
}

func TestRun_SinceFilter(t *testing.T) {
	var requestedURL string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if handleEmptyLookups(w, r) {
			return
		}
		if r.URL.Path == "/api/documents/" {
			requestedURL = r.URL.String()
		}
		json.NewEncoder(w).Encode(map[string]any{
			"count": 0, "results": []any{}, "next": nil,
		})
	}))
	defer srv.Close()

	dir := t.TempDir()
	s, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	pipeline := &ingest.Pipeline{
		Store:      s,
		Writer:     &writer.NoteWriter{Vault: filepath.Join(dir, "vault")},
		ArchiveDir: filepath.Join(dir, "archive"),
	}

	since := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	_, err = Run(context.Background(), Options{
		BaseURL: srv.URL,
		Token:   "test-token",
		Since:   since,
	}, pipeline)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if !contains(requestedURL, "created_date__gte=2026-01-01") {
		t.Errorf("expected since filter in URL, got: %s", requestedURL)
	}
}

func TestRun_ErrorStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte("unauthorized"))
	}))
	defer srv.Close()

	dir := t.TempDir()
	s, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	pipeline := &ingest.Pipeline{
		Store:      s,
		Writer:     &writer.NoteWriter{Vault: filepath.Join(dir, "vault")},
		ArchiveDir: filepath.Join(dir, "archive"),
	}

	_, err = Run(context.Background(), Options{
		BaseURL: srv.URL,
		Token:   "bad-token",
	}, pipeline)
	if err == nil {
		t.Fatal("expected error for 401 status")
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsSubstr(s, substr))
}

func containsSubstr(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

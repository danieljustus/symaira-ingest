package paperlessimport

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/danieljustus/symaira-ingest/internal/ingest"
	"github.com/danieljustus/symaira-ingest/internal/store"
	"github.com/danieljustus/symaira-ingest/internal/writer"
)

// verifyDoc is a minimal Paperless document description for the fake API used
// by the verifier tests.
type verifyDoc struct {
	id            int
	title         string
	correspondent map[string]any
	tags          []map[string]any
}

// newVerifyServer serves the document list, lookups, and downloads needed to
// import and then verify the given documents.
func newVerifyServer(t *testing.T, docs []verifyDoc) *httptest.Server {
	t.Helper()
	results := make([]map[string]any, 0, len(docs))
	for _, d := range docs {
		m := map[string]any{
			"id": d.id, "title": d.title,
			"created_date": "2026-01-15T00:00:00Z", "created": "2026-01-15T00:00:00Z",
			"file_type": ".txt",
		}
		if d.correspondent != nil {
			m["correspondent"] = d.correspondent
		}
		if d.tags != nil {
			m["tags"] = d.tags
		}
		results = append(results, m)
	}

	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if handleEmptyLookups(w, r) {
			return
		}
		switch {
		case r.URL.Path == "/api/documents/":
			json.NewEncoder(w).Encode(map[string]any{
				"count": len(results), "results": results, "next": nil,
			})
		case strings.HasPrefix(r.URL.Path, "/api/documents/") && strings.HasSuffix(r.URL.Path, "/download/"):
			w.Write([]byte("content of " + r.URL.Path))
		case strings.HasPrefix(r.URL.Path, "/api/documents/"):
			// GetDocument by ID (trailing /?format=json)
			id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/documents/"), "/")
			for _, res := range results {
				if strconv.Itoa(res["id"].(int)) == id {
					json.NewEncoder(w).Encode(res)
					return
				}
			}
			w.WriteHeader(http.StatusNotFound)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
}

// importForVerify imports docs into a fresh vault and returns the vault dir.
func importForVerify(t *testing.T, srvURL string, opts Options) (vault string) {
	t.Helper()
	dir := t.TempDir()
	vault = filepath.Join(dir, "vault")
	s, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })

	pipeline := &ingest.Pipeline{
		Engine:     fakeEngine{},
		Store:      s,
		Writer:     &writer.NoteWriter{Vault: vault},
		ArchiveDir: filepath.Join(dir, "archive"),
	}
	base := Options{BaseURL: srvURL, Token: "test-token"}
	base.IDs = opts.IDs
	stats, err := Run(context.Background(), base, pipeline)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if stats.Failed != 0 {
		t.Fatalf("import failed %d documents", stats.Failed)
	}
	return vault
}

func TestVerify_CompleteAfterImport(t *testing.T) {
	docs := []verifyDoc{{id: 1, title: "Doc 1"}, {id: 2, title: "Doc 2"}}
	srv := newVerifyServer(t, docs)
	defer srv.Close()

	vault := importForVerify(t, srv.URL, Options{})

	report, err := Verify(context.Background(), Options{BaseURL: srv.URL, Token: "test-token"}, vault)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if !report.Complete() {
		t.Fatalf("expected complete verification, got %+v", report)
	}
	if report.SourceDocuments != 2 || report.VaultNotes != 2 || report.Verified != 2 {
		t.Errorf("report = %+v, want source=2 notes=2 verified=2", report)
	}
}

func TestVerify_MissingDocument(t *testing.T) {
	docs := []verifyDoc{{id: 1, title: "Doc 1"}, {id: 2, title: "Doc 2"}}
	srv := newVerifyServer(t, docs)
	defer srv.Close()

	// Import only document 1, leaving document 2 missing from the vault.
	vault := importForVerify(t, srv.URL, Options{IDs: []int{1}})

	report, err := Verify(context.Background(), Options{BaseURL: srv.URL, Token: "test-token"}, vault)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if report.Complete() {
		t.Fatal("expected incomplete verification when a document is missing")
	}
	if len(report.Missing) != 1 || report.Missing[0] != 2 {
		t.Errorf("Missing = %v, want [2]", report.Missing)
	}
}

func TestVerify_MissingArchivedOriginal(t *testing.T) {
	docs := []verifyDoc{{id: 1, title: "Doc 1"}}
	srv := newVerifyServer(t, docs)
	defer srv.Close()

	vault := importForVerify(t, srv.URL, Options{})

	// Locate the note and delete the archived original it references.
	notes, err := scanVaultNotes(vault)
	if err != nil {
		t.Fatal(err)
	}
	note := notes[1][0]
	if note.ArchivePath == "" {
		t.Fatal("imported note has no archive path")
	}
	if err := os.Remove(note.ArchivePath); err != nil {
		t.Fatalf("remove archive: %v", err)
	}

	report, err := Verify(context.Background(), Options{BaseURL: srv.URL, Token: "test-token"}, vault)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if report.Complete() {
		t.Fatal("expected incomplete verification when the archived original is gone")
	}
	if len(report.MissingArchive) != 1 || report.MissingArchive[0] != 1 {
		t.Errorf("MissingArchive = %v, want [1]", report.MissingArchive)
	}
}

func TestVerify_MetadataMismatch(t *testing.T) {
	docs := []verifyDoc{{
		id: 1, title: "Doc 1",
		correspondent: map[string]any{"id": 5, "name": "Acme Corp"},
	}}
	srv := newVerifyServer(t, docs)
	defer srv.Close()

	vault := importForVerify(t, srv.URL, Options{})

	// Corrupt the stored correspondent so it no longer matches the source.
	notes, _ := scanVaultNotes(vault)
	note := notes[1][0]
	tamperNoteCorrespondent(t, vault, note, "Wrong Corp")

	report, err := Verify(context.Background(), Options{BaseURL: srv.URL, Token: "test-token"}, vault)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if report.Complete() {
		t.Fatal("expected incomplete verification on a metadata mismatch")
	}
	found := false
	for _, m := range report.Mismatches {
		if m.DocumentID == 1 && m.Field == "correspondent" && m.Expected == "Acme Corp" && m.Got == "Wrong Corp" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected correspondent mismatch (Acme Corp vs Wrong Corp), got %+v", report.Mismatches)
	}
}

// tamperNoteCorrespondent rewrites the single vault note in place, replacing
// the correspondent frontmatter value.
func tamperNoteCorrespondent(t *testing.T, vault string, note *writer.Note, newValue string) {
	t.Helper()
	matches, _ := filepath.Glob(filepath.Join(vault, "*.md"))
	if len(matches) != 1 {
		t.Fatalf("expected 1 note to tamper, got %d", len(matches))
	}
	data, err := os.ReadFile(matches[0])
	if err != nil {
		t.Fatal(err)
	}
	out := strings.Replace(string(data), "correspondent: "+note.Correspondent, "correspondent: "+newValue, 1)
	if out == string(data) {
		t.Fatalf("could not find correspondent %q to replace in note", note.Correspondent)
	}
	if err := os.WriteFile(matches[0], []byte(out), 0o644); err != nil {
		t.Fatal(err)
	}
}

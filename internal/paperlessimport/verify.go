package paperlessimport

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/danieljustus/symaira-ingest/internal/paperless"
	"github.com/danieljustus/symaira-ingest/internal/writer"
	"gopkg.in/yaml.v3"
)

// VerifyMismatch records a single metadata field that differs between a
// Paperless source document and its generated vault note.
type VerifyMismatch struct {
	DocumentID int    `json:"document_id"`
	Field      string `json:"field"`
	Expected   string `json:"expected"`
	Got        string `json:"got"`
}

// VerifyReport is the structured result of comparing Paperless source records
// against generated vault notes and archived originals. It is designed as a
// stable, machine-readable gate for a migration: an empty set of discrepancies
// means the vault faithfully represents the Paperless archive. It never
// contains document content, only IDs, field names, and paths.
type VerifyReport struct {
	SourceDocuments int              `json:"source_documents"`
	VaultNotes      int              `json:"vault_notes"`
	Verified        int              `json:"verified"`
	Missing         []int            `json:"missing,omitempty"`         // source doc IDs with no note
	Duplicate       []int            `json:"duplicate,omitempty"`       // doc IDs with more than one note
	MissingArchive  []int            `json:"missing_archive,omitempty"` // note exists but the archived original is gone
	Mismatches      []VerifyMismatch `json:"mismatches,omitempty"`      // metadata differences
}

// Complete reports whether the vault faithfully represents the Paperless
// archive: no missing, duplicate, archive-less, or metadata-mismatched
// documents. Callers use it to decide the process exit code.
func (r *VerifyReport) Complete() bool {
	return len(r.Missing) == 0 && len(r.Duplicate) == 0 &&
		len(r.MissingArchive) == 0 && len(r.Mismatches) == 0
}

// Verify compares every Paperless source document (bounded by opts.Since/
// opts.Limit/opts.IDs when set) against the notes under vault and the archived
// originals they reference. It never downloads document content; the
// comparison uses metadata only.
func Verify(ctx context.Context, opts Options, vault string) (*VerifyReport, error) {
	client := paperless.NewClient(opts.BaseURL, opts.Token)

	docs, err := selectDocuments(client, opts)
	if err != nil {
		return nil, err
	}
	lu, err := loadLookups(client)
	if err != nil {
		return nil, fmt.Errorf("load lookup maps: %w", err)
	}

	notes, err := scanVaultNotes(vault)
	if err != nil {
		return nil, fmt.Errorf("scan vault notes: %w", err)
	}

	report := &VerifyReport{SourceDocuments: len(docs)}
	for _, list := range notes {
		report.VaultNotes += len(list)
	}

	for _, doc := range docs {
		matches := notes[doc.ID]
		if len(matches) == 0 {
			report.Missing = append(report.Missing, doc.ID)
			continue
		}
		docOK := true
		if len(matches) > 1 {
			report.Duplicate = append(report.Duplicate, doc.ID)
			docOK = false
		}
		note := matches[0]

		if note.ArchivePath == "" || !fileExists(note.ArchivePath) {
			report.MissingArchive = append(report.MissingArchive, doc.ID)
			docOK = false
		}

		if mm := compareMetadata(doc, note, lu); len(mm) > 0 {
			report.Mismatches = append(report.Mismatches, mm...)
			docOK = false
		}

		if docOK {
			report.Verified++
		}
	}

	sort.Ints(report.Missing)
	sort.Ints(report.Duplicate)
	sort.Ints(report.MissingArchive)
	sort.Slice(report.Mismatches, func(i, j int) bool {
		if report.Mismatches[i].DocumentID != report.Mismatches[j].DocumentID {
			return report.Mismatches[i].DocumentID < report.Mismatches[j].DocumentID
		}
		return report.Mismatches[i].Field < report.Mismatches[j].Field
	})
	return report, nil
}

// compareMetadata returns the metadata fields that differ between a source
// document and its note, comparing against the exact values the importer would
// have written (via resolveDocMeta).
func compareMetadata(doc paperless.Document, note *writer.Note, lu *lookups) []VerifyMismatch {
	var out []VerifyMismatch
	meta := resolveDocMeta(doc, lu)

	wantTags := append([]string(nil), meta.Tags...)
	gotTags := append([]string(nil), note.Tags...)
	sort.Strings(wantTags)
	sort.Strings(gotTags)
	if strings.Join(wantTags, ",") != strings.Join(gotTags, ",") {
		out = append(out, VerifyMismatch{doc.ID, "tags", strings.Join(wantTags, ","), strings.Join(gotTags, ",")})
	}
	if meta.Correspondent != note.Correspondent {
		out = append(out, VerifyMismatch{doc.ID, "correspondent", meta.Correspondent, note.Correspondent})
	}
	if meta.DocumentType != note.DocumentType {
		out = append(out, VerifyMismatch{doc.ID, "document_type", meta.DocumentType, note.DocumentType})
	}

	var gotStorage string
	var gotCreated time.Time
	if note.Paperless != nil {
		gotStorage = note.Paperless.StoragePath
		gotCreated = note.Paperless.Created
	}
	if meta.StoragePath != gotStorage {
		out = append(out, VerifyMismatch{doc.ID, "storage_path", meta.StoragePath, gotStorage})
	}
	if wantCreated := paperlessCreated(doc); !wantCreated.Equal(gotCreated) {
		out = append(out, VerifyMismatch{doc.ID, "created", formatDate(wantCreated), formatDate(gotCreated)})
	}
	return out
}

func formatDate(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format(time.RFC3339)
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

// scanVaultNotes indexes every Paperless-migrated note under vault by its
// Paperless document ID. Notes without Paperless frontmatter (ordinary
// one-shot ingests) are ignored, as are unparseable files.
func scanVaultNotes(vault string) (map[int][]*writer.Note, error) {
	notes := map[int][]*writer.Note{}
	if vault == "" {
		return notes, nil
	}
	if info, err := os.Stat(vault); err != nil {
		if os.IsNotExist(err) {
			return notes, nil
		}
		return nil, err
	} else if !info.IsDir() {
		return notes, nil
	}

	err := filepath.WalkDir(vault, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(strings.ToLower(d.Name()), ".md") {
			return nil
		}
		data, rerr := os.ReadFile(path)
		if rerr != nil {
			return rerr
		}
		note := parseNoteFrontmatter(data)
		if note == nil || note.Paperless == nil || note.Paperless.DocumentID == 0 {
			return nil
		}
		id := note.Paperless.DocumentID
		notes[id] = append(notes[id], note)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return notes, nil
}

// parseNoteFrontmatter extracts the leading YAML frontmatter block written by
// writer.WriteNote and unmarshals it into a Note. It returns nil for files
// without a well-formed frontmatter block.
func parseNoteFrontmatter(data []byte) *writer.Note {
	s := string(data)
	const open = "---\n"
	if !strings.HasPrefix(s, open) {
		return nil
	}
	rest := s[len(open):]
	end := strings.Index(rest, "\n---")
	if end < 0 {
		return nil
	}
	var note writer.Note
	if err := yaml.Unmarshal([]byte(rest[:end]), &note); err != nil {
		return nil
	}
	return &note
}

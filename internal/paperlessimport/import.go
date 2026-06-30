package paperlessimport

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/danieljustus/symaira-ingest/internal/ingest"
	"github.com/danieljustus/symaira-ingest/internal/paperless"
)

type Stats struct {
	Imported int
	Skipped  int
	Failed   int
	Total    int
	Warnings []string
}

type Options struct {
	BaseURL string
	Token   string
	Since   time.Time
	DryRun  bool
}

// lookups resolves Paperless tag/correspondent/document-type/storage-path
// IDs to names. Real documents reference these entities by integer ID; the
// name tables are fetched once per run.
type lookups struct {
	tags           map[int]string
	correspondents map[int]string
	documentTypes  map[int]string
	storagePaths   map[int]string
}

func loadLookups(client *paperless.Client) (*lookups, error) {
	tags, err := client.ListTags()
	if err != nil {
		return nil, fmt.Errorf("list tags: %w", err)
	}
	correspondents, err := client.ListCorrespondents()
	if err != nil {
		return nil, fmt.Errorf("list correspondents: %w", err)
	}
	documentTypes, err := client.ListDocumentTypes()
	if err != nil {
		return nil, fmt.Errorf("list document types: %w", err)
	}
	storagePaths, err := client.ListStoragePaths()
	if err != nil {
		return nil, fmt.Errorf("list storage paths: %w", err)
	}

	l := &lookups{
		tags:           make(map[int]string, len(tags)),
		correspondents: make(map[int]string, len(correspondents)),
		documentTypes:  make(map[int]string, len(documentTypes)),
		storagePaths:   make(map[int]string, len(storagePaths)),
	}
	for _, t := range tags {
		l.tags[t.ID] = t.Name
	}
	for _, c := range correspondents {
		l.correspondents[c.ID] = c.Name
	}
	for _, d := range documentTypes {
		l.documentTypes[d.ID] = d.Name
	}
	for _, sp := range storagePaths {
		l.storagePaths[sp.ID] = sp.Name
	}
	return l, nil
}

// resolveRef returns the display name for ref, preferring an already
// embedded name over a table lookup by ID. ok is false only when ref
// carries an ID that has no name anywhere (an unresolved reference).
func resolveRef(ref paperless.Ref, table map[int]string) (name string, ok bool) {
	if ref.Name != "" {
		return ref.Name, true
	}
	if ref.ID == 0 {
		return "", true
	}
	name, found := table[ref.ID]
	return name, found
}

func Run(ctx context.Context, opts Options, pipeline *ingest.Pipeline) (*Stats, error) {
	client := paperless.NewClient(opts.BaseURL, opts.Token)

	docs, err := client.ListDocuments(opts.Since)
	if err != nil {
		return nil, fmt.Errorf("list documents: %w", err)
	}

	lu, err := loadLookups(client)
	if err != nil {
		return nil, fmt.Errorf("load lookup maps: %w", err)
	}

	stats := &Stats{Total: len(docs)}

	for i, doc := range docs {
		fmt.Fprintf(os.Stderr, "[%d/%d] %s\n", i+1, stats.Total, doc.Title)

		if opts.DryRun {
			fmt.Fprintf(os.Stderr, "  would import: %s (created: %s)\n", doc.Title, doc.CreatedDate.Format("2006-01-02"))
			stats.Skipped++
			continue
		}

		warnings, err := importOne(ctx, client, doc, lu, pipeline)
		stats.Warnings = append(stats.Warnings, warnings...)
		if err != nil {
			fmt.Fprintf(os.Stderr, "  failed: %v\n", err)
			stats.Failed++
			continue
		}
		stats.Imported++
	}

	return stats, nil
}

func importOne(ctx context.Context, client *paperless.Client, doc paperless.Document, lu *lookups, pipeline *ingest.Pipeline) ([]string, error) {
	var warnings []string

	tmpFile, err := os.CreateTemp("", "symingest-import-*.tmp")
	if err != nil {
		return warnings, fmt.Errorf("create temp file: %w", err)
	}
	tmpName := tmpFile.Name()
	defer os.Remove(tmpName)

	if err := client.DownloadDocument(doc.ID, tmpFile); err != nil {
		tmpFile.Close()
		return warnings, fmt.Errorf("download document: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		return warnings, fmt.Errorf("close temp file: %w", err)
	}

	ext := doc.FileType
	if ext == "" {
		ext = ".pdf"
	}
	finalPath := tmpName + ext
	if err := os.Rename(tmpName, finalPath); err != nil {
		return warnings, fmt.Errorf("rename temp file: %w", err)
	}
	defer os.Remove(finalPath)

	tags := make([]string, 0, len(doc.Tags))
	for _, t := range doc.Tags {
		name, ok := resolveRef(t, lu.tags)
		if !ok {
			warnings = append(warnings, fmt.Sprintf("document %d (%s): unresolved tag ID %d", doc.ID, doc.Title, t.ID))
			name = fmt.Sprintf("id:%d", t.ID)
		}
		if name != "" {
			tags = append(tags, name)
		}
	}

	var correspondent, documentType string
	if doc.Correspondent != nil {
		name, ok := resolveRef(*doc.Correspondent, lu.correspondents)
		if !ok {
			warnings = append(warnings, fmt.Sprintf("document %d (%s): unresolved correspondent ID %d", doc.ID, doc.Title, doc.Correspondent.ID))
			name = fmt.Sprintf("id:%d", doc.Correspondent.ID)
		}
		correspondent = name
	}
	if doc.DocumentType != nil {
		name, ok := resolveRef(*doc.DocumentType, lu.documentTypes)
		if !ok {
			warnings = append(warnings, fmt.Sprintf("document %d (%s): unresolved document type ID %d", doc.ID, doc.Title, doc.DocumentType.ID))
			name = fmt.Sprintf("id:%d", doc.DocumentType.ID)
		}
		documentType = name
	}
	if doc.StoragePath != nil {
		if _, ok := resolveRef(*doc.StoragePath, lu.storagePaths); !ok {
			warnings = append(warnings, fmt.Sprintf("document %d (%s): unresolved storage path ID %d", doc.ID, doc.Title, doc.StoragePath.ID))
		}
	}

	preset := &ingest.IngestOptions{
		PresetCategory:      documentType,
		PresetTags:          tags,
		PresetCorrespondent: correspondent,
		PresetDocumentType:  documentType,
	}

	_, err = pipeline.Ingest(ctx, finalPath, preset)
	if err != nil {
		if errors.Is(err, ingest.ErrDuplicate) {
			log.Printf("  skipped (duplicate): %s", doc.Title)
			return warnings, nil
		}
		return warnings, fmt.Errorf("ingest: %w", err)
	}

	log.Printf("  imported: %s → vault", doc.Title)
	return warnings, nil
}

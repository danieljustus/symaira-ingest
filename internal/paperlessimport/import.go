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
}

type Options struct {
	BaseURL string
	Token   string
	Since   time.Time
	DryRun  bool
}

func Run(ctx context.Context, opts Options, pipeline *ingest.Pipeline) (*Stats, error) {
	client := paperless.NewClient(opts.BaseURL, opts.Token)

	docs, err := client.ListDocuments(opts.Since)
	if err != nil {
		return nil, fmt.Errorf("list documents: %w", err)
	}

	stats := &Stats{Total: len(docs)}

	for i, doc := range docs {
		fmt.Fprintf(os.Stderr, "[%d/%d] %s\n", i+1, stats.Total, doc.Title)

		if opts.DryRun {
			fmt.Fprintf(os.Stderr, "  would import: %s (created: %s)\n", doc.Title, doc.CreatedDate.Format("2006-01-02"))
			stats.Skipped++
			continue
		}

		if err := importOne(ctx, client, doc, pipeline); err != nil {
			fmt.Fprintf(os.Stderr, "  failed: %v\n", err)
			stats.Failed++
			continue
		}
		stats.Imported++
	}

	return stats, nil
}

func importOne(ctx context.Context, client *paperless.Client, doc paperless.Document, pipeline *ingest.Pipeline) error {
	tmpFile, err := os.CreateTemp("", "symingest-import-*.tmp")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpName := tmpFile.Name()
	defer os.Remove(tmpName)

	if err := client.DownloadDocument(doc.ID, tmpFile); err != nil {
		tmpFile.Close()
		return fmt.Errorf("download document: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		return fmt.Errorf("close temp file: %w", err)
	}

	ext := doc.FileType
	if ext == "" {
		ext = ".pdf"
	}
	finalPath := tmpName + ext
	if err := os.Rename(tmpName, finalPath); err != nil {
		return fmt.Errorf("rename temp file: %w", err)
	}
	defer os.Remove(finalPath)

	tags := make([]string, len(doc.Tags))
	for i, t := range doc.Tags {
		tags[i] = t.Name
	}

	var correspondent, documentType string
	if doc.Correspondent != nil {
		correspondent = doc.Correspondent.Name
	}
	if doc.DocumentType != nil {
		documentType = doc.DocumentType.Name
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
			return nil
		}
		return fmt.Errorf("ingest: %w", err)
	}

	log.Printf("  imported: %s → vault", doc.Title)
	return nil
}

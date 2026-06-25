package ingest

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"time"

	"github.com/danieljustus/symaira-ingest/internal/extract"
	"github.com/danieljustus/symaira-ingest/internal/store"
	"github.com/danieljustus/symaira-ingest/internal/writer"
)

// Pipeline orchestrates extraction, persistence, and Markdown output.
type Pipeline struct {
	Engine extract.Engine
	Store  *store.Store
	Writer *writer.NoteWriter
}

// ErrDuplicate is returned when a source has already been ingested.
var ErrDuplicate = fmt.Errorf("source already ingested")

// Ingest processes a single source file through the full one-shot pipeline.
func (p *Pipeline) Ingest(ctx context.Context, source string) (*Result, error) {
	info, err := os.Stat(source)
	if err != nil {
		return nil, fmt.Errorf("stat source: %w", err)
	}
	if info.IsDir() {
		return nil, fmt.Errorf("source is a directory: %s", source)
	}

	kind, err := extract.Detect(source)
	if err != nil {
		return nil, fmt.Errorf("detect source type: %w", err)
	}

	hash, err := hashFile(source)
	if err != nil {
		return nil, fmt.Errorf("hash source: %w", err)
	}

	doc, err := p.Store.CreateOrGet(ctx, source, hash, string(kind))
	if err != nil {
		return nil, fmt.Errorf("record document: %w", err)
	}
	if doc.Status == "done" && doc.VaultPath != nil {
		return nil, ErrDuplicate
	}

	var res *Result
	switch kind {
	case extract.KindText, extract.KindMarkdown:
		res, err = OneShot(ctx, source, nil)
	default:
		if p.Engine == nil {
			return nil, fmt.Errorf("no extraction engine available for %q", kind)
		}
		res, err = OneShot(ctx, source, p.Engine)
	}
	if err != nil {
		return nil, err
	}

	vaultPath, err := p.Writer.WriteNote(
		source,
		hash,
		res.Extract.MIME,
		res.Extract.Engine,
		res.Extract.Text,
		time.Now().UTC(),
	)
	if err != nil {
		return nil, fmt.Errorf("write note: %w", err)
	}

	if err := p.Store.SetVaultPath(ctx, doc.ID, vaultPath); err != nil {
		return nil, fmt.Errorf("update document: %w", err)
	}

	return &Result{
		SourcePath: source,
		Kind:       kind,
		Extract:    res.Extract,
	}, nil
}

func hashFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

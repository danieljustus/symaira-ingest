// Package ingest implements the one-shot ingestion pipeline.
package ingest

import (
	"context"
	"fmt"
	"os"

	"github.com/danieljustus/symaira-ingest/internal/extract"
)

// Result is the outcome of a one-shot ingest.
type Result struct {
	SourcePath string
	Kind       extract.Kind
	Extract    *extract.Result
	VaultPath  string
}

// OneShot extracts text from a single source file.
func OneShot(ctx context.Context, source string, engine extract.Engine) (*Result, error) {
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

	var res *extract.Result
	switch kind {
	case extract.KindText, extract.KindMarkdown:
		res, err = extract.ReadText(ctx, source)
	default:
		if engine == nil {
			return nil, fmt.Errorf("no extraction engine available for %q", kind)
		}
		res, err = engine.Extract(ctx, source, kind)
	}
	if err != nil {
		return nil, fmt.Errorf("extract text: %w", err)
	}

	return &Result{
		SourcePath: source,
		Kind:       kind,
		Extract:    res,
	}, nil
}

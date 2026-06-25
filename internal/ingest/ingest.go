// Package ingest implements the one-shot ingestion pipeline.
package ingest

import (
	"context"
	"fmt"

	"github.com/danieljustus/symaira-ingest/internal/extract"
)

// Result is the outcome of a one-shot ingest.
type Result struct {
	SourcePath string
	Kind       extract.Kind
	Extract    *extract.Result
	VaultPath  string
}

func extractText(ctx context.Context, source string, kind extract.Kind, engine extract.Engine) (*extract.Result, error) {
	var res *extract.Result
	var err error

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

	return res, nil
}

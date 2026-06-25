// Package ocr shells out to external OCR tools (tesseract, pdftoppm).
package ocr

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/danieljustus/symaira-ingest/internal/extract"
	"golang.org/x/sync/errgroup"
)

// Runner executes external OCR tools.
type Runner struct {
	Tesseract string // path to tesseract binary
	PDFToPPM  string // path to pdftoppm binary
	OCRLang   string // tesseract language, e.g. "eng" or "deu+eng"
}

// DefaultRunner returns a runner that looks up tools on PATH.
func DefaultRunner(ocrLang string) *Runner {
	return &Runner{
		Tesseract: filepath.Clean("tesseract"),
		PDFToPPM:  filepath.Clean("pdftoppm"),
		OCRLang:   ocrLang,
	}
}

// cleanToolPath sanitises a tool path and returns an error if it is unusable.
func cleanToolPath(name, path string) (string, error) {
	cleaned := filepath.Clean(path)
	if cleaned == "" || cleaned == "." {
		return "", fmt.Errorf("%s command is not configured", name)
	}
	return cleaned, nil
}

// Available returns an error if required tools are missing.
// For image-only pipelines pdftoppm is not required.
func (r *Runner) Available() error {
	path, err := cleanToolPath("tesseract", r.Tesseract)
	if err != nil {
		return err
	}
	if _, err := exec.LookPath(path); err != nil {
		return fmt.Errorf("tesseract not found on PATH: %w", err)
	}
	return nil
}

// AvailableForPDF returns an error if tools for PDF OCR are missing.
func (r *Runner) AvailableForPDF() error {
	if err := r.Available(); err != nil {
		return err
	}
	path, err := cleanToolPath("pdftoppm", r.PDFToPPM)
	if err != nil {
		return err
	}
	if _, err := exec.LookPath(path); err != nil {
		return fmt.Errorf("pdftoppm not found on PATH: %w", err)
	}
	return nil
}

// Extract implements extract.Engine.
func (r *Runner) Extract(ctx context.Context, path string, kind extract.Kind) (*extract.Result, error) {
	switch kind {
	case extract.KindPNG, extract.KindJPEG, extract.KindTIFF:
		return r.extractImage(ctx, path)
	case extract.KindPDF:
		return r.extractPDF(ctx, path)
	default:
		return nil, fmt.Errorf("ocr: unsupported source kind %q", kind)
	}
}

func (r *Runner) extractImage(ctx context.Context, path string) (*extract.Result, error) {
	if err := r.Available(); err != nil {
		return nil, err
	}
	out, err := r.runTool(ctx, r.Tesseract, "-l", r.OCRLang, path, "stdout")
	if err != nil {
		return nil, fmt.Errorf("tesseract failed: %w", err)
	}
	return &extract.Result{
		Text:   strings.TrimSpace(string(out)),
		MIME:   "image/ocr",
		Engine: "tesseract",
	}, nil
}

func (r *Runner) extractPDF(ctx context.Context, path string) (*extract.Result, error) {
	if err := r.AvailableForPDF(); err != nil {
		return nil, err
	}

	dir, err := os.MkdirTemp("", "symingest-pdf-*")
	if err != nil {
		return nil, fmt.Errorf("create temp dir: %w", err)
	}
	defer os.RemoveAll(dir)

	prefix := filepath.Join(dir, "page")
	if _, err := r.runTool(ctx, r.PDFToPPM, "-png", "-r", "150", path, prefix); err != nil {
		return nil, fmt.Errorf("pdftoppm failed: %w", err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read rendered pages: %w", err)
	}

	var pages []string
	for _, e := range entries {
		if strings.HasSuffix(strings.ToLower(e.Name()), ".png") {
			pages = append(pages, filepath.Join(dir, e.Name()))
		}
	}
	sort.Strings(pages)

	// Process pages concurrently with errgroup.
	// Results are collected in order by page index.
	g, ctx := errgroup.WithContext(ctx)
	g.SetLimit(4) // reasonable default concurrency

	var (
		mu      sync.Mutex
		results = make([]string, len(pages))
	)
	for i, p := range pages {
		i, p := i, p
		g.Go(func() error {
			out, err := r.runTool(ctx, r.Tesseract, "-l", r.OCRLang, p, "stdout")
			if err != nil {
				return fmt.Errorf("tesseract failed for page %s: %w", filepath.Base(p), err)
			}
			mu.Lock()
			results[i] = string(out)
			mu.Unlock()
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return nil, err
	}

	var sb strings.Builder
	for _, text := range results {
		sb.WriteString(text)
		sb.WriteByte('\n')
	}

	return &extract.Result{
		Text:   strings.TrimSpace(sb.String()),
		MIME:   "application/pdf",
		Engine: "pdftoppm+tesseract",
	}, nil
}

// runTool runs a command with stdout/stderr captured away from the process stdout.
// On error the captured stderr is included.
func (r *Runner) runTool(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	var out, stderr bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return nil, fmt.Errorf("%s: %s", filepath.Base(name), msg)
	}
	return out.Bytes(), nil
}

// Ensure Runner implements extract.Engine.
var _ extract.Engine = (*Runner)(nil)

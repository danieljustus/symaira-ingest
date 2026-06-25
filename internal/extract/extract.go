// Package extract detects source-file types and extracts text.
package extract

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Kind is a normalized MIME-like type for supported sources.
type Kind string

const (
	KindPDF      Kind = "application/pdf"
	KindPNG      Kind = "image/png"
	KindJPEG     Kind = "image/jpeg"
	KindTIFF     Kind = "image/tiff"
	KindText     Kind = "text/plain"
	KindMarkdown Kind = "text/markdown"
	KindUnknown  Kind = ""
)

// Result holds extracted text and metadata.
type Result struct {
	Text   string
	MIME   string
	Engine string
}

// Engine extracts text from a file.
type Engine interface {
	Extract(ctx context.Context, path string, kind Kind) (*Result, error)
}

// Detect identifies the kind of file at path using magic bytes and extension fallback.
func Detect(path string) (Kind, error) {
	f, err := os.Open(path)
	if err != nil {
		return KindUnknown, fmt.Errorf("open file: %w", err)
	}
	defer f.Close()

	buf := make([]byte, 512)
	n, err := f.Read(buf)
	if err != nil && err.Error() != "EOF" {
		return KindUnknown, fmt.Errorf("read file: %w", err)
	}
	head := buf[:n]

	switch {
	case len(head) >= 4 && bytes.Equal(head[:4], []byte("%PDF")):
		return KindPDF, nil
	case len(head) >= 8 && bytes.Equal(head[:8], []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A}):
		return KindPNG, nil
	case len(head) >= 3 && bytes.Equal(head[:3], []byte{0xFF, 0xD8, 0xFF}):
		return KindJPEG, nil
	case len(head) >= 4 && (bytes.Equal(head[:4], []byte("II*\x00")) || bytes.Equal(head[:4], []byte("MM\x00*"))):
		return KindTIFF, nil
	}

	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".txt", ".text":
		return KindText, nil
	case ".md", ".markdown":
		return KindMarkdown, nil
	case ".pdf":
		return KindPDF, nil
	case ".png":
		return KindPNG, nil
	case ".jpg", ".jpeg":
		return KindJPEG, nil
	case ".tiff", ".tif":
		return KindTIFF, nil
	}

	return KindUnknown, fmt.Errorf("unsupported file type: %s", path)
}

// ReadText reads plain text files directly.
func ReadText(ctx context.Context, path string) (*Result, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read text file: %w", err)
	}
	return &Result{Text: string(data), MIME: string(KindText), Engine: "text"}, nil
}

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
	KindWebP     Kind = "image/webp"
	KindHEIC     Kind = "image/heic"
	KindText     Kind = "text/plain"
	KindCSV      Kind = "text/csv"
	KindMarkdown Kind = "text/markdown"
	KindHTML     Kind = "text/html"
	KindRTF      Kind = "application/rtf"
	KindDOCX     Kind = "application/vnd.openxmlformats-officedocument.wordprocessingml.document"
	KindXLSX     Kind = "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"
	KindODT      Kind = "application/vnd.oasis.opendocument.text"
	KindEML      Kind = "message/rfc822"
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
	case len(head) >= 12 && bytes.Equal(head[:4], []byte("RIFF")) && bytes.Equal(head[8:12], []byte("WEBP")):
		return KindWebP, nil
	case len(head) >= 12 && bytes.Equal(head[4:8], []byte("ftyp")) && isHEIFBrand(string(head[8:12])):
		return KindHEIC, nil
	}

	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".txt", ".text":
		return KindText, nil
	case ".csv":
		return KindCSV, nil
	case ".md", ".markdown":
		return KindMarkdown, nil
	case ".html", ".htm":
		return KindHTML, nil
	case ".rtf":
		return KindRTF, nil
	case ".docx":
		return KindDOCX, nil
	case ".xlsx":
		return KindXLSX, nil
	case ".odt":
		return KindODT, nil
	case ".eml":
		return KindEML, nil
	case ".pdf":
		return KindPDF, nil
	case ".png":
		return KindPNG, nil
	case ".jpg", ".jpeg":
		return KindJPEG, nil
	case ".tiff", ".tif":
		return KindTIFF, nil
	case ".webp":
		return KindWebP, nil
	case ".heic", ".heif":
		return KindHEIC, nil
	}

	return KindUnknown, fmt.Errorf("unsupported file type: %s", path)
}

func IsExplicitlyUnsupported(kind Kind) bool {
	switch kind {
	case KindHTML, KindRTF, KindDOCX, KindXLSX, KindODT, KindEML:
		return true
	default:
		return false
	}
}

func UnsupportedFormatError(kind Kind) error {
	return fmt.Errorf("unsupported optional extraction format %q; install/configure an optional converter in a future release or exclude this file", kind)
}

func isHEIFBrand(brand string) bool {
	switch brand {
	case "heic", "heix", "hevc", "hevx", "heim", "heis", "mif1", "msf1":
		return true
	default:
		return false
	}
}

// ReadText reads plain text files directly.
func ReadText(ctx context.Context, path string) (*Result, error) {
	return ReadTextKind(ctx, path, KindText)
}

// ReadTextKind reads a text-like file directly while preserving its normalized MIME kind.
func ReadTextKind(ctx context.Context, path string, kind Kind) (*Result, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read text file: %w", err)
	}
	if kind == "" {
		kind = KindText
	}
	return &Result{Text: string(data), MIME: string(kind), Engine: "text"}, nil
}

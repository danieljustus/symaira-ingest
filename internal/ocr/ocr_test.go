package ocr

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/danieljustus/symaira-ingest/internal/extract"
)

// writeFakeBin creates an executable script that prints out and exits 0.
func writeFakeBin(t *testing.T, dir, name, script string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if runtime.GOOS == "windows" {
		path += ".bat"
		script = "@echo off\n" + script
	} else {
		script = "#!/bin/sh\n" + script
	}
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestRunner_Available_MissingTool(t *testing.T) {
	r := &Runner{Tesseract: filepath.Join(t.TempDir(), "does-not-exist")}
	if err := r.Available(); err == nil {
		t.Fatal("expected error for missing tesseract")
	}
}

func TestRunner_ExtractImage(t *testing.T) {
	dir := t.TempDir()
	tess := writeFakeBin(t, dir, "tesseract", `echo "extracted text"`)

	r := &Runner{Tesseract: tess, OCRLang: "eng"}
	img := filepath.Join(dir, "scan.png")
	if err := os.WriteFile(img, []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A}, 0o644); err != nil {
		t.Fatal(err)
	}

	res, err := r.Extract(context.Background(), img, extract.KindPNG)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if res.Text != "extracted text" {
		t.Fatalf("text = %q, want %q", res.Text, "extracted text")
	}
	if res.Engine != "tesseract" {
		t.Fatalf("engine = %q, want tesseract", res.Engine)
	}
}

func TestRunner_ExtractPDF(t *testing.T) {
	dir := t.TempDir()
	// pdftoppm renders the PDF to page-1.png in the temp dir.
	pdfppm := writeFakeBin(t, dir, "pdftoppm", `echo "page-1.png" > "$5-page-1.png"`)
	tess := writeFakeBin(t, dir, "tesseract", `echo "page text"`)

	r := &Runner{Tesseract: tess, PDFToPPM: pdfppm, OCRLang: "eng"}
	pdf := filepath.Join(dir, "doc.pdf")
	if err := os.WriteFile(pdf, []byte("%PDF-1.4"), 0o644); err != nil {
		t.Fatal(err)
	}

	res, err := r.Extract(context.Background(), pdf, extract.KindPDF)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if res.Text != "page text" {
		t.Fatalf("text = %q, want %q", res.Text, "page text")
	}
	if res.Engine != "pdftoppm+tesseract" {
		t.Fatalf("engine = %q", res.Engine)
	}
}

func TestRunner_ExtractPDF_MissingPDFToPPM(t *testing.T) {
	dir := t.TempDir()
	tess := writeFakeBin(t, dir, "tesseract", `echo "ok"`)
	r := &Runner{Tesseract: tess, PDFToPPM: filepath.Join(dir, "missing"), OCRLang: "eng"}
	pdf := filepath.Join(dir, "doc.pdf")
	if err := os.WriteFile(pdf, []byte("%PDF-1.4"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Extract(context.Background(), pdf, extract.KindPDF); err == nil {
		t.Fatal("expected error for missing pdftoppm")
	}
}

func TestRunner_CapturesStderr(t *testing.T) {
	dir := t.TempDir()
	tess := writeFakeBin(t, dir, "tesseract", `echo "broken" >&2; exit 1`)
	r := &Runner{Tesseract: tess, OCRLang: "eng"}
	img := filepath.Join(dir, "scan.png")
	if err := os.WriteFile(img, []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A}, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Extract(context.Background(), img, extract.KindPNG); err == nil {
		t.Fatal("expected error when tesseract writes to stderr")
	}
}

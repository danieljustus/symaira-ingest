package extract

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDetect(t *testing.T) {
	dir := t.TempDir()

	cases := []struct {
		name     string
		data     []byte
		ext      string
		wantKind Kind
	}{
		{"pdf", []byte("%PDF-1.4"), ".pdf", KindPDF},
		{"png", []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A, 0x00}, ".png", KindPNG},
		{"jpeg", []byte{0xFF, 0xD8, 0xFF, 0xE0}, ".jpg", KindJPEG},
		{"tiff_le", []byte("II*\x00"), ".tiff", KindTIFF},
		{"text", []byte("hello world"), ".txt", KindText},
		{"markdown", []byte("# hi"), ".md", KindMarkdown},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(dir, tc.name+tc.ext)
			if err := os.WriteFile(path, tc.data, 0o644); err != nil {
				t.Fatal(err)
			}
			got, err := Detect(path)
			if err != nil {
				t.Fatalf("Detect: %v", err)
			}
			if got != tc.wantKind {
				t.Fatalf("kind = %q, want %q", got, tc.wantKind)
			}
		})
	}
}

func TestDetect_Unknown(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "foo.bin")
	if err := os.WriteFile(path, []byte("\x00\x01\x02\x03"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Detect(path); err == nil {
		t.Fatal("expected error for unknown type")
	}
}

func TestReadText(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "note.txt")
	want := "hello world"
	if err := os.WriteFile(path, []byte(want), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := ReadText(nil, path)
	if err != nil {
		t.Fatal(err)
	}
	if res.Text != want {
		t.Fatalf("text = %q, want %q", res.Text, want)
	}
	if res.Engine != "text" {
		t.Fatalf("engine = %q, want text", res.Engine)
	}
}

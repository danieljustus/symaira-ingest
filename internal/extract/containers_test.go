package extract

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

const (
	opcRelNS     = "http://schemas.openxmlformats.org/package/2006/relationships"
	wordMLNS     = "http://schemas.openxmlformats.org/wordprocessingml/2006/main"
	smlNS        = "http://schemas.openxmlformats.org/spreadsheetml/2006/main"
	officeDocRel = "http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument"
)

// TestDetect_ZIPContentBeatsExtension: a DOCX package renamed to an unrelated
// extension is detected as KindDOCX from its content.
func TestDetect_ZIPContentBeatsExtension(t *testing.T) {
	dir := t.TempDir()
	cases := []struct {
		name  string
		ext   string
		files map[string]string
		want  Kind
	}{
		{
			"docx-renamed-txt",
			".txt",
			map[string]string{"word/document.xml": `<w:document xmlns:w="` + wordMLNS + `"/>`},
			KindDOCX,
		},
		{
			"xlsx-renamed-bin",
			".bin",
			map[string]string{"xl/workbook.xml": `<workbook xmlns="` + smlNS + `"/>`},
			KindXLSX,
		},
		{
			"pptx-renamed-jpg",
			".jpg",
			map[string]string{"ppt/presentation.xml": `<p:presentation xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main"/>`},
			KindPPTX,
		},
		{
			"epub-no-extension",
			"",
			map[string]string{"mimetype": "application/epub+zip"},
			KindEPUB,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(dir, tc.name+tc.ext)
			writeZip(t, path, tc.files)
			kind, err := Detect(path)
			if err != nil {
				t.Fatalf("Detect: %v", err)
			}
			if kind != tc.want {
				t.Fatalf("kind = %q, want %q", kind, tc.want)
			}
		})
	}
}

// TestDetect_ODFvsOOXMLDistinguished: an ODT and an OOXML package are told
// apart without consulting the extension.
func TestDetect_ODFvsOOXMLDistinguished(t *testing.T) {
	dir := t.TempDir()

	odtPath := filepath.Join(dir, "document.xlsx") // ODT content, xlsx extension
	writeZip(t, odtPath, map[string]string{"mimetype": "application/vnd.oasis.opendocument.text"})
	kind, err := Detect(odtPath)
	if err != nil {
		t.Fatalf("Detect odt: %v", err)
	}
	if kind != KindODT {
		t.Fatalf("ODT content with .xlsx extension: kind = %q, want %q", kind, KindODT)
	}

	xlsxPath := filepath.Join(dir, "book.odt") // OOXML content, odt extension
	writeZip(t, xlsxPath, map[string]string{"xl/workbook.xml": `<workbook xmlns="` + smlNS + `"/>`})
	kind, err = Detect(xlsxPath)
	if err != nil {
		t.Fatalf("Detect xlsx: %v", err)
	}
	if kind != KindXLSX {
		t.Fatalf("OOXML content with .odt extension: kind = %q, want %q", kind, KindXLSX)
	}
}

// TestDetect_OPCRelationshipResolution: the officeDocument relationship in
// _rels/.rels resolves the kind even without conventional part paths being
// consulted.
func TestDetect_OPCRelationshipResolution(t *testing.T) {
	path := filepath.Join(t.TempDir(), "package.bin")
	writeZip(t, path, map[string]string{
		"_rels/.rels": `<Relationships xmlns="` + opcRelNS + `"><Relationship Id="rId1" Type="` + officeDocRel + `" Target="word/document.xml"/></Relationships>`,
	})
	kind, err := Detect(path)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if kind != KindDOCX {
		t.Fatalf("kind = %q, want %q", kind, KindDOCX)
	}
}

// TestDetect_UnrecognizedContainerNoError: a ZIP whose format cannot be
// resolved yields unknown without an error, even with a misleading extension.
func TestDetect_UnrecognizedContainerNoError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "random.docx")
	writeZip(t, path, map[string]string{"some/random.txt": "hello"})
	kind, err := Detect(path)
	if err != nil {
		t.Fatalf("Detect: unexpected error %v", err)
	}
	if kind != KindUnknown {
		t.Fatalf("kind = %q, want KindUnknown", kind)
	}
}

// TestDetect_OLEStreamName: an OLE compound file is classified by its
// mandated stream name; with no legacy kinds in the surface it is reported
// as unknown rather than guessed, and never as an error.
func TestDetect_OLEStreamName(t *testing.T) {
	dir := t.TempDir()
	for _, stream := range []string{"WordDocument", "PowerPoint Document", "Workbook", "Book"} {
		t.Run(stream, func(t *testing.T) {
			path := filepath.Join(dir, "legacy.doc")
			writeOLEFixture(t, path, stream)
			kind, err := Detect(path)
			if err != nil {
				t.Fatalf("Detect: unexpected error %v", err)
			}
			if kind != KindUnknown {
				t.Fatalf("kind = %q, want KindUnknown (no legacy kind yet)", kind)
			}
		})
	}
}

// TestDetect_OLEWithTxtExtension: an OLE file renamed to .txt must not be
// misdetected as text; content is authoritative.
func TestDetect_OLEWithTxtExtension(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.txt")
	writeOLEFixture(t, path, "WordDocument")
	kind, err := Detect(path)
	if err != nil {
		t.Fatalf("Detect: unexpected error %v", err)
	}
	if kind != KindUnknown {
		t.Fatalf("kind = %q, want KindUnknown", kind)
	}
}

// writeOLEFixture writes a minimal OLE compound file: a 512-byte header plus
// one directory sector whose second entry carries the given stream name.
func writeOLEFixture(t *testing.T, path, streamName string) {
	t.Helper()
	head := make([]byte, 512)
	copy(head, oleSignature)
	head[30] = 9 // 512-byte sectors: 1 << (9 + 9)
	binary.LittleEndian.PutUint32(head[48:52], 0)

	dir := make([]byte, 512)
	// Entry 0: root storage.
	rootName := "Root Entry"
	copy(dir[0:], utf16LEBytes(rootName))
	binary.LittleEndian.PutUint16(dir[64:66], uint16(len(rootName)*2+2))
	dir[66] = 5 // STGTY_ROOT
	// Entry 1: the mandated stream.
	off := 128
	copy(dir[off:], utf16LEBytes(streamName))
	binary.LittleEndian.PutUint16(dir[off+64:off+66], uint16(len(streamName)*2+2))
	dir[off+66] = 2 // STGTY_STREAM

	data := append(head, dir...)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func utf16LEBytes(s string) []byte {
	out := make([]byte, 0, len(s)*2+2)
	for _, r := range s {
		out = append(out, byte(r), byte(r>>8))
	}
	return append(out, 0, 0) // NUL terminator
}

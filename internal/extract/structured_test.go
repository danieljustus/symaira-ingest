package extract

import (
	"archive/zip"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadStructuredKind_HTML(t *testing.T) {
	path := writeTempFile(t, "sample.html", `<html><head><style>.x{}</style></head><body><h1>Title</h1><p>Hello&nbsp;<strong>World</strong></p></body></html>`)
	res, err := ReadStructuredKind(context.Background(), path, KindHTML)
	if err != nil {
		t.Fatal(err)
	}
	assertContains(t, res.Text, "Title")
	assertContains(t, res.Text, "Hello World")
	if res.MIME != string(KindHTML) || res.Engine != "html" {
		t.Fatalf("metadata = %q/%q", res.MIME, res.Engine)
	}
}

func TestReadStructuredKind_RTF(t *testing.T) {
	path := writeTempFile(t, "sample.rtf", `{\rtf1\ansi{\fonttbl\f0\fswiss Helvetica;}\f0\pard Hello\tab World\par Second line}`)
	res, err := ReadStructuredKind(context.Background(), path, KindRTF)
	if err != nil {
		t.Fatal(err)
	}
	assertContains(t, res.Text, "Hello")
	assertContains(t, res.Text, "World")
	assertContains(t, res.Text, "Second line")
}

func TestReadStructuredKind_DOCX(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sample.docx")
	writeZip(t, path, map[string]string{
		"word/document.xml": `<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body><w:p><w:r><w:t>Hello DOCX</w:t></w:r></w:p><w:p><w:r><w:t>Second paragraph</w:t></w:r></w:p></w:body></w:document>`,
	})
	res, err := ReadStructuredKind(context.Background(), path, KindDOCX)
	if err != nil {
		t.Fatal(err)
	}
	assertContains(t, res.Text, "Hello DOCX")
	assertContains(t, res.Text, "Second paragraph")
}

func TestReadStructuredKind_ODT(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sample.odt")
	writeZip(t, path, map[string]string{
		"content.xml": `<office:document-content xmlns:office="urn:oasis:names:tc:opendocument:xmlns:office:1.0" xmlns:text="urn:oasis:names:tc:opendocument:xmlns:text:1.0"><office:body><office:text><text:p>Hello ODT</text:p><text:p>Second paragraph</text:p></office:text></office:body></office:document-content>`,
	})
	res, err := ReadStructuredKind(context.Background(), path, KindODT)
	if err != nil {
		t.Fatal(err)
	}
	assertContains(t, res.Text, "Hello ODT")
	assertContains(t, res.Text, "Second paragraph")
}

func TestReadStructuredKind_XLSX(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sample.xlsx")
	writeZip(t, path, map[string]string{
		"xl/sharedStrings.xml":     `<sst xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><si><t>Header</t></si><si><t>Invoice 123</t></si></sst>`,
		"xl/worksheets/sheet1.xml": `<worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><sheetData><row><c t="s"><v>0</v></c><c><v>42</v></c></row><row><c t="s"><v>1</v></c></row></sheetData></worksheet>`,
	})
	res, err := ReadStructuredKind(context.Background(), path, KindXLSX)
	if err != nil {
		t.Fatal(err)
	}
	assertContains(t, res.Text, "sheet1.xml")
	assertContains(t, res.Text, "Header 42")
	assertContains(t, res.Text, "Invoice 123")
}

func TestReadStructuredKind_EMLPlainAndHTML(t *testing.T) {
	path := writeTempFile(t, "sample.eml", strings.Join([]string{
		"Subject: Test Mail",
		"MIME-Version: 1.0",
		"Content-Type: multipart/alternative; boundary=abc",
		"",
		"--abc",
		"Content-Type: text/plain; charset=utf-8",
		"",
		"Plain body wins.",
		"--abc",
		"Content-Type: text/html; charset=utf-8",
		"",
		"<p>HTML body</p>",
		"--abc--",
	}, "\r\n"))
	res, err := ReadStructuredKind(context.Background(), path, KindEML)
	if err != nil {
		t.Fatal(err)
	}
	assertContains(t, res.Text, "Subject: Test Mail")
	assertContains(t, res.Text, "Plain body wins.")
	if strings.Contains(res.Text, "HTML body") {
		t.Fatalf("plain text part should be preferred over html fallback: %q", res.Text)
	}
}

func TestReadStructuredKind_EMLIgnoresAttachments(t *testing.T) {
	path := writeTempFile(t, "attachment.eml", strings.Join([]string{
		"Subject: Attachment Mail",
		"MIME-Version: 1.0",
		"Content-Type: multipart/mixed; boundary=mix",
		"",
		"--mix",
		"Content-Type: text/plain; charset=utf-8",
		"",
		"Visible message body.",
		"--mix",
		"Content-Type: application/octet-stream",
		"Content-Disposition: attachment; filename=secret.bin",
		"",
		"BINARY-ATTACHMENT-SHOULD-NOT-APPEAR",
		"--mix--",
	}, "\r\n"))
	res, err := ReadStructuredKind(context.Background(), path, KindEML)
	if err != nil {
		t.Fatal(err)
	}
	assertContains(t, res.Text, "Subject: Attachment Mail")
	assertContains(t, res.Text, "Visible message body.")
	if strings.Contains(res.Text, "BINARY-ATTACHMENT-SHOULD-NOT-APPEAR") {
		t.Fatalf("attachment payload leaked into extracted text: %q", res.Text)
	}
}

func writeTempFile(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func writeZip(t *testing.T, path string, files map[string]string) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	for name, content := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

func assertContains(t *testing.T, got, want string) {
	t.Helper()
	if !strings.Contains(got, want) {
		t.Fatalf("text %q does not contain %q", got, want)
	}
}

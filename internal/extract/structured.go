package extract

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/xml"
	"fmt"
	"html"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net/mail"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// ReadStructuredKind extracts text from supported text-like container formats.
func ReadStructuredKind(ctx context.Context, path string, kind Kind) (*Result, error) {
	if err := ctxErr(ctx); err != nil {
		return nil, err
	}
	switch kind {
	case KindHTML:
		text, err := readHTML(path)
		return structuredResult(text, kind, "html", err)
	case KindRTF:
		text, err := readRTF(path)
		return structuredResult(text, kind, "rtf", err)
	case KindDOCX:
		text, err := readDOCX(path)
		return structuredResult(text, kind, "docx-native", err)
	case KindXLSX:
		text, err := readXLSX(path)
		return structuredResult(text, kind, "xlsx-native", err)
	case KindODT:
		text, err := readODT(path)
		return structuredResult(text, kind, "odt-native", err)
	case KindEML:
		text, err := readEML(path)
		return structuredResult(text, kind, "eml-native", err)
	default:
		return nil, fmt.Errorf("structured extraction does not support kind %q", kind)
	}
}

func structuredResult(text string, kind Kind, engine string, err error) (*Result, error) {
	if err != nil {
		return nil, err
	}
	text = normalizeExtractedText(text)
	return &Result{Text: text, MIME: string(kind), Engine: engine}, nil
}

func ctxErr(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	return ctx.Err()
}

func readHTML(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read html: %w", err)
	}
	return htmlToText(string(data)), nil
}

var (
	scriptRe    = regexp.MustCompile(`(?is)<script[^>]*>.*?</script>`)
	styleRe     = regexp.MustCompile(`(?is)<style[^>]*>.*?</style>`)
	brRe        = regexp.MustCompile(`(?i)<br\s*/?>`)
	blockEndRe  = regexp.MustCompile(`(?i)</(p|div|li|h[1-6]|tr)>`)
	cellEndRe   = regexp.MustCompile(`(?i)</t[dh]>`)
	tagRe       = regexp.MustCompile(`(?s)<[^>]+>`)
	spaceRe     = regexp.MustCompile(`[ 	\r\f\v]+`)
	blankLineRe = regexp.MustCompile(`\n{3,}`)
)

func htmlToText(s string) string {
	s = scriptRe.ReplaceAllString(s, " ")
	s = styleRe.ReplaceAllString(s, " ")
	s = brRe.ReplaceAllString(s, "\n")
	s = blockEndRe.ReplaceAllString(s, "\n")
	s = cellEndRe.ReplaceAllString(s, "	")
	s = tagRe.ReplaceAllString(s, " ")
	return html.UnescapeString(s)
}

func readRTF(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read rtf: %w", err)
	}
	return rtfToText(string(data)), nil
}

func rtfToText(s string) string {
	var out strings.Builder
	ignoreDepth := 0
	depth := 0
	for i := 0; i < len(s); i++ {
		ch := s[i]
		switch ch {
		case '{':
			depth++
			if strings.HasPrefix(s[i+1:], "\\fonttbl") || strings.HasPrefix(s[i+1:], "\\colortbl") || strings.HasPrefix(s[i+1:], "\\stylesheet") || strings.HasPrefix(s[i+1:], "\\*") {
				ignoreDepth = depth
			}
		case '}':
			if ignoreDepth == depth {
				ignoreDepth = 0
			}
			if depth > 0 {
				depth--
			}
		case '\\':
			wordStart := i + 1
			if wordStart >= len(s) {
				continue
			}
			next := s[wordStart]
			if next == '\\' || next == '{' || next == '}' {
				if ignoreDepth == 0 {
					out.WriteByte(next)
				}
				i++
				continue
			}
			if next == '\'' && wordStart+2 < len(s) {
				if ignoreDepth == 0 {
					var b byte
					fmt.Sscanf(s[wordStart+1:wordStart+3], "%02x", &b)
					out.WriteByte(b)
				}
				i += 3
				continue
			}
			j := wordStart
			for j < len(s) && ((s[j] >= 'a' && s[j] <= 'z') || (s[j] >= 'A' && s[j] <= 'Z')) {
				j++
			}
			word := s[wordStart:j]
			if word == "par" || word == "line" || word == "page" {
				if ignoreDepth == 0 {
					out.WriteByte('\n')
				}
			} else if word == "tab" && ignoreDepth == 0 {
				out.WriteByte('\t')
			}
			for j < len(s) && (s[j] == '-' || (s[j] >= '0' && s[j] <= '9')) {
				j++
			}
			if j < len(s) && s[j] == ' ' {
				j++
			}
			i = j - 1
		default:
			if ignoreDepth == 0 && ch != '\r' && ch != '\n' {
				out.WriteByte(ch)
			}
		}
	}
	return out.String()
}

func readDOCX(path string) (string, error) {
	zr, err := zip.OpenReader(path)
	if err != nil {
		return "", fmt.Errorf("open docx: %w", err)
	}
	defer zr.Close()
	f := zipFind(&zr.Reader, "word/document.xml")
	if f == nil {
		return "", fmt.Errorf("docx missing word/document.xml")
	}
	r, err := f.Open()
	if err != nil {
		return "", err
	}
	defer r.Close()
	return wordXMLToText(r)
}

func wordXMLToText(r io.Reader) (string, error) {
	dec := xml.NewDecoder(r)
	var out strings.Builder
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", fmt.Errorf("parse word xml: %w", err)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "t":
				var text string
				if err := dec.DecodeElement(&text, &t); err != nil {
					return "", err
				}
				out.WriteString(text)
			case "tab":
				out.WriteByte('\t')
			case "br", "cr", "p":
				out.WriteByte('\n')
			}
		}
	}
	return out.String(), nil
}

func readODT(path string) (string, error) {
	zr, err := zip.OpenReader(path)
	if err != nil {
		return "", fmt.Errorf("open odt: %w", err)
	}
	defer zr.Close()
	f := zipFind(&zr.Reader, "content.xml")
	if f == nil {
		return "", fmt.Errorf("odt missing content.xml")
	}
	r, err := f.Open()
	if err != nil {
		return "", err
	}
	defer r.Close()
	return odfXMLToText(r)
}

func odfXMLToText(r io.Reader) (string, error) {
	dec := xml.NewDecoder(r)
	var out strings.Builder
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", fmt.Errorf("parse odf xml: %w", err)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "p", "h":
				if out.Len() > 0 {
					out.WriteByte('\n')
				}
			case "tab":
				out.WriteByte('\t')
			case "line-break":
				out.WriteByte('\n')
			}
		case xml.CharData:
			out.Write([]byte(t))
		}
	}
	return out.String(), nil
}

func readXLSX(path string) (string, error) {
	zr, err := zip.OpenReader(path)
	if err != nil {
		return "", fmt.Errorf("open xlsx: %w", err)
	}
	defer zr.Close()
	shared, err := readSharedStrings(&zr.Reader)
	if err != nil {
		return "", err
	}
	var names []string
	for _, f := range zr.File {
		if strings.HasPrefix(f.Name, "xl/worksheets/sheet") && strings.HasSuffix(f.Name, ".xml") {
			names = append(names, f.Name)
		}
	}
	sort.Strings(names)
	var out strings.Builder
	for _, name := range names {
		f := zipFind(&zr.Reader, name)
		if f == nil {
			continue
		}
		r, err := f.Open()
		if err != nil {
			return "", err
		}
		text, parseErr := sheetXMLToText(r, shared)
		_ = r.Close()
		if parseErr != nil {
			return "", parseErr
		}
		if strings.TrimSpace(text) != "" {
			if out.Len() > 0 {
				out.WriteString("\n\n")
			}
			out.WriteString(filepath.Base(name))
			out.WriteByte('\n')
			out.WriteString(text)
		}
	}
	return out.String(), nil
}

func readSharedStrings(zr *zip.Reader) ([]string, error) {
	f := zipFind(zr, "xl/sharedStrings.xml")
	if f == nil {
		return nil, nil
	}
	r, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer r.Close()
	dec := xml.NewDecoder(r)
	var vals []string
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("parse sharedStrings.xml: %w", err)
		}
		if start, ok := tok.(xml.StartElement); ok && start.Name.Local == "si" {
			text, err := collectXMLText(dec, start.Name.Local)
			if err != nil {
				return nil, err
			}
			vals = append(vals, text)
		}
	}
	return vals, nil
}

func sheetXMLToText(r io.Reader, shared []string) (string, error) {
	dec := xml.NewDecoder(r)
	var rows [][]string
	var current []string
	inRow := false
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", fmt.Errorf("parse sheet xml: %w", err)
		}
		start, ok := tok.(xml.StartElement)
		if !ok {
			if end, ok := tok.(xml.EndElement); ok && end.Name.Local == "row" && inRow {
				rows = append(rows, current)
				current = nil
				inRow = false
			}
			continue
		}
		switch start.Name.Local {
		case "row":
			inRow = true
			current = nil
		case "c":
			text, err := readCell(dec, start, shared)
			if err != nil {
				return "", err
			}
			if inRow {
				current = append(current, text)
			}
		}
	}
	lines := make([]string, 0, len(rows))
	for _, row := range rows {
		lines = append(lines, strings.Join(row, "\t"))
	}
	return strings.Join(lines, "\n"), nil
}

func readCell(dec *xml.Decoder, start xml.StartElement, shared []string) (string, error) {
	cellType := ""
	for _, a := range start.Attr {
		if a.Name.Local == "t" {
			cellType = a.Value
			break
		}
	}
	var val string
	for {
		tok, err := dec.Token()
		if err != nil {
			return "", err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if t.Name.Local == "v" || t.Name.Local == "t" {
				if err := dec.DecodeElement(&val, &t); err != nil {
					return "", err
				}
			}
		case xml.EndElement:
			if t.Name.Local == start.Name.Local {
				if cellType == "s" {
					idx := 0
					if _, err := fmt.Sscanf(strings.TrimSpace(val), "%d", &idx); err == nil && idx >= 0 && idx < len(shared) {
						return shared[idx], nil
					}
				}
				return val, nil
			}
		}
	}
}

func collectXMLText(dec *xml.Decoder, endLocal string) (string, error) {
	var out strings.Builder
	depth := 1
	for depth > 0 {
		tok, err := dec.Token()
		if err != nil {
			return "", err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			depth++
		case xml.EndElement:
			depth--
		case xml.CharData:
			out.Write([]byte(t))
		}
	}
	return out.String(), nil
}

func readEML(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read eml: %w", err)
	}
	msg, err := mail.ReadMessage(bytes.NewReader(data))
	if err != nil {
		return "", fmt.Errorf("parse eml: %w", err)
	}
	var parts []string
	if subj := msg.Header.Get("Subject"); subj != "" {
		parts = append(parts, "Subject: "+subj)
	}
	body, err := readMailBody(msg.Header, msg.Body)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(body) != "" {
		parts = append(parts, body)
	}
	return strings.Join(parts, "\n\n"), nil
}

func readMailBody(header mail.Header, body io.Reader) (string, error) {
	mediaType, params, _ := mime.ParseMediaType(header.Get("Content-Type"))
	if strings.HasPrefix(mediaType, "multipart/") {
		mr := multipart.NewReader(body, params["boundary"])
		var plain, htmlPart []string
		for {
			part, err := mr.NextPart()
			if err == io.EOF {
				break
			}
			if err != nil {
				return "", err
			}
			partHeader := mail.Header(part.Header)
			disposition, _, _ := mime.ParseMediaType(partHeader.Get("Content-Disposition"))
			if strings.EqualFold(disposition, "attachment") {
				continue
			}
			text, err := readMailBody(partHeader, part)
			if err != nil {
				return "", err
			}
			ct, _, _ := mime.ParseMediaType(partHeader.Get("Content-Type"))
			if strings.HasPrefix(ct, "text/html") {
				htmlPart = append(htmlPart, text)
			} else if strings.TrimSpace(text) != "" {
				plain = append(plain, text)
			}
		}
		if len(plain) > 0 {
			return strings.Join(plain, "\n\n"), nil
		}
		return strings.Join(htmlPart, "\n\n"), nil
	}
	if mediaType != "" && !strings.HasPrefix(mediaType, "text/plain") && !strings.HasPrefix(mediaType, "text/html") {
		return "", nil
	}
	decoded, err := decodeMailTransfer(body, header.Get("Content-Transfer-Encoding"))
	if err != nil {
		return "", err
	}
	if strings.HasPrefix(mediaType, "text/html") {
		return htmlToText(string(decoded)), nil
	}
	return string(decoded), nil
}

func decodeMailTransfer(r io.Reader, encoding string) ([]byte, error) {
	encoding = strings.ToLower(strings.TrimSpace(encoding))
	switch encoding {
	case "base64":
		return io.ReadAll(base64.NewDecoder(base64.StdEncoding, r))
	case "quoted-printable":
		return io.ReadAll(quotedprintable.NewReader(r))
	default:
		return io.ReadAll(r)
	}
}

func zipFind(zr *zip.Reader, name string) *zip.File {
	for _, f := range zr.File {
		if f.Name == name {
			return f
		}
	}
	return nil
}

func normalizeExtractedText(s string) string {
	s = strings.ReplaceAll(s, "\u00a0", " ")
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		line = spaceRe.ReplaceAllString(line, " ")
		lines[i] = strings.TrimSpace(line)
	}
	s = strings.Join(lines, "\n")
	s = blankLineRe.ReplaceAllString(s, "\n\n")
	return strings.TrimSpace(s)
}

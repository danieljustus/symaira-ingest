// Package writer writes Markdown sidecar files atomically.
package writer

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/danieljustus/symaira-corekit/fsutil"
	"gopkg.in/yaml.v3"
)

type Note struct {
	SourcePath    string         `yaml:"source_path"`
	ImportedFrom  string         `yaml:"imported_from,omitempty"`
	ImportRunID   string         `yaml:"import_run_id,omitempty"`
	SourceURI     string         `yaml:"source_uri,omitempty"`
	DownloadURI   string         `yaml:"download_uri,omitempty"`
	IngestedAt    time.Time      `yaml:"ingested_at"`
	SHA256        string         `yaml:"sha256"`
	MIME          string         `yaml:"mime"`
	Tags          []string       `yaml:"tags"`
	Category      string         `yaml:"category"`
	Correspondent string         `yaml:"correspondent,omitempty"`
	DocumentType  string         `yaml:"document_type,omitempty"`
	OCREngine     string         `yaml:"ocr_engine,omitempty"`
	ArchivePath   string         `yaml:"archive_path"`
	Paperless     *PaperlessMeta `yaml:"paperless,omitempty"`
}

// PaperlessMeta carries traceability metadata from a migrated Paperless-ngx
// document so a generated note can be traced back to the original record and
// migration completeness can be audited. Fields are omitted individually
// when Paperless did not provide them.
type PaperlessMeta struct {
	DocumentID       int       `yaml:"document_id"`
	Title            string    `yaml:"title,omitempty"`
	Created          time.Time `yaml:"created,omitempty"`
	Added            time.Time `yaml:"added,omitempty"`
	Modified         time.Time `yaml:"modified,omitempty"`
	StoragePath      string    `yaml:"storage_path,omitempty"`
	OriginalFileName string    `yaml:"original_file_name,omitempty"`
	ArchivedFileName string    `yaml:"archived_file_name,omitempty"`
	PageCount        int       `yaml:"page_count,omitempty"`
	URL              string    `yaml:"url,omitempty"`
}

// NoteWriter writes deduplicated Markdown sidecars into a vault.
type NoteWriter struct {
	Vault string
	mu    sync.Mutex
}

// NoteLayout optionally overrides where a note is written within the vault.
// When nil, the note lands flat in the vault root, named after the source
// file. When set, the note is placed under Subdir (a relative directory) with
// BaseName as its file name; both are sanitized before use, and filename
// collisions within the same directory are resolved deterministically.
type NoteLayout struct {
	Subdir   string // relative subdirectory of the vault (e.g. from a storage path)
	BaseName string // note file name without extension
}

// SidecarPath returns the Markdown path for a source file in the vault.
func SidecarPath(vault, source string) string {
	return filepath.Join(vault, filepath.Base(source)+".md")
}

// SanitizeStoragePath converts a Paperless storage path into a safe relative
// vault subdirectory. Path separators (`/` or `\`) become nested directories;
// each segment is stripped of characters unsafe on common filesystems, and
// traversal or empty segments (".", "..", "") are dropped, so the result can
// never escape the vault. An unusable input yields "" (a flat placement).
func SanitizeStoragePath(storagePath string) string {
	segments := strings.FieldsFunc(storagePath, func(r rune) bool {
		return r == '/' || r == '\\'
	})
	clean := make([]string, 0, len(segments))
	for _, seg := range segments {
		s := sanitizeSegment(seg)
		if s == "" || s == "." || s == ".." {
			continue
		}
		clean = append(clean, s)
	}
	return filepath.Join(clean...)
}

// sanitizeSegment strips a single path segment of characters that are unsafe
// or reserved on common filesystems, and of leading/trailing dots and spaces.
func sanitizeSegment(seg string) string {
	var b strings.Builder
	for _, r := range strings.TrimSpace(seg) {
		if r < 0x20 || strings.ContainsRune(`<>:"|?*/\`, r) {
			b.WriteRune('_')
			continue
		}
		b.WriteRune(r)
	}
	return strings.Trim(strings.TrimSpace(b.String()), ".")
}

// resolveNotePath computes the on-disk note path for a source, honoring an
// optional layout. Without a layout the note is a flat sidecar in the vault
// root. With a layout it is placed under the sanitized subdirectory using the
// sanitized base name; if that file already exists (a different document with
// the same name in the same directory) a numeric suffix is appended
// deterministically until a free name is found.
func (w *NoteWriter) resolveNotePath(sourcePath string, layout *NoteLayout) string {
	if layout == nil {
		return SidecarPath(w.Vault, sourcePath)
	}
	dir := filepath.Join(w.Vault, layout.Subdir)
	base := sanitizeSegment(layout.BaseName)
	if base == "" {
		base = strings.TrimSuffix(filepath.Base(sourcePath), filepath.Ext(sourcePath))
	}
	candidate := filepath.Join(dir, base+".md")
	for i := 2; fileExists(candidate); i++ {
		candidate = filepath.Join(dir, fmt.Sprintf("%s-%d.md", base, i))
	}
	return candidate
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// WriteNote writes a Markdown note with YAML frontmatter atomically.
// It returns the vault path and any error. A write failure must not leave
// a partially written file behind.
func (w *NoteWriter) WriteNote(sourcePath, sha256, mime, ocrEngine, text, archivePath string, ingestedAt time.Time, category string, tags []string, correspondent, documentType, importedFrom, importRunID, sourceURI, downloadURI string, paperless *PaperlessMeta, layout *NoteLayout) (string, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	vaultPath := w.resolveNotePath(sourcePath, layout)
	if err := os.MkdirAll(filepath.Dir(vaultPath), 0o700); err != nil {
		return "", fmt.Errorf("create vault directory: %w", err)
	}

	meta := Note{
		SourcePath:    sourcePath,
		ImportedFrom:  importedFrom,
		ImportRunID:   importRunID,
		SourceURI:     sourceURI,
		DownloadURI:   downloadURI,
		IngestedAt:    ingestedAt,
		SHA256:        sha256,
		MIME:          mime,
		Tags:          tags,
		Category:      category,
		Correspondent: correspondent,
		DocumentType:  documentType,
		OCREngine:     ocrEngine,
		ArchivePath:   archivePath,
		Paperless:     paperless,
	}
	if meta.Tags == nil {
		meta.Tags = []string{}
	}

	yamlBytes, err := yaml.Marshal(meta)
	if err != nil {
		return "", fmt.Errorf("marshal frontmatter: %w", err)
	}

	var sb strings.Builder
	sb.WriteString("---\n")
	sb.Write(yamlBytes)
	sb.WriteString("---\n\n")
	sb.WriteString(text)
	if !strings.HasSuffix(text, "\n") {
		sb.WriteByte('\n')
	}
	if archivePath != "" {
		sb.WriteString("\n---\n")
		sb.WriteString(fmt.Sprintf("[Archived Original](file://%s)\n", filepath.ToSlash(archivePath)))
	}

	if err := fsutil.AtomicWriteFile(vaultPath, []byte(sb.String()), 0o600); err != nil {
		return "", fmt.Errorf("write note: %w", err)
	}
	return vaultPath, nil
}

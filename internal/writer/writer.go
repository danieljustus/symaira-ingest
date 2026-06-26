// Package writer writes Markdown sidecar files atomically.
package writer

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/danieljustus/symaira-corekit/fsutil"
	"gopkg.in/yaml.v3"
)

type Note struct {
	SourcePath  string    `yaml:"source_path"`
	IngestedAt  time.Time `yaml:"ingested_at"`
	SHA256      string    `yaml:"sha256"`
	MIME        string    `yaml:"mime"`
	Tags        []string  `yaml:"tags"`
	Category    string    `yaml:"category"`
	OCREngine   string    `yaml:"ocr_engine,omitempty"`
	ArchivePath string    `yaml:"archive_path"`
}

// NoteWriter writes deduplicated Markdown sidecars into a vault.
type NoteWriter struct {
	Vault string
}

// SidecarPath returns the Markdown path for a source file in the vault.
func SidecarPath(vault, source string) string {
	return filepath.Join(vault, filepath.Base(source)+".md")
}

// WriteNote writes a Markdown note with YAML frontmatter atomically.
// It returns the vault path and any error. A write failure must not leave
// a partially written file behind.
func (w *NoteWriter) WriteNote(sourcePath, sha256, mime, ocrEngine, text, archivePath string, ingestedAt time.Time) (string, error) {
	if err := os.MkdirAll(w.Vault, 0o755); err != nil {
		return "", fmt.Errorf("create vault directory: %w", err)
	}

	meta := Note{
		SourcePath:  sourcePath,
		IngestedAt:  ingestedAt,
		SHA256:      sha256,
		MIME:        mime,
		Tags:        []string{},
		Category:    "",
		OCREngine:   ocrEngine,
		ArchivePath: archivePath,
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

	vaultPath := SidecarPath(w.Vault, sourcePath)
	if err := fsutil.AtomicWriteFile(vaultPath, []byte(sb.String()), 0o644); err != nil {
		return "", fmt.Errorf("write note: %w", err)
	}
	return vaultPath, nil
}

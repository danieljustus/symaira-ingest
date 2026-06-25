// Package frontmatter defines the YAML frontmatter written with each note.
package frontmatter

import (
	"time"
)

// Note holds the metadata block for an ingested Markdown file.
type Note struct {
	SourcePath string    `yaml:"source_path"`
	IngestedAt time.Time `yaml:"ingested_at"`
	SHA256     string    `yaml:"sha256"`
	MIME       string    `yaml:"mime"`
	OCREngine  string    `yaml:"ocr_engine,omitempty"`
}

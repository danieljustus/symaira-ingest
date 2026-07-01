package paperlessimport

import (
	"encoding/json"
	"fmt"
	"os"
)

// MigrationReport is the machine-readable artifact of an import or dry-run,
// suitable as a review surface for an operator or a later UI/bulk-review tool.
// It never contains document content or secrets — only counts, document IDs,
// generated paths, and warnings.
type MigrationReport struct {
	DryRun    bool             `json:"dry_run"`
	Total     int              `json:"total"`
	Imported  int              `json:"imported"`
	Skipped   int              `json:"skipped"`
	Failed    int              `json:"failed"`
	Documents []DocumentResult `json:"documents"`
	Warnings  []string         `json:"warnings,omitempty"`

	// These are populated from the dry-run audit when it is available.
	UnsupportedFileTypes       map[string]int `json:"unsupported_file_types,omitempty"`
	UnresolvedTagIDs           []int          `json:"unresolved_tag_ids,omitempty"`
	UnresolvedCorrespondentIDs []int          `json:"unresolved_correspondent_ids,omitempty"`
	UnresolvedDocumentTypeIDs  []int          `json:"unresolved_document_type_ids,omitempty"`
	UnresolvedStoragePathIDs   []int          `json:"unresolved_storage_path_ids,omitempty"`
}

// BuildMigrationReport assembles a MigrationReport from run statistics. Pass
// the same dryRun flag the run used so the report records whether documents
// were actually written.
func (s *Stats) BuildMigrationReport(dryRun bool) *MigrationReport {
	r := &MigrationReport{
		DryRun:    dryRun,
		Total:     s.Total,
		Imported:  s.Imported,
		Skipped:   s.Skipped,
		Failed:    s.Failed,
		Documents: s.Results,
		Warnings:  s.Warnings,
	}
	if r.Documents == nil {
		r.Documents = []DocumentResult{}
	}
	if s.Audit != nil {
		r.UnsupportedFileTypes = s.Audit.UnsupportedFileTypes
		r.UnresolvedTagIDs = s.Audit.UnresolvedTagIDs
		r.UnresolvedCorrespondentIDs = s.Audit.UnresolvedCorrespondentIDs
		r.UnresolvedDocumentTypeIDs = s.Audit.UnresolvedDocumentTypeIDs
		r.UnresolvedStoragePathIDs = s.Audit.UnresolvedStoragePathIDs
	}
	return r
}

// WriteMigrationReport writes report to path as indented JSON with a trailing
// newline.
func WriteMigrationReport(path string, report *MigrationReport) error {
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal migration report: %w", err)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o644); err != nil {
		return fmt.Errorf("write migration report: %w", err)
	}
	return nil
}

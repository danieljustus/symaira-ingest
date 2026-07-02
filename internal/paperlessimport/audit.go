package paperlessimport

import (
	"path/filepath"
	"sort"
	"strings"

	"github.com/danieljustus/symaira-ingest/internal/paperless"
)

// supportedFileExtensions mirrors the extensions extract.Detect recognizes,
// so a dry-run audit can flag documents symingest would not know how to
// process without needing to download them first.
var supportedFileExtensions = map[string]bool{
	"pdf":      true,
	"png":      true,
	"jpg":      true,
	"jpeg":     true,
	"tiff":     true,
	"tif":      true,
	"webp":     true,
	"heic":     true,
	"heif":     true,
	"txt":      true,
	"text":     true,
	"csv":      true,
	"md":       true,
	"markdown": true,
}

var supportedMIMEDefaultExtensions = map[string]string{
	"application/pdf": ".pdf",
	"image/png":       ".png",
	"image/jpeg":      ".jpg",
	"image/tiff":      ".tiff",
	"image/webp":      ".webp",
	"image/heic":      ".heic",
	"image/heif":      ".heif",
	"text/plain":      ".txt",
	"text/csv":        ".csv",
	"text/markdown":   ".md",
}

// paperlessDownloadExtension returns the extension that should be used for
// the downloaded Paperless payload. Paperless' file_type can be null on real
// installations; when an archived/OCR PDF exists, /download returns that
// archived file, otherwise it returns the original upload.
func paperlessDownloadExtension(doc paperless.Document) string {
	for _, candidate := range []string{doc.FileType, doc.ArchivedFileName, doc.OriginalFileName} {
		if ext := normalizeExtension(candidate); ext != "" {
			return ext
		}
	}
	if ext := supportedMIMEDefaultExtensions[strings.ToLower(strings.TrimSpace(doc.MimeType))]; ext != "" {
		return ext
	}
	return ""
}

func normalizeExtension(candidate string) string {
	candidate = strings.TrimSpace(candidate)
	if candidate == "" {
		return ""
	}
	if strings.HasPrefix(candidate, ".") && !strings.ContainsAny(candidate[1:], `/\\`) {
		return strings.ToLower(candidate)
	}
	if !strings.ContainsAny(candidate, `/\\`) && !strings.Contains(candidate, ".") {
		return "." + strings.ToLower(candidate)
	}
	return strings.ToLower(filepath.Ext(candidate))
}

// AuditReport summarizes a dry-run scan of Paperless documents so a
// migration readiness decision does not require reading per-document lines.
type AuditReport struct {
	TotalDocuments      int            `json:"total_documents"`
	ByMIME              map[string]int `json:"by_mime"`
	TagCounts           map[string]int `json:"tag_counts,omitempty"`
	CorrespondentCounts map[string]int `json:"correspondent_counts,omitempty"`
	DocumentTypeCounts  map[string]int `json:"document_type_counts,omitempty"`
	StoragePathCounts   map[string]int `json:"storage_path_counts,omitempty"`

	UnresolvedTagIDs           []int `json:"unresolved_tag_ids,omitempty"`
	UnresolvedCorrespondentIDs []int `json:"unresolved_correspondent_ids,omitempty"`
	UnresolvedDocumentTypeIDs  []int `json:"unresolved_document_type_ids,omitempty"`
	UnresolvedStoragePathIDs   []int `json:"unresolved_storage_path_ids,omitempty"`

	UnsupportedFileTypes map[string]int `json:"unsupported_file_types,omitempty"`
}

// buildAuditReport inspects docs (and resolves names via lu) without
// downloading or importing anything, so it is safe to run during a dry-run.
func buildAuditReport(docs []paperless.Document, lu *lookups) *AuditReport {
	r := &AuditReport{
		TotalDocuments:       len(docs),
		ByMIME:               map[string]int{},
		TagCounts:            map[string]int{},
		CorrespondentCounts:  map[string]int{},
		DocumentTypeCounts:   map[string]int{},
		StoragePathCounts:    map[string]int{},
		UnsupportedFileTypes: map[string]int{},
	}

	unresolvedTags := map[int]bool{}
	unresolvedCorrespondents := map[int]bool{}
	unresolvedDocumentTypes := map[int]bool{}
	unresolvedStoragePaths := map[int]bool{}

	for _, doc := range docs {
		mime := doc.MimeType
		if mime == "" {
			mime = "unknown"
		}
		r.ByMIME[mime]++

		for _, t := range doc.Tags {
			name, ok := resolveRef(t, lu.tags)
			if !ok {
				unresolvedTags[t.ID] = true
				continue
			}
			if name != "" {
				r.TagCounts[name]++
			}
		}
		if doc.Correspondent != nil {
			name, ok := resolveRef(*doc.Correspondent, lu.correspondents)
			if !ok {
				unresolvedCorrespondents[doc.Correspondent.ID] = true
			} else if name != "" {
				r.CorrespondentCounts[name]++
			}
		}
		if doc.DocumentType != nil {
			name, ok := resolveRef(*doc.DocumentType, lu.documentTypes)
			if !ok {
				unresolvedDocumentTypes[doc.DocumentType.ID] = true
			} else if name != "" {
				r.DocumentTypeCounts[name]++
			}
		}
		if doc.StoragePath != nil {
			name, ok := resolveRef(*doc.StoragePath, lu.storagePaths)
			if !ok {
				unresolvedStoragePaths[doc.StoragePath.ID] = true
			} else if name != "" {
				r.StoragePathCounts[name]++
			}
		}

		ext := strings.ToLower(strings.TrimPrefix(paperlessDownloadExtension(doc), "."))
		if ext == "" {
			ext = "unknown"
		}
		if !supportedFileExtensions[ext] && supportedMIMEDefaultExtensions[strings.ToLower(strings.TrimSpace(doc.MimeType))] == "" {
			r.UnsupportedFileTypes[ext]++
		}
	}

	r.UnresolvedTagIDs = sortedKeys(unresolvedTags)
	r.UnresolvedCorrespondentIDs = sortedKeys(unresolvedCorrespondents)
	r.UnresolvedDocumentTypeIDs = sortedKeys(unresolvedDocumentTypes)
	r.UnresolvedStoragePathIDs = sortedKeys(unresolvedStoragePaths)

	return r
}

func sortedKeys(set map[int]bool) []int {
	if len(set) == 0 {
		return nil
	}
	keys := make([]int, 0, len(set))
	for k := range set {
		keys = append(keys, k)
	}
	sort.Ints(keys)
	return keys
}

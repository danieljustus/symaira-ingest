package vaultreview

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/danieljustus/symaira-ingest/internal/paperlessimport"
	"gopkg.in/yaml.v3"
)

type Failure struct {
	File    string `json:"file"`
	Check   string `json:"check"`
	Message string `json:"message"`
}

type ValidationReport struct {
	Vault    string    `json:"vault"`
	Files    int       `json:"files"`
	Failures []Failure `json:"failures"`
}

func (r ValidationReport) OK() bool { return len(r.Failures) == 0 }

func SplitFrontmatter(data []byte) (frontmatter []byte, body []byte, err error) {
	if !bytes.HasPrefix(data, []byte("---\n")) {
		return nil, nil, fmt.Errorf("missing YAML frontmatter delimiter")
	}
	rest := data[len("---\n"):]
	idx := bytes.Index(rest, []byte("\n---"))
	if idx < 0 {
		return nil, nil, fmt.Errorf("unterminated YAML frontmatter")
	}
	frontmatter = rest[:idx]
	after := rest[idx+len("\n---"):]
	if bytes.HasPrefix(after, []byte("\r\n")) {
		after = after[2:]
	} else if bytes.HasPrefix(after, []byte("\n")) {
		after = after[1:]
	}
	return frontmatter, after, nil
}

func parseNote(path string) (map[string]any, []byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, err
	}
	fm, body, err := SplitFrontmatter(data)
	if err != nil {
		return nil, nil, err
	}
	var meta map[string]any
	if err := yaml.Unmarshal(fm, &meta); err != nil {
		return nil, nil, fmt.Errorf("parse frontmatter: %w", err)
	}
	if meta == nil {
		meta = map[string]any{}
	}
	return meta, body, nil
}

func ValidateVault(vault string) (*ValidationReport, error) {
	r := &ValidationReport{Vault: vault}
	seenPaperless := map[int]string{}
	err := filepath.WalkDir(vault, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			r.Failures = append(r.Failures, Failure{File: path, Check: "walk", Message: err.Error()})
			return nil
		}
		if d.IsDir() || strings.ToLower(filepath.Ext(path)) != ".md" {
			return nil
		}
		r.Files++
		meta, _, err := parseNote(path)
		if err != nil {
			r.Failures = append(r.Failures, Failure{File: path, Check: "frontmatter", Message: err.Error()})
			return nil
		}
		validateRequired(r, path, meta)
		validateArchive(r, path, meta)
		validatePaperlessID(r, path, meta, seenPaperless)
		validateSafeTypes(r, path, meta)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return r, nil
}

func validateRequired(r *ValidationReport, path string, meta map[string]any) {
	for _, key := range []string{"source_path", "ingested_at", "sha256", "mime"} {
		v, ok := meta[key]
		if !ok {
			r.Failures = append(r.Failures, Failure{File: path, Check: "required." + key, Message: "missing required frontmatter field"})
			continue
		}
		if s, ok := v.(string); ok && strings.TrimSpace(s) == "" {
			r.Failures = append(r.Failures, Failure{File: path, Check: "required." + key, Message: "required field is empty"})
		}
	}
}

func validateArchive(r *ValidationReport, path string, meta map[string]any) {
	archive, _ := meta["archive_path"].(string)
	if archive == "" {
		return
	}
	if _, err := os.Stat(archive); err != nil {
		r.Failures = append(r.Failures, Failure{File: path, Check: "archive.exists", Message: err.Error()})
		return
	}
	want, _ := meta["sha256"].(string)
	if want == "" {
		return
	}
	got, err := fileSHA256(archive)
	if err != nil {
		r.Failures = append(r.Failures, Failure{File: path, Check: "archive.hash", Message: err.Error()})
		return
	}
	if !strings.EqualFold(got, want) {
		r.Failures = append(r.Failures, Failure{File: path, Check: "archive.hash", Message: fmt.Sprintf("sha256 mismatch: frontmatter %s, archive %s", want, got)})
	}
}

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func validatePaperlessID(r *ValidationReport, path string, meta map[string]any, seen map[int]string) {
	id, ok := paperlessID(meta)
	if !ok {
		return
	}
	if prev := seen[id]; prev != "" {
		r.Failures = append(r.Failures, Failure{File: path, Check: "paperless.document_id.unique", Message: fmt.Sprintf("duplicate Paperless ID %d also in %s", id, prev)})
		return
	}
	seen[id] = path
}

func validateSafeTypes(r *ValidationReport, path string, meta map[string]any) {
	if tags, ok := meta["tags"]; ok {
		list, ok := tags.([]any)
		if !ok {
			r.Failures = append(r.Failures, Failure{File: path, Check: "tags.type", Message: "tags must be a YAML list of strings"})
		} else {
			for _, item := range list {
				if _, ok := item.(string); !ok {
					r.Failures = append(r.Failures, Failure{File: path, Check: "tags.type", Message: "tags must contain only strings"})
					break
				}
			}
		}
	}
	if p, ok := meta["paperless"]; ok {
		pm, ok := p.(map[string]any)
		if !ok {
			r.Failures = append(r.Failures, Failure{File: path, Check: "paperless.type", Message: "paperless must be a YAML mapping"})
			return
		}
		if v, ok := pm["document_id"]; ok {
			if _, ok := numericID(v); !ok {
				r.Failures = append(r.Failures, Failure{File: path, Check: "paperless.document_id.type", Message: "document_id must be numeric"})
			}
		}
	}
}

func paperlessID(meta map[string]any) (int, bool) {
	p, ok := meta["paperless"].(map[string]any)
	if !ok {
		return 0, false
	}
	return numericID(p["document_id"])
}

func numericID(v any) (int, bool) {
	switch n := v.(type) {
	case int:
		return n, true
	case int64:
		return int(n), true
	case uint64:
		return int(n), true
	case float64:
		if n == float64(int(n)) {
			return int(n), true
		}
	case string:
		id, err := strconv.Atoi(n)
		return id, err == nil
	}
	return 0, false
}

type Correction struct {
	PaperlessID   int      `yaml:"paperless_id" json:"paperless_id"`
	AddTags       []string `yaml:"add_tags" json:"add_tags"`
	RemoveTags    []string `yaml:"remove_tags" json:"remove_tags"`
	Correspondent *string  `yaml:"correspondent" json:"correspondent"`
	DocumentType  *string  `yaml:"document_type" json:"document_type"`
	StoragePath   *string  `yaml:"storage_path" json:"storage_path"`
}

type UpdateResult struct {
	File    string   `json:"file"`
	DryRun  bool     `json:"dry_run"`
	Written bool     `json:"written"`
	Changes []string `json:"changes"`
}

func ApplyCorrection(vault string, c Correction, dryRun bool) (*UpdateResult, error) {
	if c.PaperlessID <= 0 {
		return nil, fmt.Errorf("paperless_id is required")
	}
	for _, tag := range c.RemoveTags {
		if strings.EqualFold(strings.TrimSpace(tag), "inbox") {
			return nil, fmt.Errorf("refusing to remove inbox tag automatically")
		}
	}
	path, meta, body, err := findByPaperlessID(vault, c.PaperlessID)
	if err != nil {
		return nil, err
	}
	res := &UpdateResult{File: path, DryRun: dryRun}
	applyCorrectionToMeta(meta, c, res)
	if dryRun || len(res.Changes) == 0 {
		return res, nil
	}
	if err := writeNote(path, meta, body); err != nil {
		return nil, err
	}
	res.Written = true
	return res, nil
}

func ApplyCorrectionsFile(vault, path string, dryRun bool) ([]UpdateResult, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var corrections []Correction
	if err := yaml.Unmarshal(data, &corrections); err != nil {
		return nil, fmt.Errorf("parse corrections YAML: %w", err)
	}
	results := make([]UpdateResult, 0, len(corrections))
	for _, c := range corrections {
		res, err := ApplyCorrection(vault, c, dryRun)
		if err != nil {
			return results, err
		}
		results = append(results, *res)
	}
	return results, nil
}

func BulkUpdateByTag(vault, tag string, c Correction, dryRun bool) ([]UpdateResult, error) {
	if strings.EqualFold(strings.TrimSpace(tag), "") {
		return nil, fmt.Errorf("tag selector is required")
	}
	var ids []int
	err := filepath.WalkDir(vault, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || strings.ToLower(filepath.Ext(path)) != ".md" {
			return nil
		}
		meta, _, err := parseNote(path)
		if err != nil {
			return nil
		}
		if !tagsContain(tagsFromMeta(meta), tag) {
			return nil
		}
		if id, ok := paperlessID(meta); ok {
			ids = append(ids, id)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Ints(ids)
	results := make([]UpdateResult, 0, len(ids))
	for _, id := range ids {
		c.PaperlessID = id
		res, err := ApplyCorrection(vault, c, dryRun)
		if err != nil {
			return results, err
		}
		results = append(results, *res)
	}
	return results, nil
}

func findByPaperlessID(vault string, id int) (string, map[string]any, []byte, error) {
	var foundPath string
	var foundMeta map[string]any
	var foundBody []byte
	err := filepath.WalkDir(vault, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || strings.ToLower(filepath.Ext(path)) != ".md" || foundPath != "" {
			return nil
		}
		meta, body, err := parseNote(path)
		if err != nil {
			return nil
		}
		if got, ok := paperlessID(meta); ok && got == id {
			foundPath, foundMeta, foundBody = path, meta, body
		}
		return nil
	})
	if err != nil {
		return "", nil, nil, err
	}
	if foundPath == "" {
		return "", nil, nil, fmt.Errorf("paperless_id %d not found in vault", id)
	}
	return foundPath, foundMeta, foundBody, nil
}

func applyCorrectionToMeta(meta map[string]any, c Correction, res *UpdateResult) {
	tags := tagsFromMeta(meta)
	beforeTags := strings.Join(tags, ",")
	for _, tag := range c.AddTags {
		if !tagsContain(tags, tag) {
			tags = append(tags, tag)
		}
	}
	for _, tag := range c.RemoveTags {
		tags = removeTag(tags, tag)
	}
	sort.Strings(tags)
	if strings.Join(tags, ",") != beforeTags {
		meta["tags"] = stringSliceToAny(tags)
		res.Changes = append(res.Changes, "tags")
	}
	if c.Correspondent != nil && metaString(meta, "correspondent") != *c.Correspondent {
		meta["correspondent"] = *c.Correspondent
		res.Changes = append(res.Changes, "correspondent")
	}
	if c.DocumentType != nil && metaString(meta, "document_type") != *c.DocumentType {
		meta["document_type"] = *c.DocumentType
		res.Changes = append(res.Changes, "document_type")
	}
	if c.StoragePath != nil {
		p, _ := meta["paperless"].(map[string]any)
		if p == nil {
			p = map[string]any{}
			meta["paperless"] = p
		}
		if metaString(p, "storage_path") != *c.StoragePath {
			p["storage_path"] = *c.StoragePath
			res.Changes = append(res.Changes, "paperless.storage_path")
		}
	}
}

func tagsFromMeta(meta map[string]any) []string {
	var tags []string
	if raw, ok := meta["tags"].([]any); ok {
		for _, item := range raw {
			if s, ok := item.(string); ok && s != "" {
				tags = append(tags, s)
			}
		}
	}
	return tags
}

func stringSliceToAny(in []string) []any {
	out := make([]any, len(in))
	for i, v := range in {
		out[i] = v
	}
	return out
}

func tagsContain(tags []string, tag string) bool {
	for _, t := range tags {
		if strings.EqualFold(t, tag) {
			return true
		}
	}
	return false
}

func removeTag(tags []string, tag string) []string {
	out := tags[:0]
	for _, t := range tags {
		if !strings.EqualFold(t, tag) {
			out = append(out, t)
		}
	}
	return out
}

func metaString(meta map[string]any, key string) string {
	if s, ok := meta[key].(string); ok {
		return s
	}
	return ""
}

func writeNote(path string, meta map[string]any, body []byte) error {
	fm, err := yaml.Marshal(meta)
	if err != nil {
		return err
	}
	var out bytes.Buffer
	out.WriteString("---\n")
	out.Write(fm)
	out.WriteString("---\n")
	out.Write(body)
	return os.WriteFile(path, out.Bytes(), 0o600)
}

type ReviewFilters struct {
	Failed          bool
	Warnings        bool
	MissingMetadata bool
}

type ReviewDocument struct {
	ID          int      `json:"id"`
	Status      string   `json:"status"`
	MIME        string   `json:"mime,omitempty"`
	VaultPath   string   `json:"vault_path,omitempty"`
	ArchivePath string   `json:"archive_path,omitempty"`
	Error       string   `json:"error,omitempty"`
	Warnings    []string `json:"warnings,omitempty"`
}

type ReviewReport struct {
	RunID     string           `json:"run_id,omitempty"`
	Total     int              `json:"total"`
	Documents []ReviewDocument `json:"documents"`
	Warnings  []string         `json:"warnings,omitempty"`
}

func BuildReviewReport(path string, filters ReviewFilters) (*ReviewReport, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var migration paperlessimport.MigrationReport
	if err := json.Unmarshal(data, &migration); err != nil {
		return nil, fmt.Errorf("parse migration report JSON: %w", err)
	}
	report := &ReviewReport{RunID: migration.RunID, Warnings: migration.Warnings}
	for _, d := range migration.Documents {
		if !includeDocument(d, filters) {
			continue
		}
		report.Documents = append(report.Documents, ReviewDocument{ID: d.ID, Status: d.Status, MIME: d.MIME, VaultPath: d.VaultPath, ArchivePath: d.ArchivePath, Error: d.Error, Warnings: d.Warnings})
	}
	report.Total = len(report.Documents)
	return report, nil
}

func includeDocument(d paperlessimport.DocumentResult, f ReviewFilters) bool {
	if !f.Failed && !f.Warnings && !f.MissingMetadata {
		return true
	}
	if f.Failed && d.Status == "failed" {
		return true
	}
	if f.Warnings && (len(d.Warnings) > 0 || d.Error != "") {
		return true
	}
	if f.MissingMetadata && (d.MIME == "" || d.VaultPath == "" || d.ArchivePath == "") {
		return true
	}
	return false
}

func WriteReviewHTML(path string, report *ReviewReport) error {
	const tpl = `<!doctype html><meta charset="utf-8"><title>symingest migration review</title><style>body{font-family:system-ui,sans-serif;margin:2rem}table{border-collapse:collapse;width:100%}td,th{border:1px solid #ddd;padding:.4rem;text-align:left}tr.failed{background:#fee}</style><h1>Migration review {{.RunID}}</h1><p>{{.Total}} documents shown. Document body text is intentionally not included.</p><table><thead><tr><th>ID</th><th>Status</th><th>MIME</th><th>Vault path</th><th>Archive path</th><th>Error</th><th>Warnings</th></tr></thead><tbody>{{range .Documents}}<tr class="{{.Status}}"><td>{{.ID}}</td><td>{{.Status}}</td><td>{{.MIME}}</td><td>{{.VaultPath}}</td><td>{{.ArchivePath}}</td><td>{{.Error}}</td><td>{{range .Warnings}}{{.}}<br>{{end}}</td></tr>{{end}}</tbody></table>`
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return template.Must(template.New("review").Parse(tpl)).Execute(f, report)
}

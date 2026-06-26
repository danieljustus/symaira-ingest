// Package store persists document state using SQLite.
package store

import (
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"

	"github.com/danieljustus/symaira-corekit/sqlitekit"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// Document represents a row in the documents table.
type Document struct {
	ID            int64
	SourcePath    string
	SHA256        string
	MIME          string
	Status        string
	VaultPath     *string
	ArchivePath   *string
	Category      string
	Tags          []string
	Correspondent string
	DocumentType  string
}

// ClassificationRule represents a row in the classification_rules table.
type ClassificationRule struct {
	ID        int64  `json:"id"`
	Pattern   string `json:"pattern"`
	Kind      string `json:"kind"` // 'category', 'tag', 'correspondent', 'document_type'
	Value     string `json:"value"`
	CreatedAt string `json:"created_at"`
}

// Store provides document persistence.
type Store struct {
	db *sql.DB
}

// Open opens or creates the SQLite store at path.
func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create store directory: %w", err)
	}
	db, err := sqlitekit.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open store: %w", err)
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`PRAGMA busy_timeout = 5000; PRAGMA journal_mode = WAL;`); err != nil {
		db.Close()
		return nil, fmt.Errorf("configure store: %w", err)
	}
	if err := sqlitekit.Migrate(db, migrationsFS); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate store: %w", err)
	}
	return &Store{db: db}, nil
}

// Close closes the database.
func (s *Store) Close() error {
	return s.db.Close()
}

// CreateOrGet creates a document row for the given hash if it does not exist,
// returning the document and a boolean indicating if it was created (inserted).
func (s *Store) CreateOrGet(ctx context.Context, sourcePath, sha256, mime string) (*Document, bool, error) {
	existing, err := s.ByHash(ctx, sha256)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, false, err
	}
	if existing != nil {
		return existing, false, nil
	}

	res, err := s.db.ExecContext(ctx,
		`INSERT INTO documents (source_path, sha256, mime, status) VALUES (?, ?, ?, 'pending')`,
		sourcePath, sha256, mime)
	if err != nil {
		return nil, false, fmt.Errorf("insert document: %w", err)
	}
	id, _ := res.LastInsertId()
	return &Document{ID: id, SourcePath: sourcePath, SHA256: sha256, MIME: mime, Status: "pending"}, true, nil
}

// ByHash returns the document with the given sha256.
func (s *Store) ByHash(ctx context.Context, sha256 string) (*Document, error) {
	var d Document
	var vaultPath sql.NullString
	var archivePath sql.NullString
	var category sql.NullString
	var tags sql.NullString
	var correspondent sql.NullString
	var documentType sql.NullString
	err := s.db.QueryRowContext(ctx,
		`SELECT id, source_path, sha256, mime, status, vault_path, archive_path, category, tags, correspondent, document_type FROM documents WHERE sha256 = ?`,
		sha256).Scan(&d.ID, &d.SourcePath, &d.SHA256, &d.MIME, &d.Status, &vaultPath, &archivePath, &category, &tags, &correspondent, &documentType)
	if err != nil {
		return nil, err
	}
	if vaultPath.Valid {
		d.VaultPath = &vaultPath.String
	}
	if archivePath.Valid {
		d.ArchivePath = &archivePath.String
	}
	if category.Valid {
		d.Category = category.String
	}
	if tags.Valid && tags.String != "" {
		_ = json.Unmarshal([]byte(tags.String), &d.Tags)
	}
	if correspondent.Valid {
		d.Correspondent = correspondent.String
	}
	if documentType.Valid {
		d.DocumentType = documentType.String
	}
	return &d, nil
}

// SetVaultAndArchivePath marks a document as done and records its vault, archive paths, and metadata.
func (s *Store) SetVaultAndArchivePath(ctx context.Context, id int64, vaultPath, archivePath string, category string, tags []string, correspondent, documentType string) error {
	var tagsJSON []byte
	if len(tags) > 0 {
		var err error
		tagsJSON, err = json.Marshal(tags)
		if err != nil {
			return fmt.Errorf("marshal tags: %w", err)
		}
	}
	var tagsVal sql.NullString
	if len(tagsJSON) > 0 {
		tagsVal = sql.NullString{String: string(tagsJSON), Valid: true}
	}

	var catVal sql.NullString
	if category != "" {
		catVal = sql.NullString{String: category, Valid: true}
	}
	var corrVal sql.NullString
	if correspondent != "" {
		corrVal = sql.NullString{String: correspondent, Valid: true}
	}
	var dtVal sql.NullString
	if documentType != "" {
		dtVal = sql.NullString{String: documentType, Valid: true}
	}

	_, err := s.db.ExecContext(ctx,
		`UPDATE documents SET vault_path = ?, archive_path = ?, category = ?, tags = ?, correspondent = ?, document_type = ?, status = 'done', updated_at = CURRENT_TIMESTAMP WHERE id = ?`,
		vaultPath, archivePath, catVal, tagsVal, corrVal, dtVal, id)
	if err != nil {
		return fmt.Errorf("update document: %w", err)
	}
	return nil
}

// ByID returns the document with the given ID.
func (s *Store) ByID(ctx context.Context, id int64) (*Document, error) {
	var d Document
	var vaultPath sql.NullString
	var archivePath sql.NullString
	var category sql.NullString
	var tags sql.NullString
	var correspondent sql.NullString
	var documentType sql.NullString
	err := s.db.QueryRowContext(ctx,
		`SELECT id, source_path, sha256, mime, status, vault_path, archive_path, category, tags, correspondent, document_type FROM documents WHERE id = ?`,
		id).Scan(&d.ID, &d.SourcePath, &d.SHA256, &d.MIME, &d.Status, &vaultPath, &archivePath, &category, &tags, &correspondent, &documentType)
	if err != nil {
		return nil, err
	}
	if vaultPath.Valid {
		d.VaultPath = &vaultPath.String
	}
	if archivePath.Valid {
		d.ArchivePath = &archivePath.String
	}
	if category.Valid {
		d.Category = category.String
	}
	if tags.Valid && tags.String != "" {
		_ = json.Unmarshal([]byte(tags.String), &d.Tags)
	}
	if correspondent.Valid {
		d.Correspondent = correspondent.String
	}
	if documentType.Valid {
		d.DocumentType = documentType.String
	}
	return &d, nil
}

// Job represents a row in the jobs table.
type Job struct {
	ID         int64   `json:"id"`
	DocumentID int64   `json:"document_id"`
	Kind       string  `json:"kind"`
	Status     string  `json:"status"`
	Attempts   int     `json:"attempts"`
	LastError  *string `json:"last_error,omitempty"`
	CreatedAt  string  `json:"created_at"`
	UpdatedAt  string  `json:"updated_at"`

	// Joined field
	SourcePath string  `json:"source_path"`
}

// EnqueueJob enqueues a new ingest job in the queue.
func (s *Store) EnqueueJob(ctx context.Context, docID int64, kind string) (*Job, error) {
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO jobs (document_id, kind, status, attempts) VALUES (?, ?, 'pending', 0)`,
		docID, kind)
	if err != nil {
		return nil, fmt.Errorf("enqueue job: %w", err)
	}
	id, _ := res.LastInsertId()
	return &Job{
		ID:         id,
		DocumentID: docID,
		Kind:       kind,
		Status:     "pending",
		Attempts:   0,
	}, nil
}

// EnqueueSkippedJob enqueues a job records with a status of 'skipped'.
func (s *Store) EnqueueSkippedJob(ctx context.Context, docID int64, kind string, reason string) (*Job, error) {
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO jobs (document_id, kind, status, attempts, last_error) VALUES (?, ?, 'skipped', 0, ?)`,
		docID, kind, reason)
	if err != nil {
		return nil, fmt.Errorf("enqueue skipped job: %w", err)
	}
	id, _ := res.LastInsertId()
	return &Job{
		ID:         id,
		DocumentID: docID,
		Kind:       kind,
		Status:     "skipped",
		Attempts:   0,
		LastError:  &reason,
	}, nil
}

// ClaimJob atomically finds a job to claim and marks it as running.
func (s *Store) ClaimJob(ctx context.Context) (*Job, error) {
	var job Job
	var lastErr sql.NullString
	query := `
		UPDATE jobs
		SET status = 'running',
			attempts = attempts + 1,
			updated_at = CURRENT_TIMESTAMP
		WHERE id = (
			SELECT id FROM jobs
			WHERE status = 'pending'
			   OR (status = 'failed' AND attempts < 3 AND updated_at <= datetime('now', '-10 seconds'))
			ORDER BY created_at ASC
			LIMIT 1
		)
		RETURNING id, document_id, kind, status, attempts, last_error, created_at, updated_at
	`
	err := s.db.QueryRowContext(ctx, query).Scan(
		&job.ID,
		&job.DocumentID,
		&job.Kind,
		&job.Status,
		&job.Attempts,
		&lastErr,
		&job.CreatedAt,
		&job.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil // No jobs available to claim
		}
		return nil, fmt.Errorf("claim job: %w", err)
	}
	if lastErr.Valid {
		job.LastError = &lastErr.String
	}

	doc, err := s.ByID(ctx, job.DocumentID)
	if err != nil {
		return nil, fmt.Errorf("get document for claimed job: %w", err)
	}
	job.SourcePath = doc.SourcePath
	return &job, nil
}

// CompleteJob completes the job and marks the associated document as 'done'.
func (s *Store) CompleteJob(ctx context.Context, jobID int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var docID int64
	err = tx.QueryRowContext(ctx, `UPDATE jobs SET status = 'completed', updated_at = CURRENT_TIMESTAMP WHERE id = ? RETURNING document_id`, jobID).Scan(&docID)
	if err != nil {
		return fmt.Errorf("update job status: %w", err)
	}

	_, err = tx.ExecContext(ctx, `UPDATE documents SET status = 'done', updated_at = CURRENT_TIMESTAMP WHERE id = ?`, docID)
	if err != nil {
		return fmt.Errorf("update document status: %w", err)
	}

	return tx.Commit()
}

// FailJob marks the job as failed and if it has reached 3 attempts, marks the document as terminally 'failed'.
func (s *Store) FailJob(ctx context.Context, jobID int64, errStr string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var docID int64
	var attempts int
	err = tx.QueryRowContext(ctx,
		`UPDATE jobs SET status = 'failed', last_error = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ? RETURNING document_id, attempts`,
		errStr, jobID).Scan(&docID, &attempts)
	if err != nil {
		return fmt.Errorf("update job status: %w", err)
	}

	if attempts >= 3 {
		_, err = tx.ExecContext(ctx, `UPDATE documents SET status = 'failed', updated_at = CURRENT_TIMESTAMP WHERE id = ?`, docID)
		if err != nil {
			return fmt.Errorf("update document status: %w", err)
		}
	}

	return tx.Commit()
}

// ListJobs lists all jobs in the queue ordered by creation date (newest first).
func (s *Store) ListJobs(ctx context.Context) ([]*Job, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT j.id, j.document_id, j.kind, j.status, j.attempts, j.last_error, j.created_at, j.updated_at, d.source_path
		FROM jobs j
		JOIN documents d ON j.document_id = d.id
		ORDER BY j.id DESC
	`)
	if err != nil {
		return nil, fmt.Errorf("query jobs: %w", err)
	}
	defer rows.Close()

	var jobs []*Job
	for rows.Next() {
		var j Job
		var lastErr sql.NullString
		err := rows.Scan(
			&j.ID,
			&j.DocumentID,
			&j.Kind,
			&j.Status,
			&j.Attempts,
			&lastErr,
			&j.CreatedAt,
			&j.UpdatedAt,
			&j.SourcePath,
		)
		if err != nil {
			return nil, fmt.Errorf("scan job: %w", err)
		}
		if lastErr.Valid {
			j.LastError = &lastErr.String
		}
		jobs = append(jobs, &j)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return jobs, nil
}

// RetryJob resets a failed job back to pending and 0 attempts.
func (s *Store) RetryJob(ctx context.Context, jobID int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var docID int64
	err = tx.QueryRowContext(ctx,
		`UPDATE jobs SET status = 'pending', attempts = 0, last_error = NULL, updated_at = CURRENT_TIMESTAMP WHERE id = ? RETURNING document_id`,
		jobID).Scan(&docID)
	if err != nil {
		return fmt.Errorf("update job status for retry: %w", err)
	}

	_, err = tx.ExecContext(ctx, `UPDATE documents SET status = 'pending', updated_at = CURRENT_TIMESTAMP WHERE id = ?`, docID)
	if err != nil {
		return fmt.Errorf("update document status for retry: %w", err)
	}

	return tx.Commit()
}

// ResetRunningJobs resets any jobs left in 'running' state back to 'pending'.
func (s *Store) ResetRunningJobs(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `UPDATE jobs SET status = 'pending' WHERE status = 'running'`)
	if err != nil {
		return fmt.Errorf("reset running jobs: %w", err)
	}
	return nil
}

// AddRule adds a new classification rule to the store.
func (s *Store) AddRule(ctx context.Context, pattern, kind, value string) (*ClassificationRule, error) {
	if pattern == "" {
		return nil, errors.New("pattern cannot be empty")
	}
	switch kind {
	case "category", "tag", "correspondent", "document_type":
	default:
		return nil, fmt.Errorf("invalid rule kind: %q", kind)
	}
	if value == "" {
		return nil, errors.New("value cannot be empty")
	}

	res, err := s.db.ExecContext(ctx,
		`INSERT INTO classification_rules (pattern, kind, value) VALUES (?, ?, ?)`,
		pattern, kind, value)
	if err != nil {
		return nil, fmt.Errorf("insert rule: %w", err)
	}
	id, _ := res.LastInsertId()

	var createdAt string
	err = s.db.QueryRowContext(ctx, `SELECT created_at FROM classification_rules WHERE id = ?`, id).Scan(&createdAt)
	if err != nil {
		return nil, fmt.Errorf("get rule created_at: %w", err)
	}

	return &ClassificationRule{
		ID:        id,
		Pattern:   pattern,
		Kind:      kind,
		Value:     value,
		CreatedAt: createdAt,
	}, nil
}

// ListRules lists all classification rules ordered by ID.
func (s *Store) ListRules(ctx context.Context) ([]*ClassificationRule, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, pattern, kind, value, created_at
		FROM classification_rules
		ORDER BY id ASC
	`)
	if err != nil {
		return nil, fmt.Errorf("query rules: %w", err)
	}
	defer rows.Close()

	var rules []*ClassificationRule
	for rows.Next() {
		var r ClassificationRule
		err := rows.Scan(&r.ID, &r.Pattern, &r.Kind, &r.Value, &r.CreatedAt)
		if err != nil {
			return nil, fmt.Errorf("scan rule: %w", err)
		}
		rules = append(rules, &r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return rules, nil
}

// DeleteRule deletes a classification rule by ID.
func (s *Store) DeleteRule(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM classification_rules WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete rule: %w", err)
	}
	rows, _ := res.RowsAffected()
	if rows == 0 {
		return fmt.Errorf("rule not found with ID %d", id)
	}
	return nil
}


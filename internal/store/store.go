// Package store persists document state using SQLite.
package store

import (
	"context"
	"database/sql"
	"embed"
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
	ID         int64
	SourcePath string
	SHA256     string
	MIME       string
	Status     string
	VaultPath  *string
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
// or returns the existing document.
func (s *Store) CreateOrGet(ctx context.Context, sourcePath, sha256, mime string) (*Document, error) {
	existing, err := s.ByHash(ctx, sha256)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	if existing != nil {
		return existing, nil
	}

	res, err := s.db.ExecContext(ctx,
		`INSERT INTO documents (source_path, sha256, mime, status) VALUES (?, ?, ?, 'pending')`,
		sourcePath, sha256, mime)
	if err != nil {
		return nil, fmt.Errorf("insert document: %w", err)
	}
	id, _ := res.LastInsertId()
	return &Document{ID: id, SourcePath: sourcePath, SHA256: sha256, MIME: mime, Status: "pending"}, nil
}

// ByHash returns the document with the given sha256.
func (s *Store) ByHash(ctx context.Context, sha256 string) (*Document, error) {
	var d Document
	var vaultPath sql.NullString
	err := s.db.QueryRowContext(ctx,
		`SELECT id, source_path, sha256, mime, status, vault_path FROM documents WHERE sha256 = ?`,
		sha256).Scan(&d.ID, &d.SourcePath, &d.SHA256, &d.MIME, &d.Status, &vaultPath)
	if err != nil {
		return nil, err
	}
	if vaultPath.Valid {
		d.VaultPath = &vaultPath.String
	}
	return &d, nil
}

// SetVaultPath marks a document as done and records its vault path.
func (s *Store) SetVaultPath(ctx context.Context, id int64, vaultPath string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE documents SET vault_path = ?, status = 'done', updated_at = CURRENT_TIMESTAMP WHERE id = ?`,
		vaultPath, id)
	if err != nil {
		return fmt.Errorf("update document: %w", err)
	}
	return nil
}

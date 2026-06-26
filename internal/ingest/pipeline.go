package ingest

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/danieljustus/symaira-ingest/internal/extract"
	"github.com/danieljustus/symaira-ingest/internal/store"
	"github.com/danieljustus/symaira-ingest/internal/writer"
)

// Pipeline orchestrates extraction, persistence, and Markdown output.
type Pipeline struct {
	Engine     extract.Engine
	Store      *store.Store
	Writer     *writer.NoteWriter
	ArchiveDir string
}

// ErrDuplicate is returned when a source has already been ingested.
var ErrDuplicate = errors.New("source already ingested")

// DuplicateError holds details of a duplicate document that was already ingested.
type DuplicateError struct {
	SourcePath  string
	VaultPath   string
	ArchivePath string
}

func (e *DuplicateError) Error() string {
	return fmt.Sprintf("source already ingested: %s (vault: %s, archive: %s)", e.SourcePath, e.VaultPath, e.ArchivePath)
}

func (e *DuplicateError) Is(target error) bool {
	return target == ErrDuplicate
}

// Ingest processes a single source file through the full one-shot pipeline.
func (p *Pipeline) Ingest(ctx context.Context, source string) (*Result, error) {
	info, err := os.Stat(source)
	if err != nil {
		return nil, fmt.Errorf("stat source: %w", err)
	}
	if info.IsDir() {
		return nil, fmt.Errorf("source is a directory: %s", source)
	}

	kind, err := extract.Detect(source)
	if err != nil {
		return nil, fmt.Errorf("detect source type: %w", err)
	}

	hash, err := hashFile(source)
	if err != nil {
		return nil, fmt.Errorf("hash source: %w", err)
	}

	doc, created, err := p.Store.CreateOrGet(ctx, source, hash, string(kind))
	if err != nil {
		return nil, fmt.Errorf("record document: %w", err)
	}
	if !created {
		if _, err := p.Store.EnqueueSkippedJob(ctx, doc.ID, string(kind), "duplicate"); err != nil {
			return nil, fmt.Errorf("enqueue skipped job: %w", err)
		}
		var vPath, aPath string
		if doc.VaultPath != nil {
			vPath = *doc.VaultPath
		}
		if doc.ArchivePath != nil {
			aPath = *doc.ArchivePath
		}
		return nil, &DuplicateError{
			SourcePath:  source,
			VaultPath:   vPath,
			ArchivePath: aPath,
		}
	}

	// Enqueue the job
	var enqueueErr error
	_, enqueueErr = p.Store.EnqueueJob(ctx, doc.ID, string(kind))
	if enqueueErr != nil {
		return nil, fmt.Errorf("enqueue job: %w", enqueueErr)
	}

	// Claim the job immediately for synchronous processing
	claimed, err := p.Store.ClaimJob(ctx)
	if err != nil {
		return nil, fmt.Errorf("claim job: %w", err)
	}
	if claimed == nil {
		return nil, fmt.Errorf("failed to claim enqueued job immediately")
	}

	// Run processJob
	res, err := p.processJob(ctx, claimed)
	if err != nil {
		if failErr := p.Store.FailJob(ctx, claimed.ID, err.Error()); failErr != nil {
			return nil, fmt.Errorf("process job failed: %v (failed to mark job as failed: %v)", err, failErr)
		}
		return nil, err
	}

	// Complete the job
	if err := p.Store.SetVaultAndArchivePath(ctx, doc.ID, res.VaultPath, res.ArchivePath); err != nil {
		return nil, fmt.Errorf("set vault path: %w", err)
	}
	if err := p.Store.CompleteJob(ctx, claimed.ID); err != nil {
		return nil, fmt.Errorf("complete job: %w", err)
	}

	return res, nil
}

// processJob performs the text extraction and writes the resulting note.
func (p *Pipeline) processJob(ctx context.Context, job *store.Job) (*Result, error) {
	doc, err := p.Store.ByID(ctx, job.DocumentID)
	if err != nil {
		return nil, fmt.Errorf("get document: %w", err)
	}

	kind := extract.Kind(job.Kind)

	var extractRes *extract.Result
	switch kind {
	case extract.KindText, extract.KindMarkdown:
		extractRes, err = extractText(ctx, doc.SourcePath, kind, nil)
	default:
		if p.Engine == nil {
			return nil, fmt.Errorf("no extraction engine available for %q", kind)
		}
		extractRes, err = extractText(ctx, doc.SourcePath, kind, p.Engine)
	}
	if err != nil {
		return nil, err
	}

	var archivePath string
	if p.ArchiveDir != "" {
		ext := filepath.Ext(doc.SourcePath)
		archivePath = filepath.Join(p.ArchiveDir, doc.SHA256+ext)
		if err := atomicCopy(doc.SourcePath, archivePath); err != nil {
			return nil, fmt.Errorf("archive file: %w", err)
		}
	}

	vaultPath, err := p.Writer.WriteNote(
		doc.SourcePath,
		doc.SHA256,
		extractRes.MIME,
		extractRes.Engine,
		extractRes.Text,
		archivePath,
		time.Now().UTC(),
	)
	if err != nil {
		return nil, fmt.Errorf("write note: %w", err)
	}

	return &Result{
		SourcePath:  doc.SourcePath,
		Kind:        kind,
		Extract:     extractRes,
		VaultPath:   vaultPath,
		ArchivePath: archivePath,
	}, nil
}

func hashFile(path string) (string, error) {
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

func atomicCopy(src, dst string) error {
	// If it already exists, do nothing (no conflicting copies)
	if _, err := os.Stat(dst); err == nil {
		return nil
	}

	srcFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer srcFile.Close()

	dstDir := filepath.Dir(dst)
	if err := os.MkdirAll(dstDir, 0o755); err != nil {
		return err
	}

	tmpFile, err := os.CreateTemp(dstDir, "symingest-archive-tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmpFile.Name()
	defer func() {
		if tmpFile != nil {
			tmpFile.Close()
		}
		os.Remove(tmpName)
	}()

	if _, err := io.Copy(tmpFile, srcFile); err != nil {
		return err
	}

	if err := tmpFile.Sync(); err != nil {
		return err
	}

	if err := tmpFile.Close(); err != nil {
		return err
	}
	tmpFile = nil

	if err := os.Rename(tmpName, dst); err != nil {
		return err
	}

	return nil
}

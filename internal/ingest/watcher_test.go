package ingest

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/danieljustus/symaira-ingest/internal/store"
)

func TestWatcher_DebouncesAndEnqueues(t *testing.T) {
	dir := t.TempDir()
	inbox := filepath.Join(dir, "inbox")
	if err := os.MkdirAll(inbox, 0o755); err != nil {
		t.Fatal(err)
	}

	dbPath := filepath.Join(dir, "docs.db")
	s, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	w, err := NewWatcher(s, inbox)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := w.Start(ctx); err != nil {
		t.Fatalf("Start watcher: %v", err)
	}
	time.Sleep(100 * time.Millisecond)

	// 1. Write an ignored file.
	ignoredPath := filepath.Join(inbox, "test.tmp")
	if err := os.WriteFile(ignoredPath, []byte("ignored"), 0o644); err != nil {
		t.Fatal(err)
	}

	// 2. Write a real file, but write to it continuously to check debounce.
	realPath := filepath.Join(inbox, "doc.txt")
	if err := os.WriteFile(realPath, []byte("init"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Sleep 300ms, then write again. Size/modtime should change, resetting the debounce timer.
	time.Sleep(300 * time.Millisecond)
	if err := os.WriteFile(realPath, []byte("init updated"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Sleep another 300ms, then write again.
	time.Sleep(300 * time.Millisecond)
	if err := os.WriteFile(realPath, []byte("final content"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Now wait 1.5 seconds for the file to become stable and enqueue.
	time.Sleep(1500 * time.Millisecond)

	// Check if the job was enqueued in the store.
	jobs, err := s.ListJobs(ctx)
	if err != nil {
		t.Fatal(err)
	}

	// We should only have 1 job (for doc.txt). test.tmp should have been ignored.
	if len(jobs) != 1 {
		t.Fatalf("expected 1 job, got %d. Jobs: %+v", len(jobs), jobs)
	}

	if jobs[0].SourcePath != realPath {
		t.Fatalf("expected job for %s, got %s", realPath, jobs[0].SourcePath)
	}
}

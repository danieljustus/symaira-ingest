package store

import (
	"context"
	"path/filepath"
	"testing"
)

func TestCreateOrGet_Deduplicates(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	ctx := context.Background()
	d1, created1, err := s.CreateOrGet(ctx, "/tmp/a.pdf", "abc123", "application/pdf")
	if err != nil {
		t.Fatalf("CreateOrGet first: %v", err)
	}
	if !created1 {
		t.Fatal("expected first document to be created")
	}
	d2, created2, err := s.CreateOrGet(ctx, "/tmp/b.pdf", "abc123", "application/pdf")
	if err != nil {
		t.Fatalf("CreateOrGet second: %v", err)
	}
	if created2 {
		t.Fatal("expected second duplicate document not to be created")
	}
	if d1.ID != d2.ID {
		t.Fatal("expected duplicate hash to return same document")
	}
}

func TestSetVaultPath(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	ctx := context.Background()
	d, _, err := s.CreateOrGet(ctx, "/tmp/a.pdf", "abc", "application/pdf")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetVaultPath(ctx, d.ID, "/vault/a.pdf.md"); err != nil {
		t.Fatalf("SetVaultPath: %v", err)
	}
	got, err := s.ByHash(ctx, "abc")
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "done" {
		t.Fatalf("status = %q, want done", got.Status)
	}
	if got.VaultPath == nil || *got.VaultPath != "/vault/a.pdf.md" {
		t.Fatalf("vault_path mismatch")
	}
}

func TestStore_JobsQueue(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	ctx := context.Background()

	// 1. Create a document and enqueue a job.
	d, _, err := s.CreateOrGet(ctx, "/tmp/doc1.txt", "hash1", "text/plain")
	if err != nil {
		t.Fatalf("CreateOrGet: %v", err)
	}

	job, err := s.EnqueueJob(ctx, d.ID, "text")
	if err != nil {
		t.Fatalf("EnqueueJob: %v", err)
	}
	if job.Status != "pending" || job.DocumentID != d.ID || job.Attempts != 0 {
		t.Fatalf("unexpected job state: %+v", job)
	}

	// 2. Claim the job.
	claimed, err := s.ClaimJob(ctx)
	if err != nil {
		t.Fatalf("ClaimJob: %v", err)
	}
	if claimed == nil {
		t.Fatal("expected to claim a job, got nil")
	}
	if claimed.ID != job.ID || claimed.Status != "running" || claimed.Attempts != 1 {
		t.Fatalf("unexpected claimed job state: %+v", claimed)
	}

	// 3. Fail the job (non-terminally since attempts = 1 < 3).
	err = s.FailJob(ctx, claimed.ID, "first error")
	if err != nil {
		t.Fatalf("FailJob: %v", err)
	}

	// Verify document is still pending.
	doc, err := s.ByID(ctx, d.ID)
	if err != nil {
		t.Fatalf("ByID: %v", err)
	}
	if doc.Status != "pending" {
		t.Fatalf("expected document to remain pending, got %s", doc.Status)
	}

	// 4. Claim again. Since we failed it, and backoff is 10s, immediate claim should return nil!
	claimed2, err := s.ClaimJob(ctx)
	if err != nil {
		t.Fatalf("ClaimJob retry: %v", err)
	}
	if claimed2 != nil {
		t.Fatalf("expected nil claim due to 10s cooldown, got %+v", claimed2)
	}

	// 5. Retry the job manually.
	err = s.RetryJob(ctx, claimed.ID)
	if err != nil {
		t.Fatalf("RetryJob: %v", err)
	}

	// Check status has reset to pending, and attempts is 0.
	jobs, err := s.ListJobs(ctx)
	if err != nil {
		t.Fatalf("ListJobs: %v", err)
	}
	if len(jobs) != 1 || jobs[0].Status != "pending" || jobs[0].Attempts != 0 {
		t.Fatalf("unexpected job list/state: %+v", jobs[0])
	}

	// 6. Claim again (now that it is pending and attempts = 0).
	claimed3, err := s.ClaimJob(ctx)
	if err != nil {
		t.Fatalf("ClaimJob after retry: %v", err)
	}
	if claimed3 == nil || claimed3.ID != job.ID || claimed3.Attempts != 1 {
		t.Fatalf("unexpected claim after retry: %+v", claimed3)
	}

	// 7. Fail it. Attempts was 1.
	if err := s.FailJob(ctx, claimed3.ID, "second error"); err != nil {
		t.Fatal(err)
	}

	// 8. We manually update the updated_at in the db to bypass the 10s backoff for testing.
	_, err = s.db.ExecContext(ctx, "UPDATE jobs SET updated_at = datetime('now', '-20 seconds') WHERE id = ?", claimed3.ID)
	if err != nil {
		t.Fatalf("bypass backoff: %v", err)
	}

	// 9. Claim it again. Attempts becomes 2.
	claimed4, err := s.ClaimJob(ctx)
	if err != nil {
		t.Fatalf("ClaimJob (attempts=2): %v", err)
	}
	if claimed4 == nil || claimed4.Attempts != 2 {
		t.Fatalf("expected to claim job for 2nd attempt, got %+v", claimed4)
	}

	// 10. Fail it again.
	if err := s.FailJob(ctx, claimed4.ID, "third error"); err != nil {
		t.Fatal(err)
	}

	// 11. We manually update the updated_at in the db to bypass the 10s backoff for testing.
	_, err = s.db.ExecContext(ctx, "UPDATE jobs SET updated_at = datetime('now', '-20 seconds') WHERE id = ?", claimed4.ID)
	if err != nil {
		t.Fatalf("bypass backoff: %v", err)
	}

	// 12. Claim it again. Attempts becomes 3.
	claimed5, err := s.ClaimJob(ctx)
	if err != nil {
		t.Fatalf("ClaimJob (attempts=3): %v", err)
	}
	if claimed5 == nil || claimed5.Attempts != 3 {
		t.Fatalf("expected to claim job for 3rd attempt, got %+v", claimed5)
	}

	// 13. Fail it terminally (attempts = 3).
	if err := s.FailJob(ctx, claimed5.ID, "third terminal error"); err != nil {
		t.Fatal(err)
	}

	// Verify document is now terminally failed.
	doc, err = s.ByID(ctx, d.ID)
	if err != nil {
		t.Fatalf("ByID: %v", err)
	}
	if doc.Status != "failed" {
		t.Fatalf("expected document status to be failed, got %s", doc.Status)
	}

	// 11. Complete a different job.
	d2, _, err := s.CreateOrGet(ctx, "/tmp/doc2.txt", "hash2", "text/plain")
	if err != nil {
		t.Fatal(err)
	}
	job2, err := s.EnqueueJob(ctx, d2.ID, "text")
	if err != nil {
		t.Fatal(err)
	}
	claimed6, err := s.ClaimJob(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if claimed6.ID != job2.ID {
		t.Fatalf("expected job 2, got %d", claimed6.ID)
	}
	if err := s.CompleteJob(ctx, claimed6.ID); err != nil {
		t.Fatalf("CompleteJob: %v", err)
	}

	doc2, err := s.ByID(ctx, d2.ID)
	if err != nil {
		t.Fatal(err)
	}
	if doc2.Status != "done" {
		t.Fatalf("expected doc2 status to be done, got %s", doc2.Status)
	}

	// 12. List jobs and check.
	allJobs, err := s.ListJobs(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(allJobs) != 2 {
		t.Fatalf("expected 2 jobs, got %d", len(allJobs))
	}
}


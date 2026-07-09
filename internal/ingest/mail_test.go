package ingest

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/danieljustus/symaira-ingest/internal/config"
	"github.com/danieljustus/symaira-ingest/internal/store"
	"github.com/emersion/go-imap/v2"
)

type fakeIMAPClient struct {
	loginErr     error
	selectErr    error
	searchRes    []uint32
	searchErr    error
	fetchRes     []*imapMessage
	fetchErr     error
	storeSeenErr error
	moveErr      error
	closeErr     error

	loginUsername  string
	loginPassword  string
	selectedFolder string
	storedSeenSeqs []uint32
	movedSeqs      map[uint32]string
	closed         bool
}

func (f *fakeIMAPClient) Login(username, password string) error {
	f.loginUsername = username
	f.loginPassword = password
	return f.loginErr
}

func (f *fakeIMAPClient) Select(folder string) error {
	f.selectedFolder = folder
	return f.selectErr
}

func (f *fakeIMAPClient) Search(criteria *imap.SearchCriteria) ([]uint32, error) {
	return f.searchRes, f.searchErr
}

func (f *fakeIMAPClient) Fetch(seqs []uint32) ([]*imapMessage, error) {
	return f.fetchRes, f.fetchErr
}

func (f *fakeIMAPClient) StoreSeen(seq uint32) error {
	f.storedSeenSeqs = append(f.storedSeenSeqs, seq)
	return f.storeSeenErr
}

func (f *fakeIMAPClient) Move(seq uint32, dest string) error {
	if f.movedSeqs == nil {
		f.movedSeqs = make(map[uint32]string)
	}
	f.movedSeqs[seq] = dest
	return f.moveErr
}

func (f *fakeIMAPClient) Close() error {
	f.closed = true
	return f.closeErr
}

func createFakeEmail(msgID, from, filename, bodyContent string) []byte {
	var buf bytes.Buffer
	buf.WriteString("MIME-Version: 1.0\r\n")
	buf.WriteString(fmt.Sprintf("Message-ID: <%s>\r\n", msgID))
	buf.WriteString(fmt.Sprintf("From: %s\r\n", from))
	buf.WriteString("Content-Type: multipart/mixed; boundary=boundary\r\n\r\n")
	buf.WriteString("--boundary\r\n")
	buf.WriteString("Content-Type: text/plain; charset=utf-8\r\n\r\n")
	buf.WriteString("This is the email body.\r\n")
	buf.WriteString("--boundary\r\n")
	buf.WriteString(fmt.Sprintf("Content-Type: text/plain; name=\"%s\"\r\n", filename))
	buf.WriteString("Content-Disposition: attachment; filename=\"" + filename + "\"\r\n\r\n")
	buf.WriteString(bodyContent + "\r\n")
	buf.WriteString("--boundary--\r\n")
	return buf.Bytes()
}

func TestMailPoller_Success(t *testing.T) {
	dir := t.TempDir()
	s, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("Open store: %v", err)
	}
	defer s.Close()

	processingDir := filepath.Join(dir, "processing")
	failedDir := filepath.Join(dir, "failed")
	if err := os.MkdirAll(processingDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(failedDir, 0700); err != nil {
		t.Fatal(err)
	}

	accounts := []config.IMAPAccount{
		{
			Username:       "test@example.com",
			PasswordSecret: "myplaintextpw",
			Host:           "imap.example.com",
			Port:           993,
			Folder:         "INBOX",
			Action:         "mark_seen",
		},
	}

	poller, err := NewMailPoller(s, accounts, MailPollerOptions{
		Interval:      1 * time.Second,
		ProcessingDir: processingDir,
		FailedDir:     failedDir,
	})
	if err != nil {
		t.Fatalf("NewMailPoller: %v", err)
	}

	fakeClient := &fakeIMAPClient{
		searchRes: []uint32{42},
		fetchRes: []*imapMessage{
			{
				SeqNum: 42,
				Envelope: &imapEnvelope{
					MessageID: "test-msg-id-123@example.com",
				},
				Body: createFakeEmail("test-msg-id-123@example.com", "sender@example.com", "invoice.txt", "Hello World Invoiced Data"),
			},
		},
	}

	poller.dialIMAP = func(ctx context.Context, addr string, host string) (imapClient, error) {
		if addr != "imap.example.com:993" {
			t.Errorf("expected addr imap.example.com:993, got %s", addr)
		}
		if host != "imap.example.com" {
			t.Errorf("expected host imap.example.com, got %s", host)
		}
		return fakeClient, nil
	}

	ctx := context.Background()
	err = poller.pollAccount(ctx, accounts[0])
	if err != nil {
		t.Fatalf("pollAccount failed: %v", err)
	}

	// Verify client interactions
	if fakeClient.loginUsername != "test@example.com" || fakeClient.loginPassword != "myplaintextpw" {
		t.Errorf("unexpected login credentials: %s, %s", fakeClient.loginUsername, fakeClient.loginPassword)
	}
	if fakeClient.selectedFolder != "INBOX" {
		t.Errorf("expected selected folder INBOX, got %s", fakeClient.selectedFolder)
	}
	if !fakeClient.closed {
		t.Error("expected client to be closed")
	}
	if len(fakeClient.storedSeenSeqs) != 1 || fakeClient.storedSeenSeqs[0] != 42 {
		t.Errorf("expected StoreSeen to be called for seq 42, got %v", fakeClient.storedSeenSeqs)
	}

	// Verify enqueued file and store state
	has, err := s.HasMailMessage(ctx, "test-msg-id-123@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if !has {
		t.Error("expected store to track the mail message ID")
	}

	// Retrieve doc from the store using the hash of the created file
	expectedFilePath := filepath.Join(processingDir, "test-msg-id-123@example.com-invoice.txt")
	hash, err := hashFile(expectedFilePath)
	if err != nil {
		t.Fatal(err)
	}

	doc, err := s.ByHash(ctx, hash)
	if err != nil {
		t.Fatal(err)
	}
	if doc.Correspondent != "sender@example.com" {
		t.Errorf("expected correspondent sender@example.com, got %q", doc.Correspondent)
	}

	jobs, err := s.ListJobs(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 1 {
		t.Fatalf("expected 1 job, got %d", len(jobs))
	}
	if jobs[0].DocumentID != doc.ID {
		t.Error("expected job to reference document")
	}
}

func TestMailPoller_Idempotency(t *testing.T) {
	dir := t.TempDir()
	s, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	processingDir := filepath.Join(dir, "processing")
	failedDir := filepath.Join(dir, "failed")
	_ = os.MkdirAll(processingDir, 0700)
	_ = os.MkdirAll(failedDir, 0700)

	acc := config.IMAPAccount{
		Username:       "test@example.com",
		PasswordSecret: "myplaintextpw",
		Host:           "imap.example.com",
		Port:           993,
		Action:         "mark_seen",
	}

	poller, _ := NewMailPoller(s, []config.IMAPAccount{acc}, MailPollerOptions{
		ProcessingDir: processingDir,
		FailedDir:     failedDir,
	})

	fakeClient := &fakeIMAPClient{
		searchRes: []uint32{42},
		fetchRes: []*imapMessage{
			{
				SeqNum: 42,
				Envelope: &imapEnvelope{
					MessageID: "dup@example.com",
				},
				Body: createFakeEmail("dup@example.com", "sender@example.com", "invoice.txt", "Data"),
			},
		},
	}

	poller.dialIMAP = func(ctx context.Context, addr string, host string) (imapClient, error) {
		return fakeClient, nil
	}

	ctx := context.Background()
	// Poll once
	err = poller.pollAccount(ctx, acc)
	if err != nil {
		t.Fatal(err)
	}

	// Reset mock counters
	fakeClient.storedSeenSeqs = nil

	// Poll second time
	err = poller.pollAccount(ctx, acc)
	if err != nil {
		t.Fatal(err)
	}

	// Should skip enqueuing and not call StoreSeen again (idempotency check happens before StoreSeen/Move)
	if len(fakeClient.storedSeenSeqs) != 0 {
		t.Errorf("expected StoreSeen not to be called again, got %v", fakeClient.storedSeenSeqs)
	}

	// Let's verify ByHash is retrievable
	expectedFilePath := filepath.Join(processingDir, "dup@example.com-invoice.txt")
	hash, _ := hashFile(expectedFilePath)
	_, err = s.ByHash(ctx, hash)
	if err != nil {
		t.Errorf("expected 1 document in store to be queryable by hash: %v", err)
	}
}

func TestMailPoller_AuthFailure(t *testing.T) {
	dir := t.TempDir()
	s, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	acc := config.IMAPAccount{
		Username:       "test@example.com",
		PasswordSecret: "myplaintextpw",
		Host:           "imap.example.com",
		Port:           993,
	}

	poller, _ := NewMailPoller(s, []config.IMAPAccount{acc}, MailPollerOptions{})

	fakeClient := &fakeIMAPClient{
		loginErr: errors.New("invalid credentials"),
	}

	poller.dialIMAP = func(ctx context.Context, addr string, host string) (imapClient, error) {
		return fakeClient, nil
	}

	err = poller.pollAccount(context.Background(), acc)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !fakeClient.closed {
		t.Error("expected client to be closed on login failure")
	}
}

func TestMailPoller_DialFailure(t *testing.T) {
	dir := t.TempDir()
	s, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	acc := config.IMAPAccount{
		Username:       "test@example.com",
		PasswordSecret: "myplaintextpw",
		Host:           "imap.example.com",
		Port:           993,
	}

	poller, _ := NewMailPoller(s, []config.IMAPAccount{acc}, MailPollerOptions{})

	poller.dialIMAP = func(ctx context.Context, addr string, host string) (imapClient, error) {
		return nil, errors.New("network unreachable")
	}

	err = poller.pollAccount(context.Background(), acc)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestMailPoller_SearchNoMessages(t *testing.T) {
	dir := t.TempDir()
	s, _ := store.Open(filepath.Join(dir, "test.db"))
	defer s.Close()

	acc := config.IMAPAccount{
		Username:       "test@example.com",
		PasswordSecret: "myplaintextpw",
		Host:           "imap.example.com",
		Port:           993,
	}

	poller, _ := NewMailPoller(s, []config.IMAPAccount{acc}, MailPollerOptions{})

	fakeClient := &fakeIMAPClient{
		searchRes: []uint32{},
	}

	poller.dialIMAP = func(ctx context.Context, addr string, host string) (imapClient, error) {
		return fakeClient, nil
	}

	err := poller.pollAccount(context.Background(), acc)
	if err != nil {
		t.Fatal(err)
	}
}

func TestMailPoller_MoveAction(t *testing.T) {
	dir := t.TempDir()
	s, _ := store.Open(filepath.Join(dir, "test.db"))
	defer s.Close()

	processingDir := filepath.Join(dir, "processing")
	failedDir := filepath.Join(dir, "failed")
	_ = os.MkdirAll(processingDir, 0700)
	_ = os.MkdirAll(failedDir, 0700)

	acc := config.IMAPAccount{
		Username:       "test@example.com",
		PasswordSecret: "myplaintextpw",
		Host:           "imap.example.com",
		Port:           993,
		Action:         "move",
		MoveTo:         "Archive",
	}

	poller, _ := NewMailPoller(s, []config.IMAPAccount{acc}, MailPollerOptions{
		ProcessingDir: processingDir,
		FailedDir:     failedDir,
	})

	fakeClient := &fakeIMAPClient{
		searchRes: []uint32{99},
		fetchRes: []*imapMessage{
			{
				SeqNum: 99,
				Envelope: &imapEnvelope{
					MessageID: "move-test@example.com",
				},
				Body: createFakeEmail("move-test@example.com", "sender@example.com", "invoice.txt", "Data"),
			},
		},
	}

	poller.dialIMAP = func(ctx context.Context, addr string, host string) (imapClient, error) {
		return fakeClient, nil
	}

	err := poller.pollAccount(context.Background(), acc)
	if err != nil {
		t.Fatal(err)
	}

	if fakeClient.movedSeqs == nil || fakeClient.movedSeqs[99] != "Archive" {
		t.Errorf("expected message 99 to be moved to Archive, got %v", fakeClient.movedSeqs)
	}
}

func TestMailPoller_HasAttachmentFilter(t *testing.T) {
	dir := t.TempDir()
	s, _ := store.Open(filepath.Join(dir, "test.db"))
	defer s.Close()

	processingDir := filepath.Join(dir, "processing")
	failedDir := filepath.Join(dir, "failed")
	_ = os.MkdirAll(processingDir, 0700)
	_ = os.MkdirAll(failedDir, 0700)

	acc := config.IMAPAccount{
		Username:       "test@example.com",
		PasswordSecret: "myplaintextpw",
		Host:           "imap.example.com",
		Port:           993,
		HasAttachment:  true,
	}

	poller, _ := NewMailPoller(s, []config.IMAPAccount{acc}, MailPollerOptions{
		ProcessingDir: processingDir,
		FailedDir:     failedDir,
	})

	// Create an email with no parts/attachments (empty body specifier)
	var emailNoAttachment bytes.Buffer
	emailNoAttachment.WriteString("MIME-Version: 1.0\r\n")
	emailNoAttachment.WriteString("Message-ID: <no-att@example.com>\r\n")
	emailNoAttachment.WriteString("From: sender@example.com\r\n")
	emailNoAttachment.WriteString("Content-Type: text/plain; charset=utf-8\r\n\r\n")
	emailNoAttachment.WriteString("This is a plain body with no attachments.\r\n")

	fakeClient := &fakeIMAPClient{
		searchRes: []uint32{77},
		fetchRes: []*imapMessage{
			{
				SeqNum: 77,
				Envelope: &imapEnvelope{
					MessageID: "no-att@example.com",
				},
				Body: emailNoAttachment.Bytes(),
			},
		},
	}

	poller.dialIMAP = func(ctx context.Context, addr string, host string) (imapClient, error) {
		return fakeClient, nil
	}

	ctx := context.Background()
	err := poller.pollAccount(ctx, acc)
	if err != nil {
		t.Fatal(err)
	}

	// Verify no documents are created
	// Since s.ByHash is the only check, let's verify nothing was saved in processingDir
	files, _ := os.ReadDir(processingDir)
	if len(files) != 0 {
		t.Errorf("expected 0 files, got %d", len(files))
	}
}

func TestMailPoller_StartStop(t *testing.T) {
	dir := t.TempDir()
	s, _ := store.Open(filepath.Join(dir, "test.db"))
	defer s.Close()

	acc := config.IMAPAccount{
		Username:       "test@example.com",
		PasswordSecret: "myplaintextpw",
		Host:           "imap.example.com",
		Port:           993,
	}

	poller, _ := NewMailPoller(s, []config.IMAPAccount{acc}, MailPollerOptions{
		Interval: 50 * time.Millisecond,
	})

	fakeClient := &fakeIMAPClient{}
	poller.dialIMAP = func(ctx context.Context, addr string, host string) (imapClient, error) {
		return fakeClient, nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := poller.Start(ctx); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	if err := poller.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}
}

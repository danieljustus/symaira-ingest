package ingest

import (
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/danieljustus/symaira-ingest/internal/config"
	"github.com/danieljustus/symaira-ingest/internal/extract"
	"github.com/danieljustus/symaira-ingest/internal/secret"
	"github.com/danieljustus/symaira-ingest/internal/store"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
	"github.com/emersion/go-message/mail"
)

// MailPoller periodically connects to IMAP accounts to fetch and ingest attachments.
type MailPoller struct {
	store         *store.Store
	accounts      []config.IMAPAccount
	interval      time.Duration
	processingDir string
	failedDir     string
	wg            sync.WaitGroup
	cancel        context.CancelFunc
}

type MailPollerOptions struct {
	Interval      time.Duration
	ProcessingDir string
	FailedDir     string
}

// NewMailPoller creates a new MailPoller.
func NewMailPoller(s *store.Store, accounts []config.IMAPAccount, opts MailPollerOptions) (*MailPoller, error) {
	if opts.Interval <= 0 {
		opts.Interval = 5 * time.Minute
	}
	processingDir, err := cleanOptionalDir(opts.ProcessingDir)
	if err != nil {
		return nil, fmt.Errorf("resolve processing dir: %w", err)
	}
	failedDir, err := cleanOptionalDir(opts.FailedDir)
	if err != nil {
		return nil, fmt.Errorf("resolve failed dir: %w", err)
	}
	return &MailPoller{
		store:         s,
		accounts:      accounts,
		interval:      opts.Interval,
		processingDir: processingDir,
		failedDir:     failedDir,
	}, nil
}

// Start begins the polling loop.
func (m *MailPoller) Start(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	m.cancel = cancel

	for i, acc := range m.accounts {
		m.wg.Add(1)
		go func(account config.IMAPAccount, index int) {
			defer m.wg.Done()
			m.pollLoop(ctx, account, index)
		}(acc, i)
	}
	return nil
}

// Close stops the polling loops.
func (m *MailPoller) Close() error {
	if m.cancel != nil {
		m.cancel()
	}
	m.wg.Wait()
	return nil
}

func (m *MailPoller) pollLoop(ctx context.Context, acc config.IMAPAccount, index int) {
	ticker := time.NewTicker(m.interval)
	defer ticker.Stop()

	// Initial poll
	if err := m.pollAccount(ctx, acc); err != nil {
		log.Printf("[MailPoller] Account %d (%s) initial poll error: %v", index, acc.Username, err)
	}

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := m.pollAccount(ctx, acc); err != nil {
				log.Printf("[MailPoller] Account %d (%s) poll error: %v", index, acc.Username, err)
			}
		}
	}
}

func (m *MailPoller) pollAccount(ctx context.Context, acc config.IMAPAccount) error {
	pwd, err := secret.Resolve(ctx, acc.PasswordSecret)
	if err != nil {
		return fmt.Errorf("resolve password failed")
	}

	addr := fmt.Sprintf("%s:%d", acc.Host, acc.Port)
	client, err := imapclient.DialTLS(addr, &imapclient.Options{TLSConfig: &tls.Config{ServerName: acc.Host}})
	if err != nil {
		return fmt.Errorf("dial tls: %w", err)
	}
	defer client.Logout()

	if err := client.Login(acc.Username, pwd).Wait(); err != nil {
		return fmt.Errorf("login failed")
	}

	folder := acc.Folder
	if folder == "" {
		folder = "INBOX"
	}
	if _, err := client.Select(folder, nil).Wait(); err != nil {
		return fmt.Errorf("select folder %q: %w", folder, err)
	}

	searchCriteria := &imap.SearchCriteria{}
	if acc.Action == "mark_seen" {
		searchCriteria.NotFlag = []imap.Flag{imap.FlagSeen}
	}

	// Add arbitrary limits, e.g. recent first, but imap search doesn't sort by default.
	// For now we just get the sequence set of matching messages.
	searchData, err := client.Search(searchCriteria, nil).Wait()
	if err != nil {
		return fmt.Errorf("search: %w", err)
	}

	seqs := searchData.AllSeqNums()
	if len(seqs) == 0 {
		return nil // No new messages
	}

	seqSet := imap.SeqSetNum(seqs...)
	fetchOptions := &imap.FetchOptions{
		Envelope: true,
		BodySection: []*imap.FetchItemBodySection{
			{Peek: true}, // Peek to avoid automatically marking as seen
		},
	}

	fetchCmd := client.Fetch(seqSet, fetchOptions)
	defer fetchCmd.Close()

	for {
		msg := fetchCmd.Next()
		if msg == nil {
			break
		}

		msgBuf, err := msg.Collect()
		if err != nil {
			log.Printf("[MailPoller] Failed to collect message: %v", err)
			continue
		}

		if err := m.processMessage(ctx, acc, client, msgBuf); err != nil {
			log.Printf("[MailPoller] Failed to process message %v: %v", msgBuf.SeqNum, err)
		}
	}

	if err := fetchCmd.Close(); err != nil {
		return fmt.Errorf("fetch close: %w", err)
	}

	return nil
}

func (m *MailPoller) processMessage(ctx context.Context, acc config.IMAPAccount, client *imapclient.Client, msgBuf *imapclient.FetchMessageBuffer) error {
	envelope := msgBuf.Envelope
	if envelope == nil {
		return nil
	}

	msgID := envelope.MessageID
	if msgID == "" {
		msgID = fmt.Sprintf("fallback-seq-%d", msgBuf.SeqNum)
	}

	// Idempotency check
	has, err := m.store.HasMailMessage(ctx, msgID)
	if err != nil {
		return fmt.Errorf("check idempotency: %w", err)
	}
	if has {
		return nil // already processed
	}

	var body []byte
	for _, sec := range msgBuf.BodySection {
		if sec.Section.Specifier == imap.PartSpecifierNone {
			body = sec.Bytes
			break
		}
	}

	if len(body) == 0 {
		return fmt.Errorf("no body section found")
	}

	mr, err := mail.CreateReader(bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create mail reader: %w", err)
	}

	correspondent := ""
	if from := mr.Header.Get("From"); from != "" {
		correspondent = from
	}

	var attachments []string

	for {
		p, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			log.Printf("[MailPoller] Error reading part: %v", err)
			continue
		}

		switch h := p.Header.(type) {
		case *mail.AttachmentHeader:
			filename, _ := h.Filename()
			if filename == "" {
				filename = "attachment.bin"
			}

			// Save attachment
			outPath := filepath.Join(m.processingDir, fmt.Sprintf("%s-%s", strings.ReplaceAll(msgID, "/", "_"), filename))
			if err := m.saveStream(outPath, p.Body); err != nil {
				log.Printf("[MailPoller] Failed to save attachment: %v", err)
				continue
			}
			attachments = append(attachments, outPath)
		}
	}

	if len(attachments) == 0 && acc.HasAttachment {
		// skip processing if no attachments and config requires it
	} else {
		for _, attPath := range attachments {
			if err := m.enqueueFile(ctx, attPath, msgID, correspondent); err != nil {
				log.Printf("[MailPoller] Failed to enqueue attachment %s: %v", attPath, err)
			}
		}
	}

	// Apply IMAP Action
	seqSet := imap.SeqSetNum(msgBuf.SeqNum)
	if acc.Action == "mark_seen" {
		flags := imap.StoreFlags{
			Op:    imap.StoreFlagsAdd,
			Flags: []imap.Flag{imap.FlagSeen},
		}
		if err := client.Store(seqSet, &flags, nil).Close(); err != nil {
			return fmt.Errorf("mark seen: %w", err)
		}
	} else if acc.Action == "move" && acc.MoveTo != "" {
		if _, err := client.Move(seqSet, acc.MoveTo).Wait(); err != nil {
			return fmt.Errorf("move to %s: %w", acc.MoveTo, err)
		}
	}

	// Record idempotency
	if err := m.store.TrackMailMessage(ctx, msgID, acc.Username); err != nil {
		return fmt.Errorf("track mail message: %w", err)
	}

	return nil
}

func (m *MailPoller) saveStream(path string, r io.Reader) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(f, r)
	return err
}

func (m *MailPoller) enqueueFile(ctx context.Context, workPath, msgID, correspondent string) error {
	kind, err := extract.Detect(workPath)
	if err != nil {
		return fmt.Errorf("detect file kind: %w", err)
	}

	hash, err := hashFile(workPath)
	if err != nil {
		return fmt.Errorf("hash file: %w", err)
	}

	doc, created, err := m.store.CreateOrGet(ctx, workPath, hash, string(kind))
	if err != nil {
		return fmt.Errorf("create or get document: %w", err)
	}

	if !created {
		log.Printf("[MailPoller] File %s (hash %s) already ingested.", workPath, hash)
		return nil
	}

	if err := m.store.SetProvenance(ctx, doc.ID, msgID, correspondent); err != nil {
		return fmt.Errorf("set provenance: %w", err)
	}

	_, err = m.store.EnqueueJob(ctx, doc.ID, string(kind))
	if err != nil {
		return fmt.Errorf("enqueue job: %w", err)
	}

	return nil
}

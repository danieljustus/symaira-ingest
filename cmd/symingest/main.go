package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/danieljustus/symaira-corekit/exitcodes"
	"github.com/danieljustus/symaira-corekit/logkit"
	"github.com/danieljustus/symaira-corekit/mcpserver"

	"github.com/danieljustus/symaira-ingest/internal/config"
	"github.com/danieljustus/symaira-ingest/internal/ingest"
	"github.com/danieljustus/symaira-ingest/internal/mcp"
	"github.com/danieljustus/symaira-ingest/internal/ocr"
	"github.com/danieljustus/symaira-ingest/internal/paperlessimport"
	"github.com/danieljustus/symaira-ingest/internal/store"
	"github.com/danieljustus/symaira-ingest/internal/version"
	"github.com/danieljustus/symaira-ingest/internal/writer"
)

var stdout io.Writer = os.Stdout

func main() {
	logkit.InitDefault("symingest")
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, exitcodes.FormatCLIError(err))
		os.Exit(int(exitcodes.ExitCodeFromError(err)))
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return printUsage()
	}

	switch args[0] {
	case "--version", "-v", "version":
		fmt.Fprintln(stdout, version.Version)
		return nil
	case "--help", "-h", "help":
		return printUsage()
	case "ingest":
		return runIngest(args[1:])
	case "watch":
		return runWatch(args[1:])
	case "jobs":
		return runJobs(args[1:])
	case "retry":
		return runRetry(args[1:])
	case "rules":
		return runRules(args[1:])
	case "import":
		return runImport(args[1:])
	case "mcp":
		return runMCP(args[1:])
	default:
		return exitcodes.Wrapf(nil, exitcodes.ExitNoInput, exitcodes.KindValidation,
			"unknown command %q", args[0])
	}
}

func printUsage() error {
	fmt.Fprintln(stdout, `symingest — document ingestion + OCR for the Symaira ecosystem
 
Usage:
  symingest [command]

Commands:
  ingest <file>       Ingest a file into the vault (one-shot)
  watch <dir>         Watch a directory for new/modified files and ingest in the background
  import paperless    Import documents from a Paperless-ngx instance
  jobs                List ingestion jobs in the queue
  retry <id>          Retry a failed job by ID
  rules               Manage classification rules (list, add, delete)
  mcp                 Start the MCP server
  version             Print version
  help                Show this help`)
	return nil
}

func runIngest(args []string) error {
	fs := flag.NewFlagSet("ingest", flag.ContinueOnError)
	ocrLang, vault, archive, db := registerSharedFlags(fs)
	if err := fs.Parse(args); err != nil {
		return exitcodes.Wrap(err, exitcodes.ExitData, exitcodes.KindValidation,
			"invalid ingest flags")
	}
	cfg, err := resolveConfig(fs, ocrLang, vault, archive, db)
	if err != nil {
		return err
	}
	remaining := fs.Args()
	if len(remaining) == 0 || remaining[0] == "--help" || remaining[0] == "-h" {
		fmt.Fprintln(stdout, `Usage: symingest ingest [flags] <file>

Flags:
  --ocr-lang string   Tesseract language override (default "eng")
  --vault string      Target vault directory
  --archive string    Target archive directory
  --db string         SQLite database path

Ingest a single file into the configured vault.`)
		return nil
	}

	if cfg.vault == "" {
		return exitcodes.Wrapf(nil, exitcodes.ExitConfig, exitcodes.KindConfig,
			"no vault configured; use --vault, SYMINGEST_VAULT env, or set vault in ~/.config/symingest/config.toml")
	}

	source, err := filepath.Abs(remaining[0])
	if err != nil {
		return exitcodes.Wrap(err, exitcodes.ExitData, exitcodes.KindValidation,
			"invalid source path")
	}

	st, err := store.Open(cfg.db)
	if err != nil {
		return exitcodes.Wrap(err, exitcodes.ExitConfig, exitcodes.KindConfig,
			"failed to open document store")
	}
	defer st.Close()

	engine := ocr.DefaultRunner(cfg.ocrLang)
	pipeline := &ingest.Pipeline{
		Engine:     engine,
		Store:      st,
		Writer:     &writer.NoteWriter{Vault: cfg.vault},
		ArchiveDir: cfg.archive,
	}

	ctx := context.Background()
	res, err := pipeline.Ingest(ctx, source, nil)
	if err != nil {
		if errors.Is(err, ingest.ErrDuplicate) {
			var vPath, aPath string
			if dupErr, ok := err.(*ingest.DuplicateError); ok {
				vPath = dupErr.VaultPath
				aPath = dupErr.ArchivePath
			}
			fmt.Fprintf(stdout, "already ingested: %s\n", source)
			if vPath != "" {
				fmt.Fprintf(stdout, "existing vault path: %s\n", vPath)
			}
			if aPath != "" {
				fmt.Fprintf(stdout, "existing archive path: %s\n", aPath)
			}
			return nil
		}
		return exitcodes.Wrap(err, exitcodes.ExitGeneric, exitcodes.KindInternal,
			"ingestion failed")
	}

	fmt.Fprintf(stdout, "ingested: %s\nengine: %s\ntext length: %d\n",
		source, res.Extract.Engine, len(res.Extract.Text))
	return nil
}

func runImport(args []string) error {
	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" {
		fmt.Fprintln(stdout, `Usage: symingest import paperless [flags]

Flags:
  --base-url string   Paperless-ngx instance URL (or PAPERLESS_URL env)
  --token string      API token (or PAPERLESS_TOKEN env)
  --since string      Only import documents whose Paperless created date is on
                      or after this date (YYYY-MM-DD)
  --limit int         Import at most N documents (newest first); 0 means no limit
  --ids string        Import only these Paperless document IDs (comma-separated,
                      e.g. 123,456); takes precedence over --since and --limit
  --vault string      Target vault directory
  --archive string    Target archive directory
  --db string         SQLite database path
  --dry-run           List what would be imported without writing
  --status            List per-document import status from a previous run, then exit
  --json              With --status, output the status list as JSON

Import documents from a Paperless-ngx instance into the vault. Use --limit or
--ids to run a small, inspectable pilot before a full migration; both bounds
apply to --dry-run and real imports alike. Imports are resumable: a document
already recorded as imported is skipped on a re-run, and a document that
previously failed is retried automatically.`)
		return nil
	}

	if args[0] != "paperless" {
		return exitcodes.Wrapf(nil, exitcodes.ExitNoInput, exitcodes.KindValidation,
			"unknown import subcommand %q; supported: paperless", args[0])
	}

	fs := flag.NewFlagSet("import paperless", flag.ContinueOnError)
	baseURL := fs.String("base-url", "", "Paperless-ngx instance URL")
	token := fs.String("token", "", "API token")
	sinceStr := fs.String("since", "", "Only import documents whose Paperless created date is on or after this date (YYYY-MM-DD)")
	limit := fs.Int("limit", 0, "Import at most N documents (newest first); 0 means no limit")
	idsStr := fs.String("ids", "", "Import only these Paperless document IDs (comma-separated); takes precedence over --since and --limit")
	dryRun := fs.Bool("dry-run", false, "List what would be imported without writing")
	statusOnly := fs.Bool("status", false, "List per-document import status from a previous run, then exit")
	jsonFlag := fs.Bool("json", false, "With --status, output the status list as JSON")
	ocrLang, vault, archive, db := registerSharedFlags(fs)
	if err := fs.Parse(args[1:]); err != nil {
		return exitcodes.Wrap(err, exitcodes.ExitData, exitcodes.KindValidation,
			"invalid import flags")
	}
	cfg, err := resolveConfig(fs, ocrLang, vault, archive, db)
	if err != nil {
		return err
	}

	if *baseURL == "" {
		*baseURL = os.Getenv("PAPERLESS_URL")
	}
	if *token == "" {
		*token = os.Getenv("PAPERLESS_TOKEN")
	}
	if *baseURL == "" {
		return exitcodes.Wrapf(nil, exitcodes.ExitConfig, exitcodes.KindConfig,
			"base-url is required (use --base-url or the PAPERLESS_URL env var)")
	}
	if *token == "" && !*statusOnly {
		return exitcodes.Wrapf(nil, exitcodes.ExitConfig, exitcodes.KindConfig,
			"token is required (use --token or the PAPERLESS_TOKEN env var)")
	}

	if cfg.vault == "" && !*statusOnly {
		return exitcodes.Wrapf(nil, exitcodes.ExitConfig, exitcodes.KindConfig,
			"no vault configured; use --vault, SYMINGEST_VAULT env, or set vault in ~/.config/symingest/config.toml")
	}

	if *statusOnly {
		st, err := store.Open(cfg.db)
		if err != nil {
			return exitcodes.Wrap(err, exitcodes.ExitConfig, exitcodes.KindConfig,
				"failed to open document store")
		}
		defer st.Close()

		states, err := st.ListPaperlessImportState(context.Background(), *baseURL, "")
		if err != nil {
			return exitcodes.Wrap(err, exitcodes.ExitGeneric, exitcodes.KindInternal,
				"failed to list paperless import status")
		}

		if *jsonFlag {
			if states == nil {
				fmt.Fprintln(stdout, "[]")
				return nil
			}
			data, err := json.MarshalIndent(states, "", "  ")
			if err != nil {
				return exitcodes.Wrap(err, exitcodes.ExitGeneric, exitcodes.KindInternal,
					"failed to marshal import status to JSON")
			}
			fmt.Fprintln(stdout, string(data))
			return nil
		}

		if len(states) == 0 {
			fmt.Fprintf(stdout, "No recorded import status for %s\n", *baseURL)
			return nil
		}
		for _, s := range states {
			if s.LastError != "" {
				fmt.Fprintf(stdout, "document %d: %s (%s)\n", s.PaperlessDocumentID, s.Status, s.LastError)
			} else {
				fmt.Fprintf(stdout, "document %d: %s\n", s.PaperlessDocumentID, s.Status)
			}
		}
		return nil
	}

	var since time.Time
	if *sinceStr != "" {
		since, err = time.Parse("2006-01-02", *sinceStr)
		if err != nil {
			return exitcodes.Wrapf(err, exitcodes.ExitData, exitcodes.KindValidation,
				"invalid since date %q; expected YYYY-MM-DD", *sinceStr)
		}
	}

	if *limit < 0 {
		return exitcodes.Wrapf(nil, exitcodes.ExitData, exitcodes.KindValidation,
			"invalid limit %d; must be zero or positive", *limit)
	}

	ids, err := parseDocumentIDs(*idsStr)
	if err != nil {
		return exitcodes.Wrap(err, exitcodes.ExitData, exitcodes.KindValidation,
			"invalid ids")
	}

	st, err := store.Open(cfg.db)
	if err != nil {
		return exitcodes.Wrap(err, exitcodes.ExitConfig, exitcodes.KindConfig,
			"failed to open document store")
	}
	defer st.Close()

	engine := ocr.DefaultRunner(cfg.ocrLang)
	pipeline := &ingest.Pipeline{
		Engine:     engine,
		Store:      st,
		Writer:     &writer.NoteWriter{Vault: cfg.vault},
		ArchiveDir: cfg.archive,
	}

	opts := paperlessimport.Options{
		BaseURL: *baseURL,
		Token:   *token,
		Since:   since,
		DryRun:  *dryRun,
		Limit:   *limit,
		IDs:     ids,
	}

	ctx := context.Background()
	stats, err := paperlessimport.Run(ctx, opts, pipeline)
	if err != nil {
		return exitcodes.Wrap(err, exitcodes.ExitGeneric, exitcodes.KindInternal,
			"import failed")
	}

	fmt.Fprintf(stdout, "Import complete: %d imported, %d skipped, %d failed (of %d total)\n",
		stats.Imported, stats.Skipped, stats.Failed, stats.Total)
	// For a bounded pilot run, echo exactly which documents were selected so
	// the operator can inspect them. Document content is never printed.
	if (*limit > 0 || len(ids) > 0) && len(stats.SelectedIDs) > 0 {
		fmt.Fprintf(stdout, "Selected document IDs: %s\n", joinInts(stats.SelectedIDs))
	}
	if stats.Failed > 0 {
		fmt.Fprintf(stdout, "Re-run the same command to retry failed documents; use --status to inspect them.\n")
	}
	return nil
}

// parseDocumentIDs parses a comma-separated list of Paperless document IDs,
// tolerating surrounding whitespace and empty entries. Each ID must be a
// positive integer.
func parseDocumentIDs(s string) ([]int, error) {
	if strings.TrimSpace(s) == "" {
		return nil, nil
	}
	var ids []int
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		id, err := strconv.Atoi(part)
		if err != nil {
			return nil, fmt.Errorf("document ID %q is not a number", part)
		}
		if id <= 0 {
			return nil, fmt.Errorf("document ID %d must be positive", id)
		}
		ids = append(ids, id)
	}
	return ids, nil
}

// joinInts renders a slice of ints as a comma-separated string.
func joinInts(nums []int) string {
	parts := make([]string, len(nums))
	for i, n := range nums {
		parts[i] = strconv.Itoa(n)
	}
	return strings.Join(parts, ",")
}

func defaultDBPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", exitcodes.Wrapf(err, exitcodes.ExitConfig, exitcodes.KindConfig,
			"cannot determine home directory; use --db to specify a database path explicitly")
	}
	return filepath.Join(home, ".local", "share", "symingest", "symingest.db"), nil
}

func defaultArchivePath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", exitcodes.Wrapf(err, exitcodes.ExitConfig, exitcodes.KindConfig,
			"cannot determine home directory; use --archive to specify an archive path explicitly")
	}
	return filepath.Join(home, ".local", "share", "symingest", "archive"), nil
}

type resolvedConfig struct {
	vault   string
	archive string
	db      string
	ocrLang string
}

// registerSharedFlags adds the shared CLI flags to fs and returns pointers to
// their values. Call fs.Parse(args) after this, then resolveConfig to merge
// flag values with config/env/defaults.
func registerSharedFlags(fs *flag.FlagSet) (ocrLang, vault, archive, db *string) {
	ocrLang = fs.String("ocr-lang", "", "Tesseract language override")
	vault = fs.String("vault", "", "Target vault directory")
	archive = fs.String("archive", "", "Target archive directory")
	db = fs.String("db", "", "SQLite database path")
	return
}

// resolveConfig merges parsed flag values with config-file / env-var / default
// values. Precedence: explicit CLI flags > env vars / config file > defaults.
// fs.Parse(args) must already have been called so that fs.Visit can tell which
// flags the user actually supplied.
func resolveConfig(fs *flag.FlagSet, ocrLang, vaultFlag, archiveFlag, dbFlag *string) (*resolvedConfig, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, exitcodes.Wrap(err, exitcodes.ExitConfig, exitcodes.KindConfig,
			"failed to load configuration")
	}

	explicitlySet := make(map[string]bool)
	fs.Visit(func(f *flag.Flag) {
		explicitlySet[f.Name] = true
	})

	if !explicitlySet["ocr-lang"] {
		if cfg.OCRLang != "" {
			*ocrLang = cfg.OCRLang
		}
	}
	if *ocrLang == "" {
		*ocrLang = "eng"
	}

	if !explicitlySet["vault"] {
		*vaultFlag = cfg.Vault
	}

	if !explicitlySet["archive"] {
		if cfg.ArchivePath != "" {
			*archiveFlag = cfg.ArchivePath
		} else {
			path, err := defaultArchivePath()
			if err != nil {
				return nil, err
			}
			*archiveFlag = path
		}
	}

	if !explicitlySet["db"] {
		if cfg.DBPath != "" {
			*dbFlag = cfg.DBPath
		} else {
			path, err := defaultDBPath()
			if err != nil {
				return nil, err
			}
			*dbFlag = path
		}
	}

	return &resolvedConfig{
		vault:   *vaultFlag,
		archive: *archiveFlag,
		db:      *dbFlag,
		ocrLang: *ocrLang,
	}, nil
}

func runMCP(args []string) error {
	fs := flag.NewFlagSet("mcp", flag.ContinueOnError)
	ocrLang, vault, archive, db := registerSharedFlags(fs)
	if err := fs.Parse(args); err != nil {
		return exitcodes.Wrap(err, exitcodes.ExitData, exitcodes.KindValidation,
			"invalid mcp flags")
	}
	cfg, err := resolveConfig(fs, ocrLang, vault, archive, db)
	if err != nil {
		return err
	}
	remaining := fs.Args()
	if len(remaining) > 0 && (remaining[0] == "--help" || remaining[0] == "-h") {
		fmt.Fprintln(stdout, `Usage: symingest mcp [flags]

Flags:
  --ocr-lang string   Tesseract language override (default "eng")
  --vault string      Target vault directory
  --archive string    Target archive directory
  --db string         SQLite database path

Start the MCP server for AI-powered document processing.`)
		return nil
	}

	st, err := store.Open(cfg.db)
	if err != nil {
		return exitcodes.Wrap(err, exitcodes.ExitConfig, exitcodes.KindConfig,
			"failed to open document store")
	}
	defer st.Close()

	engine := ocr.DefaultRunner(cfg.ocrLang)
	server := mcpserver.New("symingest", version.Version)
	mcp.Register(server, st, engine, cfg.vault, cfg.archive)

	ctx := context.Background()
	if err := server.ServeStdio(ctx); err != nil {
		return exitcodes.Wrap(err, exitcodes.ExitSoftware, exitcodes.KindInternal,
			"mcp server failed")
	}
	return nil
}

func runWatch(args []string) error {
	fs := flag.NewFlagSet("watch", flag.ContinueOnError)
	ocrLang, vault, archive, db := registerSharedFlags(fs)
	if err := fs.Parse(args); err != nil {
		return exitcodes.Wrap(err, exitcodes.ExitData, exitcodes.KindValidation,
			"invalid watch flags")
	}
	cfg, err := resolveConfig(fs, ocrLang, vault, archive, db)
	if err != nil {
		return err
	}
	remaining := fs.Args()
	if len(remaining) == 0 || remaining[0] == "--help" || remaining[0] == "-h" {
		fmt.Fprintln(stdout, `Usage: symingest watch [flags] <dir>

Flags:
  --ocr-lang string   Tesseract language override (default "eng")
  --vault string      Target vault directory
  --archive string    Target archive directory
  --db string         SQLite database path

Watch a directory for new or modified files and ingest them in the background.`)
		return nil
	}

	if cfg.vault == "" {
		return exitcodes.Wrapf(nil, exitcodes.ExitConfig, exitcodes.KindConfig,
			"no vault configured; use --vault, SYMINGEST_VAULT env, or set vault in ~/.config/symingest/config.toml")
	}

	inboxDir, err := filepath.Abs(remaining[0])
	if err != nil {
		return exitcodes.Wrap(err, exitcodes.ExitData, exitcodes.KindValidation,
			"invalid inbox directory path")
	}

	st, err := store.Open(cfg.db)
	if err != nil {
		return exitcodes.Wrap(err, exitcodes.ExitConfig, exitcodes.KindConfig,
			"failed to open document store")
	}
	defer st.Close()

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	// Reset any running jobs to pending on startup
	if err := st.ResetRunningJobs(ctx); err != nil {
		return exitcodes.Wrap(err, exitcodes.ExitConfig, exitcodes.KindInternal,
			"failed to reset running jobs")
	}

	watcher, err := ingest.NewWatcher(st, inboxDir)
	if err != nil {
		return exitcodes.Wrap(err, exitcodes.ExitConfig, exitcodes.KindInternal,
			"failed to initialize watcher")
	}
	defer watcher.Close()

	engine := ocr.DefaultRunner(cfg.ocrLang)
	pipeline := &ingest.Pipeline{
		Engine:     engine,
		Store:      st,
		Writer:     &writer.NoteWriter{Vault: cfg.vault},
		ArchiveDir: cfg.archive,
	}

	if err := watcher.Start(ctx); err != nil {
		return exitcodes.Wrap(err, exitcodes.ExitConfig, exitcodes.KindInternal,
			"failed to start watcher")
	}

	go ingest.StartWorker(ctx, pipeline)

	log.Printf("Watching directory: %s", inboxDir)
	log.Printf("Vault directory:    %s", cfg.vault)
	log.Printf("Archive directory:  %s", cfg.archive)
	log.Printf("Database:           %s", cfg.db)
	log.Println("Press Ctrl+C to stop.")

	<-ctx.Done()
	log.Println("Shutting down watch command...")
	return nil
}

func runJobs(args []string) error {
	fs := flag.NewFlagSet("jobs", flag.ContinueOnError)
	jsonFlag := fs.Bool("json", false, "Output jobs in JSON format")
	limitFlag := fs.Int("limit", 100, "Maximum number of jobs to return")
	ocrLang, vault, archive, db := registerSharedFlags(fs)
	if err := fs.Parse(args); err != nil {
		return exitcodes.Wrap(err, exitcodes.ExitData, exitcodes.KindValidation,
			"invalid jobs flags")
	}
	cfg, err := resolveConfig(fs, ocrLang, vault, archive, db)
	if err != nil {
		return err
	}

	st, err := store.Open(cfg.db)
	if err != nil {
		return exitcodes.Wrap(err, exitcodes.ExitConfig, exitcodes.KindConfig,
			"failed to open document store")
	}
	defer st.Close()

	ctx := context.Background()
	jobs, err := st.ListJobs(ctx, *limitFlag)
	if err != nil {
		return exitcodes.Wrap(err, exitcodes.ExitGeneric, exitcodes.KindInternal,
			"failed to list jobs")
	}

	if *jsonFlag {
		if jobs == nil {
			// Ensure we output empty array instead of null
			fmt.Fprintln(stdout, "[]")
			return nil
		}
		data, err := json.MarshalIndent(jobs, "", "  ")
		if err != nil {
			return exitcodes.Wrap(err, exitcodes.ExitGeneric, exitcodes.KindInternal,
				"failed to marshal jobs to JSON")
		}
		fmt.Fprintln(stdout, string(data))
		return nil
	}

	if len(jobs) == 0 {
		fmt.Fprintln(stdout, "No jobs in queue.")
		return nil
	}

	w := tabwriter.NewWriter(stdout, 0, 0, 3, ' ', 0)
	fmt.Fprintln(w, "ID\tDOCUMENT ID\tSTATUS\tATTEMPTS\tKIND\tSOURCE PATH")
	for _, j := range jobs {
		fmt.Fprintf(w, "%d\t%d\t%s\t%d\t%s\t%s\n",
			j.ID, j.DocumentID, j.Status, j.Attempts, j.Kind, j.SourcePath)
	}
	w.Flush()
	return nil
}

func runRetry(args []string) error {
	fs := flag.NewFlagSet("retry", flag.ContinueOnError)
	ocrLang, vault, archive, db := registerSharedFlags(fs)
	if err := fs.Parse(args); err != nil {
		return exitcodes.Wrap(err, exitcodes.ExitData, exitcodes.KindValidation,
			"invalid retry flags")
	}
	cfg, err := resolveConfig(fs, ocrLang, vault, archive, db)
	if err != nil {
		return err
	}
	remaining := fs.Args()
	if len(remaining) == 0 || remaining[0] == "--help" || remaining[0] == "-h" {
		fmt.Fprintln(stdout, `Usage: symingest retry [flags] <job-id>

Retry a failed job by resetting its status to pending.`)
		return nil
	}

	jobIDStr := remaining[0]
	jobID, err := strconv.ParseInt(jobIDStr, 10, 64)
	if err != nil {
		return exitcodes.Wrapf(err, exitcodes.ExitData, exitcodes.KindValidation,
			"invalid job ID %q; must be an integer", jobIDStr)
	}

	st, err := store.Open(cfg.db)
	if err != nil {
		return exitcodes.Wrap(err, exitcodes.ExitConfig, exitcodes.KindConfig,
			"failed to open document store")
	}
	defer st.Close()

	ctx := context.Background()
	if err := st.RetryJob(ctx, jobID); err != nil {
		return exitcodes.Wrapf(err, exitcodes.ExitGeneric, exitcodes.KindInternal,
			"failed to retry job %d", jobID)
	}

	fmt.Fprintf(stdout, "Job %d status set to pending. Background workers will process it shortly.\n", jobID)
	return nil
}

func runRules(args []string) error {
	fs := flag.NewFlagSet("rules", flag.ContinueOnError)
	ocrLang, vault, archive, db := registerSharedFlags(fs)
	if err := fs.Parse(args); err != nil {
		return exitcodes.Wrap(err, exitcodes.ExitData, exitcodes.KindValidation,
			"invalid rules flags")
	}
	cfg, err := resolveConfig(fs, ocrLang, vault, archive, db)
	if err != nil {
		return err
	}

	remaining := fs.Args()
	if len(remaining) == 0 || remaining[0] == "--help" || remaining[0] == "-h" {
		return printRulesUsage()
	}

	st, err := store.Open(cfg.db)
	if err != nil {
		return exitcodes.Wrap(err, exitcodes.ExitConfig, exitcodes.KindConfig,
			"failed to open document store")
	}
	defer st.Close()

	ctx := context.Background()

	switch remaining[0] {
	case "list":
		return listRules(ctx, st)
	case "add":
		return addRule(ctx, st, remaining[1:])
	case "delete":
		return deleteRule(ctx, st, remaining[1:])
	default:
		return exitcodes.Wrapf(nil, exitcodes.ExitNoInput, exitcodes.KindValidation,
			"unknown rules subcommand %q", remaining[0])
	}
}

func printRulesUsage() error {
	fmt.Fprintln(stdout, `Usage: symingest rules [flags] [command]

Flags:
  --db string   SQLite database path

Commands:
  list                          List all classification rules
  add <pattern> <kind> <value>  Add a classification rule
  delete <id>                   Delete a classification rule by ID

Kinds for add command:
  category, tag, correspondent, document_type`)
	return nil
}

func listRules(ctx context.Context, st *store.Store) error {
	rules, err := st.ListRules(ctx)
	if err != nil {
		return exitcodes.Wrap(err, exitcodes.ExitGeneric, exitcodes.KindInternal,
			"failed to list rules")
	}

	if len(rules) == 0 {
		fmt.Fprintln(stdout, "No classification rules defined.")
		return nil
	}

	w := tabwriter.NewWriter(stdout, 0, 0, 3, ' ', 0)
	fmt.Fprintln(w, "ID\tPATTERN\tKIND\tVALUE\tCREATED AT")
	for _, r := range rules {
		fmt.Fprintf(w, "%d\t%s\t%s\t%s\t%s\n",
			r.ID, r.Pattern, r.Kind, r.Value, r.CreatedAt)
	}
	w.Flush()
	return nil
}

func addRule(ctx context.Context, st *store.Store, args []string) error {
	if len(args) < 3 {
		return exitcodes.Wrapf(nil, exitcodes.ExitData, exitcodes.KindValidation,
			"missing arguments; usage: symingest rules add <pattern> <kind> <value>")
	}

	pattern := args[0]
	kind := args[1]
	value := args[2]

	rule, err := st.AddRule(ctx, pattern, kind, value)
	if err != nil {
		return exitcodes.Wrap(err, exitcodes.ExitGeneric, exitcodes.KindInternal,
			"failed to add rule")
	}

	fmt.Fprintf(stdout, "Added classification rule %d: pattern=%q, kind=%q, value=%q\n",
		rule.ID, rule.Pattern, rule.Kind, rule.Value)
	return nil
}

func deleteRule(ctx context.Context, st *store.Store, args []string) error {
	if len(args) < 1 {
		return exitcodes.Wrapf(nil, exitcodes.ExitData, exitcodes.KindValidation,
			"missing rule ID; usage: symingest rules delete <id>")
	}

	idStr := args[0]
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		return exitcodes.Wrapf(err, exitcodes.ExitData, exitcodes.KindValidation,
			"invalid rule ID %q; must be an integer", idStr)
	}

	if err := st.DeleteRule(ctx, id); err != nil {
		return exitcodes.Wrapf(err, exitcodes.ExitGeneric, exitcodes.KindInternal,
			"failed to delete rule %d", id)
	}

	fmt.Fprintf(stdout, "Deleted classification rule %d.\n", id)
	return nil
}

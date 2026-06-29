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
	"syscall"
	"text/tabwriter"

	"github.com/danieljustus/symaira-corekit/exitcodes"
	"github.com/danieljustus/symaira-corekit/logkit"
	"github.com/danieljustus/symaira-corekit/mcpserver"

	"github.com/danieljustus/symaira-ingest/internal/config"
	"github.com/danieljustus/symaira-ingest/internal/ingest"
	"github.com/danieljustus/symaira-ingest/internal/mcp"
	"github.com/danieljustus/symaira-ingest/internal/ocr"
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
  ingest <file>  Ingest a file into the vault (one-shot)
  watch <dir>    Watch a directory for new/modified files and ingest in the background
  jobs           List ingestion jobs in the queue
  retry <id>     Retry a failed job by ID
  rules          Manage classification rules (list, add, delete)
  mcp            Start the MCP server
  version        Print version
  help           Show this help`)
	return nil
}

func runIngest(args []string) error {
	fs := flag.NewFlagSet("ingest", flag.ContinueOnError)
	cfg, err := resolveConfig(fs)
	if err != nil {
		return err
	}
	if err := fs.Parse(args); err != nil {
		return exitcodes.Wrap(err, exitcodes.ExitData, exitcodes.KindValidation,
			"invalid ingest flags")
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
	res, err := pipeline.Ingest(ctx, source)
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

func resolveConfig(fs *flag.FlagSet) (*resolvedConfig, error) {
	ocrLang := fs.String("ocr-lang", "", "Tesseract language override")
	vaultFlag := fs.String("vault", "", "Target vault directory")
	archiveFlag := fs.String("archive", "", "Target archive directory")
	dbFlag := fs.String("db", "", "SQLite database path")

	cfg, err := config.Load()
	if err != nil {
		return nil, exitcodes.Wrap(err, exitcodes.ExitConfig, exitcodes.KindConfig,
			"failed to load configuration")
	}

	if *ocrLang == "" {
		*ocrLang = cfg.OCRLang
	}
	if *ocrLang == "" {
		*ocrLang = "eng"
	}
	if *vaultFlag == "" {
		*vaultFlag = cfg.Vault
	}
	if *archiveFlag == "" {
		*archiveFlag = cfg.ArchivePath
	}
	if *archiveFlag == "" {
		path, err := defaultArchivePath()
		if err != nil {
			return nil, err
		}
		*archiveFlag = path
	}
	if *dbFlag == "" {
		*dbFlag = cfg.DBPath
	}
	if *dbFlag == "" {
		path, err := defaultDBPath()
		if err != nil {
			return nil, err
		}
		*dbFlag = path
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
	cfg, err := resolveConfig(fs)
	if err != nil {
		return err
	}
	if err := fs.Parse(args); err != nil {
		return exitcodes.Wrap(err, exitcodes.ExitData, exitcodes.KindValidation,
			"invalid mcp flags")
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
	cfg, err := resolveConfig(fs)
	if err != nil {
		return err
	}
	if err := fs.Parse(args); err != nil {
		return exitcodes.Wrap(err, exitcodes.ExitData, exitcodes.KindValidation,
			"invalid watch flags")
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
	cfg, err := resolveConfig(fs)
	if err != nil {
		return err
	}
	if err := fs.Parse(args); err != nil {
		return exitcodes.Wrap(err, exitcodes.ExitData, exitcodes.KindValidation,
			"invalid jobs flags")
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
	cfg, err := resolveConfig(fs)
	if err != nil {
		return err
	}
	if err := fs.Parse(args); err != nil {
		return exitcodes.Wrap(err, exitcodes.ExitData, exitcodes.KindValidation,
			"invalid retry flags")
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
	cfg, err := resolveConfig(fs)
	if err != nil {
		return err
	}
	if err := fs.Parse(args); err != nil {
		return exitcodes.Wrap(err, exitcodes.ExitData, exitcodes.KindValidation,
			"invalid rules flags")
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

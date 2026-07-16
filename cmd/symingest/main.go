package main

import (
	"context"
	"crypto/tls"
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/danieljustus/symaira-corekit/exitcodes"
	"github.com/danieljustus/symaira-corekit/logkit"
	"github.com/danieljustus/symaira-corekit/mcpserver"
	"github.com/danieljustus/symaira-corekit/versionkit"
	"github.com/emersion/go-imap/v2/imapclient"

	"github.com/danieljustus/symaira-ingest/internal/annotate"
	"github.com/danieljustus/symaira-ingest/internal/config"
	"github.com/danieljustus/symaira-ingest/internal/extract"
	"github.com/danieljustus/symaira-ingest/internal/ingest"
	"github.com/danieljustus/symaira-ingest/internal/mcp"
	"github.com/danieljustus/symaira-ingest/internal/notionimport"
	"github.com/danieljustus/symaira-ingest/internal/ocr"
	"github.com/danieljustus/symaira-ingest/internal/paperlessimport"
	"github.com/danieljustus/symaira-ingest/internal/pdfops"
	"github.com/danieljustus/symaira-ingest/internal/secret"
	"github.com/danieljustus/symaira-ingest/internal/store"
	symseekint "github.com/danieljustus/symaira-ingest/internal/symseek"
	"github.com/danieljustus/symaira-ingest/internal/vaultreview"
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
		jsonFlag := false
		for _, arg := range args[1:] {
			if arg == "--json" {
				jsonFlag = true
				break
			}
		}
		info := versionkit.New("symingest", version.Version, 1)
		if jsonFlag {
			return info.Write(stdout)
		}
		fmt.Fprintln(stdout, info.String())
		return nil
	case "--help", "-h", "help":
		return printUsage()
	case "ingest":
		return runIngest(args[1:])
	case "reocr":
		return runReocr(args[1:])
	case "split":
		return runPDFSplit(args[1:])
	case "merge":
		return runPDFMerge(args[1:])
	case "rotate":
		return runPDFRotate(args[1:])
	case "watch":
		return runWatch(args[1:])
	case "service":
		return runService(args[1:])
	case "search":
		return runSearch(args[1:])
	case "jobs":
		return runJobs(args[1:])
	case "retry":
		return runRetry(args[1:])
	case "rules":
		return runRules(args[1:])
	case "mail":
		return runMail(args[1:])
	case "import":
		return runImport(args[1:])
	case "doctor":
		return runDoctor(args[1:])
	case "setup":
		return runSetup(args[1:])
	case "validate-vault":
		return runValidateVault(args[1:])
	case "update":
		return runUpdate(args[1:])
	case "bulk-update":
		return runBulkUpdate(args[1:])
	case "apply-corrections":
		return runApplyCorrections(args[1:])
	case "review-report":
		return runReviewReport(args[1:])
	case "report":
		return runReport(args[1:])
	case "cutover-check":
		return runCutoverCheck(args[1:])
	case "extract":
		return runExtract(args[1:])
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
  reocr <file|id>     Reprocess an archived original for an existing document
  split <file>        Split a PDF after selected pages
  merge <files...>    Merge two or more PDFs into one output
  rotate <file>       Rotate selected PDF pages
  extract <file>      Extract structured data from a file (one-shot preview)
  watch <dir>         Watch a directory for new/modified files and ingest in the background
  service             Manage the macOS LaunchAgent for the watcher
  search              Index the vault with symseek and validate search fixtures
  mail                Read/write IMAP mail-ingest configuration
  import paperless    Import documents from a Paperless-ngx instance
  import notion       Import a Notion Markdown + CSV export into the vault
  doctor              Validate production readiness
  setup               Generate a production config file
  validate-vault      Validate generated Markdown notes and archived originals
  update              Safely update one note by Paperless ID
  bulk-update         Safely update multiple notes selected by frontmatter
  apply-corrections   Apply YAML corrections keyed by paperless_id
  review-report       Generate a human-reviewable migration report
  report              Validate machine-readable migration reports
  cutover-check       Gate whether Paperless can stop being source of truth
  jobs                List ingestion jobs in the queue
  retry <id>          Retry a failed job by ID
  rules               Manage classification rules (list, add, update, test, dry-run, delete)
  mcp                 Start the MCP server
  version             Print version
  help                Show this help`)
	return nil
}

func configureUsage(fs *flag.FlagSet, usage, description string) {
	fs.SetOutput(stdout)
	fs.Usage = func() {
		fmt.Fprintf(stdout, "Usage: symingest %s\n\n%s\n\nFlags:\n", usage, description)
		fs.PrintDefaults()
	}
}

func parseFlags(fs *flag.FlagSet, args []string, message string) (bool, error) {
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return true, nil
		}
		return false, exitcodes.Wrap(err, exitcodes.ExitData, exitcodes.KindValidation, message)
	}
	return false, nil
}

func runIngest(args []string) error {
	fs := flag.NewFlagSet("ingest", flag.ContinueOnError)
	ocrLang, vault, archive, db := registerSharedFlags(fs)
	configureUsage(fs, "ingest [flags] <file>", "Ingest a single file into the configured vault.")
	help, err := parseFlags(fs, args, "invalid ingest flags")
	if help || err != nil {
		return err
	}
	cfg, err := resolveConfig(fs, ocrLang, vault, archive, db)
	if err != nil {
		return err
	}
	remaining := fs.Args()
	if len(remaining) == 0 {
		fs.Usage()
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
	configurePostIndex(pipeline, cfg)

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

type reocrErrorResponse struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type reocrResponse struct {
	SchemaVersion int                 `json:"schema_version"`
	DocumentID    int64               `json:"document_id"`
	JobID         int64               `json:"job_id"`
	Status        string              `json:"status"`
	OutputPath    string              `json:"output_path"`
	Error         *reocrErrorResponse `json:"error"`
}

func printReocrResponse(response reocrResponse, jsonFlag bool) error {
	if jsonFlag {
		data, err := json.MarshalIndent(response, "", "  ")
		if err != nil {
			return exitcodes.Wrap(err, exitcodes.ExitGeneric, exitcodes.KindInternal,
				"failed to marshal reocr response")
		}
		fmt.Fprintln(stdout, string(data))
		return nil
	}
	if response.Error != nil {
		return nil
	}
	if response.Status == "already_running" {
		fmt.Fprintf(stdout, "reprocess already running for document %d (job %d)\n", response.DocumentID, response.JobID)
		return nil
	}
	fmt.Fprintf(stdout, "reprocessed: document %d\njob ID: %d\nstatus: %s\noutput path: %s\n",
		response.DocumentID, response.JobID, response.Status, response.OutputPath)
	return nil
}

func reocrFailure(jsonFlag bool, documentID, jobID int64, code, message string) error {
	response := reocrResponse{
		SchemaVersion: 1,
		DocumentID:    documentID,
		JobID:         jobID,
		Status:        "failed",
		Error:         &reocrErrorResponse{Code: code, Message: message},
	}
	if err := printReocrResponse(response, jsonFlag); err != nil {
		return err
	}
	return exitcodes.Wrapf(nil, exitcodes.ExitData, exitcodes.KindValidation, "%s: %s", code, message)
}

func runReocr(args []string) error {
	fs := flag.NewFlagSet("reocr", flag.ContinueOnError)
	jsonFlag := fs.Bool("json", false, "Output a versioned JSON response")
	documentIDFlag := fs.String("document-id", "", "Document ID to reprocess")
	sourceFlag := fs.String("source", "", "Archived source path to reprocess (use for numeric paths)")
	langFlag := fs.String("lang", "", "Tesseract language override")
	ocrLang, vault, archive, db := registerSharedFlags(fs)
	configureUsage(fs, "reocr [flags] <document-id|archived-source-path>", "Re-run extraction/OCR for an already-ingested document. A numeric operand is a document ID; a non-numeric operand is an archived source path. Use --document-id or --source to make the identifier explicit.\n\nJSON responses use schema_version 1 and include document_id, job_id, status, output_path, and error.")
	help, err := parseFlags(fs, args, "invalid reocr flags")
	if help || err != nil {
		return err
	}
	if *langFlag != "" {
		*ocrLang = *langFlag
	}
	cfg, err := resolveConfig(fs, ocrLang, vault, archive, db)
	if err != nil {
		return err
	}
	if cfg.vault == "" {
		return reocrFailure(*jsonFlag, 0, 0, "vault_not_configured", "no vault configured; use --vault, SYMINGEST_VAULT env, or set vault in config")
	}

	remaining := fs.Args()
	if *documentIDFlag != "" && (*sourceFlag != "" || len(remaining) > 0) {
		return reocrFailure(*jsonFlag, 0, 0, "invalid_identifier", "use exactly one of --document-id, --source, or a positional identifier")
	}
	if *sourceFlag != "" && len(remaining) > 0 {
		return reocrFailure(*jsonFlag, 0, 0, "invalid_identifier", "use exactly one of --source or a positional identifier")
	}
	if *documentIDFlag == "" && *sourceFlag == "" && len(remaining) == 0 {
		fs.Usage()
		return reocrFailure(*jsonFlag, 0, 0, "invalid_identifier", "a document ID or archived source path is required")
	}
	if len(remaining) > 1 {
		return reocrFailure(*jsonFlag, 0, 0, "invalid_identifier", "expected exactly one document ID or archived source path")
	}

	st, err := store.Open(cfg.db)
	if err != nil {
		return exitcodes.Wrap(err, exitcodes.ExitConfig, exitcodes.KindConfig, "failed to open document store")
	}
	defer st.Close()
	ctx := context.Background()

	var documentID int64
	var sourcePath string
	if *documentIDFlag != "" {
		documentID, err = strconv.ParseInt(*documentIDFlag, 10, 64)
		if err != nil || documentID <= 0 {
			return reocrFailure(*jsonFlag, 0, 0, "invalid_identifier", fmt.Sprintf("invalid document ID %q; must be a positive integer", *documentIDFlag))
		}
	} else if *sourceFlag != "" {
		sourcePath = *sourceFlag
	} else if id, parseErr := strconv.ParseInt(remaining[0], 10, 64); parseErr == nil {
		if id <= 0 {
			return reocrFailure(*jsonFlag, 0, 0, "invalid_identifier", fmt.Sprintf("invalid document ID %q; must be a positive integer", remaining[0]))
		}
		documentID = id
	} else {
		sourcePath = remaining[0]
	}

	var doc *store.Document
	if documentID != 0 {
		doc, err = st.ByID(ctx, documentID)
		if errors.Is(err, sql.ErrNoRows) {
			return reocrFailure(*jsonFlag, documentID, 0, "document_not_found", fmt.Sprintf("document %d does not exist", documentID))
		}
		if err != nil {
			return exitcodes.Wrap(err, exitcodes.ExitGeneric, exitcodes.KindInternal, "failed to look up document")
		}
	} else {
		rawSourcePath := sourcePath
		sourcePath, err = filepath.Abs(sourcePath)
		if err != nil {
			return reocrFailure(*jsonFlag, 0, 0, "invalid_identifier", fmt.Sprintf("invalid archived source path: %v", err))
		}
		doc, err = st.ByArchivePath(ctx, sourcePath)
		if errors.Is(err, sql.ErrNoRows) && filepath.Clean(rawSourcePath) != sourcePath {
			sourcePath = filepath.Clean(rawSourcePath)
			doc, err = st.ByArchivePath(ctx, sourcePath)
		}
		if errors.Is(err, sql.ErrNoRows) {
			return reocrFailure(*jsonFlag, 0, 0, "source_not_registered", fmt.Sprintf("archived source path is not registered: %s", sourcePath))
		}
		if err != nil {
			return exitcodes.Wrap(err, exitcodes.ExitGeneric, exitcodes.KindInternal, "failed to look up archived source")
		}
		documentID = doc.ID
	}
	if doc.ArchivePath == nil || *doc.ArchivePath == "" {
		return reocrFailure(*jsonFlag, documentID, 0, "archive_missing", "document has no recorded archived source")
	}
	if sourcePath == "" {
		sourcePath = *doc.ArchivePath
	}
	if _, err := os.Stat(sourcePath); err != nil {
		code := "source_missing"
		message := fmt.Sprintf("archived source is unavailable: %s", sourcePath)
		if !os.IsNotExist(err) {
			message = fmt.Sprintf("cannot access archived source %s: %v", sourcePath, err)
		}
		return reocrFailure(*jsonFlag, documentID, 0, code, message)
	}

	pipeline := &ingest.Pipeline{
		Engine:     ocr.DefaultRunner(cfg.ocrLang),
		Store:      st,
		Writer:     &writer.NoteWriter{Vault: cfg.vault},
		ArchiveDir: cfg.archive,
	}
	configurePostIndex(pipeline, cfg)
	result, err := pipeline.Reprocess(ctx, documentID, sourcePath, nil)
	if err != nil {
		code := "reprocess_failed"
		if strings.Contains(err.Error(), "hash mismatch") {
			code = "source_hash_mismatch"
		} else if strings.Contains(err.Error(), "stat archived source") && os.IsNotExist(err) {
			code = "source_missing"
		}
		return reocrFailure(*jsonFlag, documentID, 0, code, err.Error())
	}
	response := reocrResponse{SchemaVersion: 1, DocumentID: documentID, JobID: result.Job.ID, Status: "completed"}
	if result.AlreadyRunning {
		response.Status = "already_running"
		if doc.VaultPath != nil {
			response.OutputPath = *doc.VaultPath
		}
	} else if result.Result != nil {
		response.OutputPath = result.Result.VaultPath
	}
	return printReocrResponse(response, *jsonFlag)
}

func ingestDerivedPDFs(cfg *resolvedConfig, paths []string) ([]string, error) {
	if cfg.vault == "" {
		return nil, fmt.Errorf("cannot ingest generated PDFs: no vault configured; pass --vault or configure a vault")
	}
	st, err := store.Open(cfg.db)
	if err != nil {
		return nil, fmt.Errorf("open document store: %w", err)
	}
	defer st.Close()
	pipeline := &ingest.Pipeline{
		Engine:     ocr.DefaultRunner(cfg.ocrLang),
		Store:      st,
		Writer:     &writer.NoteWriter{Vault: cfg.vault},
		ArchiveDir: cfg.archive,
	}
	configurePostIndex(pipeline, cfg)
	var notes []string
	for _, path := range paths {
		result, err := pipeline.Ingest(context.Background(), path, nil)
		if err != nil {
			return nil, fmt.Errorf("ingest generated PDF %s: %w", path, err)
		}
		notes = append(notes, result.VaultPath)
	}
	return notes, nil
}

func runPDFSplit(args []string) error {
	fs := flag.NewFlagSet("split", flag.ContinueOnError)
	at := fs.String("at", "", "Split after these pages, e.g. 2,4 or 2-3,6")
	outputDir := fs.String("output-dir", "", "Directory for generated PDF parts (default: <input>.parts)")
	ingestFlag := fs.Bool("ingest", false, "Run generated PDFs through the normal ingest pipeline")
	ocrLang, vault, archive, db := registerSharedFlags(fs)
	configureUsage(fs, "split [flags] <file>", "Split a PDF after selected pages. Requires Poppler pdfinfo, pdfseparate and pdfunite.")
	help, err := parseFlags(fs, args, "invalid split flags")
	if help || err != nil {
		return err
	}
	if *at == "" || len(fs.Args()) != 1 {
		fs.Usage()
		return exitcodes.Wrapf(nil, exitcodes.ExitNoInput, exitcodes.KindValidation, "split requires one PDF and --at")
	}
	cfg, err := resolveConfig(fs, ocrLang, vault, archive, db)
	if err != nil {
		return err
	}
	input, err := filepath.Abs(fs.Args()[0])
	if err != nil {
		return exitcodes.Wrap(err, exitcodes.ExitData, exitcodes.KindValidation, "invalid split input")
	}
	outDir := *outputDir
	if outDir == "" {
		outDir = strings.TrimSuffix(input, filepath.Ext(input)) + ".parts"
	}
	outDir, err = filepath.Abs(outDir)
	if err != nil {
		return exitcodes.Wrap(err, exitcodes.ExitData, exitcodes.KindValidation, "invalid split output directory")
	}
	outputs, err := pdfops.DefaultTools().Split(context.Background(), input, *at, outDir)
	if err != nil {
		return exitcodes.Wrap(err, exitcodes.ExitGeneric, exitcodes.KindInternal, "PDF split failed")
	}
	for _, path := range outputs {
		fmt.Fprintf(stdout, "created: %s\n", path)
	}
	if *ingestFlag {
		notes, err := ingestDerivedPDFs(cfg, outputs)
		if err != nil {
			return exitcodes.Wrap(err, exitcodes.ExitGeneric, exitcodes.KindInternal, "generated PDF ingest failed")
		}
		for _, note := range notes {
			fmt.Fprintf(stdout, "ingested note: %s\n", note)
		}
	}
	return nil
}

func runPDFMerge(args []string) error {
	fs := flag.NewFlagSet("merge", flag.ContinueOnError)
	output := fs.String("output", "", "Output PDF path")
	ingestFlag := fs.Bool("ingest", false, "Run the generated PDF through the normal ingest pipeline")
	ocrLang, vault, archive, db := registerSharedFlags(fs)
	configureUsage(fs, "merge [flags] <file>...", "Merge two or more PDFs. Requires Poppler pdfunite.")
	help, err := parseFlags(fs, args, "invalid merge flags")
	if help || err != nil {
		return err
	}
	if *output == "" || len(fs.Args()) < 2 {
		fs.Usage()
		return exitcodes.Wrapf(nil, exitcodes.ExitNoInput, exitcodes.KindValidation, "merge requires --output and at least two input PDFs")
	}
	cfg, err := resolveConfig(fs, ocrLang, vault, archive, db)
	if err != nil {
		return err
	}
	inputs := make([]string, len(fs.Args()))
	for i, input := range fs.Args() {
		inputs[i], err = filepath.Abs(input)
		if err != nil {
			return exitcodes.Wrap(err, exitcodes.ExitData, exitcodes.KindValidation, "invalid merge input")
		}
	}
	outputPath, err := filepath.Abs(*output)
	if err != nil {
		return exitcodes.Wrap(err, exitcodes.ExitData, exitcodes.KindValidation, "invalid merge output")
	}
	if err := pdfops.DefaultTools().Merge(context.Background(), inputs, outputPath); err != nil {
		return exitcodes.Wrap(err, exitcodes.ExitGeneric, exitcodes.KindInternal, "PDF merge failed")
	}
	fmt.Fprintf(stdout, "created: %s\n", outputPath)
	if *ingestFlag {
		notes, err := ingestDerivedPDFs(cfg, []string{outputPath})
		if err != nil {
			return exitcodes.Wrap(err, exitcodes.ExitGeneric, exitcodes.KindInternal, "generated PDF ingest failed")
		}
		fmt.Fprintf(stdout, "ingested note: %s\n", notes[0])
	}
	return nil
}

func runPDFRotate(args []string) error {
	fs := flag.NewFlagSet("rotate", flag.ContinueOnError)
	degrees := fs.Int("degrees", 0, "Rotation in degrees: -270, -180, -90, 90, 180 or 270")
	pages := fs.String("pages", "", "Optional pages to rotate, e.g. 1,3-4 (default: all pages)")
	output := fs.String("output", "", "Output PDF path")
	ingestFlag := fs.Bool("ingest", false, "Run the generated PDF through the normal ingest pipeline")
	ocrLang, vault, archive, db := registerSharedFlags(fs)
	configureUsage(fs, "rotate [flags] <file>", "Rotate selected PDF pages. Requires qpdf; the original is never modified.")
	help, err := parseFlags(fs, args, "invalid rotate flags")
	if help || err != nil {
		return err
	}
	if *output == "" || len(fs.Args()) != 1 {
		fs.Usage()
		return exitcodes.Wrapf(nil, exitcodes.ExitNoInput, exitcodes.KindValidation, "rotate requires one input PDF and --output")
	}
	cfg, err := resolveConfig(fs, ocrLang, vault, archive, db)
	if err != nil {
		return err
	}
	input, err := filepath.Abs(fs.Args()[0])
	if err != nil {
		return exitcodes.Wrap(err, exitcodes.ExitData, exitcodes.KindValidation, "invalid rotate input")
	}
	outputPath, err := filepath.Abs(*output)
	if err != nil {
		return exitcodes.Wrap(err, exitcodes.ExitData, exitcodes.KindValidation, "invalid rotate output")
	}
	if err := pdfops.DefaultTools().Rotate(context.Background(), input, outputPath, *degrees, *pages); err != nil {
		return exitcodes.Wrap(err, exitcodes.ExitGeneric, exitcodes.KindInternal, "PDF rotation failed")
	}
	fmt.Fprintf(stdout, "created: %s\n", outputPath)
	if *ingestFlag {
		notes, err := ingestDerivedPDFs(cfg, []string{outputPath})
		if err != nil {
			return exitcodes.Wrap(err, exitcodes.ExitGeneric, exitcodes.KindInternal, "generated PDF ingest failed")
		}
		fmt.Fprintf(stdout, "ingested note: %s\n", notes[0])
	}
	return nil
}

func runImport(args []string) error {
	fs := flag.NewFlagSet("import paperless", flag.ContinueOnError)
	baseURL := fs.String("base-url", "", "Paperless-ngx instance URL (or PAPERLESS_URL env)")
	token := fs.String("token", "", "API token (or PAPERLESS_TOKEN env)")
	sinceStr := fs.String("since", "", "Only import documents whose Paperless created date is on or after this date (YYYY-MM-DD)")
	limit := fs.Int("limit", 0, "Import at most N documents (newest first); 0 means no limit")
	idsStr := fs.String("ids", "", "Import only these Paperless document IDs (comma-separated); takes precedence over --since and --limit")
	preserveStoragePaths := fs.Bool("preserve-storage-paths", false, "Place notes under vault subdirectories derived from each document's Paperless storage path")
	dryRun := fs.Bool("dry-run", false, "List what would be imported without writing")
	plan := fs.Bool("plan", false, "Plan a Paperless import without downloading document bodies or writing vault/archive/import-state")
	resume := fs.Bool("resume", false, "Resume an interrupted import by skipping documents already imported for this target")
	retryFailed := fs.Bool("retry-failed", false, "Retry only documents recorded as failed for this target")
	concurrency := fs.Int("concurrency", 1, "Maximum number of Paperless documents to process concurrently")
	checkpointEvery := fs.Int("checkpoint-every", 0, "Print a progress checkpoint after every N processed documents; 0 disables checkpoints")
	reportPath := fs.String("report", "", "Write a JSON migration report to this path (works with --plan, --dry-run and real imports)")
	verify := fs.Bool("verify", false, "Verify a completed import against the Paperless source instead of importing")
	deepVerify := fs.Bool("deep", false, "With --verify, re-download each Paperless original and compare SHA-256 against the archive")
	statusOnly := fs.Bool("status", false, "List per-document import status from a previous run, then exit")
	statusSummary := fs.Bool("summary", false, "With --status, print counts by import status")
	statusFailed := fs.Bool("failed", false, "With --status, show only failed documents")
	jsonFlag := fs.Bool("json", false, "With --status or --verify, output the result as JSON")
	ocrLang, vault, archive, db := registerSharedFlags(fs)
	configureUsage(fs, "import paperless [flags]", "Import documents from a Paperless-ngx instance into the vault. Use --plan, --dry-run, --limit, or --ids to run a small, inspectable pilot before a full migration; bounds apply to --plan, --dry-run and real imports alike. Imports are resumable: a document already recorded as imported is skipped on a re-run, and a document that previously failed is retried automatically.")

	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" {
		fs.Usage()
		return nil
	}

	if args[0] == "notion" {
		return runNotionImport(args[1:])
	}

	if args[0] != "paperless" {
		return exitcodes.Wrapf(nil, exitcodes.ExitNoInput, exitcodes.KindValidation,
			"unknown import subcommand %q; supported: paperless, notion", args[0])
	}

	help, err := parseFlags(fs, args[1:], "invalid import flags")
	if help || err != nil {
		return err
	}
	cfg, err := resolveConfig(fs, ocrLang, vault, archive, db)
	if err != nil {
		return err
	}

	if *baseURL == "" {
		*baseURL = os.Getenv("PAPERLESS_URL")
	}
	if *baseURL == "" {
		*baseURL = cfg.paperlessBaseURL
	}
	if *token == "" {
		*token = os.Getenv("PAPERLESS_TOKEN")
	}
	if *baseURL == "" {
		return exitcodes.Wrapf(nil, exitcodes.ExitConfig, exitcodes.KindConfig,
			"base-url is required (use --base-url, PAPERLESS_URL env, or paperless_base_url in config)")
	}
	if *token == "" && !*statusOnly {
		return exitcodes.Wrapf(nil, exitcodes.ExitConfig, exitcodes.KindConfig,
			"token is required (use --token or the PAPERLESS_TOKEN env var)")
	}

	if *plan && *dryRun {
		return exitcodes.Wrapf(nil, exitcodes.ExitData, exitcodes.KindValidation,
			"--plan and --dry-run are mutually exclusive")
	}
	if *retryFailed && (*plan || *dryRun) {
		return exitcodes.Wrapf(nil, exitcodes.ExitData, exitcodes.KindValidation,
			"--retry-failed is only valid for real imports")
	}
	if *deepVerify && !*verify {
		return exitcodes.Wrapf(nil, exitcodes.ExitData, exitcodes.KindValidation,
			"--deep is only valid with --verify")
	}
	if *concurrency < 1 {
		return exitcodes.Wrapf(nil, exitcodes.ExitData, exitcodes.KindValidation,
			"invalid concurrency %d; must be at least 1", *concurrency)
	}
	if *checkpointEvery < 0 {
		return exitcodes.Wrapf(nil, exitcodes.ExitData, exitcodes.KindValidation,
			"invalid checkpoint interval %d; must be zero or positive", *checkpointEvery)
	}

	if cfg.vault == "" && !*statusOnly && !*plan && !*dryRun {
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

		statusFilter := ""
		if *statusFailed {
			statusFilter = "failed"
		}
		states, err := st.ListPaperlessImportState(context.Background(), *baseURL, statusFilter)
		if err != nil {
			return exitcodes.Wrap(err, exitcodes.ExitGeneric, exitcodes.KindInternal,
				"failed to list paperless import status")
		}

		if *statusSummary {
			summary := buildPaperlessStatusSummary(*baseURL, states)
			if *jsonFlag {
				data, err := json.MarshalIndent(summary, "", "  ")
				if err != nil {
					return exitcodes.Wrap(err, exitcodes.ExitGeneric, exitcodes.KindInternal,
						"failed to marshal import status summary to JSON")
				}
				fmt.Fprintln(stdout, string(data))
				return nil
			}
			fmt.Fprintf(stdout, "Paperless import status for %s: total=%d imported=%d skipped=%d failed=%d pending=%d\n",
				summary.BaseURL, summary.Total, summary.Imported, summary.Skipped, summary.Failed, summary.Pending)
			return nil
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

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var st *store.Store
	{
		var err error
		st, err = store.Open(cfg.db)
		if err != nil {
			return exitcodes.Wrap(err, exitcodes.ExitConfig, exitcodes.KindConfig,
				"failed to open document store")
		}
		defer st.Close()
	}

	if *verify {
		report, err := paperlessimport.Verify(ctx, paperlessimport.Options{BaseURL: *baseURL, Token: *token, Since: since, Limit: *limit, IDs: ids, DeepVerify: *deepVerify, TargetVault: cfg.vault, TargetArchive: cfg.archive}, cfg.vault, st)
		if err != nil {
			return exitcodes.Wrap(err, exitcodes.ExitGeneric, exitcodes.KindInternal,
				"verification failed")
		}
		if *jsonFlag {
			data, err := json.MarshalIndent(report, "", "  ")
			if err != nil {
				return exitcodes.Wrap(err, exitcodes.ExitGeneric, exitcodes.KindInternal,
					"failed to marshal verification report to JSON")
			}
			fmt.Fprintln(stdout, string(data))
		} else {
			printVerifyReport(stdout, report)
		}
		if !report.Complete() {
			return exitcodes.Wrapf(nil, exitcodes.ExitConflict, exitcodes.KindConflict,
				"migration verification found discrepancies: %d missing, %d duplicate, %d missing-archive, %d hash-mismatch, %d source-hash-mismatch, %d mismatched",
				len(report.Missing), len(report.Duplicate), len(report.MissingArchive), len(report.HashMismatch), len(report.SourceHashMismatch), len(report.Mismatches))
		}
		return nil
	}

	engine := ocr.DefaultRunner(cfg.ocrLang)
	pipeline := &ingest.Pipeline{
		Engine:     engine,
		Store:      st,
		Writer:     &writer.NoteWriter{Vault: cfg.vault},
		ArchiveDir: cfg.archive,
	}
	configurePostIndex(pipeline, cfg)

	opts := paperlessimport.Options{
		BaseURL:              *baseURL,
		Token:                *token,
		Since:                since,
		DryRun:               *dryRun,
		Plan:                 *plan,
		Resume:               *resume,
		RetryFailed:          *retryFailed,
		Concurrency:          *concurrency,
		CheckpointEvery:      *checkpointEvery,
		Limit:                *limit,
		IDs:                  ids,
		PreserveStoragePaths: *preserveStoragePaths,
		TargetVault:          cfg.vault,
		TargetArchive:        cfg.archive,
	}

	stats, err := paperlessimport.Run(ctx, opts, pipeline)
	if err != nil {
		return exitcodes.Wrap(err, exitcodes.ExitGeneric, exitcodes.KindInternal,
			"import failed")
	}

	if *plan {
		fmt.Fprintf(stdout, "Import plan complete: %d documents analyzed, %d unsupported type groups\n", stats.Total, len(stats.Audit.UnsupportedFileTypes))
	} else {
		fmt.Fprintf(stdout, "Import complete: %d imported, %d skipped, %d failed (of %d total)\n",
			stats.Imported, stats.Skipped, stats.Failed, stats.Total)
	}
	// For a bounded pilot run, echo exactly which documents were selected so
	// the operator can inspect them. Document content is never printed.
	if (*limit > 0 || len(ids) > 0) && len(stats.SelectedIDs) > 0 {
		fmt.Fprintf(stdout, "Selected document IDs: %s\n", joinInts(stats.SelectedIDs))
	}
	if *reportPath != "" {
		if err := paperlessimport.WriteMigrationReport(*reportPath, stats.BuildMigrationReport(*dryRun || *plan)); err != nil {
			return exitcodes.Wrap(err, exitcodes.ExitGeneric, exitcodes.KindInternal,
				"failed to write migration report")
		}
		fmt.Fprintf(stdout, "Migration report written to %s\n", *reportPath)
	}
	if stats.Failed > 0 {
		fmt.Fprintf(stdout, "Re-run the same command to retry failed documents; use --status to inspect them.\n")
		return exitcodes.Wrapf(nil, exitcodes.ExitConflict, exitcodes.KindConflict,
			"import completed with %d failed document(s)", stats.Failed)
	}
	return nil
}

func runNotionImport(args []string) error {
	fs := flag.NewFlagSet("import notion", flag.ContinueOnError)
	dryRun := fs.Bool("dry-run", false, "List what would be imported without writing")
	reportPath := fs.String("report", "", "Write a JSON migration report to this path")
	importRunID := fs.String("import-run-id", "", "Use this run ID for idempotency; re-running with the same ID skips already-imported notes")
	ocrLang, vault, archive, db := registerSharedFlags(fs)
	configureUsage(fs, "import notion [flags] <path>", "Import a Notion Markdown + CSV export into the vault. Provide the path to the unzipped Notion export directory.")

	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" {
		fs.Usage()
		return nil
	}

	help, err := parseFlags(fs, args, "invalid import notion flags")
	if help || err != nil {
		return err
	}
	cfg, err := resolveConfig(fs, ocrLang, vault, archive, db)
	if err != nil {
		return err
	}

	remaining := fs.Args()
	if len(remaining) == 0 {
		fs.Usage()
		return nil
	}

	sourceDir, err := filepath.Abs(remaining[0])
	if err != nil {
		return exitcodes.Wrap(err, exitcodes.ExitData, exitcodes.KindValidation,
			"invalid source path")
	}

	if cfg.vault == "" && !*dryRun {
		return exitcodes.Wrapf(nil, exitcodes.ExitConfig, exitcodes.KindConfig,
			"no vault configured; use --vault, SYMINGEST_VAULT env, or set vault in ~/.config/symingest/config.toml")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	opts := notionimport.Options{
		SourceDir:   sourceDir,
		Vault:       cfg.vault,
		DryRun:      *dryRun,
		ImportRunID: *importRunID,
	}

	stats, err := notionimport.Run(ctx, opts)
	if err != nil {
		return exitcodes.Wrap(err, exitcodes.ExitGeneric, exitcodes.KindInternal,
			"notion import failed")
	}

	if *dryRun {
		fmt.Fprintf(stdout, "Notion import dry-run: %d would-import, %d skipped (of %d total)\n",
			stats.Imported, stats.Skipped, stats.Total)
	} else {
		fmt.Fprintf(stdout, "Notion import complete: %d imported, %d skipped, %d failed (of %d total)\n",
			stats.Imported, stats.Skipped, stats.Failed, stats.Total)
	}
	if *reportPath != "" {
		if err := notionimport.WriteMigrationReport(*reportPath, stats.BuildMigrationReport(*dryRun)); err != nil {
			return exitcodes.Wrap(err, exitcodes.ExitGeneric, exitcodes.KindInternal,
				"failed to write migration report")
		}
		fmt.Fprintf(stdout, "Migration report written to %s\n", *reportPath)
	}
	if stats.Failed > 0 {
		return exitcodes.Wrapf(nil, exitcodes.ExitConflict, exitcodes.KindConflict,
			"notion import completed with %d failed item(s)", stats.Failed)
	}
	return nil
}

// printVerifyReport writes a human-readable migration verification summary.
type paperlessStatusSummary struct {
	BaseURL  string `json:"base_url"`
	Total    int    `json:"total"`
	Imported int    `json:"imported"`
	Skipped  int    `json:"skipped"`
	Failed   int    `json:"failed"`
	Pending  int    `json:"pending"`
}

func buildPaperlessStatusSummary(baseURL string, states []*store.PaperlessImportState) paperlessStatusSummary {
	s := paperlessStatusSummary{BaseURL: baseURL, Total: len(states)}
	for _, st := range states {
		switch st.Status {
		case "imported":
			s.Imported++
		case "skipped":
			s.Skipped++
		case "failed":
			s.Failed++
		case "pending":
			s.Pending++
		}
	}
	return s
}

func printVerifyReport(w io.Writer, r *paperlessimport.VerifyReport) {
	fmt.Fprintf(w, "Migration verification: %d source documents, %d vault notes, %d verified\n",
		r.SourceDocuments, r.VaultNotes, r.Verified)
	if r.DeepVerify {
		fmt.Fprintf(w, "  deep verify: %d Paperless downloads matched archived originals\n", r.DeepVerified)
	}
	if len(r.Missing) > 0 {
		fmt.Fprintf(w, "  missing from vault (%d): %s\n", len(r.Missing), joinInts(r.Missing))
	}
	if len(r.Duplicate) > 0 {
		fmt.Fprintf(w, "  duplicate notes (%d): %s\n", len(r.Duplicate), joinInts(r.Duplicate))
	}
	if len(r.MissingArchive) > 0 {
		fmt.Fprintf(w, "  missing archived original (%d): %s\n", len(r.MissingArchive), joinInts(r.MissingArchive))
	}
	if len(r.HashMismatch) > 0 {
		fmt.Fprintf(w, "  local archive hash mismatches (%d): %s\n", len(r.HashMismatch), joinInts(r.HashMismatch))
	}
	if len(r.SourceHashMismatch) > 0 {
		fmt.Fprintf(w, "  Paperless download hash mismatches (%d): %s\n", len(r.SourceHashMismatch), joinInts(r.SourceHashMismatch))
	}
	if len(r.Mismatches) > 0 {
		fmt.Fprintf(w, "  metadata mismatches (%d):\n", len(r.Mismatches))
		for _, m := range r.Mismatches {
			fmt.Fprintf(w, "    document %d: %s expected %q, got %q\n", m.DocumentID, m.Field, m.Expected, m.Got)
		}
	}
	if r.Complete() {
		fmt.Fprintln(w, "  OK: vault matches the Paperless source")
	}
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
	vault            string
	archive          string
	db               string
	ocrLang          string
	inbox            string
	paperlessBaseURL string
	symseekEnabled   bool
	symseekBinary    string
	raw              config.Config
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
		vault:            *vaultFlag,
		archive:          *archiveFlag,
		db:               *dbFlag,
		ocrLang:          *ocrLang,
		inbox:            cfg.Inbox,
		paperlessBaseURL: cfg.PaperlessBaseURL,
		symseekEnabled:   cfg.SymseekEnabled,
		symseekBinary:    cfg.SymseekBinary,
		raw:              *cfg,
	}, nil
}

type doctorStatus string

const (
	doctorOK   doctorStatus = "ok"
	doctorWarn doctorStatus = "warn"
	doctorFail doctorStatus = "fail"
)

type doctorCheck struct {
	Name    string       `json:"name"`
	Status  doctorStatus `json:"status"`
	Message string       `json:"message"`
}

type doctorReport struct {
	Status   doctorStatus  `json:"status"`
	Checks   []doctorCheck `json:"checks"`
	Failures int           `json:"failures"`
	Warnings int           `json:"warnings"`
}

// doctorIMAPClient is the subset of IMAP operations needed by checkIMAP.
type doctorIMAPClient interface {
	Login(username, password string) error
	Select(folder string) error
	Logout() error
}

type doctorIMAPReal struct {
	client *imapclient.Client
}

func (c *doctorIMAPReal) Login(username, password string) error {
	return c.client.Login(username, password).Wait()
}

func (c *doctorIMAPReal) Select(folder string) error {
	_, err := c.client.Select(folder, nil).Wait()
	return err
}

func (c *doctorIMAPReal) Logout() error {
	return c.client.Logout().Wait()
}

// doctorDialIMAP connects to an IMAP server over TLS. Package-level so tests
// can replace it with a fake.
var doctorDialIMAP = func(addr, host string) (doctorIMAPClient, error) {
	c, err := imapclient.DialTLS(addr, &imapclient.Options{TLSConfig: &tls.Config{ServerName: host}})
	if err != nil {
		return nil, err
	}
	return &doctorIMAPReal{client: c}, nil
}

func checkIMAP(ctx context.Context, report *doctorReport, accounts []config.IMAPAccount) {
	for i, acc := range accounts {
		name := fmt.Sprintf("imap.account.%d", i)

		pwd, err := secret.Resolve(ctx, acc.PasswordSecret)
		if err != nil {
			report.add(name, doctorFail, fmt.Sprintf("cannot resolve password for %s: %v", acc.Username, err))
			continue
		}

		addr := fmt.Sprintf("%s:%d", acc.Host, acc.Port)
		client, err := doctorDialIMAP(addr, acc.Host)
		if err != nil {
			report.add(name, doctorFail, fmt.Sprintf("cannot connect to %s: %v", addr, err))
			continue
		}

		if err := client.Login(acc.Username, pwd); err != nil {
			client.Logout()
			report.add(name, doctorFail, fmt.Sprintf("login failed for %s: %v", acc.Username, err))
			continue
		}

		folder := acc.Folder
		if folder == "" {
			folder = "INBOX"
		}

		if err := client.Select(folder); err != nil {
			client.Logout()
			report.add(name, doctorFail, fmt.Sprintf("cannot select folder %s: %v", folder, err))
			continue
		}

		client.Logout()
		report.add(name, doctorOK, fmt.Sprintf("connected to %s as %s", addr, acc.Username))
	}
}

func (r *doctorReport) add(name string, status doctorStatus, message string) {
	r.Checks = append(r.Checks, doctorCheck{Name: name, Status: status, Message: message})
	switch status {
	case doctorFail:
		r.Failures++
	case doctorWarn:
		r.Warnings++
	}
	if r.Failures > 0 {
		r.Status = doctorFail
	} else if r.Warnings > 0 {
		r.Status = doctorWarn
	} else {
		r.Status = doctorOK
	}
}

func runExtract(args []string) error {
	fs := flag.NewFlagSet("extract", flag.ContinueOnError)
	profile := fs.String("profile", "generic", "Extraction profile (generic, invoice, contract, jobcenter)")
	jsonFlag := fs.Bool("json", false, "Output extractions as JSONL")
	configureUsage(fs, "extract [flags] <file>", "Extract structured data from a file using a deterministic regex/rule-based profile.")
	help, err := parseFlags(fs, args, "invalid extract flags")
	if help || err != nil {
		return err
	}
	remaining := fs.Args()
	if len(remaining) == 0 {
		fs.Usage()
		return nil
	}

	source, err := filepath.Abs(remaining[0])
	if err != nil {
		return exitcodes.Wrap(err, exitcodes.ExitData, exitcodes.KindValidation,
			"invalid source path")
	}

	profileObj, err := annotate.GetProfile(*profile)
	if err != nil {
		return exitcodes.Wrap(err, exitcodes.ExitData, exitcodes.KindValidation,
			fmt.Sprintf("unknown profile %q", *profile))
	}

	kind, err := extract.Detect(source)
	if err != nil {
		return exitcodes.Wrap(err, exitcodes.ExitData, exitcodes.KindValidation,
			fmt.Sprintf("cannot detect file type: %v", err))
	}

	var extractRes *extract.Result
	switch kind {
	case extract.KindText, extract.KindMarkdown, extract.KindCSV:
		extractRes, err = extract.ReadTextKind(context.Background(), source, kind)
	case extract.KindHTML, extract.KindRTF, extract.KindDOCX, extract.KindXLSX, extract.KindODT, extract.KindEML:
		extractRes, err = extract.ReadStructuredKind(context.Background(), source, kind)
	default:
		engine := ocr.DefaultRunner("eng")
		extractRes, err = engine.Extract(context.Background(), source, kind)
	}
	if err != nil {
		return exitcodes.Wrap(err, exitcodes.ExitGeneric, exitcodes.KindInternal,
			"extraction failed")
	}

	extractions := annotate.Extract(profileObj, extractRes.Text)

	if *jsonFlag {
		enc := json.NewEncoder(stdout)
		for _, e := range extractions {
			if err := enc.Encode(e); err != nil {
				return exitcodes.Wrap(err, exitcodes.ExitGeneric, exitcodes.KindInternal,
					"failed to encode extraction")
			}
		}
		if len(extractions) == 0 {
			fmt.Fprintln(stdout, "[]")
		}
		return nil
	}

	fmt.Fprintf(stdout, "Profile: %s\nFile: %s\nText length: %d\nExtractions: %d\n\n",
		*profile, source, len(extractRes.Text), len(extractions))
	for _, e := range extractions {
		spanInfo := ""
		if e.Span != nil {
			spanInfo = fmt.Sprintf(" [%d:%d]", e.Span.Start, e.Span.End)
		}
		fmt.Fprintf(stdout, "  %-20s %s%s\n", e.Field+":", e.Value, spanInfo)
	}
	return nil
}

func runDoctor(args []string) error {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	paperlessFlag := fs.Bool("paperless", false, "Check Paperless API connectivity as well")
	jsonFlag := fs.Bool("json", false, "Output a stable JSON report")
	baseURL := fs.String("base-url", "", "Paperless-ngx URL override (or PAPERLESS_URL / config)")
	token := fs.String("token", "", "Paperless API token override (or PAPERLESS_TOKEN env); never printed")
	inbox := fs.String("inbox", "", "Watch inbox directory override")
	ocrLang, vault, archive, db := registerSharedFlags(fs)
	configureUsage(fs, "doctor [flags]", "Validate local prerequisites, paths, OCR tools and optional Paperless connectivity.")
	help, err := parseFlags(fs, args, "invalid doctor flags")
	if help || err != nil {
		return err
	}
	cfg, err := resolveConfig(fs, ocrLang, vault, archive, db)
	if err != nil {
		return err
	}
	if *inbox != "" {
		cfg.inbox = *inbox
	}
	if *baseURL == "" {
		*baseURL = os.Getenv("PAPERLESS_URL")
	}
	if *baseURL == "" {
		*baseURL = cfg.paperlessBaseURL
	}
	if *token == "" {
		*token = os.Getenv("PAPERLESS_TOKEN")
	}

	report := runDoctorChecks(context.Background(), cfg, *paperlessFlag, *baseURL, *token)
	if *jsonFlag {
		data, err := json.MarshalIndent(report, "", "  ")
		if err != nil {
			return exitcodes.Wrap(err, exitcodes.ExitGeneric, exitcodes.KindInternal, "failed to marshal doctor report")
		}
		fmt.Fprintln(stdout, string(data))
	} else {
		printDoctorReport(stdout, report)
	}
	if report.Failures > 0 {
		return exitcodes.Wrapf(nil, exitcodes.ExitGeneric, exitcodes.KindConfig, "doctor found %d hard blocker(s)", report.Failures)
	}
	if report.Warnings > 0 {
		return exitcodes.Wrapf(nil, exitcodes.ExitNoInput, exitcodes.KindConfig, "doctor found %d warning(s)", report.Warnings)
	}
	return nil
}

func runDoctorChecks(ctx context.Context, cfg *resolvedConfig, includePaperless bool, baseURL, token string) *doctorReport {
	report := &doctorReport{Status: doctorOK}
	if cfg.vault == "" {
		report.add("config.vault", doctorFail, "vault is not configured")
	} else {
		report.add("config.vault", doctorOK, cfg.vault)
		checkWritableDir(report, "path.vault", cfg.vault)
	}
	if cfg.archive == "" {
		report.add("config.archive", doctorFail, "archive path is not configured")
	} else {
		checkWritableDir(report, "path.archive", cfg.archive)
	}
	if cfg.db == "" {
		report.add("config.db", doctorFail, "database path is not configured")
	} else {
		checkWritableDB(report, cfg.db)
	}
	if cfg.ocrLang == "" {
		report.add("config.ocr_lang", doctorWarn, "ocr language not set; defaulting to eng")
	} else {
		report.add("config.ocr_lang", doctorOK, cfg.ocrLang)
	}
	if cfg.inbox == "" {
		report.add("config.inbox", doctorWarn, "inbox is not configured; watch mode requires an explicit directory")
	} else {
		checkWritableDir(report, "path.inbox", cfg.inbox)
	}
	checkCommand(report, "tool.pdftoppm", "pdftoppm", doctorFail)
	checkTesseract(report, cfg.ocrLang)
	if runtime.GOOS == "darwin" {
		checkCommand(report, "tool.sips", "sips", doctorWarn)
	}
	checkOptionalCommand(report, "tool.optional.textutil", "textutil")
	checkOptionalCommand(report, "tool.optional.pandoc", "pandoc")
	checkOptionalCommand(report, "tool.optional.libreoffice", "libreoffice")
	checkOptionalCommand(report, "tool.optional.soffice", "soffice")
	checkOptionalCommand(report, "tool.optional.pdfinfo", "pdfinfo")
	checkOptionalCommand(report, "tool.optional.pdfseparate", "pdfseparate")
	checkOptionalCommand(report, "tool.optional.pdfunite", "pdfunite")
	checkOptionalCommand(report, "tool.optional.qpdf", "qpdf")
	if len(cfg.raw.IMAPAccounts) > 0 {
		checkIMAP(ctx, report, cfg.raw.IMAPAccounts)
	}
	if includePaperless {
		checkPaperless(ctx, report, baseURL, token)
	}
	return report
}

func checkWritableDir(report *doctorReport, name, dir string) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		report.add(name, doctorFail, fmt.Sprintf("cannot create directory: %v", err))
		return
	}
	f, err := os.CreateTemp(dir, ".symingest-doctor-*")
	if err != nil {
		report.add(name, doctorFail, fmt.Sprintf("not writable: %v", err))
		return
	}
	path := f.Name()
	if err := f.Close(); err != nil {
		report.add(name, doctorFail, fmt.Sprintf("temp file close failed: %v", err))
		return
	}
	_ = os.Remove(path)
	report.add(name, doctorOK, dir)
}

func checkWritableDB(report *doctorReport, dbPath string) {
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o700); err != nil {
		report.add("path.db", doctorFail, fmt.Sprintf("cannot create database directory: %v", err))
		return
	}
	st, err := store.Open(dbPath)
	if err != nil {
		report.add("path.db", doctorFail, fmt.Sprintf("cannot open database: %v", err))
		return
	}
	if err := st.Close(); err != nil {
		report.add("path.db", doctorFail, fmt.Sprintf("cannot close database: %v", err))
		return
	}
	report.add("path.db", doctorOK, dbPath)
}

func checkCommand(report *doctorReport, name, command string, missing doctorStatus) {
	path, err := exec.LookPath(command)
	if err != nil {
		report.add(name, missing, fmt.Sprintf("%s not found in PATH", command))
		return
	}
	report.add(name, doctorOK, path)
}

func checkOptionalCommand(report *doctorReport, name, command string) {
	path, err := exec.LookPath(command)
	if err != nil {
		report.add(name, doctorOK, fmt.Sprintf("%s not found in PATH (optional)", command))
		return
	}
	report.add(name, doctorOK, path)
}

func checkTesseract(report *doctorReport, lang string) {
	path, err := exec.LookPath("tesseract")
	if err != nil {
		report.add("tool.tesseract", doctorFail, "tesseract not found in PATH")
		return
	}
	out, err := exec.Command(path, "--list-langs").CombinedOutput()
	if err != nil {
		report.add("tool.tesseract", doctorFail, fmt.Sprintf("tesseract --list-langs failed: %v", err))
		return
	}
	if lang != "" && !languageListed(string(out), lang) {
		report.add("tool.tesseract.lang", doctorFail, fmt.Sprintf("language %q is not installed", lang))
		return
	}
	report.add("tool.tesseract", doctorOK, path)
}

func languageListed(output, lang string) bool {
	for _, line := range strings.Split(output, "\n") {
		if strings.TrimSpace(line) == lang {
			return true
		}
	}
	return false
}

func checkPaperless(ctx context.Context, report *doctorReport, baseURL, token string) {
	if strings.TrimSpace(baseURL) == "" {
		report.add("paperless.url", doctorFail, "Paperless base URL is not configured")
		return
	}
	if strings.TrimSpace(token) == "" {
		report.add("paperless.token", doctorFail, "Paperless token is missing (set PAPERLESS_TOKEN or pass --token)")
		return
	}
	url := strings.TrimRight(baseURL, "/") + "/api/documents/?page_size=1"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		report.add("paperless.api", doctorFail, fmt.Sprintf("invalid Paperless URL: %v", err))
		return
	}
	req.Header.Set("Authorization", "Token "+token)
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		report.add("paperless.api", doctorFail, fmt.Sprintf("request failed: %v", err))
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		report.add("paperless.api", doctorFail, fmt.Sprintf("unexpected HTTP status %s", resp.Status))
		return
	}
	var payload struct {
		Count   int               `json:"count"`
		Results []json.RawMessage `json:"results"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		report.add("paperless.api", doctorFail, fmt.Sprintf("unexpected response JSON: %v", err))
		return
	}
	report.add("paperless.api", doctorOK, fmt.Sprintf("reachable; %d documents reported", payload.Count))
}

func printDoctorReport(w io.Writer, report *doctorReport) {
	fmt.Fprintf(w, "symingest doctor: %s (%d failures, %d warnings)\n", strings.ToUpper(string(report.Status)), report.Failures, report.Warnings)
	for _, c := range report.Checks {
		fmt.Fprintf(w, "[%s] %s: %s\n", strings.ToUpper(string(c.Status)), c.Name, c.Message)
	}
}

type setupConfig struct {
	Vault            string
	ArchivePath      string
	DBPath           string
	Inbox            string
	OCRLang          string
	PaperlessBaseURL string
}

func runSetup(args []string) error {
	fs := flag.NewFlagSet("setup", flag.ContinueOnError)
	vault := fs.String("vault", "", "Target vault directory")
	archive := fs.String("archive", "", "Target archive directory")
	db := fs.String("db", "", "SQLite database path")
	inbox := fs.String("inbox", "", "Watch inbox directory")
	ocrLang := fs.String("ocr-lang", "eng", "Default OCR language")
	paperlessBaseURL := fs.String("paperless-base-url", "", "Paperless base URL; tokens are never written")
	configPath := fs.String("config", "", "Config file path override (default: XDG config path)")
	dryRun := fs.Bool("dry-run", false, "Print a diff without writing")
	force := fs.Bool("force", false, "Overwrite an existing different config")
	configureUsage(fs, "setup [flags]", "Generate an idempotent production config file without storing secrets.")
	help, err := parseFlags(fs, args, "invalid setup flags")
	if help || err != nil {
		return err
	}
	if *configPath == "" {
		path, err := defaultConfigPath()
		if err != nil {
			return err
		}
		*configPath = path
	}
	if *archive == "" {
		path, err := defaultArchivePath()
		if err != nil {
			return err
		}
		*archive = path
	}
	if *db == "" {
		path, err := defaultDBPath()
		if err != nil {
			return err
		}
		*db = path
	}
	if strings.TrimSpace(*vault) == "" {
		return exitcodes.Wrapf(nil, exitcodes.ExitData, exitcodes.KindValidation, "--vault is required")
	}
	if strings.TrimSpace(*inbox) == "" {
		return exitcodes.Wrapf(nil, exitcodes.ExitData, exitcodes.KindValidation, "--inbox is required")
	}
	if strings.TrimSpace(*paperlessBaseURL) == "" {
		return exitcodes.Wrapf(nil, exitcodes.ExitData, exitcodes.KindValidation, "--paperless-base-url is required")
	}
	cfg := setupConfig{Vault: *vault, ArchivePath: *archive, DBPath: *db, Inbox: *inbox, OCRLang: *ocrLang, PaperlessBaseURL: *paperlessBaseURL}
	content := renderSetupConfig(cfg)
	current, readErr := os.ReadFile(*configPath)
	if readErr != nil && !errors.Is(readErr, os.ErrNotExist) {
		return exitcodes.Wrap(readErr, exitcodes.ExitConfig, exitcodes.KindConfig, "failed to read existing config")
	}
	if *dryRun {
		fmt.Fprintf(stdout, "Config dry-run: %s\n", *configPath)
		printConfigDiff(stdout, string(current), content)
		return nil
	}
	if readErr == nil && string(current) == content {
		fmt.Fprintf(stdout, "Config already up to date: %s\n", *configPath)
		return nil
	}
	if readErr == nil && !*force {
		fmt.Fprintf(stdout, "Existing config differs: %s\n", *configPath)
		printConfigDiff(stdout, string(current), content)
		return exitcodes.Wrapf(nil, exitcodes.ExitConflict, exitcodes.KindConflict, "config differs; rerun with --force to overwrite")
	}
	if err := os.MkdirAll(filepath.Dir(*configPath), 0o700); err != nil {
		return exitcodes.Wrap(err, exitcodes.ExitConfig, exitcodes.KindConfig, "failed to create config directory")
	}
	if err := os.WriteFile(*configPath, []byte(content), 0o600); err != nil {
		return exitcodes.Wrap(err, exitcodes.ExitConfig, exitcodes.KindConfig, "failed to write config")
	}
	fmt.Fprintf(stdout, "Config written: %s\n", *configPath)
	fmt.Fprintln(stdout, "Paperless token not written; use PAPERLESS_TOKEN or a secret manager.")
	config.Loader.ResetCache()
	return nil
}

func defaultConfigPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", exitcodes.Wrapf(err, exitcodes.ExitConfig, exitcodes.KindConfig, "cannot determine home directory; use --config explicitly")
	}
	return filepath.Join(home, ".config", "symingest", "config.toml"), nil
}

func renderSetupConfig(cfg setupConfig) string {
	return fmt.Sprintf("vault = %q\narchive_path = %q\ndb_path = %q\ninbox = %q\nocr_lang = %q\npaperless_base_url = %q\n", cfg.Vault, cfg.ArchivePath, cfg.DBPath, cfg.Inbox, cfg.OCRLang, cfg.PaperlessBaseURL)
}

func printConfigDiff(w io.Writer, old, new string) {
	if old == new {
		fmt.Fprintln(w, "No changes.")
		return
	}
	if old != "" {
		fmt.Fprintln(w, "--- current")
		for _, line := range strings.Split(strings.TrimRight(old, "\n"), "\n") {
			fmt.Fprintf(w, "- %s\n", line)
		}
	}
	fmt.Fprintln(w, "+++ proposed")
	for _, line := range strings.Split(strings.TrimRight(new, "\n"), "\n") {
		fmt.Fprintf(w, "+ %s\n", line)
	}
}

type stringList []string

func (s *stringList) String() string { return strings.Join(*s, ",") }
func (s *stringList) Set(v string) error {
	for _, part := range strings.Split(v, ",") {
		part = strings.TrimSpace(part)
		if part != "" {
			*s = append(*s, part)
		}
	}
	return nil
}

func runValidateVault(args []string) error {
	fs := flag.NewFlagSet("validate-vault", flag.ContinueOnError)
	jsonFlag := fs.Bool("json", false, "Output validation failures as JSON")
	minBodyLength := fs.Int("min-body-length", 0, "Fail notes whose extracted Markdown body is shorter than this many non-whitespace bytes")
	configureUsage(fs, "validate-vault [flags] <vault>", "Validate symingest Markdown frontmatter, archive links, hashes, Paperless IDs and optional OCR/text body length gates.")
	help, err := parseFlags(fs, args, "invalid validate-vault flags")
	if help || err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return exitcodes.Wrapf(nil, exitcodes.ExitNoInput, exitcodes.KindValidation, "validate-vault requires a vault path")
	}
	if *minBodyLength < 0 {
		return exitcodes.Wrapf(nil, exitcodes.ExitData, exitcodes.KindValidation, "min-body-length must be zero or positive")
	}
	report, err := vaultreview.ValidateVaultWithOptions(fs.Arg(0), vaultreview.ValidationOptions{MinBodyLength: *minBodyLength})
	if err != nil {
		return exitcodes.Wrap(err, exitcodes.ExitGeneric, exitcodes.KindInternal, "vault validation failed")
	}
	if *jsonFlag {
		data, err := json.MarshalIndent(report, "", "  ")
		if err != nil {
			return exitcodes.Wrap(err, exitcodes.ExitGeneric, exitcodes.KindInternal, "failed to marshal validation report")
		}
		fmt.Fprintln(stdout, string(data))
	} else {
		fmt.Fprintf(stdout, "Vault validation: %d files, %d failures\n", report.Files, len(report.Failures))
		for _, f := range report.Failures {
			fmt.Fprintf(stdout, "%s: %s: %s\n", f.File, f.Check, f.Message)
		}
	}
	if !report.OK() {
		return exitcodes.Wrapf(nil, exitcodes.ExitConflict, exitcodes.KindConflict, "vault validation found %d failures", len(report.Failures))
	}
	return nil
}

func correctionFromFlags(fs *flag.FlagSet, paperlessID *int, addTags, removeTags *stringList, correspondent, documentType, storagePath *string) vaultreview.Correction {
	c := vaultreview.Correction{PaperlessID: *paperlessID, AddTags: []string(*addTags), RemoveTags: []string(*removeTags)}
	visited := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { visited[f.Name] = true })
	if visited["correspondent"] {
		c.Correspondent = correspondent
	}
	if visited["document-type"] {
		c.DocumentType = documentType
	}
	if visited["storage-path"] {
		c.StoragePath = storagePath
	}
	return c
}

func printUpdateResults(results []vaultreview.UpdateResult) {
	for _, r := range results {
		mode := "updated"
		if r.DryRun {
			mode = "would update"
		} else if !r.Written {
			mode = "unchanged"
		}
		backup := ""
		if r.BackupPath != "" {
			backup = " backup=" + r.BackupPath
		}
		fmt.Fprintf(stdout, "%s: paperless_id=%d %s (%s)%s\n", mode, r.PaperlessID, r.File, strings.Join(r.Changes, ", "), backup)
	}
}

func runUpdate(args []string) error {
	fs := flag.NewFlagSet("update", flag.ContinueOnError)
	vault := fs.String("vault", "", "Vault path")
	paperlessID := fs.Int("paperless-id", 0, "Paperless document ID to update")
	dryRun := fs.Bool("dry-run", false, "Show exact frontmatter changes without writing")
	var addTags, removeTags stringList
	fs.Var(&addTags, "add-tag", "Tag to add (repeatable or comma-separated)")
	fs.Var(&removeTags, "remove-tag", "Tag to remove (repeatable or comma-separated; inbox is protected)")
	correspondent := fs.String("correspondent", "", "Set correspondent")
	documentType := fs.String("document-type", "", "Set document type")
	storagePath := fs.String("storage-path", "", "Set Paperless storage path metadata")
	configureUsage(fs, "update [flags]", "Safely update one note frontmatter by Paperless ID.")
	help, err := parseFlags(fs, args, "invalid update flags")
	if help || err != nil {
		return err
	}
	if *vault == "" {
		return exitcodes.Wrapf(nil, exitcodes.ExitNoInput, exitcodes.KindValidation, "--vault is required")
	}
	res, err := vaultreview.ApplyCorrection(*vault, correctionFromFlags(fs, paperlessID, &addTags, &removeTags, correspondent, documentType, storagePath), *dryRun)
	if err != nil {
		return exitcodes.Wrap(err, exitcodes.ExitData, exitcodes.KindValidation, "update failed")
	}
	printUpdateResults([]vaultreview.UpdateResult{*res})
	return nil
}

func runBulkUpdate(args []string) error {
	fs := flag.NewFlagSet("bulk-update", flag.ContinueOnError)
	vault := fs.String("vault", "", "Vault path")
	where := fs.String("where", "", "Selector, currently tag:<name>")
	dryRun := fs.Bool("dry-run", false, "Show exact frontmatter changes without writing")
	maxUpdates := fs.Int("max", 0, "Refuse when matched corrections exceed this count; 0 disables")
	requireCount := fs.Int("require-count", 0, "Refuse unless exactly this many notes match; 0 disables")
	backupDir := fs.String("backup-dir", "", "Directory for undo backups before writes; default .symingest-backups next to each note")
	paperlessID := fs.Int("paperless-id", 0, "Ignored for bulk updates")
	var addTags, removeTags stringList
	fs.Var(&addTags, "add-tag", "Tag to add")
	fs.Var(&removeTags, "remove-tag", "Tag to remove; inbox is protected")
	correspondent := fs.String("correspondent", "", "Set correspondent")
	documentType := fs.String("document-type", "", "Set document type")
	storagePath := fs.String("storage-path", "", "Set Paperless storage path metadata")
	configureUsage(fs, "bulk-update [flags]", "Safely update multiple notes selected by frontmatter.")
	help, err := parseFlags(fs, args, "invalid bulk-update flags")
	if help || err != nil {
		return err
	}
	if *vault == "" || !strings.HasPrefix(*where, "tag:") {
		return exitcodes.Wrapf(nil, exitcodes.ExitNoInput, exitcodes.KindValidation, "--vault and --where tag:<name> are required")
	}
	results, err := vaultreview.BulkUpdateByTagWithOptions(*vault, strings.TrimPrefix(*where, "tag:"), correctionFromFlags(fs, paperlessID, &addTags, &removeTags, correspondent, documentType, storagePath), vaultreview.BulkUpdateOptions{DryRun: *dryRun, Max: *maxUpdates, RequireCount: *requireCount, BackupDir: *backupDir})
	if err != nil {
		return exitcodes.Wrap(err, exitcodes.ExitData, exitcodes.KindValidation, "bulk update failed")
	}
	printUpdateResults(results)
	return nil
}

func runApplyCorrections(args []string) error {
	fs := flag.NewFlagSet("apply-corrections", flag.ContinueOnError)
	vault := fs.String("vault", "", "Vault path")
	dryRun := fs.Bool("dry-run", false, "Show exact frontmatter changes without writing")
	maxUpdates := fs.Int("max", 0, "Refuse when corrections exceed this count; 0 disables")
	requireCount := fs.Int("require-count", 0, "Refuse unless corrections.yaml contains exactly this many entries; 0 disables")
	backupDir := fs.String("backup-dir", "", "Directory for undo backups before writes; default .symingest-backups next to each note")
	configureUsage(fs, "apply-corrections [flags] <corrections.yaml>", "Apply YAML corrections keyed by paperless_id.")
	help, err := parseFlags(fs, args, "invalid apply-corrections flags")
	if help || err != nil {
		return err
	}
	if *vault == "" || fs.NArg() != 1 {
		return exitcodes.Wrapf(nil, exitcodes.ExitNoInput, exitcodes.KindValidation, "--vault and corrections.yaml are required")
	}
	results, err := vaultreview.ApplyCorrectionsFileWithOptions(*vault, fs.Arg(0), vaultreview.ApplyOptions{DryRun: *dryRun, Max: *maxUpdates, RequireCount: *requireCount, BackupDir: *backupDir})
	if err != nil {
		return exitcodes.Wrap(err, exitcodes.ExitData, exitcodes.KindValidation, "apply corrections failed")
	}
	printUpdateResults(results)
	return nil
}

func runReviewReport(args []string) error {
	fs := flag.NewFlagSet("review-report", flag.ContinueOnError)
	htmlPath := fs.String("html", "", "Write a local HTML review report")
	jsonFlag := fs.Bool("json", false, "Output filtered review rows as JSON")
	failed := fs.Bool("failed", false, "Show failed documents")
	warningsOnly := fs.Bool("warnings", false, "Show documents with warnings or errors")
	missingMetadata := fs.Bool("missing-metadata", false, "Show documents missing key metadata paths/MIME")
	lowBody := fs.Bool("low-body", false, "Show documents with low/short body warnings")
	duplicateContent := fs.Bool("duplicate-content", false, "Show verify findings for duplicate original bytes across Paperless IDs")
	unsupported := fs.Bool("unsupported", false, "Show unsupported format findings")
	unresolved := fs.Bool("unresolved", false, "Show unresolved metadata reference findings")
	configureUsage(fs, "review-report [flags] <migration-or-verify-report.json>", "Generate a human-reviewable migration/verify report without document body text.")
	help, err := parseFlags(fs, args, "invalid review-report flags")
	if help || err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return exitcodes.Wrapf(nil, exitcodes.ExitNoInput, exitcodes.KindValidation, "review-report requires migration-or-verify-report.json")
	}
	report, err := vaultreview.BuildReviewReport(fs.Arg(0), vaultreview.ReviewFilters{Failed: *failed, Warnings: *warningsOnly, MissingMetadata: *missingMetadata, LowBody: *lowBody, DuplicateContent: *duplicateContent, Unsupported: *unsupported, Unresolved: *unresolved})
	if err != nil {
		return exitcodes.Wrap(err, exitcodes.ExitData, exitcodes.KindValidation, "review report failed")
	}
	if *htmlPath != "" {
		if err := vaultreview.WriteReviewHTML(*htmlPath, report); err != nil {
			return exitcodes.Wrap(err, exitcodes.ExitGeneric, exitcodes.KindInternal, "failed to write HTML review report")
		}
		fmt.Fprintf(stdout, "Review HTML written to %s\n", *htmlPath)
	}
	if *jsonFlag {
		data, err := json.MarshalIndent(report, "", "  ")
		if err != nil {
			return exitcodes.Wrap(err, exitcodes.ExitGeneric, exitcodes.KindInternal, "failed to marshal review report")
		}
		fmt.Fprintln(stdout, string(data))
		return nil
	}
	for _, d := range report.Documents {
		fmt.Fprintf(stdout, "document %d: %s mime=%s vault=%s archive=%s error=%s warnings=%d\n", d.ID, d.Status, d.MIME, d.VaultPath, d.ArchivePath, d.Error, len(d.Warnings))
	}
	return nil
}

type reportValidationResult struct {
	Path          string   `json:"path"`
	Kind          string   `json:"kind"`
	SchemaVersion int      `json:"schema_version"`
	ToolVersion   string   `json:"tool_version,omitempty"`
	Valid         bool     `json:"valid"`
	Errors        []string `json:"errors,omitempty"`
}

func runReport(args []string) error {
	fs := flag.NewFlagSet("report", flag.ContinueOnError)
	jsonFlag := fs.Bool("json", false, "Output validation result as JSON")
	configureUsage(fs, "report [flags] validate <report.json>", "Validate a machine-readable migration, verify, or cutover JSON report.")
	help, err := parseFlags(fs, args, "invalid report flags")
	if help || err != nil {
		return err
	}
	remaining := fs.Args()
	if len(remaining) != 2 || remaining[0] != "validate" {
		fs.Usage()
		return nil
	}
	result := validateReportFile(remaining[1])
	if *jsonFlag {
		data, err := json.MarshalIndent(result, "", "  ")
		if err != nil {
			return exitcodes.Wrap(err, exitcodes.ExitGeneric, exitcodes.KindInternal, "failed to marshal report validation")
		}
		fmt.Fprintln(stdout, string(data))
	} else if result.Valid {
		fmt.Fprintf(stdout, "report valid: %s (%s schema_version=%d)\n", result.Path, result.Kind, result.SchemaVersion)
	} else {
		fmt.Fprintf(stdout, "report invalid: %s\n", result.Path)
		for _, e := range result.Errors {
			fmt.Fprintf(stdout, "- %s\n", e)
		}
	}
	if !result.Valid {
		return exitcodes.Wrapf(nil, exitcodes.ExitData, exitcodes.KindValidation, "report validation failed")
	}
	return nil
}

func validateReportFile(path string) reportValidationResult {
	result := reportValidationResult{Path: path, Valid: true}
	data, err := os.ReadFile(path)
	if err != nil {
		result.Valid = false
		result.Errors = append(result.Errors, err.Error())
		return result
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		result.Valid = false
		result.Errors = append(result.Errors, "invalid JSON: "+err.Error())
		return result
	}
	if v, ok := raw["schema_version"].(float64); ok {
		result.SchemaVersion = int(v)
	}
	if v, ok := raw["tool_version"].(string); ok {
		result.ToolVersion = v
	}
	switch {
	case raw["ready"] != nil && raw["checks"] != nil:
		result.Kind = "cutover"
		var report vaultreview.CutoverReport
		if err := json.Unmarshal(data, &report); err != nil {
			result.Errors = append(result.Errors, "invalid cutover report: "+err.Error())
		}
	case raw["ok"] != nil && raw["checks"] != nil && raw["passed"] != nil && raw["failed"] != nil:
		result.Kind = "search"
		var report symseekint.ValidationReport
		if err := json.Unmarshal(data, &report); err != nil {
			result.Errors = append(result.Errors, "invalid search validation report: "+err.Error())
		}
	case raw["source_documents"] != nil && raw["verified"] != nil:
		result.Kind = "verify"
		var report paperlessimport.VerifyReport
		if err := json.Unmarshal(data, &report); err != nil {
			result.Errors = append(result.Errors, "invalid verify report: "+err.Error())
		}
	case raw["documents"] != nil && raw["total"] != nil:
		result.Kind = "migration"
		var report paperlessimport.MigrationReport
		if err := json.Unmarshal(data, &report); err != nil {
			result.Errors = append(result.Errors, "invalid migration report: "+err.Error())
		}
	default:
		result.Errors = append(result.Errors, "unknown report kind")
	}
	if result.SchemaVersion != paperlessimport.ReportSchemaVersion {
		result.Errors = append(result.Errors, fmt.Sprintf("schema_version=%d; expected %d", result.SchemaVersion, paperlessimport.ReportSchemaVersion))
	}
	if result.Kind != "cutover" && result.ToolVersion == "" {
		result.Errors = append(result.Errors, "missing tool_version")
	}
	if len(result.Errors) > 0 {
		result.Valid = false
	}
	return result
}

func runCutoverCheck(args []string) error {
	fs := flag.NewFlagSet("cutover-check", flag.ContinueOnError)
	dryRunReport := fs.String("dry-run-report", "", "Full dry-run JSON report produced by 'symingest import paperless --dry-run --report'")
	importReport := fs.String("import-report", "", "Full real-import JSON report produced by 'symingest import paperless --report'")
	verifyReport := fs.String("verify-report", "", "Verifier JSON report produced by 'symingest import paperless --verify --json'")
	searchReport := fs.String("search-report", "", "Search validation JSON report produced by 'symingest search validate --report'")
	vault := fs.String("vault", "", "Vault path to validate")
	minDocuments := fs.Int("min-documents", 0, "Minimum source document count expected before cutover")
	minBodyLength := fs.Int("min-body-length", 0, "Fail cutover if any note body is shorter than this many non-whitespace bytes")
	jsonFlag := fs.Bool("json", false, "Output the cutover report as JSON")
	configureUsage(fs, "cutover-check [flags]", "Gate whether Paperless-ngx can stop being the source of truth. Requires the dry-run, real import, verify, and vault-validation evidence from the replacement runbook.")
	help, err := parseFlags(fs, args, "invalid cutover-check flags")
	if help || err != nil {
		return err
	}
	report, err := vaultreview.BuildCutoverReport(vaultreview.CutoverOptions{
		DryRunReportPath: *dryRunReport,
		ImportReportPath: *importReport,
		VerifyReportPath: *verifyReport,
		SearchReportPath: *searchReport,
		VaultPath:        *vault,
		MinDocuments:     *minDocuments,
		MinBodyLength:    *minBodyLength,
	})
	if err != nil {
		return exitcodes.Wrap(err, exitcodes.ExitData, exitcodes.KindValidation, "cutover check failed")
	}
	if *jsonFlag {
		data, err := json.MarshalIndent(report, "", "  ")
		if err != nil {
			return exitcodes.Wrap(err, exitcodes.ExitGeneric, exitcodes.KindInternal, "failed to marshal cutover report")
		}
		fmt.Fprintln(stdout, string(data))
	} else {
		status := "BLOCKED"
		if report.Ready {
			status = "READY"
		}
		fmt.Fprintf(stdout, "Paperless replacement cutover: %s\n", status)
		for _, c := range report.Checks {
			fmt.Fprintf(stdout, "[%s] %s: %s\n", c.Status, c.Name, c.Message)
		}
	}
	if !report.Ready {
		return exitcodes.Wrapf(nil, exitcodes.ExitConflict, exitcodes.KindConflict,
			"cutover blocked by %d issue(s)", len(report.Blockers))
	}
	return nil
}

func runMCP(args []string) error {
	fs := flag.NewFlagSet("mcp", flag.ContinueOnError)
	ocrLang, vault, archive, db := registerSharedFlags(fs)
	configureUsage(fs, "mcp [flags]", "Start the MCP server for AI-powered document processing.")
	help, err := parseFlags(fs, args, "invalid mcp flags")
	if help || err != nil {
		return err
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

	server := newMCPServer(st, cfg.ocrLang, cfg.vault, cfg.archive)

	ctx := context.Background()
	if err := server.ServeStdio(ctx); err != nil {
		return exitcodes.Wrap(err, exitcodes.ExitSoftware, exitcodes.KindInternal,
			"mcp server failed")
	}
	return nil
}

func newMCPServer(st *store.Store, ocrLang, vault, archive string) *mcpserver.Server {
	engine := ocr.DefaultRunner(ocrLang)
	server := mcpserver.New("symingest", version.Version)
	mcp.Register(server, st, engine, vault, archive)
	return server
}

func runWatch(args []string) error {
	fs := flag.NewFlagSet("watch", flag.ContinueOnError)
	processingDir := fs.String("processing-dir", "", "Move stable files here before enqueueing them")
	processedDir := fs.String("processed-dir", "", "Move successfully processed source files here")
	failedDir := fs.String("failed-dir", "", "Move failed source files here and write .error.json sidecars")
	stableFor := fs.Duration("stable-for", time.Second, "How long a file must remain unchanged before enqueueing")
	ocrLang, vault, archive, db := registerSharedFlags(fs)
	configureUsage(fs, "watch [flags] <dir>", "Watch a directory for new or modified files and ingest them in the background.")
	help, err := parseFlags(fs, args, "invalid watch flags")
	if help || err != nil {
		return err
	}
	cfg, err := resolveConfig(fs, ocrLang, vault, archive, db)
	if err != nil {
		return err
	}
	remaining := fs.Args()
	if len(remaining) == 0 {
		fs.Usage()
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

	lock, err := acquireWatchLock(inboxDir, cfg.db)
	if err != nil {
		return exitcodes.Wrap(err, exitcodes.ExitGeneric, exitcodes.KindInternal,
			"watcher lock refused duplicate start")
	}
	defer lock.Release()

	// Reset any running jobs to pending on startup
	if err := st.ResetRunningJobs(ctx); err != nil {
		return exitcodes.Wrap(err, exitcodes.ExitConfig, exitcodes.KindInternal,
			"failed to reset running jobs")
	}

	watcher, err := ingest.NewWatcherWithOptions(st, inboxDir, ingest.WatcherOptions{
		StableFor:     *stableFor,
		ProcessingDir: *processingDir,
		ProcessedDir:  *processedDir,
		FailedDir:     *failedDir,
	})
	if err != nil {
		return exitcodes.Wrap(err, exitcodes.ExitConfig, exitcodes.KindInternal,
			"failed to initialize watcher")
	}
	defer watcher.Close()

	engine := ocr.DefaultRunner(cfg.ocrLang)
	pipeline := &ingest.Pipeline{
		Engine:       engine,
		Store:        st,
		Writer:       &writer.NoteWriter{Vault: cfg.vault},
		ArchiveDir:   cfg.archive,
		ProcessedDir: *processedDir,
		FailedDir:    *failedDir,
	}
	configurePostIndex(pipeline, cfg)

	if err := watcher.Start(ctx); err != nil {
		return exitcodes.Wrap(err, exitcodes.ExitConfig, exitcodes.KindInternal,
			"failed to start watcher")
	}

	var mailPoller *ingest.MailPoller
	if len(cfg.raw.IMAPAccounts) > 0 {
		pollInterval, err := time.ParseDuration(cfg.raw.IMAPPollInterval)
		if err != nil {
			return exitcodes.Wrapf(nil, exitcodes.ExitConfig, exitcodes.KindConfig,
				"imap_poll_interval: invalid duration %q", cfg.raw.IMAPPollInterval)
		}
		mailPoller, err = ingest.NewMailPoller(st, cfg.raw.IMAPAccounts, ingest.MailPollerOptions{
			Interval:      pollInterval,
			ProcessingDir: *processingDir,
			FailedDir:     *failedDir,
		})
		if err != nil {
			return exitcodes.Wrap(err, exitcodes.ExitConfig, exitcodes.KindInternal, "failed to initialize mail poller")
		}
		if err := mailPoller.Start(ctx); err != nil {
			return exitcodes.Wrap(err, exitcodes.ExitConfig, exitcodes.KindInternal, "failed to start mail poller")
		}
		defer mailPoller.Close()
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
	configureUsage(fs, "jobs [flags]", "List ingestion jobs in the queue.")
	help, err := parseFlags(fs, args, "invalid jobs flags")
	if help || err != nil {
		return err
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
	configureUsage(fs, "retry [flags] <job-id>", "Retry a failed job by resetting its status to pending.")
	help, err := parseFlags(fs, args, "invalid retry flags")
	if help || err != nil {
		return err
	}
	cfg, err := resolveConfig(fs, ocrLang, vault, archive, db)
	if err != nil {
		return err
	}
	remaining := fs.Args()
	if len(remaining) == 0 {
		fs.Usage()
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
	jsonFlag := fs.Bool("json", false, "Output rules in JSON format")
	ocrLang, vault, archive, db := registerSharedFlags(fs)
	configureUsage(fs, "rules [flags] [command]", "Manage classification rules. Patterns are case-insensitive substrings matched against extracted document text, not filename globs.\n\nCommands:\n  list                                  List all classification rules\n  add <pattern> <kind> <value>          Add a classification rule\n  update <id> <pattern> <kind> <value>  Update a classification rule\n  test <text>                           Test rules against text\n  dry-run <pattern> <kind> <value>      Test a proposed rule against existing notes\n  delete <id>                           Delete a classification rule by ID\n\nKinds for add/update command: category, tag, correspondent, document_type")
	help, err := parseFlags(fs, args, "invalid rules flags")
	if help || err != nil {
		return err
	}
	cfg, err := resolveConfig(fs, ocrLang, vault, archive, db)
	if err != nil {
		return err
	}

	remaining := fs.Args()
	if len(remaining) == 0 {
		fs.Usage()
		return nil
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
		return listRules(ctx, st, *jsonFlag)
	case "add":
		return addRule(ctx, st, remaining[1:], *jsonFlag)
	case "update":
		return updateRule(ctx, st, remaining[1:], *jsonFlag)
	case "test":
		return testRules(ctx, st, remaining[1:], *jsonFlag)
	case "dry-run":
		if len(remaining) < 4 {
			return exitcodes.Wrapf(nil, exitcodes.ExitData, exitcodes.KindValidation,
				"missing arguments; usage: symingest rules dry-run <pattern> <kind> <value>")
		}
		if cfg.vault == "" {
			return exitcodes.Wrapf(nil, exitcodes.ExitConfig, exitcodes.KindConfig,
				"no vault configured; use --vault or set vault in config")
		}
		response, err := dryRunRuleAgainstDocuments(ctx, st, cfg.vault, remaining[1], remaining[2], remaining[3])
		if err != nil {
			return err
		}
		if *jsonFlag {
			return printRulesJSON(response, "failed to marshal rules dry-run result")
		}
		formatDryRunHuman(response)
		return nil
	case "delete":
		return deleteRule(ctx, st, remaining[1:], *jsonFlag)
	default:
		return exitcodes.Wrapf(nil, exitcodes.ExitNoInput, exitcodes.KindValidation,
			"unknown rules subcommand %q", remaining[0])
	}
}

func printRulesUsage() error {
	fmt.Fprintln(stdout, `Usage: symingest rules [flags] [command]

Commands:
  list                                  List all classification rules
  add <pattern> <kind> <value>          Add a classification rule
  update <id> <pattern> <kind> <value>  Update a classification rule
  test <text>                           Test rules against text
  dry-run <pattern> <kind> <value>      Test a proposed rule against existing notes
  delete <id>                           Delete a classification rule by ID

Patterns are case-insensitive substrings matched against extracted document text, not filename globs.
Kinds for add command: category, tag, correspondent, document_type`)
	return nil
}

func listRules(ctx context.Context, st *store.Store, outputJSON bool) error {
	rules, err := st.ListRules(ctx)
	if err != nil {
		return exitcodes.Wrap(err, exitcodes.ExitGeneric, exitcodes.KindInternal,
			"failed to list rules")
	}

	if outputJSON {
		if rules == nil {
			rules = []*store.ClassificationRule{}
		}
		return printRulesJSON(rulesListJSONResponse{
			SchemaVersion: rulesJSONSchemaVersion,
			Rules:         rules,
		}, "failed to marshal rules to JSON")
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

func addRule(ctx context.Context, st *store.Store, args []string, outputJSON bool) error {
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

	if outputJSON {
		return printRulesJSON(rulesRuleJSONResponse{
			SchemaVersion: rulesJSONSchemaVersion,
			Rule:          rule,
		}, "failed to marshal added rule to JSON")
	}
	fmt.Fprintf(stdout, "Added classification rule %d: pattern=%q, kind=%q, value=%q\n",
		rule.ID, rule.Pattern, rule.Kind, rule.Value)
	return nil
}

func updateRule(ctx context.Context, st *store.Store, args []string, outputJSON bool) error {
	if len(args) < 4 {
		return exitcodes.Wrapf(nil, exitcodes.ExitData, exitcodes.KindValidation,
			"missing arguments; usage: symingest rules update <id> <pattern> <kind> <value>")
	}
	id, err := strconv.ParseInt(args[0], 10, 64)
	if err != nil {
		return exitcodes.Wrapf(err, exitcodes.ExitData, exitcodes.KindValidation,
			"invalid rule ID %q; must be an integer", args[0])
	}
	rule, err := st.UpdateRule(ctx, id, args[1], args[2], args[3])
	if err != nil {
		return exitcodes.Wrap(err, exitcodes.ExitGeneric, exitcodes.KindInternal,
			"failed to update rule")
	}
	if outputJSON {
		return printRulesJSON(rulesRuleJSONResponse{
			SchemaVersion: rulesJSONSchemaVersion,
			Rule:          rule,
		}, "failed to marshal updated rule to JSON")
	}
	fmt.Fprintf(stdout, "Updated classification rule %d: pattern=%q, kind=%q, value=%q\n",
		rule.ID, rule.Pattern, rule.Kind, rule.Value)
	return nil
}

type ruleTestMatch struct {
	ID      int64  `json:"id"`
	Pattern string `json:"pattern"`
	Kind    string `json:"kind"`
	Value   string `json:"value"`
}

func testRules(ctx context.Context, st *store.Store, args []string, outputJSON bool) error {
	if len(args) < 1 {
		return exitcodes.Wrapf(nil, exitcodes.ExitData, exitcodes.KindValidation,
			"missing text; usage: symingest rules test <text>")
	}
	text := strings.ToLower(strings.Join(args, " "))
	rules, err := st.ListRules(ctx)
	if err != nil {
		return exitcodes.Wrap(err, exitcodes.ExitGeneric, exitcodes.KindInternal,
			"failed to list rules")
	}
	var matches []ruleTestMatch
	for _, r := range rules {
		if strings.Contains(text, strings.ToLower(r.Pattern)) {
			matches = append(matches, ruleTestMatch{ID: r.ID, Pattern: r.Pattern, Kind: r.Kind, Value: r.Value})
		}
	}
	if outputJSON {
		if matches == nil {
			matches = []ruleTestMatch{}
		}
		return printRulesJSON(rulesTestJSONResponse{
			SchemaVersion: rulesJSONSchemaVersion,
			Matches:       matches,
		}, "failed to marshal rule test result")
	}
	if len(matches) == 0 {
		fmt.Fprintln(stdout, "No matching classification rules.")
		return nil
	}
	for _, m := range matches {
		fmt.Fprintf(stdout, "match rule %d: pattern=%q kind=%q value=%q\n", m.ID, m.Pattern, m.Kind, m.Value)
	}
	return nil
}

func deleteRule(ctx context.Context, st *store.Store, args []string, outputJSON bool) error {
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

	if outputJSON {
		return printRulesJSON(rulesDeleteJSONResponse{
			SchemaVersion: rulesJSONSchemaVersion,
			ID:            id,
			Deleted:       true,
		}, "failed to marshal deleted rule result to JSON")
	}
	fmt.Fprintf(stdout, "Deleted classification rule %d.\n", id)
	return nil
}

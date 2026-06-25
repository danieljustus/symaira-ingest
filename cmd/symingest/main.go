package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/danieljustus/symaira-corekit/exitcodes"
	"github.com/danieljustus/symaira-corekit/logkit"

	"github.com/danieljustus/symaira-ingest/internal/config"
	"github.com/danieljustus/symaira-ingest/internal/ingest"
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
  mcp            Start the MCP server
  version        Print version
  help           Show this help`)
	return nil
}

func runIngest(args []string) error {
	fs := flag.NewFlagSet("ingest", flag.ContinueOnError)
	ocrLang := fs.String("ocr-lang", "", "Tesseract language override")
	vaultFlag := fs.String("vault", "", "Target vault directory")
	dbFlag := fs.String("db", "", "SQLite database path")
	if err := fs.Parse(args); err != nil {
		return exitcodes.Wrap(err, exitcodes.ExitData, exitcodes.KindValidation,
			"invalid ingest flags")
	}
	remaining := fs.Args()
	if len(remaining) == 0 || remaining[0] == "--help" || remaining[0] == "-h" {
		fmt.Fprintln(stdout, `Usage: symingest ingest [flags] <file>

Ingest a single file into the configured vault.`)
		return nil
	}

	cfg, err := config.Load()
	if err != nil {
		return exitcodes.Wrap(err, exitcodes.ExitConfig, exitcodes.KindConfig,
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
	if *vaultFlag == "" {
		return exitcodes.Wrapf(nil, exitcodes.ExitConfig, exitcodes.KindConfig,
			"no vault configured; use --vault or SYMINGEST_VAULT")
	}
	if *dbFlag == "" {
		*dbFlag = cfg.DBPath
	}
	if *dbFlag == "" {
		*dbFlag = defaultDBPath()
	}

	source, err := filepath.Abs(remaining[0])
	if err != nil {
		return exitcodes.Wrap(err, exitcodes.ExitData, exitcodes.KindValidation,
			"invalid source path")
	}

	st, err := store.Open(*dbFlag)
	if err != nil {
		return exitcodes.Wrap(err, exitcodes.ExitConfig, exitcodes.KindConfig,
			"failed to open document store")
	}
	defer st.Close()

	engine := ocr.DefaultRunner(*ocrLang)
	pipeline := &ingest.Pipeline{
		Engine: engine,
		Store:  st,
		Writer: &writer.NoteWriter{Vault: *vaultFlag},
	}

	ctx := context.Background()
	res, err := pipeline.Ingest(ctx, source)
	if err != nil {
		if errors.Is(err, ingest.ErrDuplicate) {
			fmt.Fprintf(stdout, "already ingested: %s\n", source)
			return nil
		}
		return exitcodes.Wrap(err, exitcodes.ExitGeneric, exitcodes.KindInternal,
			"ingestion failed")
	}

	fmt.Fprintf(stdout, "ingested: %s\nengine: %s\ntext length: %d\n",
		source, res.Extract.Engine, len(res.Extract.Text))
	return nil
}

func defaultDBPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".", "symingest.db")
	}
	return filepath.Join(home, ".local", "share", "symingest", "symingest.db")
}

func runMCP(args []string) error {
	return exitcodes.Wrapf(nil, exitcodes.ExitSoftware, exitcodes.KindInternal,
		"mcp server is not yet implemented")
}

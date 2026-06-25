package main

import (
	"context"
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
	"github.com/danieljustus/symaira-ingest/internal/version"
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

	source, err := filepath.Abs(remaining[0])
	if err != nil {
		return exitcodes.Wrap(err, exitcodes.ExitData, exitcodes.KindValidation,
			"invalid source path")
	}

	engine := ocr.DefaultRunner(*ocrLang)
	ctx := context.Background()
	res, err := ingest.OneShot(ctx, source, engine)
	if err != nil {
		return exitcodes.Wrap(err, exitcodes.ExitGeneric, exitcodes.KindInternal,
			"ingestion failed")
	}

	fmt.Fprintf(stdout, "# %s\n\nMIME: %s\nEngine: %s\n\n%s\n",
		filepath.Base(source), res.Extract.MIME, res.Extract.Engine, res.Extract.Text)
	return nil
}

func runMCP(args []string) error {
	return exitcodes.Wrapf(nil, exitcodes.ExitSoftware, exitcodes.KindInternal,
		"mcp server is not yet implemented")
}

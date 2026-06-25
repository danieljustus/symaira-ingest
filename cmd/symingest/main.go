package main

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/danieljustus/symaira-corekit/exitcodes"
	"github.com/danieljustus/symaira-corekit/logkit"

	"github.com/danieljustus/symaira-ingest/internal/config"
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
	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" {
		fmt.Fprintln(stdout, `Usage: symingest ingest <file>

Ingest a single file into the configured vault.`)
		return nil
	}

	_, err := config.Load()
	if err != nil {
		return exitcodes.Wrap(err, exitcodes.ExitConfig, exitcodes.KindConfig,
			"failed to load configuration")
	}

	return exitcodes.Wrapf(nil, exitcodes.ExitSoftware, exitcodes.KindInternal,
		"ingest is not yet implemented")
}

func runMCP(args []string) error {
	return exitcodes.Wrapf(nil, exitcodes.ExitSoftware, exitcodes.KindInternal,
		"mcp server is not yet implemented")
}

// silence unused import in early scaffold
var _ = context.Background

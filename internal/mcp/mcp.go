// Package mcp exposes the one-shot ingestion pipeline as an MCP server.
package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/danieljustus/symaira-corekit/mcpserver"

	"github.com/danieljustus/symaira-ingest/internal/extract"
	"github.com/danieljustus/symaira-ingest/internal/ingest"
	"github.com/danieljustus/symaira-ingest/internal/store"
	"github.com/danieljustus/symaira-ingest/internal/writer"
)

// Register adds the ingest_file tool to the MCP server.
func Register(server *mcpserver.Server, st *store.Store, engine extract.Engine, defaultVault string) {
	server.RegisterTool(&mcpserver.Tool{
		Name:        "ingest_file",
		Description: "Ingest a single file into the vault, returning metadata about the generated Markdown note including the vault_path where it was written.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"path": {"type": "string", "description": "Absolute or relative path to the source file"},
				"vault_path": {"type": "string", "description": "Optional vault directory override"}
			},
			"required": ["path"]
		}`),
		Handler: func(ctx context.Context, input json.RawMessage) (any, error) {
			var args struct {
				Path      string `json:"path"`
				VaultPath string `json:"vault_path"`
			}
			if err := json.Unmarshal(input, &args); err != nil {
				return nil, fmt.Errorf("invalid arguments: %w", err)
			}

			source, err := filepath.Abs(args.Path)
			if err != nil {
				return nil, fmt.Errorf("invalid source path: %w", err)
			}

			vault := args.VaultPath
			if vault == "" {
				vault = defaultVault
			}
			if vault == "" {
				return nil, fmt.Errorf("no vault configured")
			}

			pipeline := &ingest.Pipeline{
				Engine: engine,
				Store:  st,
				Writer: &writer.NoteWriter{Vault: vault},
			}

			res, err := pipeline.Ingest(ctx, source)
			if errors.Is(err, ingest.ErrDuplicate) {
				return map[string]any{
					"status": "duplicate",
					"source": source,
				}, nil
			}
			if err != nil {
				return nil, err
			}

		return map[string]any{
			"status":      "success",
			"source":      source,
			"vault_path":  res.VaultPath,
			"mime":        res.Extract.MIME,
			"engine":      res.Extract.Engine,
			"text_length": len(res.Extract.Text),
		}, nil
		},
	})
}

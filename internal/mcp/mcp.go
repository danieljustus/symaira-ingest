// Package mcp exposes the one-shot ingestion pipeline as an MCP server.
package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/danieljustus/symaira-corekit/mcpserver"

	"github.com/danieljustus/symaira-ingest/internal/extract"
	"github.com/danieljustus/symaira-ingest/internal/ingest"
	"github.com/danieljustus/symaira-ingest/internal/store"
	"github.com/danieljustus/symaira-ingest/internal/writer"
)

// Register adds the ingest_file tool to the MCP server.
func Register(server *mcpserver.Server, st *store.Store, engine extract.Engine, defaultVault, defaultArchive string) {
	server.RegisterTool(&mcpserver.Tool{
		Name:        "ingest_file",
		Description: "Ingest a single file into the vault, returning metadata about the generated Markdown note including the vault_path where it was written.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"path": {"type": "string", "description": "Absolute or relative path to the source file"},
				"vault_path": {"type": "string", "description": "Optional vault directory override"},
				"archive_path": {"type": "string", "description": "Optional archive directory override"}
			},
			"required": ["path"]
		}`),
		Handler: func(ctx context.Context, input json.RawMessage) (any, error) {
			var args struct {
				Path        string `json:"path"`
				VaultPath   string `json:"vault_path"`
				ArchivePath string `json:"archive_path"`
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

			archive := args.ArchivePath
			if archive == "" {
				archive = defaultArchive
			}
			if archive == "" {
				home, err := os.UserHomeDir()
				if err == nil {
					archive = filepath.Join(home, ".local", "share", "symingest", "archive")
				}
			}

			pipeline := &ingest.Pipeline{
				Engine:     engine,
				Store:      st,
				Writer:     &writer.NoteWriter{Vault: vault},
				ArchiveDir: archive,
			}

			res, err := pipeline.Ingest(ctx, source)
			if errors.Is(err, ingest.ErrDuplicate) {
				var vPath, aPath string
				if dupErr, ok := err.(*ingest.DuplicateError); ok {
					vPath = dupErr.VaultPath
					aPath = dupErr.ArchivePath
				}
				data, mErr := json.Marshal(map[string]any{
					"status":       "duplicate",
					"source":       source,
					"vault_path":   vPath,
					"archive_path": aPath,
				})
				if mErr != nil {
					return nil, fmt.Errorf("marshal duplicate result: %w", mErr)
				}
				return string(data), nil
			}
			if err != nil {
				return nil, err
			}

			data, mErr := json.Marshal(map[string]any{
				"status":       "success",
				"source":       source,
				"vault_path":   res.VaultPath,
				"archive_path": res.ArchivePath,
				"mime":         res.Extract.MIME,
				"engine":       res.Extract.Engine,
				"text_length":  len(res.Extract.Text),
			})
			if mErr != nil {
				return nil, fmt.Errorf("marshal ingest result: %w", mErr)
			}
			return string(data), nil
		},
	})

	server.RegisterTool(&mcpserver.Tool{
		Name:        "list_jobs",
		Description: "List all jobs in the ingestion queue, including their status, attempts, error messages, and source path.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {}
		}`),
		Handler: func(ctx context.Context, input json.RawMessage) (any, error) {
			jobs, err := st.ListJobs(ctx)
			if err != nil {
				return nil, fmt.Errorf("list jobs: %w", err)
			}
			data, err := json.Marshal(map[string]any{
				"status": "success",
				"jobs":   jobs,
			})
			if err != nil {
				return nil, fmt.Errorf("marshal list_jobs result: %w", err)
			}
			return string(data), nil
		},
	})

	server.RegisterTool(&mcpserver.Tool{
		Name:        "retry_job",
		Description: "Reset a failed job's status to pending and its attempts count to 0 so that it gets retried.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"job_id": {"type": "integer", "description": "The ID of the job to retry"}
			},
			"required": ["job_id"]
		}`),
		Handler: func(ctx context.Context, input json.RawMessage) (any, error) {
			var args struct {
				JobID int64 `json:"job_id"`
			}
			if err := json.Unmarshal(input, &args); err != nil {
				return nil, fmt.Errorf("invalid arguments: %w", err)
			}

			if err := st.RetryJob(ctx, args.JobID); err != nil {
				return nil, fmt.Errorf("retry job %d: %w", args.JobID, err)
			}

			data, err := json.Marshal(map[string]any{
				"status":  "success",
				"message": fmt.Sprintf("job %d reset to pending status", args.JobID),
			})
			if err != nil {
				return nil, fmt.Errorf("marshal retry result: %w", err)
			}
			return string(data), nil
		},
	})

	server.RegisterTool(&mcpserver.Tool{
		Name:        "start_watch",
		Description: "Start recursively watching a directory for new or modified files and process them automatically in the background.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"directory": {"type": "string", "description": "Absolute path to the directory to watch"},
				"vault_path": {"type": "string", "description": "Optional vault directory path override"},
				"archive_path": {"type": "string", "description": "Optional archive directory path override"}
			},
			"required": ["directory"]
		}`),
		Handler: func(ctx context.Context, input json.RawMessage) (any, error) {
			var args struct {
				Directory   string `json:"directory"`
				VaultPath   string `json:"vault_path"`
				ArchivePath string `json:"archive_path"`
			}
			if err := json.Unmarshal(input, &args); err != nil {
				return nil, fmt.Errorf("invalid arguments: %w", err)
			}

			dir, err := filepath.Abs(args.Directory)
			if err != nil {
				return nil, fmt.Errorf("invalid directory path: %w", err)
			}

			vault := args.VaultPath
			if vault == "" {
				vault = defaultVault
			}
			if vault == "" {
				return nil, fmt.Errorf("no vault configured")
			}

			archive := args.ArchivePath
			if archive == "" {
				archive = defaultArchive
			}
			if archive == "" {
				home, err := os.UserHomeDir()
				if err == nil {
					archive = filepath.Join(home, ".local", "share", "symingest", "archive")
				}
			}

			watcher, err := ingest.NewWatcher(st, dir)
			if err != nil {
				return nil, fmt.Errorf("initialize watcher: %w", err)
			}

			pipeline := &ingest.Pipeline{
				Engine:     engine,
				Store:      st,
				Writer:     &writer.NoteWriter{Vault: vault},
				ArchiveDir: archive,
			}

			bgCtx := context.Background()
			if err := watcher.Start(bgCtx); err != nil {
				watcher.Close()
				return nil, fmt.Errorf("start watcher: %w", err)
			}
			go ingest.StartWorker(bgCtx, pipeline)

			data, err := json.Marshal(map[string]any{
				"status":  "success",
				"message": fmt.Sprintf("started watching directory %s with vault %s and archive %s", dir, vault, archive),
			})
			if err != nil {
				return nil, fmt.Errorf("marshal start_watch result: %w", err)
			}
			return string(data), nil
		},
	})

	server.RegisterTool(&mcpserver.Tool{
		Name:        "list_rules",
		Description: "List all document classification rules configured in the system.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {}
		}`),
		Handler: func(ctx context.Context, input json.RawMessage) (any, error) {
			rules, err := st.ListRules(ctx)
			if err != nil {
				return nil, fmt.Errorf("list rules: %w", err)
			}
			data, err := json.Marshal(map[string]any{
				"status": "success",
				"rules":  rules,
			})
			if err != nil {
				return nil, fmt.Errorf("marshal list_rules result: %w", err)
			}
			return string(data), nil
		},
	})

	server.RegisterTool(&mcpserver.Tool{
		Name:        "add_rule",
		Description: "Add a new classification rule to automatically categorize, tag, or assign correspondent/document type to ingested documents matching a pattern.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"pattern": {"type": "string", "description": "Case-insensitive substring text pattern to match in document"},
				"kind": {"type": "string", "enum": ["category", "tag", "correspondent", "document_type"], "description": "Metadata type to apply"},
				"value": {"type": "string", "description": "The metadata value to set or tag to add"}
			},
			"required": ["pattern", "kind", "value"]
		}`),
		Handler: func(ctx context.Context, input json.RawMessage) (any, error) {
			var args struct {
				Pattern string `json:"pattern"`
				Kind    string `json:"kind"`
				Value   string `json:"value"`
			}
			if err := json.Unmarshal(input, &args); err != nil {
				return nil, fmt.Errorf("invalid arguments: %w", err)
			}

			rule, err := st.AddRule(ctx, args.Pattern, args.Kind, args.Value)
			if err != nil {
				return nil, fmt.Errorf("add rule: %w", err)
			}

			data, err := json.Marshal(map[string]any{
				"status": "success",
				"rule":   rule,
			})
			if err != nil {
				return nil, fmt.Errorf("marshal add_rule result: %w", err)
			}
			return string(data), nil
		},
	})

	server.RegisterTool(&mcpserver.Tool{
		Name:        "delete_rule",
		Description: "Delete a document classification rule by ID.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"rule_id": {"type": "integer", "description": "The ID of the rule to delete"}
			},
			"required": ["rule_id"]
		}`),
		Handler: func(ctx context.Context, input json.RawMessage) (any, error) {
			var args struct {
				RuleID int64 `json:"rule_id"`
			}
			if err := json.Unmarshal(input, &args); err != nil {
				return nil, fmt.Errorf("invalid arguments: %w", err)
			}

			if err := st.DeleteRule(ctx, args.RuleID); err != nil {
				return nil, fmt.Errorf("delete rule %d: %w", args.RuleID, err)
			}

			data, err := json.Marshal(map[string]any{
				"status":  "success",
				"message": fmt.Sprintf("rule %d deleted successfully", args.RuleID),
			})
			if err != nil {
				return nil, fmt.Errorf("marshal delete_rule result: %w", err)
			}
			return string(data), nil
		},
	})
}

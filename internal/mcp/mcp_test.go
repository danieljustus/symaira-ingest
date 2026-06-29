package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/danieljustus/symaira-corekit/mcpserver"

	"github.com/danieljustus/symaira-ingest/internal/extract"
	"github.com/danieljustus/symaira-ingest/internal/store"
)

type fakeEngine struct{}

func (fakeEngine) Extract(ctx context.Context, path string, kind extract.Kind) (*extract.Result, error) {
	return &extract.Result{Text: "ocr-result", MIME: string(kind), Engine: "fake"}, nil
}

func TestRegister_IngestFile(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "mcp.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	vault := filepath.Join(dir, "vault")
	if err := os.MkdirAll(vault, 0o755); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(dir, "scan.png")
	if err := os.WriteFile(src, []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A}, 0o644); err != nil {
		t.Fatal(err)
	}

	server := mcpserver.New("symingest", "0.1.0")
	Register(server, st, fakeEngine{}, vault, filepath.Join(dir, "archive"))

	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	defer outR.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		_ = server.ServeIO(ctx, inR, outW)
	}()

	// initialize
	writeFramed(t, inW, map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "initialize",
	})
	readResp(t, outR)

	// tools/call ingest_file
	writeFramed(t, inW, map[string]any{
		"jsonrpc": "2.0",
		"id":      2,
		"method":  "tools/call",
		"params": map[string]any{
			"name":      "ingest_file",
			"arguments": map[string]any{"path": src},
		},
	})
	resp := readResp(t, outR)

	if id, ok := resp.ID.(float64); !ok || id != 2 {
		t.Fatalf("response id = %v, want 2", resp.ID)
	}
	raw := parseToolText(t, resp)
	if raw["status"] != "success" {
		t.Fatalf("status = %v, want success", raw["status"])
	}
	if raw["text_length"] != float64(len("ocr-result")) {
		t.Fatalf("text_length = %v", raw["text_length"])
	}
}

func writeFramed(t *testing.T, w io.WriteCloser, req map[string]any) {
	t.Helper()
	data, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	_, err = w.Write([]byte("Content-Length: " + itoa(len(data)) + "\r\n\r\n" + string(data)))
	if err != nil {
		t.Fatal(err)
	}
}

func readResp(t *testing.T, r io.ReadCloser) jsonRPCResponse {
	t.Helper()
	br := bufio.NewReader(r)
	// read Content-Length header
	var length int
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			t.Fatal(err)
		}
		line = strings.TrimSpace(line)
		if line == "" {
			break
		}
		if strings.HasPrefix(line, "Content-Length:") {
			length = atoi(strings.TrimSpace(strings.TrimPrefix(line, "Content-Length:")))
		}
	}
	if length == 0 {
		t.Fatal("missing Content-Length")
	}
	body := make([]byte, length)
	if _, err := io.ReadFull(br, body); err != nil {
		t.Fatal(err)
	}
	var resp jsonRPCResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatal(err)
	}
	return resp
}

type jsonRPCResponse struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      any         `json:"id"`
	Result  any         `json:"result"`
	Error   any         `json:"error"`
}

func itoa(n int) string {
	return strconv.Itoa(n)
}

func atoi(s string) int {
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0
	}
	return n
}

// parseToolText extracts the JSON-encoded result from an MCP tool response's
// content[0].text field. The handler returns JSON-encoded strings per the MCP
// spec (TextContent.text must be a string), so this helper unmarshals them.
func parseToolText(t *testing.T, resp jsonRPCResponse) map[string]any {
	t.Helper()
	result, ok := resp.Result.(map[string]any)
	if !ok {
		t.Fatalf("result type = %T, want map", resp.Result)
	}
	content, ok := result["content"].([]any)
	if !ok || len(content) == 0 {
		t.Fatalf("missing content in result: %v", result)
	}
	first, ok := content[0].(map[string]any)
	if !ok {
		t.Fatalf("content[0] type = %T", content[0])
	}
	text, ok := first["text"].(string)
	if !ok {
		t.Fatalf("content text type = %T, want string", first["text"])
	}
	var raw map[string]any
	if err := json.Unmarshal([]byte(text), &raw); err != nil {
		t.Fatalf("failed to unmarshal tool text: %v", err)
	}
	return raw
}

func TestRegister_IngestFile_InvalidInput(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "mcp.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	server := mcpserver.New("symingest", "0.1.0")
	Register(server, st, fakeEngine{}, filepath.Join(dir, "vault"), filepath.Join(dir, "archive"))

	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	defer outR.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		_ = server.ServeIO(ctx, inR, outW)
	}()

	writeFramed(t, inW, map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "initialize",
	})
	readResp(t, outR)

	writeFramed(t, inW, map[string]any{
		"jsonrpc": "2.0",
		"id":      2,
		"method":  "tools/call",
		"params": map[string]any{
			"name":      "ingest_file",
			"arguments": "not-a-json-object",
		},
	})
	resp := readResp(t, outR)
	if resp.Error != nil {
		return
	}
	result, ok := resp.Result.(map[string]any)
	if !ok {
		t.Fatalf("result type = %T, want map", resp.Result)
	}
	content, ok := result["content"].([]any)
	if !ok || len(content) == 0 {
		t.Fatalf("missing content in result: %v", result)
	}
	first, ok := content[0].(map[string]any)
	if !ok {
		t.Fatalf("content[0] type = %T", content[0])
	}
	text, ok := first["text"].(string)
	if !ok {
		t.Fatalf("content text type = %T", first["text"])
	}
	if !strings.Contains(text, "invalid") && !strings.Contains(text, "error") {
		t.Fatalf("expected error message, got: %s", text)
	}
}

func TestRegister_IngestFile_NoVault(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "mcp.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	server := mcpserver.New("symingest", "0.1.0")
	Register(server, st, fakeEngine{}, "", "")

	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	defer outR.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		_ = server.ServeIO(ctx, inR, outW)
	}()

	writeFramed(t, inW, map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "initialize",
	})
	readResp(t, outR)

	writeFramed(t, inW, map[string]any{
		"jsonrpc": "2.0",
		"id":      2,
		"method":  "tools/call",
		"params": map[string]any{
			"name":      "ingest_file",
			"arguments": map[string]any{"path": "/nonexistent/file.txt"},
		},
	})
	resp := readResp(t, outR)
	if resp.Error != nil {
		return
	}
	result, ok := resp.Result.(map[string]any)
	if !ok {
		t.Fatalf("result type = %T, want map", resp.Result)
	}
	content, ok := result["content"].([]any)
	if !ok || len(content) == 0 {
		t.Fatalf("missing content in result: %v", result)
	}
	first, ok := content[0].(map[string]any)
	if !ok {
		t.Fatalf("content[0] type = %T", content[0])
	}
	text, ok := first["text"].(string)
	if !ok {
		t.Fatalf("content text type = %T", first["text"])
	}
	if !strings.Contains(text, "vault") && !strings.Contains(text, "error") {
		t.Fatalf("expected vault error message, got: %s", text)
	}
}

func TestRegister_IngestFile_Duplicate(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "mcp.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	vault := filepath.Join(dir, "vault")
	if err := os.MkdirAll(vault, 0o755); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(dir, "scan.png")
	if err := os.WriteFile(src, []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A}, 0o644); err != nil {
		t.Fatal(err)
	}

	server := mcpserver.New("symingest", "0.1.0")
	Register(server, st, fakeEngine{}, vault, filepath.Join(dir, "archive"))

	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	defer outR.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		_ = server.ServeIO(ctx, inR, outW)
	}()

	writeFramed(t, inW, map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "initialize",
	})
	readResp(t, outR)

	writeFramed(t, inW, map[string]any{
		"jsonrpc": "2.0",
		"id":      2,
		"method":  "tools/call",
		"params": map[string]any{
			"name":      "ingest_file",
			"arguments": map[string]any{"path": src},
		},
	})
	readResp(t, outR)

	writeFramed(t, inW, map[string]any{
		"jsonrpc": "2.0",
		"id":      3,
		"method":  "tools/call",
		"params": map[string]any{
			"name":      "ingest_file",
			"arguments": map[string]any{"path": src},
		},
	})
	resp := readResp(t, outR)
	if resp.Error != nil {
		t.Fatalf("expected no error for duplicate, got: %v", resp.Error)
	}
	raw := parseToolText(t, resp)
	if raw["status"] != "duplicate" {
		t.Fatalf("status = %v, want duplicate", raw["status"])
	}
}

func TestRegister_RetryJob_InvalidInput(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "mcp.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	server := mcpserver.New("symingest", "0.1.0")
	Register(server, st, fakeEngine{}, filepath.Join(dir, "vault"), filepath.Join(dir, "archive"))

	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	defer outR.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		_ = server.ServeIO(ctx, inR, outW)
	}()

	writeFramed(t, inW, map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "initialize",
	})
	readResp(t, outR)

	writeFramed(t, inW, map[string]any{
		"jsonrpc": "2.0",
		"id":      2,
		"method":  "tools/call",
		"params": map[string]any{
			"name":      "retry_job",
			"arguments": "not-a-json-object",
		},
	})
	resp := readResp(t, outR)
	if resp.Error != nil {
		return
	}
	result, ok := resp.Result.(map[string]any)
	if !ok {
		t.Fatalf("result type = %T, want map", resp.Result)
	}
	content, ok := result["content"].([]any)
	if !ok || len(content) == 0 {
		t.Fatalf("missing content in result: %v", result)
	}
	first, ok := content[0].(map[string]any)
	if !ok {
		t.Fatalf("content[0] type = %T", content[0])
	}
	text, ok := first["text"].(string)
	if !ok {
		t.Fatalf("content text type = %T", first["text"])
	}
	if !strings.Contains(text, "invalid") && !strings.Contains(text, "error") {
		t.Fatalf("expected error message, got: %s", text)
	}
}

func TestRegister_AddRule_InvalidInput(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "mcp.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	server := mcpserver.New("symingest", "0.1.0")
	Register(server, st, fakeEngine{}, filepath.Join(dir, "vault"), filepath.Join(dir, "archive"))

	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	defer outR.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		_ = server.ServeIO(ctx, inR, outW)
	}()

	writeFramed(t, inW, map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "initialize",
	})
	readResp(t, outR)

	writeFramed(t, inW, map[string]any{
		"jsonrpc": "2.0",
		"id":      2,
		"method":  "tools/call",
		"params": map[string]any{
			"name":      "add_rule",
			"arguments": "not-a-json-object",
		},
	})
	resp := readResp(t, outR)
	if resp.Error != nil {
		return
	}
	result, ok := resp.Result.(map[string]any)
	if !ok {
		t.Fatalf("result type = %T, want map", resp.Result)
	}
	content, ok := result["content"].([]any)
	if !ok || len(content) == 0 {
		t.Fatalf("missing content in result: %v", result)
	}
	first, ok := content[0].(map[string]any)
	if !ok {
		t.Fatalf("content[0] type = %T", content[0])
	}
	text, ok := first["text"].(string)
	if !ok {
		t.Fatalf("content text type = %T", first["text"])
	}
	if !strings.Contains(text, "invalid") && !strings.Contains(text, "error") {
		t.Fatalf("expected error message, got: %s", text)
	}
}

func TestRegister_DeleteRule_InvalidInput(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "mcp.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	server := mcpserver.New("symingest", "0.1.0")
	Register(server, st, fakeEngine{}, filepath.Join(dir, "vault"), filepath.Join(dir, "archive"))

	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	defer outR.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		_ = server.ServeIO(ctx, inR, outW)
	}()

	writeFramed(t, inW, map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "initialize",
	})
	readResp(t, outR)

	writeFramed(t, inW, map[string]any{
		"jsonrpc": "2.0",
		"id":      2,
		"method":  "tools/call",
		"params": map[string]any{
			"name":      "delete_rule",
			"arguments": "not-a-json-object",
		},
	})
	resp := readResp(t, outR)
	if resp.Error != nil {
		return
	}
	result, ok := resp.Result.(map[string]any)
	if !ok {
		t.Fatalf("result type = %T, want map", resp.Result)
	}
	content, ok := result["content"].([]any)
	if !ok || len(content) == 0 {
		t.Fatalf("missing content in result: %v", result)
	}
	first, ok := content[0].(map[string]any)
	if !ok {
		t.Fatalf("content[0] type = %T", content[0])
	}
	text, ok := first["text"].(string)
	if !ok {
		t.Fatalf("content text type = %T", first["text"])
	}
	if !strings.Contains(text, "invalid") && !strings.Contains(text, "error") {
		t.Fatalf("expected error message, got: %s", text)
	}
}

func TestRegister_DeleteRule_NotFound(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "mcp.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	server := mcpserver.New("symingest", "0.1.0")
	Register(server, st, fakeEngine{}, filepath.Join(dir, "vault"), filepath.Join(dir, "archive"))

	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	defer outR.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		_ = server.ServeIO(ctx, inR, outW)
	}()

	writeFramed(t, inW, map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "initialize",
	})
	readResp(t, outR)

	writeFramed(t, inW, map[string]any{
		"jsonrpc": "2.0",
		"id":      2,
		"method":  "tools/call",
		"params": map[string]any{
			"name":      "delete_rule",
			"arguments": map[string]any{"rule_id": 99999},
		},
	})
	resp := readResp(t, outR)
	if resp.Error != nil {
		return
	}
	result, ok := resp.Result.(map[string]any)
	if !ok {
		t.Fatalf("result type = %T, want map", resp.Result)
	}
	content, ok := result["content"].([]any)
	if !ok || len(content) == 0 {
		t.Fatalf("missing content in result: %v", result)
	}
	first, ok := content[0].(map[string]any)
	if !ok {
		t.Fatalf("content[0] type = %T", content[0])
	}
	text, ok := first["text"].(string)
	if !ok {
		t.Fatalf("content text type = %T", first["text"])
	}
	if !strings.Contains(text, "not found") && !strings.Contains(text, "error") {
		t.Fatalf("expected not found error, got: %s", text)
	}
}

func TestRegister_ListJobs_Empty(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "mcp.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	server := mcpserver.New("symingest", "0.1.0")
	Register(server, st, fakeEngine{}, filepath.Join(dir, "vault"), filepath.Join(dir, "archive"))

	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	defer outR.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		_ = server.ServeIO(ctx, inR, outW)
	}()

	writeFramed(t, inW, map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "initialize",
	})
	readResp(t, outR)

	writeFramed(t, inW, map[string]any{
		"jsonrpc": "2.0",
		"id":      2,
		"method":  "tools/call",
		"params": map[string]any{
			"name":      "list_jobs",
			"arguments": map[string]any{},
		},
	})
	resp := readResp(t, outR)
	if resp.Error != nil {
		t.Fatalf("expected no error, got: %v", resp.Error)
	}
	raw := parseToolText(t, resp)
	if raw["status"] != "success" {
		t.Fatalf("status = %v, want success", raw["status"])
	}
}

func TestRegister_Rules(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "mcp.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	server := mcpserver.New("symingest", "0.1.0")
	Register(server, st, fakeEngine{}, filepath.Join(dir, "vault"), filepath.Join(dir, "archive"))

	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	defer outR.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		_ = server.ServeIO(ctx, inR, outW)
	}()

	// initialize
	writeFramed(t, inW, map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "initialize",
	})
	readResp(t, outR)

	// tools/call add_rule
	writeFramed(t, inW, map[string]any{
		"jsonrpc": "2.0",
		"id":      2,
		"method":  "tools/call",
		"params": map[string]any{
			"name": "add_rule",
			"arguments": map[string]any{
				"pattern": "test-pattern",
				"kind":    "category",
				"value":   "test-value",
			},
		},
	})
	resp := readResp(t, outR)
	raw := parseToolText(t, resp)
	if raw["status"] != "success" {
		t.Fatalf("status = %v, want success", raw["status"])
	}
	rule, _ := raw["rule"].(map[string]any)
	if rule["pattern"] != "test-pattern" || rule["kind"] != "category" || rule["value"] != "test-value" {
		t.Fatalf("unexpected rule result: %v", rule)
	}

	// tools/call list_rules
	writeFramed(t, inW, map[string]any{
		"jsonrpc": "2.0",
		"id":      3,
		"method":  "tools/call",
		"params": map[string]any{
			"name":      "list_rules",
			"arguments": map[string]any{},
		},
	})
	resp = readResp(t, outR)
	raw = parseToolText(t, resp)
	if raw["status"] != "success" {
		t.Fatalf("status = %v, want success", raw["status"])
	}
	rules, _ := raw["rules"].([]any)
	if len(rules) != 1 {
		t.Fatalf("len(rules) = %d, want 1", len(rules))
	}

	// tools/call delete_rule
	ruleID := int64(rule["id"].(float64))
	writeFramed(t, inW, map[string]any{
		"jsonrpc": "2.0",
		"id":      4,
		"method":  "tools/call",
		"params": map[string]any{
			"name": "delete_rule",
			"arguments": map[string]any{
				"rule_id": ruleID,
			},
		},
	})
	resp = readResp(t, outR)
	raw = parseToolText(t, resp)
	if raw["status"] != "success" {
		t.Fatalf("status = %v, want success", raw["status"])
	}
}

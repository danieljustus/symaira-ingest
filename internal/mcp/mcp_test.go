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
	Register(server, st, fakeEngine{}, vault)

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
	raw, ok := first["text"].(map[string]any)
	if !ok {
		t.Fatalf("content text type = %T", first["text"])
	}
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

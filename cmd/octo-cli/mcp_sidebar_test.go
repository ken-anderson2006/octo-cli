package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestMain_MCPSidebar(t *testing.T) {
	const token = "fixture-sidebar-mcp-token"
	var requests atomic.Int32
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Header.Get("Authorization") != "Bearer "+token || r.Header.Get("X-Space-ID") != "" {
			t.Error("expected authenticated token-bound request")
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v2/cli/sidebar/conversations":
			_, _ = io.WriteString(w, `{"data":{"scope":"full","conversations":[],"categories":[],"summary":{"mention_count":0,"follow_count":0}}}`)
		case "/api/v2/cli/sidebar/conversations/g/category":
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			if value, exists := body["category_id"]; !exists || value != nil || r.Method != http.MethodPut {
				t.Error("MCP must preserve the present JSON null ungroup operation")
			}
			_, _ = io.WriteString(w, `{"data":{}}`)
		case "/api/v2/cli/sidebar/conversations/g/_mark_read":
			w.WriteHeader(http.StatusBadGateway)
		default:
			t.Errorf("unexpected sidebar path %q", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(gateway.Close)
	cli := startMCP(t, nil, "OCTO_TOKEN="+token, "OCTO_API_BASE_URL="+gateway.URL, "OCTO_SPACE_ID=untrusted-space")
	cli.discover("2025-11-25")
	list := cli.call(2, "server", "sidebar", "list")
	if list.IsError || list.Output.ExitCode != 0 || !strings.Contains(list.Output.Stdout, `"scope": "full"`) {
		t.Fatalf("MCP sidebar list failed: %+v", list.Output)
	}
	move := cli.call(3, "server", "sidebar", "move", "g", "--channel-type", "2", "--data", `{"category_id": null}`)
	if move.IsError || move.Output.ExitCode != 0 {
		t.Fatalf("MCP nullable move failed: %+v", move.Output)
	}
	schema := cli.call(4, "schema", "sidebar.list")
	if schema.IsError || !strings.Contains(schema.Output.Stdout, `"channel_id"`) || !strings.Contains(schema.Output.Stdout, `"threads"`) {
		t.Fatalf("MCP schema lost sidebar row fields: %+v", schema.Output)
	}
	before := requests.Load()
	readClear := cli.call(5, "server", "sidebar", "mark-read", "g", "--channel-type", "2", "--scope", "group_and_threads")
	if !readClear.IsError || readClear.Output.ExitCode == 0 || !strings.Contains(readClear.Output.Stderr, "RESULT_UNKNOWN") || requests.Load() != before+1 {
		t.Fatalf("MCP must not replay an uncertain read clear: %+v", readClear.Output)
	}
	if strings.Contains(list.Output.Stdout+move.Output.Stdout+readClear.Output.Stderr, token) {
		t.Fatal("MCP response exposed the configured bearer")
	}
	cli.close()
}

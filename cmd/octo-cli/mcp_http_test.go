package main

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestMain_MCPHTTP(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "octo-cli")
	build := exec.CommandContext(t.Context(), "go", "build", "-o", bin, ".")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}

	const firstToken = "fixture_mcp_http_first"
	const secondToken = "fixture_mcp_http_second"
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/v2/cli/identity" {
			t.Error("the gateway did not receive the expected Octo identity request")
			http.Error(w, "bad fixture request", http.StatusBadRequest)
			return
		}
		var userID string
		switch r.Header.Get("Authorization") {
		case "Bearer " + firstToken:
			userID = "fixture-first"
		case "Bearer " + secondToken:
			userID = "fixture-second"
		default:
			http.Error(w, "invalid Octo Token", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"data":{"user_id":"`+userID+`","user_name":"Fixture User","bot_id":"","space_id":"fixture-space"}}`)
	}))
	t.Cleanup(gateway.Close)

	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "mcp", "serve", "--listen", "127.0.0.1:0")
	cmd.Env = hermeticEnv("OCTO_TOKEN=fixture_unused_process_token", "OCTO_API_BASE_URL="+gateway.URL)
	stderr, err := cmd.StderrPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		cancel()
		_ = cmd.Wait()
	}()

	reader := bufio.NewReader(stderr)
	line, err := reader.ReadString('\n')
	addr, ok := strings.CutPrefix(strings.TrimSpace(line), "Octo CLI MCP listening on ")
	if err != nil || !ok || !strings.HasPrefix(addr, "http://127.0.0.1:") || !strings.HasSuffix(addr, "/mcp") {
		t.Fatalf("expected MCP listener address, got %q (%v)", line, err)
	}

	client := &http.Client{Timeout: 5 * time.Second}
	request := func(t *testing.T, method, token, version, origin, message string) (code int, body []byte) {
		t.Helper()
		req, err := http.NewRequestWithContext(ctx, method, addr, strings.NewReader(message))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		if version != "" {
			req.Header.Set("MCP-Protocol-Version", version)
		}
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		body, err = io.ReadAll(resp.Body)
		if err != nil {
			t.Fatal(err)
		}
		return resp.StatusCode, body
	}

	const version = "2025-11-25"
	initialize := initializeMCP(version)
	if code, body := request(t, http.MethodPost, "", "", "", initialize); code != http.StatusUnauthorized {
		t.Fatalf("MCP request should require an Octo Bearer, got %d %s", code, body)
	}
	if code, body := request(t, http.MethodPost, firstToken, "", "https://fixture.example.invalid", initialize); code != http.StatusForbidden {
		t.Fatalf("request with a browser origin should be refused, got %d %s", code, body)
	}
	if code, body := request(t, http.MethodPost, firstToken, "", "", initialize); code != http.StatusOK ||
		!strings.Contains(string(body), `"protocolVersion":"2025-11-25"`) {
		t.Fatalf("initialization failed: %d %s", code, body)
	}
	if code, body := request(t, http.MethodPost, firstToken, version, "", `{"jsonrpc":"2.0","method":"notifications/initialized"}`); code != http.StatusAccepted || len(body) != 0 {
		t.Fatalf("expected empty notification acknowledgment, got %d %s", code, body)
	}
	if code, body := request(t, http.MethodGet, firstToken, version, "", ""); code != http.StatusMethodNotAllowed {
		t.Fatalf("standalone SSE stream returned %d %s", code, body)
	}
	const list = `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`
	if code, body := request(t, http.MethodPost, firstToken, "invalid", "", list); code != http.StatusBadRequest {
		t.Fatalf("unsupported protocol should return 400, got %d %s", code, body)
	}
	t.Run("bundled skills", func(t *testing.T) {
		want := readPublicSkills(t, client, strings.TrimSuffix(addr, "/mcp")+"/v1/skills")
		const callSkills = `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"skills","arguments":{}}}`
		for _, protocol := range []string{"2024-11-05", "2025-03-26", "2025-06-18", version} {
			t.Run(protocol, func(t *testing.T) {
				cli := startMCPBinary(t, bin, nil)
				cli.discover(protocol)
				cli.send(callSkills)
				assertMCPSkills(t, cli.receive("3"), protocol, want)
				cli.close()

				if protocol == "2024-11-05" { // Only stdio supports this version.
					return
				}
				post := func(message string) mcpReply {
					t.Helper()
					code, body := request(t, http.MethodPost, firstToken, protocol, "", message)
					var reply mcpReply
					if code != http.StatusOK || json.Unmarshal(body, &reply) != nil {
						t.Fatalf("MCP request failed: %d %s", code, body)
					}
					return reply
				}
				assertMCPVersion(t, post(initializeMCP(protocol)), protocol)
				assertMCPTools(t, post(list), protocol)
				assertMCPSkills(t, post(callSkills), protocol, want)
			})
		}
	})
	const call = `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"exec","arguments":{"args":["server","identity","get","--jq",".data.user_id"]}}}`
	for _, tc := range []struct {
		token string
		want  string
	}{
		{firstToken, "\"fixture-first\"\n"},
		{secondToken, "\"fixture-second\"\n"},
	} {
		code, body := request(t, http.MethodPost, tc.token, version, "", call)
		var reply mcpReply
		var result mcpExecReply
		if code != http.StatusOK || json.Unmarshal(body, &reply) != nil || string(reply.ID) != "3" ||
			json.Unmarshal(reply.Result, &result) != nil || result.IsError || result.Output.ExitCode != 0 ||
			result.Output.Stdout != tc.want || strings.Contains(string(body), tc.token) {
			t.Fatalf("expected the caller's identity, got %d %s", code, body)
		}
	}
	code, body := request(t, http.MethodPost, "fixture_invalid_token", version, "", call)
	var denied mcpReply
	var deniedResult mcpExecReply
	if code != http.StatusOK || json.Unmarshal(body, &denied) != nil ||
		json.Unmarshal(denied.Result, &deniedResult) != nil || !deniedResult.IsError ||
		!strings.Contains(deniedResult.Output.Stderr, "auth_error") {
		t.Fatalf("expected the gateway's authentication error as a tool result, got %d %s", code, body)
	}
}

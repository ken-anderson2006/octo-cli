package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"strings"

	"github.com/Mininglamp-OSS/octo-cli/skills"
)

const (
	execTool    = "exec"
	skillsTool  = "skills"
	maxExecArgs = 256
)

// ExecResult preserves the underlying command's output streams and status.
type ExecResult struct {
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
	ExitCode int    `json:"exitCode"`
}

func listTools(version string) map[string]any {
	exec := map[string]any{
		"name":        execTool,
		"description": "Run a supported Octo CLI command. HTTP uses the request Bearer and exposes registered service operations. Use schema --list or --help to discover commands. Returns stdout, stderr, and exitCode.",
		"inputSchema": map[string]any{
			"type":                 "object",
			"required":             []string{"args"},
			"additionalProperties": false,
			"properties": map[string]any{
				"args": map[string]any{
					"type":        "array",
					"items":       map[string]string{"type": "string", "pattern": `^[^\u0000]*$`},
					"minItems":    1,
					"maxItems":    maxExecArgs,
					"description": "CLI arguments after octo-cli. Pass each argument or flag value as a separate array item.",
				},
			},
		},
	}
	bundled := map[string]any{
		"name":        skillsTool,
		"description": "Return all enabled bundled skills with their full Markdown content and reference files.",
		"inputSchema": map[string]any{
			"type":                 "object",
			"properties":           map[string]any{},
			"additionalProperties": false,
		},
	}
	if version >= "2025-03-26" {
		bundled["annotations"] = map[string]bool{"readOnlyHint": true, "idempotentHint": true}
	}
	if version >= "2025-06-18" {
		bundled["outputSchema"] = skillsOutputSchema()
	}
	return map[string]any{"tools": []map[string]any{exec, bundled}}
}

func skillsOutputSchema() map[string]any {
	text := map[string]string{"type": "string"}
	skill := map[string]any{
		"type":     "object",
		"required": []string{"name", "description", "content", "references"},
		"properties": map[string]any{
			"name":        text,
			"description": text,
			"content":     text,
			"references":  map[string]any{"type": "object", "additionalProperties": text},
		},
	}
	return map[string]any{
		"type":       "object",
		"required":   []string{"skills"},
		"properties": map[string]any{"skills": map[string]any{"type": "array", "items": skill}},
	}
}

type activeCall struct {
	cancel    context.CancelFunc
	cancelled bool
}

type completedCall struct {
	key   string
	reply *response
}

type toolCall struct {
	name string
	args []string
}

func parseToolCall(msg *frameworkRequest) (*toolCall, *response) {
	params, ok := decodeObject(msg.Params, "name", "arguments", "_meta")
	var name string
	if !ok || json.Unmarshal(params["name"], &name) != nil || name == "" {
		return nil, failure(msg.ID, -32602, "Tool parameters require a name")
	}
	if meta := params["_meta"]; len(meta) != 0 && meta[0] != '{' {
		return nil, failure(msg.ID, -32602, "Tool metadata must be an object")
	}
	switch name {
	case execTool:
		args, ok := execArgs(params["arguments"])
		if !ok {
			return nil, failure(msg.ID, -32602, fmt.Sprintf("exec requires 1 to %d strings in args without NUL characters", maxExecArgs))
		}
		return &toolCall{name: name, args: args}, nil
	case skillsTool:
		if raw := params["arguments"]; raw != nil {
			if _, ok := decodeObject(raw); !ok {
				return nil, failure(msg.ID, -32602, "skills accepts an empty arguments object")
			}
		}
		return &toolCall{name: name}, nil
	default:
		return nil, failure(msg.ID, -32602, "Unknown tool")
	}
}

func (s *session) startCall(ctx context.Context, msg *frameworkRequest) *response {
	call, reply := parseToolCall(msg)
	if reply != nil {
		return reply
	}
	key := msg.key
	if _, exists := s.active[key]; exists {
		return failure(msg.ID, -32600, "Request ID is already in use")
	}
	if len(s.active) >= maxActiveCalls {
		return failure(msg.ID, -32000, "Too many active tool calls")
	}
	callCtx, cancel := context.WithCancel(ctx)
	s.active[key] = &activeCall{cancel: cancel}
	go func() {
		reply := call.run(callCtx, msg.ID, s.protocol, s.fsys, s.execute)
		select {
		case s.completed <- completedCall{key: key, reply: reply}:
		case <-ctx.Done():
		}
	}()
	return nil
}

func execArgs(raw json.RawMessage) ([]string, bool) {
	arguments, ok := decodeObject(raw, "args")
	if !ok {
		return nil, false
	}
	decoder := json.NewDecoder(bytes.NewReader(arguments["args"]))
	if !consumeJSONToken(decoder, json.Delim('[')) {
		return nil, false
	}
	var args []string
	for decoder.More() {
		if len(args) == maxExecArgs {
			return nil, false
		}
		token, err := decoder.Token()
		arg, isString := token.(string)
		if err != nil || !isString || strings.ContainsRune(arg, 0) {
			return nil, false
		}
		args = append(args, arg)
	}
	if !consumeJSONToken(decoder, json.Delim(']')) || len(args) == 0 {
		return nil, false
	}
	return args, true
}

type textContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type frameworkToolResult struct {
	Content           []textContent   `json:"content"`
	IsError           bool            `json:"isError"`
	StructuredContent json.RawMessage `json:"structuredContent,omitempty"`
}

func (s *session) finishCall(done completedCall) *response {
	call := s.active[done.key]
	delete(s.active, done.key)
	call.cancel()
	if call.cancelled {
		return nil
	}
	return done.reply
}

func (call *toolCall) run(ctx context.Context, id json.RawMessage, version string, fsys fs.FS, execute func(context.Context, []string) ExecResult) *response {
	if call.name == skillsTool {
		bundle, err := skills.LoadAll(fsys)
		if err != nil {
			return result(id, frameworkToolResult{
				Content: []textContent{{Type: "text", Text: "Failed to load skills"}},
				IsError: true,
			})
		}
		return toolResponse(id, version, bundle, false)
	}
	exec := execute(ctx, call.args)
	return toolResponse(id, version, exec, exec.ExitCode != 0)
}

func toolResponse(id json.RawMessage, version string, value any, isError bool) *response {
	text, err := json.Marshal(value)
	if err != nil {
		return failure(id, -32603, "Could not encode tool output")
	}
	output := frameworkToolResult{
		Content: []textContent{{Type: "text", Text: string(text)}},
		IsError: isError,
	}
	if version >= "2025-06-18" {
		output.StructuredContent = text
	}
	return result(id, output)
}

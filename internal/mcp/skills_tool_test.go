package mcp

import (
	"encoding/json"
	"io/fs"
	"os"
	"reflect"
	"testing"
)

func TestSkillsToolDefinition(t *testing.T) {
	const inputSchema = `{"type":"object","properties":{},"additionalProperties":false}`
	spec, err := os.ReadFile("../../docs/mcp-skills.openapi.json")
	if err != nil {
		t.Fatal(err)
	}
	var contract struct {
		Components struct{ Schemas map[string]json.RawMessage }
	}
	if err := json.Unmarshal(spec, &contract); err != nil {
		t.Fatal(err)
	}
	outputSchema := string(contract.Components.Schemas["SkillsResult"])
	for _, version := range []string{"2024-11-05", "2025-03-26", "2025-06-18", "2025-11-25"} {
		t.Run(version, func(t *testing.T) {
			s := session{protocol: version}
			reply := s.request(t.Context(), &request{Method: "tools/list"})
			wire, err := json.Marshal(reply.Result)
			if err != nil {
				t.Fatal(err)
			}
			var listed struct {
				Tools []struct {
					Name         string
					InputSchema  json.RawMessage
					OutputSchema json.RawMessage
					Annotations  json.RawMessage
				}
			}
			if err := json.Unmarshal(wire, &listed); err != nil {
				t.Fatal(err)
			}
			for _, tool := range listed.Tools {
				if tool.Name != "skills" {
					continue
				}
				checkSchema(t, tool.InputSchema, inputSchema)
				if version == "2024-11-05" {
					if tool.Annotations != nil {
						t.Fatalf("unexpected annotations: %s", tool.Annotations)
					}
				} else {
					checkSchema(t, tool.Annotations, `{"readOnlyHint":true,"idempotentHint":true}`)
				}
				if version >= "2025-06-18" {
					checkSchema(t, tool.OutputSchema, outputSchema)
				} else if tool.OutputSchema != nil {
					t.Fatalf("unexpected outputSchema: %s", tool.OutputSchema)
				}
				return
			}
			t.Fatal("missing skills tool")
		})
	}
}

func checkSchema(t *testing.T, raw json.RawMessage, expected string) {
	t.Helper()
	var got, want any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(expected), &want); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %s, want %s", raw, expected)
	}
}

func TestSkillsToolArguments(t *testing.T) {
	for _, tc := range []struct {
		params string
		valid  bool
	}{
		{`{"name":"skills"}`, true},
		{`{"name":"skills","arguments":{},"_meta":{"progressToken":1}}`, true},
		{`{"name":"skills","arguments":null}`, false},
		{`{"name":"skills","arguments":[]}`, false},
		{`{"name":"skills","arguments":{"args":[]}}`, false},
		{`{"name":"skills","arguments":{"name":"example"}}`, false},
		{`{"name":"skills","arguments":{},"arguments":{}}`, false},
		{`{"name":"skills","_meta":null}`, false},
	} {
		t.Run(tc.params, func(t *testing.T) {
			_, reply := parseToolCall(&request{ID: json.RawMessage("2"), Params: json.RawMessage(tc.params)})
			if tc.valid && reply != nil {
				t.Fatalf("valid skills parameters rejected: %+v", reply.Error)
			}
			if !tc.valid && (reply == nil || reply.Error == nil || reply.Error.Code != -32602) {
				t.Fatalf("expected invalid parameters, got %+v", reply)
			}
		})
	}
}

type unreadableSkillsFS struct{}

func (unreadableSkillsFS) Open(string) (fs.File, error) { return nil, fs.ErrPermission }

func TestSkillsToolLoadFailure(t *testing.T) {
	s := session{
		protocol:  "2025-11-25",
		fsys:      unreadableSkillsFS{},
		active:    make(map[string]*activeCall),
		completed: make(chan completedCall),
	}
	msg := &request{ID: json.RawMessage("1"), key: "n:1", Method: "tools/call", Params: json.RawMessage(`{"name":"skills"}`)}
	if reply := s.request(t.Context(), msg); reply != nil {
		t.Fatalf("skills load failed at protocol dispatch: %+v", reply.Error)
	}
	reply := s.finishCall(<-s.completed)
	wire, err := json.Marshal(reply.Result)
	if err != nil {
		t.Fatal(err)
	}
	var res struct {
		IsError           bool
		Content           []textContent
		StructuredContent json.RawMessage
	}
	if err := json.Unmarshal(wire, &res); err != nil {
		t.Fatal(err)
	}
	if !res.IsError || res.StructuredContent != nil || len(res.Content) != 1 || res.Content[0].Type != "text" || res.Content[0].Text == "" {
		t.Fatalf("invalid skills load error: %s", wire)
	}
}

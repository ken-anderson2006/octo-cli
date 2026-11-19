package main

import (
	"encoding/json"
	"io/fs"
	"net/http"
	"path"
	"reflect"
	"testing"

	"github.com/Mininglamp-OSS/octo-cli/skills"
)

func readPublicSkills(t *testing.T, client *http.Client, url string) map[string]any {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("anonymous GET /v1/skills = %s, want 200", resp.Status)
	}
	var result struct{ Data json.RawMessage }
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	var bundle struct {
		Skills []struct {
			Name       string
			Content    string
			References map[string]string
		}
	}
	if err := json.Unmarshal(result.Data, &bundle); err != nil {
		t.Fatal(err)
	}
	foundShared := false
	for _, skill := range bundle.Skills {
		if skill.Name == "octo-shared" {
			foundShared = true
		}
		mainDoc := path.Join(skill.Name, "SKILL.md")
		content, err := skills.FS.ReadFile(mainDoc)
		if err != nil || string(content) != skill.Content {
			t.Fatalf("public content differs from embedded %s (read error %v)", mainDoc, err)
		}
		docs, err := fs.Glob(skills.FS, skill.Name+"/*.md")
		if err != nil {
			t.Fatal(err)
		}
		wantRefs := make(map[string]string)
		for _, doc := range docs {
			if doc == mainDoc {
				continue
			}
			ref, err := skills.FS.ReadFile(doc)
			if err != nil {
				t.Fatal(err)
			}
			wantRefs[path.Base(doc)] = string(ref)
		}
		if !reflect.DeepEqual(skill.References, wantRefs) {
			t.Fatalf("references for %s differ from the embedded files", skill.Name)
		}
	}
	if !foundShared {
		t.Fatal("public catalog missing the bundled octo-shared skill")
	}
	var value map[string]any
	if err := json.Unmarshal(result.Data, &value); err != nil {
		t.Fatal(err)
	}
	return value
}

func assertMCPSkills(t *testing.T, reply mcpReply, version string, want map[string]any) {
	t.Helper()
	var result struct {
		IsError           *bool
		Content           []struct{ Type, Text string }
		StructuredContent json.RawMessage
	}
	if err := json.Unmarshal(reply.Result, &result); err != nil || reply.Error != nil ||
		result.IsError == nil || *result.IsError || len(result.Content) != 1 || result.Content[0].Type != "text" {
		t.Fatalf("skills tool returned invalid content: %s, error = %v (%v)", reply.Result, reply.Error, err)
	}
	if (result.StructuredContent != nil) != (version >= "2025-06-18") {
		t.Fatalf("unexpected structuredContent presence for version %s", version)
	}
	payloads := []json.RawMessage{json.RawMessage(result.Content[0].Text)}
	if result.StructuredContent != nil {
		payloads = append(payloads, result.StructuredContent)
	}
	for _, raw := range payloads {
		var got map[string]any
		if err := json.Unmarshal(raw, &got); err != nil || !reflect.DeepEqual(got, want) {
			t.Fatalf("skills tool result differs from GET /v1/skills data for %s: %s (%v)", version, raw, err)
		}
	}
}

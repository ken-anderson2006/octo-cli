package cmd

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/Mininglamp-OSS/octo-cli/internal/registry"
)

func TestSidebarMoveDeclaresConflict(t *testing.T) {
	raw, err := os.ReadFile("../internal/registry/specs/server.json")
	if err != nil {
		t.Fatal(err)
	}
	var spec struct {
		Paths map[string]map[string]struct {
			Responses map[string]struct {
				Ref string `json:"$ref"`
			} `json:"responses"`
		} `json:"paths"`
	}
	if err := json.Unmarshal(raw, &spec); err != nil {
		t.Fatal(err)
	}
	responses := spec.Paths["/api/v2/cli/sidebar/conversations/{channel_id}/category"]["put"].Responses
	if responses["409"].Ref != "#/components/responses/Conflict" {
		t.Fatal("moving an unfollowed conversation must declare the backend Conflict response")
	}
}

func TestSidebarPublicSchemaIncludesConversationAndChildFields(t *testing.T) {
	stdout, _, err := runServerV2Command(t, "schema", "sidebar.list")
	if err != nil {
		t.Fatal(err)
	}
	var out struct {
		Data registry.OperationDetail `json:"data"`
	}
	if err := json.Unmarshal([]byte(stdout), &out); err != nil {
		t.Fatal(err)
	}
	var findProperty func(*registry.SchemaInfo, string) *registry.SchemaInfo
	findProperty = func(s *registry.SchemaInfo, key string) *registry.SchemaInfo {
		if s == nil {
			return nil
		}
		if p, ok := s.Properties[key]; ok {
			return &p
		}
		for _, branches := range [][]registry.SchemaInfo{s.AnyOf, s.OneOf} {
			for i := range branches {
				if p := findProperty(&branches[i], key); p != nil {
					return p
				}
			}
		}
		return nil
	}
	property := func(s *registry.SchemaInfo, key string) *registry.SchemaInfo {
		t.Helper()
		p := findProperty(s, key)
		if p == nil {
			t.Fatalf("public sidebar schema missing %s", key)
		}
		return p
	}
	rows := property(property(out.Data.ResponseSchema, "data"), "conversations")
	for _, row := range []*registry.SchemaInfo{rows.Items, property(rows.Items, "threads").Items} {
		for _, key := range []string{"channel_id", "channel_type", "name", "unread", "is_followed", "is_muted", "avatar", "last_message"} {
			property(row, key)
		}
		property(property(row, "last_message"), "message_id")
	}
	mute := property(property(rows.Items, "threads").Items, "mute")
	allowsNull := false
	for _, v := range mute.Enum {
		allowsNull = allowsNull || v == nil
	}
	if !mute.Nullable || !allowsNull {
		t.Fatal("public thread mute schema must explicitly admit inherited null")
	}
}

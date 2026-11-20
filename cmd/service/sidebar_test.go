package service

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/Mininglamp-OSS/octo-cli/internal/client"
	"github.com/Mininglamp-OSS/octo-cli/internal/cmdutil"
	"github.com/Mininglamp-OSS/octo-cli/internal/config"
	"github.com/Mininglamp-OSS/octo-cli/internal/credential"
	"github.com/Mininglamp-OSS/octo-cli/internal/registry"
)

func sidebarRoot(t *testing.T, handler http.HandlerFunc) (*cobra.Command, *cmdutil.TestFactory) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	f := cmdutil.NewTestFactory()
	cfg := &config.Config{APIBaseURL: srv.URL, Token: "sidebar-auth-service-token", Format: "json"}
	cred := &credential.Credential{Token: cfg.Token, SpaceID: "untrusted-profile-space", Source: "test"}
	f.SetConfig(cfg)
	f.SetCredential(cred)
	f.SetClient(client.New(cfg, cred, client.Options{ErrOut: f.ErrOut}))
	f.RegistryFunc = registry.MustNew
	root := &cobra.Command{Use: "octo-cli", SilenceUsage: true, SilenceErrors: true}
	RegisterServiceCommands(root, f.Factory)
	return root, f
}

func TestSidebarCommandsWire(t *testing.T) {
	tests := []struct {
		name, command, method, path, body, response, wantData string
	}{
		{"full", "list", "GET", "/conversations", "", `{"data":{"scope":"full","conversations":[],"categories":[],"summary":{"mention_count":0,"follow_count":0}}}`, "scope"},
		{"target", "list --target-channel-id group____child --channel-type 5", "GET", "/conversations?channel_type=5&target_channel_id=group____child", "", `{"data":{"scope":"target","conversations":[]}}`, "scope"},
		{"follow_false", "follow group --channel-type 2 --is-followed=false", "PUT", "/conversations/group/follow?channel_type=2", `{"is_followed":false}`, `{"data":{}}`, ""},
		{"mute_false", "mute group____child --channel-type 5 --mute=false", "PUT", "/conversations/group____child/mute?channel_type=5", `{"mute":false}`, `{"data":{}}`, ""},
		{"move", "move peer --channel-type 1 --category-id work", "PUT", "/conversations/peer/category?channel_type=1", `{"category_id":"work"}`, `{"data":{}}`, ""},
		{"ungroup", `move peer --channel-type 1 --data {"category_id":null}`, "PUT", "/conversations/peer/category?channel_type=1", `{"category_id":null}`, `{"data":{}}`, ""},
		{"mark_read", "mark-read group --channel-type 2 --scope group_and_threads", "POST", "/conversations/group/_mark_read?channel_type=2", `{"scope":"group_and_threads"}`, `{"data":{"channel_id":"group","channel_type":2,"has_mention_me":false,"has_mention_all":false}}`, "channel_id"},
		{"delete", "category delete work", "DELETE", "/categories/work", "", `{"data":{}}`, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			root, f := sidebarRoot(t, func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Method != tc.method || r.URL.RequestURI() != "/api/v2/cli/sidebar"+tc.path {
					t.Errorf("request = %s %s", r.Method, r.URL.RequestURI())
				}
				if r.Header.Get("Authorization") != "Bearer sidebar-auth-service-token" || r.Header.Get("X-Space-ID") != "" {
					t.Error("must use bearer identity and token-bound Space, not profile routing")
				}
				var got, want any
				if tc.body != "" {
					if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
						t.Error(err)
					}
					if err := json.Unmarshal([]byte(tc.body), &want); err != nil {
						t.Fatal(err)
					}
					if !reflect.DeepEqual(got, want) {
						t.Errorf("body = %#v; want %#v", got, want)
					}
				}
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprint(w, tc.response)
			})
			root.SetArgs(append([]string{"server", "sidebar"}, strings.Fields(tc.command)...))
			if err := root.Execute(); err != nil {
				t.Fatalf("sidebar command unavailable or failed: %v; %s", err, f.ErrOut.String())
			}
			if calls != 1 {
				t.Fatalf("HTTP calls = %d; want 1", calls)
			}
			var out struct {
				OK   bool           `json:"ok"`
				Data map[string]any `json:"data"`
			}
			if err := json.Unmarshal(f.Out.Bytes(), &out); err != nil {
				t.Fatal(err)
			}
			if !out.OK || out.Data == nil || out.Data["data"] != nil {
				t.Fatalf("unexpected CLI envelope: %s", f.Out.String())
			}
			if tc.wantData != "" && out.Data[tc.wantData] == nil {
				t.Errorf("missing unwrapped payload %q: %s", tc.wantData, f.Out.String())
			}
		})
	}
}

func TestSidebarRejectsInvalidInputBeforeHTTP(t *testing.T) {
	for _, command := range []string{
		"category delete " + strings.Repeat("x", 41),
		"follow " + strings.Repeat("x", 101) + " --channel-type 2 --is-followed=true",
		"follow group --channel-type 1 --is-followed=true",
		"follow group --channel-type 2",
		"mute group --channel-type 2 --data {\"mute\":null}",
		"move group --channel-type 2",
		"move group --channel-type 2 --category-id=",
		"follow group --channel-type 2 --data {\"is_followed\":true,\"uid\":\"other\"}",
		"mark-read peer --channel-type 1 --scope group_and_threads",
		"mark-read group --channel-type 2 --scope channel",
		"list --page-size 10",
		"list --page-all",
	} {
		t.Run(command, func(t *testing.T) {
			calls := 0
			root, _ := sidebarRoot(t, func(http.ResponseWriter, *http.Request) { calls++ })
			root.SetArgs(append([]string{"server", "sidebar"}, strings.Fields(command)...))
			if err := root.Execute(); err == nil {
				t.Fatal("expected local validation error")
			}
			if calls != 0 {
				t.Fatalf("invalid input sent %d HTTP requests", calls)
			}
		})
	}
}

func TestSidebarRegistryContract(t *testing.T) {
	r := registry.MustNew()
	operations := map[string]string{
		"sidebar.list": "GET", "sidebar.follow": "PUT", "sidebar.mute": "PUT",
		"sidebar.move": "PUT", "sidebar.category.delete": "DELETE", "sidebar.mark_read": "POST",
	}
	for id, method := range operations {
		d, ok := r.GetOperation(id)
		if !ok {
			t.Errorf("missing sidebar operation %s", id)
			continue
		}
		if d.Method != method || !strings.HasPrefix(d.Path, "/api/v2/cli/sidebar/") || !d.DataEnvelope || !d.SpaceHeaderSet || d.SpaceHeader {
			t.Errorf("invalid sidebar contract for %s: %+v", id, d)
		}
		if d.Pagination != nil {
			t.Errorf("%s must not introduce pagination", id)
		}
		if id == "sidebar.mark_read" && d.RetryMode != "never" {
			t.Error("mark-read must not automatically retry and clear later messages")
		}
	}
}

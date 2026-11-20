package cmd

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/Mininglamp-OSS/octo-cli/internal/config"
	"github.com/Mininglamp-OSS/octo-cli/internal/output"
)

func TestSidebarMoveConflictIsDefinitiveAndNotReplayed(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != http.MethodPut || r.URL.RequestURI() != "/api/v2/cli/sidebar/conversations/g/category?channel_type=2" {
			t.Errorf("request = %s %s", r.Method, r.URL.RequestURI())
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"error":{"code":"CONFLICT","message":"Conversation is not followed."}}`))
	}))
	defer server.Close()
	t.Setenv(config.EnvAPIBaseURL, server.URL)
	t.Setenv(config.EnvToken, phaseOneToken)
	for _, args := range [][]string{
		{"server", "sidebar", "move", "g", "--channel-type", "2", "--data", `{"category_id":null}`},
		{"api", "PUT", "/api/v2/cli/sidebar/conversations/g/category", "--params", `{"channel_type":2}`, "--data", `{"category_id":null}`},
	} {
		calls.Store(0)
		stdout, _, err := runServerV2Command(t, args...)
		if exit := output.AsExitError(err); exit == nil || exit.Code != "CONFLICT" {
			t.Errorf("expected definitive conflict, got %v", err)
		}
		if calls.Load() != 1 || stdout != "" {
			t.Errorf("expected one request and no success; calls=%d stdout=%q", calls.Load(), stdout)
		}
	}
}

func TestSidebarGeneratedAndRawMutationsDoNotReplay(t *testing.T) {
	for _, tc := range []struct {
		name, method, path, body string
		args                     []string
	}{
		{"follow", "PUT", "/conversations/g/follow?channel_type=2", `{"is_followed":false}`, []string{"follow", "g", "--channel-type", "2", "--is-followed=false"}},
		{"mute", "PUT", "/conversations/g/mute?channel_type=2", `{"mute":false}`, []string{"mute", "g", "--channel-type", "2", "--mute=false"}},
		{"move", "PUT", "/conversations/g/category?channel_type=2", `{"category_id":null}`, []string{"move", "g", "--channel-type", "2", "--data", `{"category_id":null}`}},
		{"mark-read", "POST", "/conversations/g/_mark_read?channel_type=2", `{"scope":"group_and_threads"}`, []string{"mark-read", "g", "--channel-type", "2", "--scope", "group_and_threads"}},
		{"delete", "DELETE", "/categories/c", "", []string{"category", "delete", "c"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.Method != tc.method || r.URL.RequestURI() != "/api/v2/cli/sidebar"+tc.path {
					t.Errorf("request = %s %s", r.Method, r.URL.RequestURI())
				}
				if r.Header.Get("Authorization") != "Bearer "+phaseOneToken || r.Header.Get("X-Space-ID") != "" {
					t.Error("expected configured Bearer and token-bound Space")
				}
				w.WriteHeader(http.StatusBadGateway)
			}))
			defer server.Close()
			t.Setenv(config.EnvAPIBaseURL, server.URL)
			t.Setenv(config.EnvToken, phaseOneToken)
			t.Setenv(config.EnvSpaceID, "untrusted-runtime-space")
			raw := []string{"api", tc.method, "/api/v2/cli/sidebar" + tc.path}
			if tc.body != "" {
				raw = append(raw, "--data", tc.body)
			}
			// Raw API query parameters use --params, never a query in the path.
			if tc.name != "delete" {
				raw[2] = "/api/v2/cli/sidebar" + tc.path[:len(tc.path)-len("?channel_type=2")]
				raw = append(raw, "--params", `{"channel_type":2}`)
			}
			for _, args := range [][]string{append([]string{"server", "sidebar"}, tc.args...), raw} {
				calls.Store(0)
				stdout, _, err := runServerV2Command(t, args...)
				if exit := output.AsExitError(err); exit == nil || exit.Code != "RESULT_UNKNOWN" {
					t.Errorf("expected unknown outcome, got %v", err)
				}
				if calls.Load() != 1 || stdout != "" {
					t.Errorf("expected exactly one request and no success; calls=%d stdout=%q", calls.Load(), stdout)
				}
			}
		})
	}
}

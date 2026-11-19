package cmd

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Mininglamp-OSS/octo-cli/internal/cmdutil"
	"github.com/Mininglamp-OSS/octo-cli/internal/config"
	"github.com/Mininglamp-OSS/octo-cli/internal/output"
)

const loopWorkspace = "11111111-1111-4111-8111-111111111111"
const loopTask = "22222222-2222-4222-8222-222222222222"

func runLoopV2Command(t *testing.T, args ...string) (string, string, error) {
	t.Helper()
	streams, in, out, errOut := cmdutil.NewTestIOStreams()
	f := cmdutil.NewDefaultFactory()
	f.IOStreams = streams
	return execRoot(t, &cmdutil.TestFactory{Factory: f, In: in, Out: out, ErrOut: errOut}, args...)
}

func TestLoopV2ReadCommands(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+phaseOneToken || r.Header.Get("X-Space-Id") != "" {
			t.Error("Fleet must use the business Token and its bound Space")
		}
		switch r.URL.Path {
		case "/fleet/api/v2/cli/workspaces":
			if r.Header.Get("X-Workspace-ID") != "" {
				t.Error("workspace discovery must not require a workspace")
			}
			io.WriteString(w, `{"data":[{"workspace_id":"`+loopWorkspace+`"}],"pagination":{"page":1,"page_size":20,"total":1}}`)
		case "/fleet/api/v2/cli/workspaces/" + loopWorkspace:
			io.WriteString(w, `{"data":{"workspace_id":"`+loopWorkspace+`"}}`)
		case "/fleet/api/v2/cli/workspaces/" + loopWorkspace + "/members":
			if r.Header.Get("X-Workspace-ID") != "" || r.URL.RawQuery != "page=2&page_size=3" {
				t.Errorf("member scope must come from the URL: %q %q", r.Header.Get("X-Workspace-ID"), r.URL.RawQuery)
			}
			io.WriteString(w, `{"data":[{"member_id":"owner-uid"}],"pagination":{"page":2,"page_size":3,"total":4}}`)
		case "/fleet/api/v2/cli/tasks":
			if r.Header.Get("X-Workspace-ID") != loopWorkspace || r.URL.RawQuery != "page=2&page_size=10" {
				t.Errorf("unexpected task scope/query: %q %q", r.Header.Get("X-Workspace-ID"), r.URL.RawQuery)
			}
			io.WriteString(w, `{"data":[{"task_id":"`+loopTask+`"}],"pagination":{"page":2,"page_size":10,"total":11}}`)
		default:
			t.Errorf("unexpected Fleet path %q", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	t.Setenv(config.EnvAPIBaseURL, server.URL)
	t.Setenv(config.EnvToken, phaseOneToken)
	t.Setenv(config.EnvSpaceID, "profile-space")
	for _, tc := range []struct {
		name     string
		args     []string
		jq, want string
	}{
		{"workspace list", []string{"loop", "workspace", "list"}, ".data[0].workspace_id", `"` + loopWorkspace + `"`},
		{"workspace detail", []string{"loop", "workspace", "get", loopWorkspace}, ".data.workspace_id", `"` + loopWorkspace + `"`},
		{"workspace members positional", []string{"loop", "workspace", "member", "list", loopWorkspace, "--page", "2", "--page-size", "3"}, ".data[0].member_id", `"owner-uid"`},
		{"workspace members flag", []string{"loop", "workspace", "member", "list", "--workspace-id", loopWorkspace, "--page", "2", "--page-size", "3"}, ".data[0].member_id", `"owner-uid"`},
		{"task list", []string{"loop", "task", "list", "--workspace-id", loopWorkspace, "--page", "2", "--page-size", "10"}, "[.data[0].task_id,._pagination.page]", `["` + loopTask + `",2]`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stdout, _, err := runLoopV2Command(t, append([]string{"--jq", tc.jq}, tc.args...)...)
			if err != nil {
				t.Fatal(err)
			}
			var value any
			if err := json.Unmarshal([]byte(stdout), &value); err != nil {
				t.Fatal(err)
			}
			got, _ := json.Marshal(value)
			if string(got) != tc.want {
				t.Fatalf("got %s, want %s", got, tc.want)
			}
		})
	}
}

func TestLoopV2TaskCommands(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Workspace-ID") != loopWorkspace {
			t.Error("task command lost workspace scope")
		}
		if r.Header.Get("Authorization") != "Bearer "+phaseOneToken {
			t.Error("task command lost Token")
		}
		if !strings.HasPrefix(r.URL.Path, "/fleet/api/v2/cli/tasks") {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		switch r.Method {
		case http.MethodPost, http.MethodPatch:
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			if body["title"] != "fixture task" {
				t.Errorf("body title = %v", body["title"])
			}
			if r.Method == http.MethodPatch && body["expected_version"] != float64(1) {
				t.Errorf("expected version = %v", body["expected_version"])
			}
		case http.MethodGet, http.MethodDelete:
			if r.URL.Path != "/fleet/api/v2/cli/tasks/"+loopTask {
				t.Errorf("task id path = %s", r.URL.Path)
			}
		default:
			t.Errorf("unexpected method: %s", r.Method)
		}
		io.WriteString(w, `{"data":{"task_id":"`+loopTask+`","title":"fixture task"}}`)
	}))
	defer server.Close()
	t.Setenv(config.EnvAPIBaseURL, server.URL)
	t.Setenv(config.EnvToken, phaseOneToken)
	for _, args := range [][]string{
		{"loop", "task", "create", "--workspace-id", loopWorkspace, "--title", "fixture task"},
		{"loop", "task", "get", loopTask, "--workspace-id", loopWorkspace},
		{"loop", "task", "update", loopTask, "--workspace-id", loopWorkspace, "--title", "fixture task", "--expected-version", "1"},
		{"loop", "task", "delete", loopTask, "--workspace-id", loopWorkspace},
	} {
		t.Run(strings.Join(args[:3], " "), func(t *testing.T) {
			stdout, _, err := runLoopV2Command(t, append([]string{"--jq", ".data.task_id"}, args...)...)
			if err != nil {
				t.Fatal(err)
			}
			if strings.TrimSpace(stdout) != `"`+loopTask+`"` {
				t.Fatalf("unexpected task response: %s", stdout)
			}
		})
	}
}

func TestLoopV2WorkspaceRequired(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests.Add(1) }))
	defer server.Close()
	t.Setenv(config.EnvAPIBaseURL, server.URL)
	t.Setenv(config.EnvToken, phaseOneToken)
	_, _, err := runLoopV2Command(t, "loop", "task", "get", loopTask)
	if ee := output.AsExitError(cmdutil.WrapCLIError(err)); ee == nil || ee.Type != "validation" || !strings.Contains(err.Error(), `required flag(s) "workspace-id" not set`) {
		t.Fatalf("expected local workspace validation, got %v", err)
	}
	if requests.Load() != 0 {
		t.Fatal("missing workspace must not send a request")
	}
}

func TestLoopV2MutationsNotReplayed(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer server.Close()
	t.Setenv(config.EnvAPIBaseURL, server.URL)
	t.Setenv(config.EnvToken, phaseOneToken)
	_, _, err := runLoopV2Command(t, "loop", "task", "create", "--workspace-id", loopWorkspace, "--title", "fixture task")
	if ee := output.AsExitError(err); ee == nil || ee.Code != "RESULT_UNKNOWN" {
		t.Fatalf("expected unknown mutation outcome, got %v", err)
	}
	if requests.Load() != 1 {
		t.Fatalf("mutation sent %d requests", requests.Load())
	}
}

func TestLoopV2EnvironmentDiagnostics(t *testing.T) {
	const secret = "fixture-environment-secret"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"data":{"custom_env":{"SERVICE_KEY":"`+secret+`"}}}`)
	}))
	defer server.Close()
	t.Setenv(config.EnvAPIBaseURL, server.URL)
	t.Setenv(config.EnvToken, phaseOneToken)
	args := []string{"loop", "expert", "environment", "get", loopTask, "--workspace-id", loopWorkspace, "--verbose", "--jq", ".data.custom_env.SERVICE_KEY"}
	stdout, stderr, err := runLoopV2Command(t, args...)
	if err != nil || !strings.Contains(stdout, secret) {
		t.Fatalf("environment read failed: %v", err)
	}
	if strings.Contains(stderr, secret) {
		t.Fatal("environment response leaked into verbose diagnostics")
	}
	args = []string{"loop", "expert", "environment", "update", loopTask, "--workspace-id", loopWorkspace, "--data", `{"custom_env":{"SERVICE_KEY":"` + secret + `"}}`, "--dry-run"}
	stdout, stderr, err = runLoopV2Command(t, args...)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(stdout+stderr, secret) {
		t.Fatal("environment request leaked into dry-run diagnostics")
	}
}

func TestLoopV2CommandHierarchy(t *testing.T) {
	f := cmdutil.NewDefaultFactory()
	root := NewRootCmd(f)
	loop, _, err := root.Find([]string{"loop"})
	if err != nil || loop.Name() != "loop" {
		t.Fatalf("expected Loop entry, got %v", err)
	}
	for _, child := range loop.Commands() {
		if child.Name() == "loop" || child.Name() == "fleet" {
			t.Fatalf("unexpected nested service %s", child.Name())
		}
	}
	for _, child := range root.Commands() {
		if child.Name() == "fleet" {
			t.Fatal("Fleet implementation name must not be a CLI entry")
		}
	}
	operations := f.Registry().ListOperations("loop")
	if len(operations) == 0 {
		t.Fatal("Loop service must register operations")
	}
	paths := map[string]string{}
	for _, op := range operations {
		parts := strings.Split(op.ID, ".")
		if parts[0] == "loop" {
			parts = parts[1:]
		}
		args := []string{"loop"}
		for _, part := range parts {
			args = append(args, strings.ReplaceAll(part, "_", "-"))
		}
		path := strings.Join(args, " ")
		if previous := paths[path]; previous != "" {
			t.Fatalf("command %s is shared by %s and %s", path, previous, op.ID)
		}
		paths[path] = op.ID
		leaf, _, err := root.Find(args)
		if err != nil || !strings.Contains(leaf.Long, "operationId: "+op.ID+"\n") {
			t.Fatalf("operation %s is not reachable as %s: %v", op.ID, path, err)
		}
	}
}

func TestLoopV2OperationScope(t *testing.T) {
	registry := cmdutil.NewDefaultFactory().Registry()
	for _, id := range []string{
		"runtime.update",
		"runtime.delete",
		"runtime.archive_and_delete",
		"runtime.update_request.create",
		"runtime.model_request.create",
		"runtime.local_skill_request.create",
		"runtime.local_skill_import.create",
		"workspace.create",
		"workspace.delete",
		"workspace.update",
		"task.count",
		"task.creator.list",
		"task.selection",
		"task.watch",
		"task.unwatch",
		"task_view.create",
		"task_view.list",
		"task_view.get",
		"task_view.delete",
		"skill.deletion_impact.get",
	} {
		if _, ok := registry.GetOperation(id); ok {
			t.Errorf("excluded operation %s must not register a command", id)
		}
	}
	for _, id := range []string{
		"runtime.list", "runtime.usage.get", "runtime.usage_by_expert.get",
		"runtime.usage_by_hour.get", "runtime.activity.list", "runtime.update_request.get",
		"runtime.model_request.get", "runtime.local_skill_request.get", "runtime.local_skill_import.get",
		"workspace.list", "workspace.get", "workspace.member.list",
	} {
		op, ok := registry.GetOperation(id)
		if !ok || op.Service != "loop" || op.Method != http.MethodGet {
			t.Errorf("missing Loop read operation %s", id)
		}
	}
	if got := len(registry.ListOperations("loop")); got != 126 {
		t.Errorf("Loop operation count = %d, want 126", got)
	}
}

func TestLoopV2DeclaredCommandInputs(t *testing.T) {
	for _, tc := range []struct {
		name, method, path, query, body string
		args                            []string
	}{
		{"project create", "POST", "/projects", "", `{"title":"fixture","description":"details","status":"planned","priority":"high"}`, []string{"project", "create", "--title", "fixture", "--description", "details", "--status", "planned", "--priority", "high"}},
		{"project update", "PATCH", "/projects/" + loopTask, "", `{"title":"fixture"}`, []string{"project", "update", loopTask, "--title", "fixture"}},
		{"resource create", "POST", "/projects/" + loopTask + "/resources", "", `{"resource_type":"github_repo","resource_ref":{"url":"https://github.com/example/fixture"}}`, []string{"project", "resource", "create", loopTask, "--type", "github_repo", "--data", `{"resource_ref":{"url":"https://github.com/example/fixture"}}`}},
		{"resource delete", "DELETE", "/projects/" + loopTask + "/resources/" + loopTask, "", `null`, []string{"project", "resource", "delete", loopTask, loopTask}},
		{"project search positional", "GET", "/projects/_search", "q=fixture", `null`, []string{"project", "search", "fixture"}},
		{"task search positional", "GET", "/tasks/_search", "q=fixture", `null`, []string{"task", "search", "fixture"}},
		{"task group filters", "GET", "/tasks/_grouped", "group_by=assignee&project_id=" + loopTask + "&status=todo", `null`, []string{"task", "group", "--group-by", "assignee", "--project-id", loopTask, "--status", "todo"}},
		{"task create optional fields", "POST", "/tasks", "", `{"title":"fixture","status":"todo","project_id":"` + loopTask + `","due_date":"2026-11-18"}`, []string{"task", "create", "--title", "fixture", "--status", "todo", "--project-id", loopTask, "--due-date", "2026-11-18"}},
		{"task clear project", "PATCH", "/tasks/" + loopTask, "", `{"project_id":null,"handoff_note":"handoff","suppress_run":true}`, []string{"task", "update", loopTask, "--data", `{"project_id":null}`, "--handoff-note", "handoff", "--suppress-run"}},
		{"task member flag", "POST", "/tasks", "", `{"title":"fixture","assignee_type":"member","assignee_octo_uid":"owner-uid"}`, []string{"task", "create", "--title", "fixture", "--assignee-type", "member", "--assignee-member-id", "owner-uid"}},
		{"task expert ID", "PATCH", "/tasks/" + loopTask, "", `{"assignee_id":"` + loopTask + `","assignee_type":"expert"}`, []string{"task", "update", loopTask, "--assignee-type", "expert", "--assignee-id", loopTask}},
		{"task clear assignee", "PATCH", "/tasks/" + loopTask, "", `{"assignee_id":null,"assignee_type":null}`, []string{"task", "update", loopTask, "--data", `{"assignee_id":null,"assignee_type":null}`}},
		{"task assignee filter", "GET", "/tasks", "assignee_id=" + loopTask + "&assignee_types=expert", `null`, []string{"task", "list", "--assignee-id", loopTask, "--assignee-types", "expert"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if r.Method != tc.method || r.URL.Path != "/fleet/api/v2/cli"+tc.path || r.URL.RawQuery != tc.query {
					t.Errorf("request = %s %s?%s", r.Method, r.URL.Path, r.URL.RawQuery)
				}
				var got, want any
				if r.Body != nil {
					raw, _ := io.ReadAll(r.Body)
					if len(raw) > 0 {
						if err := json.Unmarshal(raw, &got); err != nil {
							t.Error(err)
						}
					}
				}
				if err := json.Unmarshal([]byte(tc.body), &want); err != nil {
					t.Fatal(err)
				}
				left, _ := json.Marshal(got)
				right, _ := json.Marshal(want)
				if string(left) != string(right) {
					t.Errorf("body=%s want=%s", left, right)
				}
				io.WriteString(w, `{"data":{"matched":true}}`)
			}))
			defer server.Close()
			t.Setenv(config.EnvAPIBaseURL, server.URL)
			t.Setenv(config.EnvToken, phaseOneToken)
			args := append([]string{"loop"}, tc.args...)
			args = append(args, "--workspace-id", loopWorkspace)
			_, _, err := runLoopV2Command(t, args...)
			if err != nil {
				t.Fatal(err)
			}
			if requests.Load() != 1 {
				t.Fatalf("command sent %d requests; expected exactly one without roster lookups", requests.Load())
			}
		})
	}
}

func TestLoopV2UnsupportedCompatibilityInputs(t *testing.T) {
	t.Setenv(config.EnvAPIBaseURL, "http://127.0.0.1:1")
	t.Setenv(config.EnvToken, phaseOneToken)
	for _, args := range [][]string{
		{"task", "assign", loopTask, "--unassign"},
		{"project", "resource", "add", loopTask},
		{"project", "resource", "remove", loopTask, loopTask},
		{"task", "create", "--title", "fixture", "--project", loopTask},
		{"task", "create", "--title", "fixture", "--assignee", "fixture"},
		{"task", "create", "--title", "fixture", "--assignee-octo-uid", "owner-uid"},
		{"project", "resource", "create", loopTask, "--type", "github_repo", "--ref", `{"url":"https://github.com/example/fixture"}`},
		{"workspace", "member", "list", loopWorkspace, "--offset", "50"},
		{"workspace", "member", "list", loopWorkspace, "--limit", "50"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			command := append([]string{"loop"}, args...)
			if args[0] != "workspace" {
				command = append(command, "--workspace-id", loopWorkspace)
			}
			command = append(command, "--dry-run")
			_, _, err := runLoopV2Command(t, command...)
			exit := output.AsExitError(cmdutil.WrapCLIError(err))
			if err == nil || (!strings.Contains(err.Error(), "unknown") && (exit == nil || exit.Code != "INVALID_FLAG")) {
				t.Fatalf("expected a rejected command or flag, got %v", err)
			}
		})
	}
}

func TestLoopV2ProjectLeadSelectors(t *testing.T) {
	t.Setenv(config.EnvAPIBaseURL, "http://127.0.0.1:1")
	t.Setenv(config.EnvToken, phaseOneToken)
	_, _, err := runLoopV2Command(t, "loop", "project", "create", "--workspace-id", loopWorkspace,
		"--title", "fixture", "--lead-id", loopTask, "--lead-member-id", "owner-uid", "--dry-run")
	exit := output.AsExitError(cmdutil.WrapCLIError(err))
	if exit == nil || exit.Type != "validation" {
		t.Fatalf("expected mutually exclusive lead selector validation, got %v", err)
	}
}

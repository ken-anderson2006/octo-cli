package registry

import (
	"testing"
)

func TestGetSpecReturnsNilForUnknown(t *testing.T) {
	r := MustNew()
	if spec := r.GetSpec("nosuch"); spec != nil {
		t.Fatalf("GetSpec(nosuch): got non-nil %v", spec)
	}
}

func TestConditionalQueryMetadataRejectsMalformedContracts(t *testing.T) {
	base := func(extension any, peerSchema map[string]any) map[string]any {
		return map[string]any{
			"paths": map[string]any{
				"/items": map[string]any{
					"get": map[string]any{
						"operationId": "item.list",
						"parameters": []any{
							map[string]any{"name": "kind", "in": "query", "schema": map[string]any{"type": "string"}, "x-octo-required-unless-query": extension},
							map[string]any{"name": "mode", "in": "query", "schema": peerSchema},
						},
					},
				},
			},
		}
	}
	for _, tc := range []struct {
		name string
		doc  map[string]any
	}{
		{"extension is not an object", base("mode=mine", map[string]any{"type": "string"})},
		{"missing peer", base(map[string]any{"name": "other", "value": "mine"}, map[string]any{"type": "string"})},
		{"non-string peer", base(map[string]any{"name": "mode", "value": "1"}, map[string]any{"type": "integer"})},
		{"missing string value", base(map[string]any{"name": "mode"}, map[string]any{"type": "string"})},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := validateConditionalQueryMetadata(tc.doc); err == nil {
				t.Fatal("malformed conditional query metadata was accepted")
			}
		})
	}
}

func TestGetOperationNotFound(t *testing.T) {
	r := MustNew()
	if _, ok := r.GetOperation("does.not.exist"); ok {
		t.Fatal("GetOperation: expected ok=false for unknown id")
	}
}

// TestHasSuccessBodyResolvesResponseRef pins the item-3 fix: a 2xx response may
// be expressed inline OR via {"$ref":"#/components/responses/..."}. hasSuccessBody
// must resolve the ref before checking for a content body, otherwise a spec that
// factors its success response into components.responses would fail-closed and
// silently drop the --output/-o flag (BinaryBody=false) for a real binary body.
func TestHasSuccessBodyResolvesResponseRef(t *testing.T) {
	doc := map[string]any{
		"components": map[string]any{
			"responses": map[string]any{
				"BoardImage": map[string]any{
					"description": "shared image response",
					"content": map[string]any{
						"image/png": map[string]any{},
					},
				},
				"NoBody": map[string]any{
					"description": "bodyless shared response",
				},
			},
		},
	}

	cases := []struct {
		name  string
		resps map[string]any
		want  bool
	}{
		{
			name:  "inline 2xx content body",
			resps: map[string]any{"200": map[string]any{"content": map[string]any{"image/png": map[string]any{}}}},
			want:  true,
		},
		{
			name:  "2xx response via components.responses $ref with body",
			resps: map[string]any{"200": map[string]any{"$ref": "#/components/responses/BoardImage"}},
			want:  true,
		},
		{
			name:  "2xx response via $ref to a bodyless response",
			resps: map[string]any{"204": map[string]any{"$ref": "#/components/responses/NoBody"}},
			want:  false,
		},
		{
			name:  "unresolvable $ref is treated as no body",
			resps: map[string]any{"200": map[string]any{"$ref": "#/components/responses/Missing"}},
			want:  false,
		},
		{
			name:  "non-2xx content body is ignored",
			resps: map[string]any{"400": map[string]any{"content": map[string]any{"application/json": map[string]any{}}}},
			want:  false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := hasSuccessBody(doc, tc.resps); got != tc.want {
				t.Errorf("hasSuccessBody = %v, want %v", got, tc.want)
			}
		})
	}
}

// matter carries x-octo-disabled in its embedded spec — it must stay loaded
// (engine + schema introspection depend on it) yet drop out of the
// caller-facing enabled views.

func TestTruthy(t *testing.T) {
	cases := []struct {
		in   any
		want bool
	}{
		{true, true},
		{"true", true},
		{false, false},
		{"false", false},
		{"", false},
		{nil, false},
		{1, false},
	}
	for _, c := range cases {
		if got := truthy(c.in); got != c.want {
			t.Errorf("truthy(%#v) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestAllDomainOperationCounts(t *testing.T) {
	// Caller-facing operation counts per service. Operations marked
	// x-octo-cli-hidden remain in an embedded backend spec but are excluded
	// here; Loop workspace and runtime mutations are examples.
	r := MustNewBotAPI()
	expected := map[string]int{
		"matter":      14,
		"message":     14,
		"group":       17,
		"thread":      17,
		"file":        4,
		"bot":         12,
		"event":       2,
		"docs":        76,
		"drive":       56,
		"html":        36,
		"marketplace": 29,
		"mail":        18,
		"summary":     4,
		"loop":        128,
	}
	totalWant := 0
	for svc, want := range expected {
		totalWant += want
		got := len(r.ListOperations(svc))
		if got != want {
			t.Errorf("%s: got %d ops (%v), want %d", svc, got, operationIDs(r.ListOperations(svc)), want)
		}
	}
	all := r.ListAllOperations()
	if len(all) != totalWant {
		t.Errorf("ListAllOperations: got %d, want %d", len(all), totalWant)
	}
}

func TestCLIHiddenOperationsAreExcludedFromRegistry(t *testing.T) {
	var doc map[string]any
	if err := json.Unmarshal([]byte(`{
		"openapi": "3.1.0",
		"x-octo-service": "fixture",
		"paths": {
			"/fixture/items": {
				"get": {"operationId": "fixture.item.list", "summary": "List items", "x-octo-risk": "read"},
				"post": {"operationId": "fixture.item.create", "summary": "Create item", "x-octo-risk": "write", "x-octo-cli-hidden": true}
			},
			"/fixture/items/{item_id}": {
				"delete": {"operationId": "fixture.item.delete", "summary": "Delete item", "x-octo-risk": "high-risk-write", "x-octo-cli-hidden": "true"}
			}
		}
	}`), &doc); err != nil {
		t.Fatalf("fixture spec: %v", err)
	}
	r := &Registry{specs: map[string]map[string]any{"fixture": doc}}

	if _, ok := r.GetOperation("fixture.item.list"); !ok {
		t.Fatal("fixture.item.list: visible operation must be exposed")
	}
	for _, operationID := range []string{"fixture.item.create", "fixture.item.delete"} {
		if _, ok := r.GetOperation(operationID); ok {
			t.Fatalf("%s must not be exposed by the CLI registry", operationID)
		}
	}
	for _, list := range [][]OperationInfo{r.ListOperations("fixture"), r.ListAllOperations()} {
		if len(list) != 1 || list[0].ID != "fixture.item.list" {
			t.Fatalf("listed operations = %v, want only fixture.item.list", operationIDs(list))
		}
	}
}

func TestHTMLOperationsUseDocsHTMLGatewayPrefix(t *testing.T) {
	r := MustNewBotAPI()
	for _, op := range r.ListOperations("html") {
		// The rendered page and asset routes live outside the JSON API under
		// /docs-html/d/ (octo-docs-html internal/transport/httpx/server.go).
		if strings.HasPrefix(op.Path, "/docs-html/d/") {
			if d, ok := r.GetOperation(op.ID); !ok || !d.BinaryResponse {
				t.Errorf("%s: page route %q must be a binary-response operation", op.ID, op.Path)
			}
			continue
		}
		if want := "/docs-html/v1/"; !strings.HasPrefix(op.Path, want) {
			t.Errorf("%s: path = %q, want prefix %q", op.ID, op.Path, want)
		}
	}
}

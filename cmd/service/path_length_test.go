package service

import (
	"testing"

	"github.com/Mininglamp-OSS/octo-cli/internal/registry"
)

func TestStrictPathStringLengthOptIn(t *testing.T) {
	for _, tc := range []struct {
		name, value string
		strict      bool
		wantErr     bool
	}{
		{"too long", "abcd", true, true},
		{"too short", "a", true, true},
		{"maximum", "abc", true, false},
		{"Unicode code points", "界界界", true, false},
		{"legacy unchanged", "abcd", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rt := &operationRuntime{
				pathParams: []string{"item_id"},
				detail: &registry.OperationDetail{StrictRequestSchema: tc.strict, Parameters: []registry.ParamInfo{
					{Name: "item_id", In: "path", Type: "string", MinLength: 2, MaxLength: 3},
				}},
			}
			err := validatePathArgs(rt, []string{tc.value})
			if (err != nil) != tc.wantErr {
				t.Fatalf("path validation error = %v, want error %t", err, tc.wantErr)
			}
		})
	}
}

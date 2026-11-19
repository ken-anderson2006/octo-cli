package service

import (
	"testing"

	"github.com/Mininglamp-OSS/octo-cli/internal/registry"
)

func TestRequiredNullablePropertyPresence(t *testing.T) {
	schema := &registry.SchemaInfo{
		Type: "object", Required: []string{"category_id"},
		Properties: map[string]registry.SchemaInfo{"category_id": {Type: "string", Nullable: true}},
	}
	v := bodySchemaValidator{enforcePublicAPIConstraints: true}
	if err := v.validateRequiredProperties(schema, map[string]any{"category_id": nil}, ""); err != nil {
		t.Fatalf("present nullable property must satisfy required: %v", err)
	}
	if err := v.validateRequiredProperties(schema, map[string]any{}, ""); err == nil {
		t.Fatal("nullable property must still be present")
	}
	property := schema.Properties["category_id"]
	property.Nullable = false
	schema.Properties["category_id"] = property
	if err := v.validateRequiredProperties(schema, map[string]any{"category_id": nil}, ""); err == nil {
		t.Fatal("non-nullable required null must still be refused")
	}
}

func TestRequiredNullablePropertyRetainsOtherConstraints(t *testing.T) {
	for _, tc := range []struct {
		name     string
		property registry.SchemaInfo
		value    any
		wantErr  bool
	}{
		{"string null", registry.SchemaInfo{Type: "string", Nullable: true, MinLength: 1}, nil, false},
		{"empty string", registry.SchemaInfo{Type: "string", Nullable: true, MinLength: 1}, "", true},
		{"boolean null", registry.SchemaInfo{Type: "boolean", Nullable: true}, nil, false},
		{"enum excludes null", registry.SchemaInfo{Type: "string", Nullable: true, Enum: []any{"work"}}, nil, true},
		{"enum includes null", registry.SchemaInfo{Type: "string", Nullable: true, Enum: []any{"work", nil}}, nil, false},
		{"const excludes null", registry.SchemaInfo{Type: "string", Nullable: true, Const: "work"}, nil, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			schema := &registry.SchemaInfo{Type: "object", Required: []string{"value"}, Properties: map[string]registry.SchemaInfo{"value": tc.property}}
			v := bodySchemaValidator{enforcePublicAPIConstraints: true}
			err := v.validate(schema, map[string]any{"value": tc.value}, "", "")
			if (err != nil) != tc.wantErr {
				t.Fatalf("validation error = %v, want error %t", err, tc.wantErr)
			}
		})
	}
}

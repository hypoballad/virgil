package jsonutil

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestAnalyzeStructureDepth(t *testing.T) {
	var data interface{}
	if err := json.Unmarshal([]byte(`{
		"users": [{"id": 1, "name": "Alice", "roles": ["admin"]}],
		"metadata": {"version": "1", "tags": ["a", "b"]},
		"enabled": true
	}`), &data); err != nil {
		t.Fatal(err)
	}

	depth1 := AnalyzeStructure(data, 1)
	if containsPath(depth1, "$.metadata.version") {
		t.Fatalf("depth=1 should not include nested metadata.version: %#v", depth1)
	}
	if !containsPath(depth1, "$.users") {
		t.Fatalf("depth=1 should include top-level users: %#v", depth1)
	}

	depth2 := AnalyzeStructure(data, 2)
	for _, want := range []string{"$.metadata.version", "$.users[*].id", "$.users[*].roles"} {
		if !containsPath(depth2, want) {
			t.Fatalf("depth=2 missing %s: %#v", want, depth2)
		}
	}
}

func TestQueryJSONPath(t *testing.T) {
	var data interface{}
	if err := json.Unmarshal([]byte(`{
		"users": [
			{"name": "Alice", "tags": ["a", "b"]},
			{"name": "Bob", "tags": ["c"]}
		],
		"metadata": {"version": "1"}
	}`), &data); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		expr string
		want string
	}{
		{"$.metadata.version", `"1"`},
		{"$.users[0].name", `"Alice"`},
		{"$.users[0:1]", `"Alice"`},
		{"$.users[*].name", `"Bob"`},
		{`$["metadata"].version`, `"1"`},
	}

	for _, tt := range tests {
		got, err := QueryJSONPath(data, tt.expr)
		if err != nil {
			t.Fatalf("QueryJSONPath(%s) error = %v", tt.expr, err)
		}
		encoded, _ := json.Marshal(got)
		if !strings.Contains(string(encoded), tt.want) {
			t.Fatalf("QueryJSONPath(%s) = %s, want contains %s", tt.expr, encoded, tt.want)
		}
	}
}

func TestQueryJSONPathNotFound(t *testing.T) {
	var data interface{}
	if err := json.Unmarshal([]byte(`{"a": 1}`), &data); err != nil {
		t.Fatal(err)
	}
	_, err := QueryJSONPath(data, "$.missing")
	if !errors.Is(err, ErrPathNotFound) {
		t.Fatalf("error = %v, want ErrPathNotFound", err)
	}
}

func containsPath(entries []OutlineEntry, path string) bool {
	for _, entry := range entries {
		if entry.Path == path {
			return true
		}
	}
	return false
}

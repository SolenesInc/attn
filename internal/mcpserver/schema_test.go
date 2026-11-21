package mcpserver_test

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/victorarias/attn/internal/mcpserver"
	"pgregory.net/rapid"
)

type catalogTool struct {
	Name        string          `json:"name"`
	InputSchema json.RawMessage `json:"inputSchema"`
}

func catalogInputSchemas(t *testing.T) []catalogTool {
	t.Helper()
	data, err := os.ReadFile("../daemon/agent_tools_generated.json")
	if err != nil {
		t.Fatal(err)
	}
	var catalog struct {
		Tools []catalogTool `json:"tools"`
	}
	if err := json.Unmarshal(data, &catalog); err != nil {
		t.Fatal(err)
	}
	return catalog.Tools
}

func TestValidateAcceptsExactlyWhatJSONSchemaAcceptsForEveryCatalogInput(t *testing.T) {
	type pair struct {
		name      string
		raw       map[string]any
		ours      *mcpserver.Schema
		reference *jsonschema.Resolved
	}
	var tools []pair
	for _, tool := range catalogInputSchemas(t) {
		ours, err := mcpserver.ParseSchema(tool.InputSchema)
		if err != nil {
			t.Fatalf("%s: %v", tool.Name, err)
		}
		var schema jsonschema.Schema
		if err := json.Unmarshal(tool.InputSchema, &schema); err != nil {
			t.Fatal(err)
		}
		reference, err := schema.Resolve(nil)
		if err != nil {
			t.Fatal(err)
		}
		var raw map[string]any
		if err := json.Unmarshal(tool.InputSchema, &raw); err != nil {
			t.Fatal(err)
		}
		tools = append(tools, pair{tool.Name, raw, ours, reference})
	}
	rapid.Check(t, func(t *rapid.T) {
		tool := rapid.SampledFrom(tools).Draw(t, "tool")
		value := drawFor(t, tool.raw, tool.raw, "arguments", 0)
		data, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		var instance any
		if err := json.Unmarshal(data, &instance); err != nil {
			t.Fatal(err)
		}
		ours, reference := tool.ours.Validate(instance), tool.reference.Validate(instance)
		if (ours == nil) != (reference == nil) {
			t.Fatalf("%s %s: ours says %v, JSON Schema says %v", tool.name, data, ours, reference)
		}
	})
}

func drawFor(t *rapid.T, root, schema map[string]any, label string, depth int) any {
	if ref, ok := schema["$ref"].(string); ok {
		schema = root["$defs"].(map[string]any)[ref[len("#/$defs/"):]].(map[string]any)
	}
	if depth > 4 || rapid.IntRange(0, 9).Draw(t, label+" wrong") == 0 {
		return rapid.SampledFrom([]any{nil, true, 0, 1.5, -3, "", "x", []any{}, map[string]any{}}).Draw(t, label+" any")
	}
	if enum, ok := schema["enum"].([]any); ok && rapid.Bool().Draw(t, label+" from enum") {
		return rapid.SampledFrom(enum).Draw(t, label+" member")
	}
	switch schema["type"] {
	case "string":
		candidates := []string{"", "C3", "C0", "x", "s-7k3f9m", "é✓", "2026-11-05T09:00:00Z", "https://example.com/pull/1"}
		if pattern, ok := schema["pattern"].(string); ok {
			candidates = append(candidates, pattern)
		}
		return rapid.OneOf(rapid.SampledFrom(candidates), rapid.String()).Draw(t, label)
	case "integer":
		return rapid.OneOf(rapid.IntRange(-2, 2000), rapid.SampledFrom([]int{0, 1})).Draw(t, label)
	case "boolean":
		return rapid.Bool().Draw(t, label)
	case "array":
		items, _ := schema["items"].(map[string]any)
		n := rapid.IntRange(0, 3).Draw(t, label+" length")
		out := make([]any, n)
		for i := range out {
			out[i] = drawFor(t, root, items, label+"[]", depth+1)
		}
		return out
	case "object":
		properties, _ := schema["properties"].(map[string]any)
		out := map[string]any{}
		for name, property := range properties {
			if rapid.IntRange(0, 3).Draw(t, label+"."+name+" present") > 0 {
				out[name] = drawFor(t, root, property.(map[string]any), label+"."+name, depth+1)
			}
		}
		if rapid.IntRange(0, 5).Draw(t, label+" extra") == 0 {
			out[rapid.SampledFrom([]string{"session", "profile", "x"}).Draw(t, label+" extra name")] = "extra"
		}
		return out
	}
	return nil
}

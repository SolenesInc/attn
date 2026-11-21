package catalog

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestAMentionOfARestArgumentRendersTheShapeTheToolSchemaTakes(t *testing.T) {
	c, err := Builtin()
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile("../daemon/agent_tools_generated.json")
	if err != nil {
		t.Fatal(err)
	}
	schemas := map[string]map[string]propertyType{}
	var server struct {
		Tools []struct {
			Command     string `json:"command"`
			InputSchema struct {
				Properties map[string]propertyType `json:"properties"`
			} `json:"inputSchema"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(raw, &server); err != nil {
		t.Fatal(err)
	}
	for _, tool := range server.Tools {
		schemas[tool.Command] = tool.InputSchema.Properties
	}
	checked := 0
	for _, cmd := range c.Commands {
		for _, arg := range cmd.Args {
			if (arg.CLI != "rest" && arg.CLI != "words") || schemas[cmd.Name][arg.Name].Type == "" {
				continue
			}
			rendered, err := c.ToolMention("attn " + cmd.Name + " alpha beta")
			if err != nil {
				t.Errorf("%s: %v", cmd.Name, err)
				continue
			}
			var args map[string]any
			if err := json.Unmarshal([]byte(rendered[strings.Index(rendered, "{"):]), &args); err != nil {
				t.Errorf("%s: %q does not end in a JSON object: %v", cmd.Name, rendered, err)
				continue
			}
			want := schemas[cmd.Name][arg.Name].Type
			got := map[bool]string{true: "array", false: "string"}[isList(args[arg.Name])]
			if got != want {
				t.Errorf("%s: a mention renders %s as a %s, but the tool's schema takes a %s: %s", cmd.Name, arg.Name, got, want, rendered)
			}
			checked++
		}
	}
	if checked == 0 {
		t.Fatal("no rest argument of an offered tool was checked")
	}
}

type propertyType struct{ Type string }

func (p *propertyType) UnmarshalJSON(raw []byte) error {
	var shape struct {
		Type any `json:"type"`
	}
	if err := json.Unmarshal(raw, &shape); err != nil {
		return err
	}
	p.Type, _ = shape.Type.(string)
	return nil
}

func isList(v any) bool { _, ok := v.([]any); return ok }

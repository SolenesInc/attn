package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/victorarias/attn/internal/catalog"
)

type toolLocal struct {
	Arg  string        `json:"arg"`
	Kind catalog.Local `json:"kind"`
	When string        `json:"when,omitempty"`
}

type toolDefinition struct {
	Command      string         `json:"command"`
	Name         string         `json:"name"`
	Title        string         `json:"title"`
	Description  string         `json:"description"`
	Core         bool           `json:"core,omitempty"`
	ReadOnly     bool           `json:"read_only,omitempty"`
	Local        []toolLocal    `json:"local,omitempty"`
	InputSchema  map[string]any `json:"inputSchema"`
	OutputSchema map[string]any `json:"outputSchema,omitempty"`
}

func writeTools(c *catalog.Catalog, goRoot string) error {
	g := generator{c: c}
	var tools []toolDefinition
	for i := range c.Commands {
		cmd := &c.Commands[i]
		if !cmd.IsTool() {
			continue
		}
		where := fmt.Sprintf("%s: %s", cmd.File, cmd.Name)
		tool := toolDefinition{
			Command:     cmd.Name,
			Name:        cmd.ToolName(),
			Title:       g.expand(where, cmd.Summary),
			Description: strings.TrimSpace(g.expand(where, cmd.Doc)),
			Core:        cmd.Core,
			ReadOnly:    cmd.ReadOnly,
			InputSchema: g.inputSchema(cmd),
		}
		for _, a := range cmd.Args {
			if a.Local != "" {
				tool.Local = append(tool.Local, toolLocal{Arg: a.Name, Kind: a.Local, When: a.LocalWhen})
			}
		}
		if cmd.Result != "" {
			schema, err := bundledSchema(cmd.Result)
			if err != nil {
				return fmt.Errorf("%s: result %s: %w", where, cmd.Result, err)
			}
			if schema["type"] != "object" {
				return fmt.Errorf("%s: result %s is not an object; an MCP outputSchema must be one", where, cmd.Result)
			}
			tool.OutputSchema = schema
		}
		tools = append(tools, tool)
	}
	if err := g.fail(); err != nil {
		return err
	}
	server, err := json.Marshal(map[string]string{"name": c.Server.Name, "instructions": c.Server.Instructions})
	if err != nil {
		return err
	}
	var out bytes.Buffer
	out.Write(server[:len(server)-1])
	out.WriteString(`,"tools":[` + "\n")
	for i, tool := range tools {
		line, err := json.Marshal(tool)
		if err != nil {
			return err
		}
		out.Write(line)
		if i < len(tools)-1 {
			out.WriteByte(',')
		}
		out.WriteByte('\n')
	}
	out.WriteString("]}\n")
	return os.WriteFile(filepath.Join(goRoot, "daemon", "agent_tools_generated.json"), out.Bytes(), 0o644)
}

func (g *generator) inputSchema(cmd *catalog.Command) map[string]any {
	where := fmt.Sprintf("%s: %s", cmd.File, cmd.Name)
	properties := map[string]any{}
	defs := map[string]any{}
	required := []string{}
	for _, a := range cmd.Args {
		if a.CLIOnly {
			continue
		}
		t := g.c.ArgType(a)
		value := maps.Clone(t.Schema)
		if nested, ok := value["$defs"].(map[string]any); ok {
			delete(value, "$defs")
			for name, def := range nested {
				if _, taken := defs[name]; taken {
					g.problems = append(g.problems, fmt.Sprintf("%s: arg %q: $defs %q is defined by two arg types", where, a.Name, name))
				}
				defs[name] = def
			}
		}
		if len(a.Values) > 0 {
			value["enum"] = a.Values
		}
		if a.MaxLength > 0 {
			value["maxLength"] = a.MaxLength
		}
		if a.List {
			value = map[string]any{"type": "array", "items": value}
		}
		if doc := g.argDoc(a, toolSurface); doc != "" {
			value["description"] = g.expand(where, doc)
		}
		properties[a.Name] = value
		if a.Required && a.Unless == "" {
			required = append(required, a.Name)
		}
	}
	schema := map[string]any{"type": "object", "properties": properties, "required": required, "additionalProperties": false}
	if len(defs) > 0 {
		schema["$defs"] = defs
	}
	return schema
}

func bundledSchema(model string) (map[string]any, error) {
	root, err := readModelSchema(model)
	if err != nil {
		return nil, err
	}
	defs := map[string]any{}
	pending := refsIn(root)
	for len(pending) > 0 {
		name := pending[0]
		pending = pending[1:]
		if _, done := defs[name]; done {
			continue
		}
		def, err := readModelSchema(name)
		if err != nil {
			return nil, err
		}
		defs[name] = def
		pending = append(pending, refsIn(def)...)
	}
	if len(defs) > 0 {
		root["$defs"] = defs
	}
	return root, nil
}

func readModelSchema(model string) (map[string]any, error) {
	data, err := os.ReadFile(filepath.Join(protocolSchemas, model+".json"))
	if err != nil {
		return nil, err
	}
	var schema map[string]any
	if err := json.Unmarshal(data, &schema); err != nil {
		return nil, err
	}
	delete(schema, "$schema")
	delete(schema, "$id")
	return schema, nil
}

func refsIn(schema any) []string {
	var refs []string
	switch v := schema.(type) {
	case map[string]any:
		for _, key := range slices.Sorted(maps.Keys(v)) {
			if ref, ok := v[key].(string); ok && key == "$ref" {
				name := strings.TrimSuffix(ref, ".json")
				v[key] = "#/$defs/" + name
				refs = append(refs, name)
				continue
			}
			refs = append(refs, refsIn(v[key])...)
		}
	case []any:
		for _, item := range v {
			refs = append(refs, refsIn(item)...)
		}
	}
	return refs
}

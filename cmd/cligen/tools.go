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
	"unicode/utf8"

	"github.com/victorarias/attn/internal/catalog"
)

type toolDefinition struct {
	Command     string         `json:"command"`
	Name        string         `json:"name"`
	Title       string         `json:"title"`
	Description string         `json:"description"`
	Tool        bool           `json:"tool,omitempty"`
	Signature   string         `json:"signature"`
	Effect      catalog.Effect `json:"effect"`
	InputSchema map[string]any `json:"inputSchema"`
	Receipt     []string       `json:"receipt,omitempty"`
}

type scriptTool struct {
	Title       string `json:"title"`
	Description string `json:"description"`
	Script      string `json:"script"`
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
			Title:       g.toolText(where, cmd.Summary),
			Description: strings.TrimSpace(g.toolText(where, cmd.Doc)),
			Tool:        cmd.Tool,
			Signature:   c.Signature(cmd),
			Effect:      cmd.Effect,
			InputSchema: g.inputSchema(cmd),
			Receipt:     cmd.Receipt,
		}
		if n := utf8.RuneCountInString(tool.Description); cmd.Tool && n > catalog.MaxUpfrontChars {
			g.problems = append(g.problems, fmt.Sprintf("%s: the description of %s is %d characters, over the limit of %d that Claude Code keeps", where, tool.Name, n, catalog.MaxUpfrontChars))
		}
		if len(cmd.Receipt) > 0 {
			schema, err := bundledSchema(cmd.Result)
			if err != nil {
				return fmt.Errorf("%s: result %s: %w", where, cmd.Result, err)
			}
			defs, _ := schema["$defs"].(map[string]any)
			if err := checkReceipt(schema, defs, catalog.ReceiptOf(cmd.Receipt), ""); err != nil {
				return fmt.Errorf("%s: receipt of %s: %w", where, cmd.Result, err)
			}
		}
		tools = append(tools, tool)
	}
	if err := g.fail(); err != nil {
		return err
	}
	do, read := c.Server.Do, c.Server.Read
	server, err := json.Marshal(map[string]any{
		"name": c.Server.Name, "description": c.Server.Description, "instructions": c.Instructions(),
		"do":   scriptTool{Title: do.Title, Description: c.DoDescription(), Script: do.Script},
		"read": scriptTool{Title: read.Title, Description: strings.TrimSpace(read.Text), Script: read.Script},
	})
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

func (g *generator) toolText(where, text string) string {
	text = g.expand(where, text, toolSurface)
	for _, problem := range g.c.MentionProblems(text) {
		g.problems = append(g.problems, where+": "+problem)
	}
	return g.c.ToolText(text)
}

func (g *generator) inputSchema(cmd *catalog.Command) map[string]any {
	where := fmt.Sprintf("%s: %s", cmd.File, cmd.Name)
	properties := map[string]any{}
	defs := map[string]any{}
	required := []string{}
	for _, a := range cmd.Args {
		if a.CLIOnly || a.Internal() {
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
			value["description"] = g.toolText(where, doc)
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

const defsPrefix = "#/$defs/"

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

func checkReceipt(schema, defs map[string]any, receipt catalog.Receipt, at string) error {
	if receipt.Whole() {
		return nil
	}
	if ref, ok := schema["$ref"].(string); ok {
		return checkReceipt(defs[strings.TrimPrefix(ref, defsPrefix)].(map[string]any), defs, receipt, at)
	}
	if items, ok := schema["items"].(map[string]any); ok {
		return checkReceipt(items, defs, receipt, at)
	}
	properties := objectProperties(schema, defs)
	for _, name := range receipt.Names() {
		path := strings.TrimPrefix(at+"."+name, ".")
		property, ok := properties[name].(map[string]any)
		if !ok {
			return fmt.Errorf("no field %q; fields here: %s", path, strings.Join(slices.Sorted(maps.Keys(properties)), ", "))
		}
		if err := checkReceipt(property, defs, receipt[name], path); err != nil {
			return err
		}
	}
	return nil
}

func objectProperties(schema, defs map[string]any) map[string]any {
	properties := map[string]any{}
	bases, _ := schema["allOf"].([]any)
	for _, base := range bases {
		ref := base.(map[string]any)["$ref"].(string)
		maps.Copy(properties, objectProperties(defs[strings.TrimPrefix(ref, defsPrefix)].(map[string]any), defs))
	}
	own, _ := schema["properties"].(map[string]any)
	maps.Copy(properties, own)
	return properties
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
	eachRef(schema, func(node map[string]any, ref string) {
		name := strings.TrimSuffix(ref, ".json")
		node["$ref"] = defsPrefix + name
		refs = append(refs, name)
	})
	return refs
}

func eachRef(schema any, visit func(node map[string]any, ref string)) {
	switch v := schema.(type) {
	case map[string]any:
		for _, key := range slices.Sorted(maps.Keys(v)) {
			if ref, ok := v[key].(string); ok && key == "$ref" {
				visit(v, ref)
				continue
			}
			eachRef(v[key], visit)
		}
	case []any:
		for _, item := range v {
			eachRef(item, visit)
		}
	}
}

func writeToolFamilies(c *catalog.Catalog, goRoot string) error {
	var out strings.Builder
	out.WriteString("// Code generated by make generate-types. DO NOT EDIT.\n\npackage prompts\n\nvar catalogFamilies = map[string]bool{\n")
	for _, top := range c.TopLevel() {
		fmt.Fprintf(&out, "\t%q: %t,\n", top.Name, c.Offers(top.Name))
	}
	out.WriteString("}\n")
	return writeGo(filepath.Join(goRoot, "prompts", "tool_families_generated.go"), out.String())
}

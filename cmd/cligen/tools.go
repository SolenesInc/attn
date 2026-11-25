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

const maxDescriptionChars = 4096

type toolDefinition struct {
	Command      string         `json:"command"`
	Name         string         `json:"name"`
	Title        string         `json:"title"`
	Description  string         `json:"description"`
	Core         bool           `json:"core,omitempty"`
	ReadOnly     bool           `json:"read_only,omitempty"`
	InputSchema  map[string]any `json:"inputSchema"`
	OutputSchema map[string]any `json:"outputSchema,omitempty"`
	Receipt      []string       `json:"receipt,omitempty"`
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
			Title:       g.expand(where, cmd.Summary, toolSurface),
			Description: strings.TrimSpace(g.expand(where, cmd.Doc, toolSurface)),
			Core:        cmd.Core,
			ReadOnly:    cmd.ReadOnly,
			InputSchema: g.inputSchema(cmd),
		}
		if n := utf8.RuneCountInString(tool.Description); n > maxDescriptionChars {
			g.problems = append(g.problems, fmt.Sprintf("%s: doc is %d characters as a tool description, over the limit of %d that Claude Code keeps", where, n, maxDescriptionChars))
		}
		if cmd.Result != "" {
			schema, err := bundledSchema(cmd.Result)
			if err != nil {
				return fmt.Errorf("%s: result %s: %w", where, cmd.Result, err)
			}
			if schema["type"] != "object" {
				return fmt.Errorf("%s: result %s is not an object; an MCP outputSchema must be one", where, cmd.Result)
			}
			if tool.Receipt = cmd.Receipt; len(tool.Receipt) > 0 {
				schema, err = receiptSchema(schema, tool.Receipt)
				if err != nil {
					return fmt.Errorf("%s: receipt of %s: %w", where, cmd.Result, err)
				}
			}
			tool.OutputSchema = schema
		}
		tools = append(tools, tool)
	}
	if err := g.fail(); err != nil {
		return err
	}
	server, err := json.Marshal(map[string]string{"name": c.Server.Name, "description": c.Server.Description, "instructions": c.Instructions(), "core_instructions": c.CoreInstructions()})
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
			value["description"] = g.expand(where, doc, toolSurface)
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

func receiptSchema(model map[string]any, paths []string) (map[string]any, error) {
	defs, _ := model["$defs"].(map[string]any)
	cut, err := cutSchema(model, defs, catalog.ReceiptOf(paths), "")
	if err != nil {
		return nil, err
	}
	kept := map[string]any{}
	var keep func(schema any)
	keep = func(schema any) {
		eachRef(schema, func(_ map[string]any, ref string) {
			name := strings.TrimPrefix(ref, defsPrefix)
			if _, done := kept[name]; !done {
				kept[name] = defs[name]
				keep(defs[name])
			}
		})
	}
	keep(cut)
	if len(kept) > 0 {
		cut["$defs"] = kept
	}
	return cut, nil
}

func cutSchema(schema, defs map[string]any, receipt catalog.Receipt, at string) (map[string]any, error) {
	if receipt.Whole() {
		return schema, nil
	}
	if ref, ok := schema["$ref"].(string); ok {
		return cutSchema(defs[strings.TrimPrefix(ref, defsPrefix)].(map[string]any), defs, receipt, at)
	}
	if items, ok := schema["items"].(map[string]any); ok {
		cut, err := cutSchema(items, defs, receipt, at)
		return map[string]any{"type": "array", "items": cut}, err
	}
	properties, required := objectFields(schema, defs)
	kept := map[string]any{}
	var keptRequired []any
	for _, name := range receipt.Names() {
		path := strings.TrimPrefix(at+"."+name, ".")
		property, ok := properties[name].(map[string]any)
		if !ok {
			return nil, fmt.Errorf("no field %q; fields here: %s", path, strings.Join(slices.Sorted(maps.Keys(properties)), ", "))
		}
		cut, err := cutSchema(property, defs, receipt[name], path)
		if err != nil {
			return nil, err
		}
		kept[name] = cut
		if slices.Contains(required, any(name)) {
			keptRequired = append(keptRequired, name)
		}
	}
	cut := map[string]any{"type": "object", "properties": kept}
	if len(keptRequired) > 0 {
		cut["required"] = keptRequired
	}
	return cut, nil
}

func objectFields(schema, defs map[string]any) (map[string]any, []any) {
	properties := map[string]any{}
	var required []any
	bases, _ := schema["allOf"].([]any)
	for _, base := range bases {
		ref := base.(map[string]any)["$ref"].(string)
		inherited, inheritedRequired := objectFields(defs[strings.TrimPrefix(ref, defsPrefix)].(map[string]any), defs)
		maps.Copy(properties, inherited)
		required = append(required, inheritedRequired...)
	}
	own, _ := schema["properties"].(map[string]any)
	maps.Copy(properties, own)
	ownRequired, _ := schema["required"].([]any)
	return properties, append(required, ownRequired...)
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

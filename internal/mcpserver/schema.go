package mcpserver

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"math/big"
	"regexp"
	"slices"
	"sort"
	"strings"
	"unicode/utf8"
)

type Schema struct {
	root *schemaNode
	defs map[string]*schemaNode
}

type schemaNode struct {
	Type                 string                 `json:"type"`
	Description          string                 `json:"description"`
	Format               string                 `json:"format"`
	Properties           map[string]*schemaNode `json:"properties"`
	Required             []string               `json:"required"`
	AdditionalProperties *bool                  `json:"additionalProperties"`
	Items                *schemaNode            `json:"items"`
	Enum                 []any                  `json:"enum"`
	MinLength            *int                   `json:"minLength"`
	MaxLength            *int                   `json:"maxLength"`
	Pattern              string                 `json:"pattern"`
	Minimum              *float64               `json:"minimum"`
	Ref                  string                 `json:"$ref"`
	Defs                 map[string]*schemaNode `json:"$defs"`

	pattern *regexp.Regexp
}

const defsRef = "#/$defs/"

func ParseSchema(raw json.RawMessage) (*Schema, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var root schemaNode
	if err := decoder.Decode(&root); err != nil {
		return nil, err
	}
	s := &Schema{root: &root, defs: root.Defs}
	if err := s.compile(&root, "root"); err != nil {
		return nil, err
	}
	for name, def := range root.Defs {
		if err := s.compile(def, defsRef+name); err != nil {
			return nil, err
		}
	}
	return s, nil
}

func (s *Schema) compile(n *schemaNode, at string) error {
	switch n.Type {
	case "", "object", "array", "string", "integer", "number", "boolean":
	default:
		return fmt.Errorf("%s: type %q is not one this validator checks", at, n.Type)
	}
	if n.Ref != "" {
		if _, ok := s.defs[strings.TrimPrefix(n.Ref, defsRef)]; !ok || !strings.HasPrefix(n.Ref, defsRef) {
			return fmt.Errorf("%s: $ref %q names no entry of $defs", at, n.Ref)
		}
	}
	if n.Pattern != "" {
		pattern, err := regexp.Compile(n.Pattern)
		if err != nil {
			return fmt.Errorf("%s: pattern: %w", at, err)
		}
		n.pattern = pattern
	}
	for name, property := range n.Properties {
		if err := s.compile(property, at+"."+name); err != nil {
			return err
		}
	}
	if n.Items != nil {
		return s.compile(n.Items, at+"[]")
	}
	return nil
}

func (s *Schema) Validate(value any) error {
	return s.check(s.root, value, "")
}

func (s *Schema) check(n *schemaNode, value any, at string) error {
	if n.Ref != "" {
		n = s.defs[strings.TrimPrefix(n.Ref, defsRef)]
	}
	name := at
	if name == "" {
		name = "the arguments"
	}
	if n.Type != "" && jsonType(value, n.Type) != n.Type {
		return fmt.Errorf("%s is %s, want %s", name, describe(value), article(n.Type))
	}
	if n.Enum != nil && !slices.Contains(n.Enum, value) {
		return fmt.Errorf("%s is %s, want one of %s", name, describe(value), describeAll(n.Enum))
	}
	switch v := value.(type) {
	case string:
		length := utf8.RuneCountInString(v)
		if n.MinLength != nil && length < *n.MinLength {
			if v == "" {
				return fmt.Errorf("%s is empty", name)
			}
			return fmt.Errorf("%s has %d characters, want at least %d", name, length, *n.MinLength)
		}
		if n.MaxLength != nil && length > *n.MaxLength {
			return fmt.Errorf("%s has %d characters, want at most %d", name, length, *n.MaxLength)
		}
		if n.pattern != nil && !n.pattern.MatchString(v) {
			return fmt.Errorf("%s is %q, which does not match %s", name, v, n.Pattern)
		}
	case float64:
		if n.Minimum != nil && v < *n.Minimum {
			return fmt.Errorf("%s is %v, want at least %v", name, v, *n.Minimum)
		}
	case []any:
		if n.Items != nil {
			for i, item := range v {
				if err := s.check(n.Items, item, fmt.Sprintf("%s[%d]", at, i)); err != nil {
					return err
				}
			}
		}
	case map[string]any:
		field := func(key string) string {
			if at == "" {
				return key
			}
			return at + "." + key
		}
		keys := make([]string, 0, len(v))
		for key := range v {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		closed := n.AdditionalProperties != nil && !*n.AdditionalProperties
		for _, key := range keys {
			if _, known := n.Properties[key]; !known && closed {
				return fmt.Errorf("%q is not in the input schema", field(key))
			}
		}
		for _, key := range n.Required {
			if _, ok := v[key]; !ok {
				return fmt.Errorf("%q is required", field(key))
			}
		}
		for _, key := range keys {
			if property, known := n.Properties[key]; known {
				if err := s.check(property, v[key], field(key)); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func (s *Schema) IntegerLiterals(raw json.RawMessage) json.RawMessage {
	return s.integerLiterals(s.root, raw)
}

func (s *Schema) integerLiterals(n *schemaNode, raw json.RawMessage) json.RawMessage {
	if n.Ref != "" {
		n = s.defs[strings.TrimPrefix(n.Ref, defsRef)]
	}
	switch {
	case n.Type == "integer":
		return integerLiteral(raw)
	case len(n.Properties) > 0:
		var fields map[string]json.RawMessage
		if json.Unmarshal(raw, &fields) != nil || fields == nil {
			return raw
		}
		changed := false
		for name, property := range n.Properties {
			if value, given := fields[name]; given {
				if written := s.integerLiterals(property, value); !bytes.Equal(written, value) {
					fields[name] = written
					changed = true
				}
			}
		}
		if !changed {
			return raw
		}
		written, _ := json.Marshal(fields)
		return written
	case n.Items != nil:
		var items []json.RawMessage
		if json.Unmarshal(raw, &items) != nil {
			return raw
		}
		changed := false
		for i, item := range items {
			if written := s.integerLiterals(n.Items, item); !bytes.Equal(written, item) {
				items[i] = written
				changed = true
			}
		}
		if !changed {
			return raw
		}
		written, _ := json.Marshal(items)
		return written
	}
	return raw
}

func integerLiteral(raw json.RawMessage) json.RawMessage {
	spelled := bytes.TrimSpace(raw)
	if !bytes.ContainsAny(spelled, ".eE") {
		return raw
	}
	whole, ok := new(big.Rat).SetString(string(spelled))
	if !ok || !whole.IsInt() {
		return raw
	}
	return json.RawMessage(whole.Num().String())
}

func jsonType(value any, want string) string {
	switch v := value.(type) {
	case nil:
		return "null"
	case bool:
		return "boolean"
	case string:
		return "string"
	case float64:
		if want == "integer" && v == math.Trunc(v) {
			return "integer"
		}
		return "number"
	case []any:
		return "array"
	case map[string]any:
		return "object"
	}
	return fmt.Sprintf("%T", value)
}

func article(jsonType string) string {
	switch jsonType {
	case "object", "array", "integer":
		return "an " + jsonType
	}
	return "a " + jsonType
}

func describe(value any) string {
	switch value.(type) {
	case map[string]any:
		return "an object"
	case []any:
		return "an array"
	}
	data, _ := json.Marshal(value)
	return string(data)
}

func describeAll(values []any) string {
	described := make([]string, len(values))
	for i, value := range values {
		described[i] = describe(value)
	}
	return strings.Join(described, ", ")
}

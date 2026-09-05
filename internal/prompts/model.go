// Package prompts describes prompt composition as data shared by rendering and inspection.
package prompts

import (
	"fmt"
	"io/fs"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

type Field struct {
	Name        string `json:"name"`
	Kind        string `json:"kind"`
	Description string `json:"description"`
	TrimSpace   bool   `json:"trim_space,omitempty"`
	From        string `json:"from,omitempty"`
}

func TextField(name, description string) Field {
	return Field{Name: name, Kind: "text", Description: description}
}
func FlagField(name, description string) Field {
	return Field{Name: name, Kind: "flag", Description: description}
}
func Trimmed(field Field) Field { field.TrimSpace = true; return field }

func ProducedBy(field Field, event string) Field { field.From = event; return field }

type Condition struct {
	Field Field  `json:"field"`
	Test  string `json:"test"`
}

func Present(field Field) Condition { return Condition{field, "present"} }
func Enabled(field Field) Condition { return Condition{field, "enabled"} }

type Binding struct {
	Name string `json:"name"`
	Node Node   `json:"node"`
}

func Bind(name string, node Node) Binding { return Binding{name, node} }

type Node struct {
	Kind             string      `json:"kind"`
	ID               string      `json:"id,omitempty"`
	Source           string      `json:"source,omitempty"`
	Field            *Field      `json:"field,omitempty"`
	Quote            bool        `json:"quote,omitempty"`
	Verbatim         bool        `json:"verbatim,omitempty"`
	KeepFinalNewline bool        `json:"keep_final_newline,omitempty"`
	Separator        string      `json:"separator,omitempty"`
	Condition        *Condition  `json:"condition,omitempty"`
	Bindings         []Binding   `json:"bindings,omitempty"`
	Children         []Node      `json:"children,omitempty"`
	Part             *SourcePart `json:"part,omitempty"`
}

type SourcePart struct {
	Marker string `json:"marker"`
	Index  int    `json:"index"`
}

func Part(node Node, marker string, index int) Node {
	node.Part = &SourcePart{marker, index}
	return node
}

func Use(id, source string, bindings ...Binding) Node {
	return Node{Kind: "text", ID: id, Source: source, Bindings: bindings}
}

func Document(id, source string) Node {
	return Node{Kind: "text", ID: id, Source: source, Verbatim: true, KeepFinalNewline: true}
}

func Exact(node Node) Node { node.KeepFinalNewline = true; return node }
func Join(separator string, nodes ...Node) Node {
	return Node{Kind: "join", Separator: separator, Children: nodes}
}
func Trim(node Node) Node { return Node{Kind: "trim", Children: []Node{node}} }

func Input(field Field) Node     { return Node{Kind: "input", Field: &field} }
func Quoted(field Field) Node    { return Node{Kind: "input", Field: &field, Quote: true} }
func Compose(nodes ...Node) Node { return Node{Kind: "compose", Children: nodes} }
func When(condition Condition, node Node) Node {
	return Node{Kind: "when", Condition: &condition, Children: []Node{node}}
}
func Choose(condition Condition, yes, no Node) Node {
	return Node{Kind: "choose", Condition: &condition, Children: []Node{yes, no}}
}

type Event struct {
	ID          string `json:"id"`
	Delivery    string `json:"delivery"`
	Description string `json:"description"`
	Body        Node   `json:"body"`
}

func On(id, delivery, description string, body Node) Event {
	return Event{ID: id, Delivery: delivery, Description: description, Body: body}
}

type Recipient struct {
	ID          string  `json:"id"`
	Description string  `json:"description"`
	Events      []Event `json:"events"`
}

type Values map[string]string

type Trace struct {
	Kind     string  `json:"kind"`
	ID       string  `json:"id,omitempty"`
	Source   string  `json:"source,omitempty"`
	Selected bool    `json:"selected"`
	Reason   string  `json:"reason,omitempty"`
	Text     string  `json:"text,omitempty"`
	Children []Trace `json:"children,omitempty"`
}

type Result struct {
	Recipient string `json:"recipient"`
	Event     string `json:"event"`
	Delivery  string `json:"delivery"`
	Text      string `json:"text"`
	Trace     Trace  `json:"trace"`
}

type Catalog struct {
	recipients []Recipient
	templates  map[string]string
}

var placeholder = regexp.MustCompile(`\{\{([a-zA-Z][a-zA-Z0-9_]*)\}\}`)

// New validates all branches, including ones that the current scenario would skip.
func New(files fs.FS, recipients ...Recipient) (*Catalog, error) {
	c := &Catalog{recipients: cloneRecipients(recipients), templates: map[string]string{}}
	ids := map[string]string{}
	seen := map[string]bool{}
	for _, recipient := range c.recipients {
		if recipient.ID == "" || seen[recipient.ID] {
			return nil, fmt.Errorf("empty or duplicate recipient %q", recipient.ID)
		}
		seen[recipient.ID] = true
		events := map[string]bool{}
		for _, event := range recipient.Events {
			if event.ID == "" || event.Delivery == "" || events[event.ID] {
				return nil, fmt.Errorf("%s: invalid or duplicate event %q", recipient.ID, event.ID)
			}
			events[event.ID] = true
			if err := c.validate(files, event.Body, ids, map[string]Field{}); err != nil {
				return nil, fmt.Errorf("%s/%s: %w", recipient.ID, event.ID, err)
			}
		}
	}
	for _, recipient := range c.recipients {
		for _, event := range recipient.Events {
			fields, _ := c.Fields(recipient.ID, event.ID)
			for _, field := range fields {
				if field.From == "" {
					continue
				}
				r, e, ok := strings.Cut(field.From, "/")
				if !ok {
					return nil, fmt.Errorf("input %s has an invalid producer %q", field.Name, field.From)
				}
				if _, err := c.event(r, e); err != nil {
					return nil, fmt.Errorf("input %s: %w", field.Name, err)
				}
			}
		}
	}
	return c, nil
}

func (c *Catalog) validate(files fs.FS, n Node, ids map[string]string, fields map[string]Field) error {
	field := func(f Field, kind string) error {
		if f.Name == "" || f.Kind != kind {
			return fmt.Errorf("%s requires a %s input", f.Name, kind)
		}
		if previous, ok := fields[f.Name]; ok && previous != f {
			return fmt.Errorf("conflicting declarations of input %s", f.Name)
		}
		fields[f.Name] = f
		return nil
	}
	switch n.Kind {
	case "text":
		if n.ID == "" || n.Source == "" {
			return fmt.Errorf("text needs an identity and source")
		}
		if previous, ok := ids[n.ID]; ok && previous != n.Source {
			return fmt.Errorf("conflicting definitions of prompt %s", n.ID)
		}
		ids[n.ID] = n.Source
		source, ok := c.templates[n.Source]
		if !ok {
			data, err := fs.ReadFile(files, n.Source)
			if err != nil {
				return fmt.Errorf("%s: %w", n.ID, err)
			}
			source = string(data)
			c.templates[n.Source] = source
		}
		if n.Part != nil {
			if n.Part.Marker == "" || strings.Count(source, n.Part.Marker) != 1 || n.Part.Index < 0 || n.Part.Index > 1 {
				return fmt.Errorf("%s: source part needs one marker and index 0 or 1", n.Source)
			}
			source = strings.SplitN(source, n.Part.Marker, 2)[n.Part.Index]
		}
		if n.Verbatim {
			if len(n.Bindings) > 0 {
				return fmt.Errorf("%s: a verbatim document cannot have bindings", n.ID)
			}
			return nil
		}
		remainder := placeholder.ReplaceAllString(source, "")
		if strings.Contains(remainder, "{{") || strings.Contains(remainder, "}}") {
			return fmt.Errorf("%s: only {{name}} substitutions are supported", n.Source)
		}
		used := map[string]bool{}
		for _, match := range placeholder.FindAllStringSubmatch(source, -1) {
			used[match[1]] = true
		}
		for _, binding := range n.Bindings {
			if !used[binding.Name] {
				return fmt.Errorf("%s: unused or duplicate binding %s", n.ID, binding.Name)
			}
			delete(used, binding.Name)
			if err := c.validate(files, binding.Node, ids, fields); err != nil {
				return err
			}
		}
		if len(used) > 0 {
			names := make([]string, 0, len(used))
			for name := range used {
				names = append(names, name)
			}
			sort.Strings(names)
			return fmt.Errorf("%s: missing bindings for %s", n.ID, strings.Join(names, ", "))
		}
	case "input":
		if n.Field == nil {
			return fmt.Errorf("input needs a field")
		}
		return field(*n.Field, "text")
	case "compose", "join":
	case "trim":
		if len(n.Children) != 1 {
			return fmt.Errorf("trim needs one child")
		}
	case "when", "choose":
		want := 1
		if n.Kind == "choose" {
			want = 2
		}
		if n.Condition == nil || len(n.Children) != want {
			return fmt.Errorf("%s needs a condition and %d branches", n.Kind, want)
		}
		kind := "text"
		switch n.Condition.Test {
		case "present":
		case "enabled":
			kind = "flag"
		default:
			return fmt.Errorf("unknown condition %q", n.Condition.Test)
		}
		if err := field(n.Condition.Field, kind); err != nil {
			return err
		}
	default:
		return fmt.Errorf("unknown node kind %q", n.Kind)
	}
	for _, child := range n.Children {
		if err := c.validate(files, child, ids, fields); err != nil {
			return err
		}
	}
	return nil
}

func (c *Catalog) Recipients() []Recipient {
	return cloneRecipients(c.recipients)
}

func cloneRecipients(recipients []Recipient) []Recipient {
	result := append([]Recipient(nil), recipients...)
	for i := range result {
		result[i].Events = append([]Event(nil), result[i].Events...)
		for j := range result[i].Events {
			result[i].Events[j].Body = cloneNode(result[i].Events[j].Body)
		}
	}
	return result
}

func cloneNode(n Node) Node {
	if n.Part != nil {
		part := *n.Part
		n.Part = &part
	}
	if n.Field != nil {
		field := *n.Field
		n.Field = &field
	}
	if n.Condition != nil {
		condition := *n.Condition
		n.Condition = &condition
	}
	n.Bindings = append([]Binding(nil), n.Bindings...)
	for i := range n.Bindings {
		n.Bindings[i].Node = cloneNode(n.Bindings[i].Node)
	}
	n.Children = append([]Node(nil), n.Children...)
	for i := range n.Children {
		n.Children[i] = cloneNode(n.Children[i])
	}
	return n
}

func (c *Catalog) event(recipientID, eventID string) (Event, error) {
	for _, recipient := range c.recipients {
		if recipient.ID != recipientID {
			continue
		}
		for _, event := range recipient.Events {
			if event.ID == eventID {
				return event, nil
			}
		}
		return Event{}, fmt.Errorf("recipient %s has no event %q", recipientID, eventID)
	}
	return Event{}, fmt.Errorf("unknown recipient %q", recipientID)
}

func (c *Catalog) Fields(recipient, event string) ([]Field, error) {
	e, err := c.event(recipient, event)
	if err != nil {
		return nil, err
	}
	return NodeFields(e.Body), nil
}

func NodeFields(body Node) []Field {
	byName := map[string]Field{}
	var visit func(Node)
	visit = func(n Node) {
		if n.Field != nil {
			byName[n.Field.Name] = *n.Field
		}
		if n.Condition != nil {
			byName[n.Condition.Field.Name] = n.Condition.Field
		}
		for _, binding := range n.Bindings {
			visit(binding.Node)
		}
		for _, child := range n.Children {
			visit(child)
		}
	}
	visit(body)
	fields := make([]Field, 0, len(byName))
	for _, f := range byName {
		fields = append(fields, f)
	}
	sort.Slice(fields, func(i, j int) bool { return fields[i].Name < fields[j].Name })
	return fields
}

func (c *Catalog) Render(recipient, event string, values Values) (Result, error) {
	e, err := c.event(recipient, event)
	if err != nil {
		return Result{}, err
	}
	fields, _ := c.Fields(recipient, event)
	known := map[string]Field{}
	for _, f := range fields {
		known[f.Name] = f
	}
	normalized := Values{}
	for name, value := range values {
		f, ok := known[name]
		if !ok {
			return Result{}, fmt.Errorf("unknown input %q for %s/%s", name, recipient, event)
		}
		if f.Kind == "flag" && value != "true" && value != "false" {
			return Result{}, fmt.Errorf("input %s needs true or false", name)
		}
		if f.TrimSpace {
			value = strings.TrimSpace(value)
		}
		normalized[name] = value
	}
	trace, err := c.render(e.Body, normalized, true, "")
	if err != nil {
		return Result{}, err
	}
	return Result{Recipient: recipient, Event: event, Delivery: e.Delivery, Text: trace.Text, Trace: trace}, nil
}

func (c *Catalog) render(n Node, values Values, selected bool, reason string) (Trace, error) {
	t := Trace{Kind: n.Kind, ID: n.ID, Source: n.Source, Selected: selected, Reason: reason}
	if n.Kind == "input" {
		t.ID = n.Field.Name
		if selected {
			value, ok := values[n.Field.Name]
			if !ok {
				return Trace{}, fmt.Errorf("missing input %s", n.Field.Name)
			}
			t.Text = value
			if n.Quote {
				t.Text = strconv.Quote(value)
			}
		}
		return t, nil
	}
	if n.Kind == "text" {
		bound := map[string]string{}
		for _, binding := range n.Bindings {
			child, err := c.render(binding.Node, values, selected, reason)
			if err != nil {
				return Trace{}, fmt.Errorf("%s: %w", n.ID, err)
			}
			t.Children = append(t.Children, child)
			bound[binding.Name] = child.Text
		}
		if selected {
			source := c.templates[n.Source]
			if n.Part != nil {
				source = strings.SplitN(source, n.Part.Marker, 2)[n.Part.Index]
			}
			if !n.KeepFinalNewline {
				source = strings.TrimSuffix(source, "\n")
			}
			t.Text = source
			if !n.Verbatim {
				t.Text = placeholder.ReplaceAllStringFunc(source, func(token string) string {
					return bound[token[2:len(token)-2]]
				})
			}
		}
		return t, nil
	}
	matched := false
	if n.Condition != nil && selected {
		value := values[n.Condition.Field.Name]
		if n.Condition.Test == "present" {
			matched = strings.TrimSpace(value) != ""
			t.Reason = n.Condition.Field.Name + " is empty"
			if matched {
				t.Reason = n.Condition.Field.Name + " is present"
			}
		} else {
			matched = value == "true"
			t.Reason = fmt.Sprintf("%s = %t", n.Condition.Field.Name, matched)
		}
	}
	if n.Kind == "when" {
		t.Selected = selected && matched
	}
	var parts []string
	for i, node := range n.Children {
		active := selected
		why := reason
		if n.Condition != nil && selected {
			active = matched
			if n.Kind == "choose" && i == 1 {
				active = !matched
			}
			why = t.Reason
		}
		child, err := c.render(node, values, active, why)
		if err != nil {
			return Trace{}, err
		}
		t.Children = append(t.Children, child)
		if active && (child.Text != "" || n.Kind == "join") {
			parts = append(parts, child.Text)
		}
	}
	t.Text = strings.Join(parts, "\n\n")
	if n.Kind == "join" {
		t.Text = strings.Join(parts, n.Separator)
	}
	if n.Kind == "trim" {
		t.Text = strings.TrimSpace(t.Text)
	}
	return t, nil
}

func (c *Catalog) Sources() map[string]string {
	result := make(map[string]string, len(c.templates))
	for path, source := range c.templates {
		result[path] = source
	}
	return result
}

func RenderText(recipient, event string, values Values) string {
	result, err := Builtin().Render(recipient, event, values)
	if err != nil {
		panic(err)
	}
	return result.Text
}

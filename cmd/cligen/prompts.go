package main

import (
	"fmt"
	"strings"

	"github.com/victorarias/attn/internal/prompts"
)

type promptRef struct{ recipient, event string }

var cliPrompts = []promptRef{
	{"session", "attn-map"},
	{"session", "garden-guidance"},
	{"session", "garden-ready"},
	{"session", "garden-row"},
	{"session", "inbox-empty"},
	{"session", "inbox-item"},
	{"session", "inbox-more"},
	{"session", "inbox-phone-silent"},
	{"session", "inbox-quiet"},
	{"session", "peer-message"},
	{"session", "quick-capture"},
	{"session", "quick-capture-file"},
	{"session", "quick-capture-file-saved"},
	{"session", "seed-artifacts"},
	{"delegation", "started"},
	{"authoring-agent", "seed-guide"},
}

func (p promptRef) words() []string {
	return strings.FieldsFunc(p.recipient+"-"+p.event, func(r rune) bool { return r == '-' })
}

func (p promptRef) functionName() string { return strings.Join(p.words(), "_") }

func (p promptRef) constantName() string { return strings.ToUpper(p.functionName()) }

func (p promptRef) typeName() string {
	var name strings.Builder
	for _, word := range p.words() {
		name.WriteString(strings.ToUpper(word[:1]) + word[1:])
	}
	return name.String()
}

func writePrompts(out *strings.Builder) {
	catalog := prompts.Builtin()
	var tests strings.Builder
	out.WriteString("pub mod prompts {\n")
	for _, prompt := range cliPrompts {
		body := eventBody(catalog, prompt)
		fields := prompts.NodeFields(body)
		if len(fields) == 0 {
			fmt.Fprintf(out, "    pub const %s: &str = %s;\n\n", prompt.constantName(), rustString(render(catalog, prompt, nil)))
			continue
		}
		writePromptFunction(out, catalog, prompt, body, fields)
		writePromptTest(&tests, catalog, prompt, body, fields)
	}
	out.WriteString("    #[cfg(test)]\n    mod tests {\n        use super::*;\n\n")
	out.WriteString(tests.String())
	out.WriteString("    }\n}\n")
}

func eventBody(catalog *prompts.Catalog, prompt promptRef) prompts.Node {
	for _, recipient := range catalog.Recipients() {
		if recipient.ID != prompt.recipient {
			continue
		}
		for _, event := range recipient.Events {
			if event.ID == prompt.event {
				return event.Body
			}
		}
	}
	fatal(fmt.Errorf("no prompt %s/%s", prompt.recipient, prompt.event))
	return prompts.Node{}
}

func render(catalog *prompts.Catalog, prompt promptRef, values prompts.Values) string {
	result, err := catalog.Render(prompt.recipient, prompt.event, values)
	if err != nil {
		fatal(fmt.Errorf("%s/%s: %w", prompt.recipient, prompt.event, err))
	}
	return result.Text
}

func writePromptFunction(out *strings.Builder, catalog *prompts.Catalog, prompt promptRef, body prompts.Node, fields []prompts.Field) {
	fmt.Fprintf(out, "    pub struct %s<'a> {\n", prompt.typeName())
	for _, field := range fields {
		fieldType := "&'a str"
		if field.Kind == "flag" {
			fieldType = "bool"
		}
		fmt.Fprintf(out, "        pub %s: %s,\n", field.Name, fieldType)
	}
	out.WriteString("    }\n\n    #[must_use]\n")
	fmt.Fprintf(out, "    pub fn %s(values: &%s<'_>) -> String {\n", prompt.functionName(), prompt.typeName())
	fmt.Fprintf(out, "        %s\n    }\n\n", promptExpression{catalog, prompt}.of(body))
}

type promptExpression struct {
	catalog *prompts.Catalog
	prompt  promptRef
}

func (e promptExpression) of(n prompts.Node) string {
	switch n.Kind {
	case "input":
		return e.input(n)
	case "text":
		return e.text(n)
	case "compose":
		return fmt.Sprintf("[%s].iter().filter(|part| !part.is_empty()).map(String::as_str).collect::<Vec<_>>().join(\"\\n\\n\")", e.list(n.Children))
	case "join":
		return fmt.Sprintf("[%s].join(%s)", e.list(n.Children), rustString(n.Separator))
	case "trim":
		return fmt.Sprintf("(%s).trim().to_string()", e.of(n.Children[0]))
	case "when":
		return fmt.Sprintf("if %s { %s } else { String::new() }", condition(n), e.of(n.Children[0]))
	case "choose":
		return fmt.Sprintf("if %s { %s } else { %s }", condition(n), e.of(n.Children[0]), e.of(n.Children[1]))
	}
	fatal(fmt.Errorf("%s/%s: cligen has no Rust form for node kind %q", e.prompt.recipient, e.prompt.event, n.Kind))
	return ""
}

func (e promptExpression) list(nodes []prompts.Node) string {
	parts := make([]string, len(nodes))
	for i, node := range nodes {
		parts[i] = e.of(node)
	}
	return strings.Join(parts, ", ")
}

func (e promptExpression) input(n prompts.Node) string {
	if n.Quote {
		fatal(fmt.Errorf("%s/%s: cligen has no Rust form for quoted input %s", e.prompt.recipient, e.prompt.event, n.Field.Name))
	}
	if n.Field.Kind == "flag" {
		return fmt.Sprintf("values.%s.to_string()", n.Field.Name)
	}
	if n.Field.TrimSpace {
		return fmt.Sprintf("values.%s.trim().to_string()", n.Field.Name)
	}
	return fmt.Sprintf("values.%s.to_string()", n.Field.Name)
}

func (e promptExpression) text(n prompts.Node) string {
	template := e.catalog.TextTemplate(n)
	if n.Verbatim {
		return rustString(template) + ".to_string()"
	}
	literals, names := prompts.SplitPlaceholders(template)
	if len(names) == 0 {
		return rustString(template) + ".to_string()"
	}
	bindings := map[string]prompts.Node{}
	for _, binding := range n.Bindings {
		bindings[binding.Name] = binding.Node
	}
	var parts []string
	for i, literal := range literals {
		if literal != "" {
			parts = append(parts, rustString(literal)+".to_string()")
		}
		if i < len(names) {
			parts = append(parts, e.of(bindings[names[i]]))
		}
	}
	return "[" + strings.Join(parts, ", ") + "].concat()"
}

func condition(n prompts.Node) string {
	if n.Condition.Test == "present" {
		return fmt.Sprintf("!values.%s.trim().is_empty()", n.Condition.Field.Name)
	}
	return "values." + n.Condition.Field.Name
}

func writePromptTest(out *strings.Builder, catalog *prompts.Catalog, prompt promptRef, body prompts.Node, fields []prompts.Field) {
	fmt.Fprintf(out, "        #[test]\n        fn %s_matches_go() {\n", prompt.functionName())
	for _, values := range valueCombinations(body, fields) {
		var initializers []string
		for _, field := range fields {
			value := values[field.Name]
			if field.Kind == "flag" {
				initializers = append(initializers, field.Name+": "+value)
				continue
			}
			initializers = append(initializers, field.Name+": "+rustString(value))
		}
		fmt.Fprintf(out, "            assert_eq!(%s(&%s { %s }), %s);\n",
			prompt.functionName(), prompt.typeName(), strings.Join(initializers, ", "), rustString(render(catalog, prompt, values)))
	}
	out.WriteString("        }\n\n")
}

func valueCombinations(body prompts.Node, fields []prompts.Field) []prompts.Values {
	tested := testedFields(body)
	combinations := []prompts.Values{{}}
	for _, field := range fields {
		var choices []string
		switch {
		case field.Kind == "flag":
			choices = []string{"true", "false"}
		case tested[field.Name]:
			choices = []string{"", "  ", "value of " + field.Name}
		case field.TrimSpace:
			choices = []string{"  value of " + field.Name + "\n"}
		default:
			choices = []string{"value of " + field.Name}
		}
		var next []prompts.Values
		for _, combination := range combinations {
			for _, choice := range choices {
				extended := prompts.Values{field.Name: choice}
				for name, value := range combination {
					extended[name] = value
				}
				next = append(next, extended)
			}
		}
		combinations = next
	}
	return combinations
}

func testedFields(body prompts.Node) map[string]bool {
	tested := map[string]bool{}
	var visit func(prompts.Node)
	visit = func(n prompts.Node) {
		if n.Condition != nil && n.Condition.Test == "present" {
			tested[n.Condition.Field.Name] = true
		}
		for _, binding := range n.Bindings {
			visit(binding.Node)
		}
		for _, child := range n.Children {
			visit(child)
		}
	}
	visit(body)
	return tested
}

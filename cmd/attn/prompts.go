package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/victorarias/attn/internal/prompts"
)

func runPrompts() { os.Exit(writePrompts(os.Stdout, os.Stderr, os.Args[2:])) }

func writePrompts(stdout, stderr io.Writer, args []string) int {
	fail := func(err error) int {
		fmt.Fprintf(stderr, "prompts: %v\n", err)
		return 2
	}
	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" {
		writePromptsHelp(stdout)
		return 0
	}
	command := args[0]
	var positional []string
	values := prompts.Values{}
	asJSON := false
	for i := 1; i < len(args); i++ {
		switch args[i] {
		case "--json":
			asJSON = true
		case "--set":
			i++
			if i == len(args) {
				return fail(fmt.Errorf("--set requires name=value"))
			}
			name, value, ok := strings.Cut(args[i], "=")
			if !ok || name == "" {
				return fail(fmt.Errorf("--set requires name=value"))
			}
			if _, duplicate := values[name]; duplicate {
				return fail(fmt.Errorf("input %s was set twice", name))
			}
			values[name] = value
		case "--help", "-h":
			writePromptsHelp(stdout)
			return 0
		default:
			if strings.HasPrefix(args[i], "-") {
				return fail(fmt.Errorf("unknown option %q", args[i]))
			}
			positional = append(positional, args[i])
		}
	}
	catalog := prompts.Builtin()
	writeJSON := func(value any) int {
		encoder := json.NewEncoder(stdout)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(value); err != nil {
			return fail(err)
		}
		return 0
	}
	switch command {
	case "list":
		if len(positional) != 0 || len(values) != 0 {
			return fail(fmt.Errorf("list takes no recipient or inputs"))
		}
		if asJSON {
			return writeJSON(catalog.Recipients())
		}
		for _, recipient := range catalog.Recipients() {
			fmt.Fprintf(stdout, "%s: %s\n", recipient.ID, recipient.Description)
			for _, event := range recipient.Events {
				fmt.Fprintf(stdout, "  %s -> %s\n", event.ID, event.Delivery)
			}
		}
	case "show":
		if len(positional) != 1 || len(values) != 0 {
			return fail(fmt.Errorf("usage: attn prompts show <recipient> [--json]"))
		}
		for _, recipient := range catalog.Recipients() {
			if recipient.ID != positional[0] {
				continue
			}
			if asJSON {
				return writeJSON(recipient)
			}
			fmt.Fprintf(stdout, "%s\n%s\n", recipient.ID, recipient.Description)
			for _, event := range recipient.Events {
				fmt.Fprintf(stdout, "\nOn %s -> %s\n%s\n", event.ID, event.Delivery, event.Description)
				writePromptNode(stdout, event.Body, "  ")
				fields, err := catalog.Fields(recipient.ID, event.ID)
				if err != nil {
					return fail(err)
				}
				fmt.Fprintln(stdout, "\nInputs (omitted flags are false; omitted text is absent):")
				for _, field := range fields {
					fmt.Fprintf(stdout, "  %s (%s): %s\n", field.Name, field.Kind, field.Description)
				}
			}
			return 0
		}
		return fail(fmt.Errorf("unknown recipient %q", positional[0]))
	case "render", "explain":
		if len(positional) != 2 {
			return fail(fmt.Errorf("usage: attn prompts %s <recipient> <event> [--set name=value] [--json]", command))
		}
		result, err := catalog.Render(positional[0], positional[1], values)
		if err != nil {
			return fail(err)
		}
		if asJSON {
			return writeJSON(result)
		}
		if command == "render" {
			if _, err := io.WriteString(stdout, result.Text); err != nil {
				return fail(err)
			}
			return 0
		}
		fmt.Fprintf(stdout, "%s / %s -> %s\nScenario preview of attn-supplied content, not evidence of delivery.\n\n", result.Recipient, result.Event, result.Delivery)
		writePromptTrace(stdout, result.Trace, "")
		fmt.Fprintln(stdout, "\nRendered content:")
		fmt.Fprintln(stdout, result.Text)
	default:
		return fail(fmt.Errorf("unknown command %q; use list, show, render, or explain", command))
	}
	return 0
}

func writePromptNode(w io.Writer, node prompts.Node, indent string) {
	label := node.Kind
	switch node.Kind {
	case "text":
		label = node.ID + " <- internal/prompts/" + node.Source
	case "input":
		label = "input " + node.Field.Name
		if node.Field.From != "" {
			label += " <- " + node.Field.From
		}
		if node.Quote {
			label += " (quoted)"
		}
	case "join":
		label += fmt.Sprintf(" %q", node.Separator)
	case "when", "choose":
		label += " " + node.Condition.Field.Name + " " + node.Condition.Test
	}
	fmt.Fprintf(w, "%s%s\n", indent, label)
	for _, binding := range node.Bindings {
		fmt.Fprintf(w, "%s  {{%s}}\n", indent, binding.Name)
		writePromptNode(w, binding.Node, indent+"    ")
	}
	for i, child := range node.Children {
		if node.Kind == "choose" {
			branch := "yes"
			if i == 1 {
				branch = "otherwise"
			}
			fmt.Fprintf(w, "%s  %s\n", indent, branch)
			writePromptNode(w, child, indent+"    ")
		} else {
			writePromptNode(w, child, indent+"  ")
		}
	}
}

func writePromptTrace(w io.Writer, trace prompts.Trace, indent string) {
	status := "included"
	if !trace.Selected {
		status = "skipped"
	}
	label := trace.ID
	if label == "" {
		label = trace.Kind
	}
	if trace.Source != "" {
		label += " <- internal/prompts/" + trace.Source
	}
	fmt.Fprintf(w, "%s[%s] %s", indent, status, label)
	if trace.Reason != "" {
		fmt.Fprintf(w, " (%s)", trace.Reason)
	}
	fmt.Fprintln(w)
	for _, child := range trace.Children {
		writePromptTrace(w, child, indent+"  ")
	}
}

func writePromptsHelp(w io.Writer) {
	fmt.Fprint(w, `usage: attn prompts <command>

Inspect the prompt composition bundled in this binary. These commands run
locally and do not contact a daemon or start an agent.

  list [--json]                         recipients and their covered events
  show <recipient> [--json]             all branches, sources, and inputs
  render <recipient> <event> [options]  composed text for a scenario
  explain <recipient> <event> [options] selected/skipped blocks and composed text

options:
  --set name=value  supply a declared input (repeat for different names)
  --json            include structured composition or render provenance

example:
  attn prompts explain session launch --set notebook_root=/tmp/notebook --set garden_available=true

The catalog covers launch, crew, delegation, automation, notifications, skills,
background model tasks, and Pi auto mode. Caller-rendered inputs link to their
producer events. A preview does not establish what a harness received.
`)
}

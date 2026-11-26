package main

import (
	"cmp"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/victorarias/attn/internal/analytics"
	"github.com/victorarias/attn/internal/catalog"
	"github.com/victorarias/attn/internal/garden"
	"github.com/victorarias/attn/internal/inbox"
	"github.com/victorarias/attn/internal/prompts"
	"github.com/victorarias/attn/internal/protocol"
)

const (
	helpWidth       = 80
	helpDocIndent   = "      "
	helpFlagColumn  = 30
	protocolSchemas = "internal/protocol/schema/tsp-output/json-schema"
)

var catalogValues = map[string]string{
	"default_results":         fmt.Sprint(garden.DefaultSearchResults),
	"max_results":             fmt.Sprint(garden.MaxSearchResults),
	"stale_window":            fmt.Sprintf("%dd", int(garden.DefaultStaleWindow.Hours()/24)),
	"entry_kinds":             strings.Join(garden.EntryKinds, ", "),
	"max_check_summary_chars": fmt.Sprint(garden.MaxCheckSummaryChars),
	"max_note_kib":            fmt.Sprint(garden.MaxNoteBytes >> 10),
	"max_inbox":               fmt.Sprint(inbox.MaxInboxLimit),
	"vent_text_limit":         fmt.Sprint(analytics.VentTextLimit),
	"ask_example":             prompts.RenderText("ask", "example", nil),
	"ask_reference_rules":     prompts.RenderText("ask", "rules", nil),
}

var surfaceValues = map[string]map[surface]string{
	"ask_rules": {
		cliSurface:  prompts.RenderText("ask", "help-rules", nil),
		toolSurface: prompts.RenderText("ask", "tool-rules", nil),
	},
	"ask_image_base": {
		cliSurface:  "resolve against the working directory, or beside the file passed to --file",
		toolSurface: "resolve against the folder the session started in",
	},
	"msg_after_dashes": {
		cliSurface:  ` In the CLI, a message that starts with - goes after --, as: attn agent msg -- <target> "-text".`,
		toolSurface: "",
	},
	"skill_command": {
		cliSurface:  "",
		toolSurface: " `command` prints one command's signature and rules, or the signatures of a family's commands.",
	},
}

var (
	catalogValuePattern = regexp.MustCompile(`\{([a-z_]+)\}`)
	backtickedPattern   = regexp.MustCompile("`([^`\n]+)`")
)

func writeCatalog(cliRoot, referencesDir, goRoot string) error {
	c, err := catalog.Builtin()
	if err != nil {
		return fmt.Errorf("catalog:\n%w", err)
	}
	g := generator{c: c}
	if err := g.checkResults(); err != nil {
		return err
	}
	if err := writeCommandTables(c, cliRoot, goRoot); err != nil {
		return err
	}
	if err := writeTools(c, goRoot); err != nil {
		return err
	}
	if err := writeToolFamilies(c, goRoot); err != nil {
		return err
	}
	for _, name := range c.LayerFamilies() {
		group := g.family(name)
		rust, err := g.rustHelp(group)
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(cliRoot, "commands", rustModule(name), "help_generated.rs"), []byte(rust), 0o644); err != nil {
			return err
		}
		if group.Reference == nil {
			continue
		}
		reference, err := g.reference(group)
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(referencesDir, group.Reference.Name+".md"), []byte(reference), 0o644); err != nil {
			return err
		}
	}
	return nil
}

type generator struct {
	c        *catalog.Catalog
	problems []string
}

func (g *generator) checkResults() error {
	var problems []string
	for _, cmd := range g.c.Commands {
		if cmd.Result == "" {
			continue
		}
		if _, err := os.Stat(filepath.Join(protocolSchemas, cmd.Result+".json")); err != nil {
			problems = append(problems, fmt.Sprintf("%s: %s: result %q is not a model in main.tsp", cmd.File, cmd.Name, cmd.Result))
		}
	}
	if len(problems) > 0 {
		return fmt.Errorf("catalog:\n%s", strings.Join(problems, "\n"))
	}
	return nil
}

func (g *generator) fail() error {
	if len(g.problems) == 0 {
		return nil
	}
	return fmt.Errorf("catalog:\n%s", strings.Join(g.problems, "\n"))
}

func (g *generator) expand(where, text string, on surface) string {
	return catalogValuePattern.ReplaceAllStringFunc(text, func(match string) string {
		name := match[1 : len(match)-1]
		if value, ok := catalogValues[name]; ok {
			return value
		}
		if values, ok := surfaceValues[name]; ok {
			return values[on]
		}
		names := append(sortedKeys(catalogValues), sortedKeys(surfaceValues)...)
		sort.Strings(names)
		g.problems = append(g.problems, fmt.Sprintf("%s: {%s} names no value; the values are %s", where, name, strings.Join(names, ", ")))
		return match
	})
}

func (g *generator) cliText(cmd *catalog.Command, where, text string) string {
	text = protocol.CLIText(g.expand(where, text, cliSurface))
	return backtickedPattern.ReplaceAllStringFunc(text, func(match string) string {
		word := match[1 : len(match)-1]
		if cmd != nil {
			for _, a := range cmd.Args {
				if a.Name == word {
					return g.argMention(cmd, a)
				}
			}
		}
		for i := range g.c.Commands {
			if g.c.Commands[i].IsTool() && g.c.Commands[i].ToolName() == word {
				return "`attn " + g.c.Commands[i].Name + "`"
			}
		}
		if strings.HasPrefix(word, "seed_") {
			g.problems = append(g.problems, fmt.Sprintf("%s: `%s` names no arg of this command and no command", where, word))
		}
		return match
	})
}

func (g *generator) argMention(cmd *catalog.Command, a catalog.Arg) string {
	switch {
	case a.CLI == "-m+":
		return "the rest of `" + splitFlag(cmd) + "`"
	case isSplitHead(cmd, a):
		return "the first line of `" + a.CLI + "`"
	case strings.Contains(a.CLI, "|"):
		return "`" + strings.ReplaceAll(a.CLI, "|", "` or `") + "`"
	case a.CLI == "pos" || a.CLI == "rest" || a.CLI == "words":
		return "`" + g.placeholder(a) + "`"
	case a.AfterDashes():
		return "the words after `--`"
	default:
		return "`" + strings.TrimSuffix(a.CLI, "...") + "`"
	}
}

func splitFlag(cmd *catalog.Command) string {
	for _, a := range cmd.Args {
		if isFlag(a) && a.CLI == "-m" {
			return a.CLI
		}
	}
	return "-m"
}

func isSplitHead(cmd *catalog.Command, a catalog.Arg) bool {
	if a.CLI != "-m" {
		return false
	}
	return slices.ContainsFunc(cmd.Args, func(other catalog.Arg) bool { return other.CLI == "-m+" })
}

func isFlag(a catalog.Arg) bool {
	return strings.HasPrefix(a.CLI, "-") && a.CLI != "-m+" && !a.AfterDashes()
}

func (g *generator) placeholder(a catalog.Arg) string {
	if a.Placeholder != "" {
		return a.Placeholder
	}
	if t := g.c.ArgType(a).Placeholder; t != "" {
		return t
	}
	return "<" + a.Name + ">"
}

type surface int

const (
	cliSurface surface = iota
	toolSurface
)

func (g *generator) argDoc(a catalog.Arg, on surface) string {
	doc := cmp.Or(a.Doc, g.c.ArgType(a).Doc)
	if doc == "" {
		return ""
	}
	switch {
	case on == cliSurface && a.Piped:
		return doc + "; read from this file, or from stdin without one"
	case on == cliSurface && a.Stdin && !a.File:
		return doc + "; - reads stdin"
	case on == cliSurface && a.Type == "path":
		return doc + "; a relative path resolves against the current directory"
	case on == toolSurface && a.Type == "path":
		return doc + "; a relative path resolves against the folder the session started in"
	}
	return doc
}

func (g *generator) argUsage(a catalog.Arg) (string, bool) {
	switch {
	case a.CLI == "-m+" || a.Internal() || a.ToolOnly():
		return "", false
	case a.CLI == "pos" || a.CLI == "rest" || a.CLI == "words":
		return g.placeholder(a), true
	case a.AfterDashes():
		return "-- " + g.placeholder(a) + "...", true
	case a.FlagWords() != "":
		return "--" + a.FlagWords() + " " + g.placeholder(a) + "...", true
	case strings.Contains(a.CLI, "|"):
		return strings.Join(strings.Split(a.CLI, "|"), " | "), true
	case a.Type == "bool":
		return a.CLI, true
	default:
		return a.CLI + " " + g.placeholder(a), true
	}
}

func (g *generator) usage(group string, cmd *catalog.Command) string {
	verb := strings.TrimPrefix(cmd.Name, group+" ")
	if !cmd.ThroughLayer() {
		return strings.TrimSpace(verb + " " + cmd.Usage)
	}
	forms := []string{g.usageForm(verb, cmd.Args)}
	if escape := unlessTarget(cmd); escape != "" {
		var main, alone []catalog.Arg
		for _, a := range cmd.Args {
			switch {
			case a.Name == escape:
				a.Required = true
				alone = append(alone, a)
			case a.Unless == escape:
				a.Required = true
				main = append(main, a)
			default:
				main = append(main, a)
				if a.CLI == "pos" && a.Required {
					alone = append([]catalog.Arg{a}, alone...)
				}
			}
		}
		forms = []string{g.usageForm(verb, main), g.usageForm(verb, alone)}
	}
	line := strings.Join(forms, " | ")
	if cmd.Result != "" {
		line += " [--json]"
	}
	return line
}

func unlessTarget(cmd *catalog.Command) string {
	for _, a := range cmd.Args {
		if a.Unless != "" {
			return a.Unless
		}
	}
	return ""
}

func (g *generator) usageForm(verb string, args []catalog.Arg) string {
	parts := []string{verb}
	done := map[string]bool{}
	for _, a := range args {
		if a.OneOf != "" {
			if done[a.OneOf] {
				continue
			}
			done[a.OneOf] = true
			var words []string
			for _, member := range args {
				if member.OneOf == a.OneOf && member.CLI == "pos" {
					words = append(words, strings.Trim(g.placeholder(member), "<>"))
				}
			}
			if len(words) > 1 {
				parts = append(parts, "<"+strings.Join(words, "|")+">")
				continue
			}
			var alternatives []string
			required := false
			for _, member := range args {
				if member.OneOf != a.OneOf {
					continue
				}
				if text, ok := g.argUsage(member); ok {
					alternatives = append(alternatives, text)
				}
				required = required || member.Required
			}
			if required {
				parts = append(parts, "("+strings.Join(alternatives, " | ")+")")
			} else {
				parts = append(parts, "["+strings.Join(alternatives, " | ")+"]")
			}
			continue
		}
		text, ok := g.argUsage(a)
		if !ok {
			continue
		}
		optional := !a.Required || a.File || a.Piped
		if strings.Contains(a.CLI, "|") {
			if a.Required {
				text = "(" + text + ")"
			} else {
				text = "[" + text + "]"
			}
		} else if optional {
			text = "[" + text + "]"
		}
		if a.List && !a.AfterDashes() && a.FlagWords() == "" {
			text += "..."
		}
		parts = append(parts, text)
	}
	return strings.Join(parts, " ")
}

func (g *generator) subgroupUsage(group, sub string) string {
	var moves []string
	for _, cmd := range g.c.GroupCommands(sub) {
		moves = append(moves, strings.TrimPrefix(cmd.Name, sub+" "))
	}
	return strings.TrimPrefix(sub, group+" ") + " <" + strings.Join(moves, "|") + ">"
}

func (g *generator) example(cmd *catalog.Command, values map[string]any) string {
	words := []string{"attn", cmd.Name}
	for _, a := range cmd.Args {
		value, ok := values[a.Name]
		if !ok {
			continue
		}
		switch {
		case a.CLI == "rest":
			words = append(words, strings.Fields(fmt.Sprint(value))...)
		case a.CLI == "words":
			items, _ := value.([]any)
			for _, item := range items {
				fields, _ := item.(map[string]any)
				for _, field := range g.wordFields(a) {
					words = append(words, shellWord(fmt.Sprint(fields[field])))
				}
			}
		case a.CLI == "pos":
			words = append(words, shellWord(fmt.Sprint(value)))
		case a.AfterDashes() || a.FlagWords() != "":
			words = append(words, strings.TrimSuffix(a.CLI, "..."))
			items, _ := value.([]any)
			for _, item := range items {
				words = append(words, shellWord(fmt.Sprint(item)))
			}
		case strings.Contains(a.CLI, "|"):
			words = append(words, "--"+fmt.Sprint(value))
		case a.Type == "bool":
			if value == true {
				words = append(words, a.CLI)
			}
		case a.List:
			items, _ := value.([]any)
			for _, item := range items {
				words = append(words, a.CLI, shellWord(fmt.Sprint(item)))
			}
		case a.CLI == "-m+":
		default:
			words = append(words, a.CLI, shellWord(fmt.Sprint(value)))
		}
	}
	return strings.Join(words, " ")
}

var bareWord = regexp.MustCompile(`^[A-Za-z0-9._/:@%+=,-]+$`)

func shellWord(value string) string {
	if bareWord.MatchString(value) {
		return value
	}
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`, "$", `\$`, "`", "\\`").Replace(value) + `"`
}

func (g *generator) flagLines(cmd *catalog.Command, group *catalog.Group) []string {
	if !cmd.ThroughLayer() {
		return nil
	}
	var lines []string
	add := func(label, doc string) {
		lines = append(lines, fmt.Sprintf("  %-*s %s", helpFlagColumn, label, doc))
	}
	for _, a := range cmd.Args {
		if !isFlag(a) {
			continue
		}
		label, _ := g.argUsage(a)
		add(label, g.cliText(cmd, cmd.Name+": arg "+a.Name, g.argDoc(a, cliSurface)))
	}
	if cmd.Result != "" {
		add("--json", "print the result as JSON")
	}
	for _, f := range group.Flags {
		add("--"+f.Name+" "+f.Placeholder, f.Doc)
	}
	return lines
}

func (g *generator) verbHelp(group *catalog.Group, cmd *catalog.Command) string {
	var out strings.Builder
	out.WriteString(g.usage(group.Name, cmd))
	out.WriteByte('\n')
	writeWrapped(&out, g.cliText(cmd, cmd.File+": "+cmd.Name, cmd.Doc), helpDocIndent)
	if len(cmd.Examples) > 0 {
		out.WriteByte('\n')
	}
	for _, values := range cmd.Examples {
		out.WriteString(helpDocIndent + "Example: " + g.example(cmd, values) + "\n")
	}
	if lines := g.flagLines(cmd, group); len(lines) > 0 {
		out.WriteString("\nflags:\n")
		for _, line := range lines {
			out.WriteString(line)
			out.WriteByte('\n')
		}
	}
	return out.String()
}

func (g *generator) subgroupHelp(group, sub *catalog.Group) string {
	var out strings.Builder
	out.WriteString(g.subgroupUsage(group.Name, sub.Name))
	out.WriteByte('\n')
	writeWrapped(&out, g.cliText(nil, sub.File+": "+sub.Name, sub.Doc), helpDocIndent)
	out.WriteString("\nflags:\n")
	for _, f := range group.Flags {
		fmt.Fprintf(&out, "  %-*s %s\n", helpFlagColumn, "--"+f.Name+" "+f.Placeholder, f.Doc)
	}
	return out.String()
}

func (g *generator) commandGroupHelp(group, sub *catalog.Group, cmd *catalog.Command) string {
	var out strings.Builder
	fmt.Fprintf(&out, "usage: attn %s\n       attn %s <command>\n\n", g.usage("", cmd), sub.Name)
	writeWrapped(&out, g.cliText(cmd, sub.File+": "+sub.Name, cmp.Or(sub.Doc, cmd.Doc)), "")
	fmt.Fprintf(&out, "Use attn %s <command> --help for one command's syntax and flags.\n\ncommands:\n", sub.Name)
	for _, child := range g.c.GroupCommands(sub.Name) {
		fmt.Fprintf(&out, "  %s\n", g.usage(sub.Name, child))
		writeWrapped(&out, g.cliText(child, child.File+": "+child.Name, child.Summary), "        ")
	}
	out.WriteString("\nflags:\n")
	for _, line := range g.flagLines(cmd, group) {
		out.WriteString(line + "\n")
	}
	return out.String()
}

func (g *generator) groupHelp(group *catalog.Group) string {
	var out strings.Builder
	bare, hasBare := g.c.Command(group.Name)
	verbs := g.c.GroupCommands(group.Name)
	if hasBare && len(verbs) == 0 {
		return "usage: attn " + g.verbHelp(group, bare)
	}
	fmt.Fprintf(&out, "usage: attn %s <command>\n", group.Name)
	if hasBare {
		fmt.Fprintf(&out, "       attn %s\n", g.usage(group.Name, bare))
	}
	out.WriteByte('\n')
	if hasBare {
		writeWrapped(&out, g.cliText(bare, group.File+": "+group.Name, cmp.Or(group.Doc, bare.Doc)), "")
		for _, values := range bare.Examples {
			out.WriteString("Example: " + g.example(bare, values) + "\n")
		}
	} else {
		writeWrapped(&out, g.cliText(nil, group.File+": "+group.Name, group.Doc), "")
	}
	fmt.Fprintf(&out, "Use attn %s <command> --help for one command's syntax and flags.\n", group.Name)
	sections := group.Sections
	if len(sections) == 0 {
		sections = []catalog.Section{{Title: "Commands"}}
		for _, cmd := range verbs {
			sections[0].Commands = append(sections[0].Commands, cmd.Name)
		}
	}
	for _, section := range sections {
		if len(section.Commands) == 0 {
			continue
		}
		fmt.Fprintf(&out, "\n%s:\n", strings.ToLower(section.Title[:1])+section.Title[1:])
		for _, name := range section.Commands {
			cmd, _ := g.c.Command(name)
			fmt.Fprintf(&out, "  %s\n", g.usage(group.Name, cmd))
			writeWrapped(&out, g.cliText(cmd, cmd.File+": "+cmd.Name, cmd.Summary), "        ")
		}
	}
	out.WriteString("\nflags:\n")
	if hasBare {
		for _, a := range bare.Args {
			if label, ok := g.argUsage(a); ok && isFlag(a) {
				fmt.Fprintf(&out, "  %-*s %s\n", helpFlagColumn, label, g.cliText(bare, bare.Name+": arg "+a.Name, g.argDoc(a, cliSurface)))
			}
		}
	}
	fmt.Fprintf(&out, "  %-*s %s\n", helpFlagColumn, "--json", "print the result as JSON (every command that prints a result)")
	for _, f := range group.Flags {
		fmt.Fprintf(&out, "  %-*s %s (every command)\n", helpFlagColumn, "--"+f.Name+" "+f.Placeholder, f.Doc)
	}
	return out.String()
}

func (g *generator) rustHelp(group *catalog.Group) (string, error) {
	type entry struct{ verb, help, usage string }
	var entries []entry
	for _, cmd := range g.c.FamilyCommands(group.Name) {
		verb := verbOf(group.Name, cmd)
		usage := g.usage(group.Name, cmd)
		if verb == "" {
			usage = strings.TrimSpace(strings.TrimPrefix(usage, group.Name))
		}
		entries = append(entries, entry{verb: verb, help: g.verbHelp(group, cmd), usage: usage})
	}
	for i := range g.c.Groups {
		sub := &g.c.Groups[i]
		if !strings.HasPrefix(sub.Name, group.Name+" ") {
			continue
		}
		verb := strings.TrimPrefix(sub.Name, group.Name+" ")
		if cmd, ok := g.c.Command(sub.Name); ok {
			for j := range entries {
				if entries[j].verb == verb {
					entries[j].help = g.commandGroupHelp(group, sub, cmd)
				}
			}
			continue
		}
		entries = append(entries, entry{
			verb:  verb,
			help:  g.subgroupHelp(group, sub),
			usage: g.subgroupUsage(group.Name, sub.Name),
		})
	}
	groupHelp := g.groupHelp(group)
	if err := g.fail(); err != nil {
		return "", err
	}
	var out strings.Builder
	out.WriteString("// Code generated by make generate-types from internal/catalog. DO NOT EDIT.\n\n#![allow(clippy::all, clippy::pedantic)]\n\n")
	fmt.Fprintf(&out, "#[rustfmt::skip]\npub const GROUP_HELP: &str = %s;\n\n", rustString(groupHelp))
	writeRustMatch(&out, "help", entries, func(e entry) string { return e.verb }, func(e entry) string { return e.help })
	out.WriteByte('\n')
	writeRustMatch(&out, "usage", entries, func(e entry) string { return e.verb }, func(e entry) string { return e.usage })
	return out.String(), nil
}

func rustModule(group string) string {
	return strings.ReplaceAll(group, "-", "_")
}

func writeRustMatch[T any](out *strings.Builder, name string, entries []T, verb, field func(T) string) {
	fmt.Fprintf(out, "#[rustfmt::skip]\npub fn %s(verb: &str) -> &'static str {\n    match verb {\n", name)
	for _, e := range entries {
		fmt.Fprintf(out, "        %s => %s,\n", rustString(verb(e)), rustString(field(e)))
	}
	out.WriteString("        _ => panic!(\"unknown verb: {verb}\"),\n    }\n}\n")
}

func (g *generator) reference(group *catalog.Group) (string, error) {
	var out strings.Builder
	ref := group.Reference
	fmt.Fprintf(&out, "# %s\n\n", ref.Title)
	out.WriteString(strings.TrimSpace(g.cliText(nil, group.File+": reference intro", ref.Intro)))
	out.WriteString("\n")
	for _, section := range group.Sections {
		fmt.Fprintf(&out, "\n## %s\n", section.Title)
		if text := strings.TrimSpace(section.Text); text != "" {
			out.WriteString("\n" + g.cliText(nil, group.File+": section "+section.Title, text) + "\n")
		}
		if len(section.Commands) > 0 {
			out.WriteString("\n")
		}
		for _, name := range section.Commands {
			cmd, _ := g.c.Command(name)
			command := group.Name + " " + g.usage(group.Name, cmd)
			if cmd.Name == group.Name {
				command = g.usage(group.Name, cmd)
			}
			fmt.Fprintf(&out, "- `attn %s`: %s\n", command, g.cliText(cmd, cmd.File+": "+cmd.Name, cmd.Summary))
		}
	}
	if err := g.fail(); err != nil {
		return "", err
	}
	return out.String(), nil
}

func writeWrapped(out *strings.Builder, text, indent string) {
	for i, block := range helpBlocks(strings.TrimSpace(text)) {
		if i > 0 {
			out.WriteByte('\n')
		}
		switch block.kind {
		case verbatimBlock:
			for _, line := range block.lines {
				out.WriteString(strings.TrimRight(indent+line, " ") + "\n")
			}
		case listItemBlock:
			for _, item := range block.lines {
				marker := listMarker.FindString(item)
				writeWords(out, item[len(marker):], indent+marker, indent+strings.Repeat(" ", len(marker)))
			}
		default:
			writeWords(out, strings.Join(block.lines, " "), indent, indent)
		}
	}
}

type helpBlockKind int

const (
	proseBlock helpBlockKind = iota
	listItemBlock
	verbatimBlock
)

type helpBlock struct {
	kind  helpBlockKind
	lines []string
}

var (
	listMarker = regexp.MustCompile(`^\s*(?:[-*+]|\d+[.)])\s+`)
	fenceOpen  = regexp.MustCompile("^\\s*(`{3,}|~{3,})")
)

func helpBlocks(text string) []helpBlock {
	var blocks []helpBlock
	lines := strings.Split(text, "\n")
	for i := 0; i < len(lines); {
		line := lines[i]
		switch {
		case strings.TrimSpace(line) == "":
			i++
		case fenceOpen.MatchString(line):
			fence := strings.TrimSpace(fenceOpen.FindStringSubmatch(line)[1])
			end := i + 1
			for end < len(lines) && !closesFence(lines[end], fence) {
				end++
			}
			end = min(end+1, len(lines))
			blocks = append(blocks, helpBlock{verbatimBlock, lines[i:end]})
			i = end
		case strings.HasPrefix(line, "    ") || strings.HasPrefix(line, "\t"):
			end := i
			for end < len(lines) && (strings.HasPrefix(lines[end], "    ") || strings.HasPrefix(lines[end], "\t") || strings.TrimSpace(lines[end]) == "" && end+1 < len(lines) && strings.HasPrefix(lines[end+1], "    ")) {
				end++
			}
			blocks = append(blocks, helpBlock{verbatimBlock, lines[i:end]})
			i = end
		case listMarker.MatchString(line):
			var items []string
			for i < len(lines) && strings.TrimSpace(lines[i]) != "" && !fenceOpen.MatchString(lines[i]) {
				if listMarker.MatchString(lines[i]) {
					items = append(items, strings.TrimRight(lines[i], " "))
				} else {
					items[len(items)-1] += " " + strings.TrimSpace(lines[i])
				}
				i++
			}
			blocks = append(blocks, helpBlock{listItemBlock, items})
		default:
			var prose []string
			for i < len(lines) && strings.TrimSpace(lines[i]) != "" && !fenceOpen.MatchString(lines[i]) && !listMarker.MatchString(lines[i]) {
				prose = append(prose, lines[i])
				i++
			}
			blocks = append(blocks, helpBlock{proseBlock, prose})
		}
	}
	return blocks
}

func closesFence(line, fence string) bool {
	trimmed := strings.TrimSpace(line)
	return len(trimmed) >= len(fence) && strings.Trim(trimmed, fence[:1]) == ""
}

func writeWords(out *strings.Builder, text, first, rest string) {
	line, lead := first, first
	for _, word := range strings.Fields(text) {
		if line != lead && len(line)+1+len(word) > helpWidth {
			out.WriteString(strings.TrimRight(line, " ") + "\n")
			line, lead = rest, rest
		}
		if line != lead {
			line += " "
		}
		line += word
	}
	out.WriteString(line + "\n")
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

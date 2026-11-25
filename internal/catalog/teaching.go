package catalog

import (
	"regexp"
	"slices"
	"strings"
	"sync"
	"unicode/utf8"
)

var (
	headingLine   = sync.OnceValue(func() *regexp.Regexp { return regexp.MustCompile("^(#{1,6} .+|[A-Z][A-Z ,]+[A-Z])$") })
	fenceLine     = sync.OnceValue(func() *regexp.Regexp { return regexp.MustCompile("^\\s*(`{3,}|~{3,})") })
	signatureLine = sync.OnceValue(func() *regexp.Regexp { return regexp.MustCompile("^(\\s*- )`(attn [^`]+)`(: .*)$") })
	blockCommand  = sync.OnceValue(func() *regexp.Regexp { return regexp.MustCompile(`^(\s*)(attn .*?)(\s{2,}\S.*)?$`) })
	runWord       = sync.OnceValue(func() *regexp.Regexp { return regexp.MustCompile(`([Rr])un(\s)$`) })
	shellOutput   = sync.OnceValue(func() *regexp.Regexp { return regexp.MustCompile(`^\$\((.+)\)$`) })
	plainAttn     = sync.OnceValue(func() *regexp.Regexp { return regexp.MustCompile("attn( [a-z_][a-z0-9_-]*)+") })
	plainFlag     = sync.OnceValue(func() *regexp.Regexp { return regexp.MustCompile(`(^|[\s(])--[a-z][a-z-]*`) })
)

const cliOnlyMark = " (CLI only)"

func (c *Catalog) TeachingToolText(text string) string {
	t := &teaching{c: c}
	t.split(text)
	t.family = t.mostTaughtFamily()
	var out strings.Builder
	for i := range t.parts {
		rendered := t.render(i)
		if strings.HasSuffix(t.parts[i].text, "--help`") && t.parts[i].kind == teachSpan {
			before := runWord().ReplaceAllStringFunc(out.String(), func(run string) string {
				return map[byte]string{'R': "Read", 'r': "read"}[run[0]] + run[3:]
			})
			out.Reset()
			out.WriteString(before)
		}
		out.WriteString(rendered)
	}
	return out.String()
}

type teachingKind int

const (
	teachProse teachingKind = iota
	teachSpan
	teachSignature
	teachBlockCommand
	teachBlockSummary
	teachBlockText
)

type teachingPart struct {
	kind    teachingKind
	text    string
	section int
	command *Command
}

type teaching struct {
	c       *Catalog
	parts   []teachingPart
	family  string
	section int
}

func (t *teaching) split(text string) {
	lines := strings.SplitAfter(text, "\n")
	var prose strings.Builder
	flush := func() {
		t.splitSpans(prose.String())
		prose.Reset()
	}
	add := func(kind teachingKind, text, command string) {
		flush()
		t.add(kind, text, t.mentioned(command))
	}
	fence, shellFence := "", false
	summaryColumn := 0
	for i := 0; i < len(lines); i++ {
		line := strings.TrimSuffix(lines[i], "\n")
		if open := fenceLine().FindStringSubmatch(line); open != nil && (fence == "" || strings.HasPrefix(open[1], fence) && strings.TrimSpace(line) == open[1]) {
			if fence == "" {
				fence, shellFence = open[1], strings.TrimSpace(line) == open[1]+"sh"
				if shellFence {
					lines[i] = strings.Replace(lines[i], "sh", "text", 1)
				}
			} else {
				fence = ""
			}
			add(teachBlockText, lines[i], "")
			summaryColumn = 0
			continue
		}
		indented := strings.HasPrefix(line, "    ")
		if match := blockCommand().FindStringSubmatch(line); match != nil && (fence != "" || indented) {
			for strings.HasSuffix(strings.TrimRight(lines[i], "\n"), "\\") && i+1 < len(lines) {
				next := strings.TrimLeft(lines[i+1], " ")
				lines[i+1] = strings.TrimSuffix(strings.TrimRight(lines[i], "\n"), "\\") + next
				i++
			}
			match = blockCommand().FindStringSubmatch(strings.TrimRight(lines[i], "\n"))
			add(teachBlockCommand, lines[i], match[2])
			summaryColumn = columnOfSummary(match)
			continue
		}
		if summaryColumn > 0 && leadingSpaces(line) >= summaryColumn {
			add(teachBlockSummary, lines[i], "")
			continue
		}
		summaryColumn = 0
		switch match := signatureLine().FindStringSubmatch(line); {
		case fence != "" || indented && t.inBlock(prose.Len()):
			add(teachBlockText, lines[i], "")
		case match != nil && t.mentioned(match[2]) != nil:
			add(teachSignature, lines[i], match[2])
		default:
			if headingLine().MatchString(line) {
				flush()
				t.section++
			}
			prose.WriteString(lines[i])
		}
	}
	flush()
}

func columnOfSummary(blockLine []string) int {
	indent, command, summary := blockLine[1], blockLine[2], blockLine[3]
	if summary == "" {
		return 0
	}
	return len(indent) + utf8.RuneCountInString(command) + leadingSpaces(summary)
}

func leadingSpaces(line string) int {
	return len(line) - len(strings.TrimLeft(line, " "))
}

func (t *teaching) inBlock(unflushed int) bool {
	if unflushed > 0 || len(t.parts) == 0 {
		return false
	}
	last := t.parts[len(t.parts)-1].kind
	return last == teachBlockCommand || last == teachBlockSummary || last == teachBlockText
}

func (t *teaching) add(kind teachingKind, text string, command *Command) {
	if text != "" {
		t.parts = append(t.parts, teachingPart{kind: kind, text: text, section: t.section, command: command})
	}
}

func (t *teaching) splitSpans(text string) {
	start := 0
	for i := 0; i < len(text); {
		if text[i] != '`' {
			i++
			continue
		}
		run := i
		for run < len(text) && text[run] == '`' {
			run++
		}
		ticks := text[i:run]
		end := closingTicks(text[run:], ticks)
		if end < 0 {
			i = run
			continue
		}
		close := run + end + len(ticks)
		if len(ticks) == 1 {
			t.add(teachProse, text[start:i], nil)
			t.add(teachSpan, text[i:close], t.mentioned(strings.Join(strings.Fields(text[run:run+end]), " ")))
			start = close
		}
		i = close
	}
	t.add(teachProse, text[start:], nil)
}

func closingTicks(text, ticks string) int {
	for at := 0; at < len(text); {
		if text[at] != '`' {
			if strings.HasPrefix(text[at:], "\n\n") {
				return -1
			}
			at++
			continue
		}
		end := at
		for end < len(text) && text[end] == '`' {
			end++
		}
		if text[at:end] == ticks {
			return at
		}
		at = end
	}
	return -1
}

func (t *teaching) mentioned(line string) *Command {
	words, err := mentionWords(line)
	if err != nil || len(words) < 2 || words[0] != "attn" {
		return nil
	}
	cmds, _ := t.c.mentionedCommands(words[1:])
	if len(cmds) == 0 {
		return nil
	}
	return cmds[0]
}

func (t *teaching) mostTaughtFamily() string {
	counts := map[string]int{}
	var order []string
	for _, part := range t.parts {
		family := t.familyOf(part.command)
		if family == "" {
			continue
		}
		if counts[family] == 0 {
			order = append(order, family)
		}
		counts[family]++
	}
	best := ""
	for _, family := range order {
		if counts[family] > counts[best] {
			best = family
		}
	}
	return best
}

func (t *teaching) familyOf(cmd *Command) string {
	if cmd == nil {
		return ""
	}
	family := cmd.Name
	if _, grouped := t.c.Group(family); !grouped {
		family = cmd.Name[:max(strings.LastIndexByte(cmd.Name, ' '), 0)]
	}
	return family
}

func (t *teaching) render(i int) string {
	part := t.parts[i]
	switch part.kind {
	case teachSignature:
		match := signatureLine().FindStringSubmatch(strings.TrimSuffix(part.text, "\n"))
		if !part.command.IsTool() {
			return part.text
		}
		return match[1] + "`" + t.c.toolSignature(part.command) + "`" + match[3] + "\n"
	case teachBlockCommand:
		return t.renderBlock(i)
	case teachBlockSummary:
		return t.plainText(i, part.text)
	case teachSpan:
		return t.renderSpan(i, strings.Join(strings.Fields(strings.Trim(part.text, "`")), " "))
	}
	return part.text
}

func (t *teaching) plainText(i int, text string) string {
	text = plainFlag().ReplaceAllStringFunc(text, func(match string) string {
		before, flag, _ := strings.Cut(match, "--")
		flag = "--" + flag
		return before + t.renderFlags(i, flag, flag)
	})
	return plainAttn().ReplaceAllStringFunc(text, func(match string) string {
		words := strings.Fields(match)[1:]
		for n := len(words); n > 0; n-- {
			if cmd, ok := t.c.Command(strings.Join(words[:n], " ")); ok && cmd.IsTool() {
				return strings.Join(append([]string{"`" + cmd.ToolName() + "`"}, words[n:]...), " ")
			}
		}
		return match
	})
}

func (t *teaching) renderBlock(i int) string {
	text, newline := strings.CutSuffix(t.parts[i].text, "\n")
	match := blockCommand().FindStringSubmatch(text)
	rendered := match[2]
	if call, ok := t.toolCall(match[2]); ok {
		rendered = call.inline()
	}
	rendered = match[1] + rendered + t.plainText(i, match[3])
	if newline {
		rendered += "\n"
	}
	return rendered
}

func (t *teaching) renderSpan(i int, span string) string {
	literal := t.parts[i].text
	switch {
	case strings.HasPrefix(span, "attn "):
		return t.renderCommand(literal, span)
	case strings.Contains(span, " attn "):
		printed, line, _ := strings.Cut(span, " attn ")
		if call, ok := t.toolCall("attn " + line); ok {
			return "`" + printed + " " + call.inline() + "`"
		}
		return literal
	case isFlag(span):
		return t.renderFlags(i, literal, span)
	case t.family == "" || !strings.Contains(span, " "):
		return literal
	}
	call, ok := t.toolCall("attn " + t.family + " " + span)
	switch {
	case !ok:
		return literal
	case len(call.leavesOut) > 0:
		return literal + cliOnlyMark
	}
	return call.mention()
}

func (t *teaching) renderCommand(literal, span string) string {
	if line, help := strings.CutSuffix(span, " --help"); help {
		return t.renderHelp(literal, line)
	}
	if call, ok := t.toolCall(span); ok {
		return call.mention()
	}
	if group, ok := t.c.Group(strings.TrimPrefix(span, "attn ")); ok && len(t.c.Tools(group.Name)) > 0 {
		return "the `" + strings.ReplaceAll(group.Name, " ", "_") + "_*` tools"
	}
	return literal
}

func (t *teaching) renderHelp(literal, line string) string {
	words := strings.Fields(strings.TrimPrefix(line, "attn "))
	last := len(words) - 1
	anyCommand := last >= 0 && strings.HasPrefix(words[last], "<")
	if anyCommand {
		words = words[:last]
	}
	name := strings.Join(words, " ")
	if cmd, ok := t.c.Command(name); ok && cmd.IsTool() && !anyCommand {
		return "`" + cmd.ToolName() + "`'s description"
	}
	if group, ok := t.c.Group(name); ok && len(t.c.Tools(group.Name)) > 0 {
		tools := "`" + strings.ReplaceAll(group.Name, " ", "_") + "_*`"
		if anyCommand {
			return "the description of one of the " + tools + " tools"
		}
		return "the descriptions of the " + tools + " tools"
	}
	return literal
}

func (t *teaching) renderFlags(i int, literal, span string) string {
	words, err := mentionWords(span)
	if err != nil || len(words) == 0 {
		return literal
	}
	for _, cmd := range append(t.nearestCommands(i), t.c.onlyMeaningOfFlag(words[0])...) {
		if t.c.flagOnlyOnCLI(cmd, words[0]) {
			return literal + cliOnlyMark
		}
		if !cmd.IsTool() {
			continue
		}
		args, err := mentionArgs(t.c, cmd, words)
		if err != nil || len(args) == 0 {
			continue
		}
		return flagMention(cmd, args, words)
	}
	return literal
}

func flagMention(cmd *Command, args []mentionArg, words []string) string {
	if len(args) == 1 && len(words) == 1 && args[0].choice == "" {
		if rest := splitRest(cmd, args[0].name); rest != "" {
			return "`" + args[0].name + "` (one line; the rest goes in `" + rest + "`)"
		}
		return "`" + args[0].name + "`"
	}
	text := jsonArgs(args)
	if len(args) == 1 {
		text = strings.TrimSuffix(strings.TrimPrefix(text, "{"), "}")
	}
	return "`" + text + "`"
}

func splitRest(cmd *Command, name string) string {
	head := slices.ContainsFunc(cmd.Args, func(a Arg) bool { return a.Name == name && a.CLI == "-m" })
	for _, a := range cmd.Args {
		if head && a.CLI == "-m+" {
			return a.Name
		}
	}
	return ""
}

func (c *Catalog) onlyMeaningOfFlag(flag string) []*Command {
	var found []*Command
	names := map[string]bool{}
	for i := range c.Commands {
		cmd := &c.Commands[i]
		if arg, _ := flagArg(cmd, flag); arg != nil && cmd.IsTool() {
			found = append(found, cmd)
			names[arg.Name] = true
		}
	}
	if len(names) != 1 {
		return nil
	}
	return found[:1]
}

func (c *Catalog) flagOnlyOnCLI(cmd *Command, flag string) bool {
	if arg, _ := flagArg(cmd, flag); arg != nil {
		return arg.CLIOnly
	}
	if flag == "--json" {
		return cmd.Result != ""
	}
	group, _, _ := strings.Cut(cmd.Name, " ")
	g, ok := c.Group(group)
	return ok && slices.ContainsFunc(g.Flags, func(f Flag) bool { return "--"+f.Name == flag })
}

func (t *teaching) nearestCommands(i int) []*Command {
	var near, far []*Command
	take := func(j int) {
		if j < 0 || j >= len(t.parts) || t.parts[j].command == nil {
			return
		}
		if t.parts[j].section == t.parts[i].section {
			near = append(near, t.parts[j].command)
		} else {
			far = append(far, t.parts[j].command)
		}
	}
	for j := i - 1; j >= 0; j-- {
		take(j)
	}
	for j := i + 1; j < len(t.parts); j++ {
		take(j)
	}
	return append(near, far...)
}

type teachingCall struct {
	tools     []string
	args      []mentionArg
	leavesOut []string
}

func (call teachingCall) inline() string {
	inline := call.tools[0]
	if len(call.args) > 0 {
		inline += " " + jsonArgs(call.args)
	}
	if len(call.leavesOut) > 0 {
		inline += " without " + strings.Join(call.leavesOut, " ") + cliOnlyMark
	}
	return inline
}

func (call teachingCall) mention() string {
	var names []string
	for _, tool := range call.tools {
		names = append(names, "`"+tool+"`")
	}
	mention := strings.Join(names, " or ")
	if args := jsonArgs(call.args); args != "" {
		mention += " with " + args
	}
	if len(call.leavesOut) > 0 {
		mention += " without `" + strings.Join(call.leavesOut, " ") + "`" + cliOnlyMark
	}
	return mention
}

func (t *teaching) toolCall(line string) (teachingCall, bool) {
	words, err := mentionWords(strings.NewReplacer("[", "", "]", "", ">...", ">").Replace(line))
	if err != nil || len(words) < 2 || words[0] != "attn" {
		return teachingCall{}, false
	}
	cmds, rest := t.c.mentionedCommands(words[1:])
	if cmds == nil {
		return teachingCall{}, false
	}
	var call teachingCall
	for _, cmd := range cmds {
		if !cmd.IsTool() {
			return teachingCall{}, false
		}
		call.tools = append(call.tools, cmd.ToolName())
	}
	args, err := mentionArgs(t.c, cmds[0], rest)
	if err != nil {
		return teachingCall{}, false
	}
	for _, arg := range args {
		if !arg.cliOnly {
			call.args = append(call.args, arg)
			continue
		}
		name, isFile := strings.CutSuffix(arg.name, "_file")
		if isFile && slices.ContainsFunc(cmds[0].Args, func(a Arg) bool { return a.Name == name }) {
			call.args = append(call.args, mentionArg{name: name, value: contentsOf(arg.value)})
			continue
		}
		call.leavesOut = append(call.leavesOut, arg.written)
	}
	return call, true
}

func (c *Catalog) toolSignature(cmd *Command) string {
	var args []string
	for _, a := range cmd.Args {
		if a.CLIOnly || a.Internal() {
			continue
		}
		text := a.Name
		if !a.Required || a.Unless != "" {
			text += "?"
		}
		schema := c.ArgType(a).Schema
		switch {
		case len(a.Values) > 0:
			text += ": " + strings.Join(a.Values, "|")
		case schema["type"] == "boolean":
			text += ": true"
		case a.List || schema["type"] == "array":
			text += ": [...]"
		case schema["type"] == "object":
			text += ": {...}"
		}
		args = append(args, text)
	}
	if len(args) == 0 {
		return cmd.ToolName()
	}
	return cmd.ToolName() + " {" + strings.Join(args, ", ") + "}"
}

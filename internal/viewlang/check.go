package viewlang

import (
	"encoding/json"
	"fmt"
	"maps"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/victorarias/attn/internal/document"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/viewlang/catalog"
)

type Checked struct {
	Language int
	Parts    []protocol.ViewPart
	Problems []protocol.ViewProblem
}

var (
	openerPattern  = sync.OnceValue(func() *regexp.Regexp { return regexp.MustCompile(`^(:{3,4})([a-z][a-z0-9-]*)(?:[ \t]+(.*))?$`) })
	versionPattern = sync.OnceValue(func() *regexp.Regexp { return regexp.MustCompile(`^<!--\s*attn-view\b(.*?)-->$`) })
	mediaPattern   = sync.OnceValue(func() *regexp.Regexp { return regexp.MustCompile(`^!\[([^\]]*)\]\(([^()\s]+)\)$`) })
	taskPattern    = sync.OnceValue(func() *regexp.Regexp { return regexp.MustCompile(`^[-*] \[( |x|X)\] (.*)$`) })
	videoTypes     = []string{".mp4", ".mov", ".m4v", ".webm", ".mkv", ".avi"}
	viewSyntax     = document.Syntax{Opens: opensPart, Containers: true}
	innerSyntax    = document.Syntax{Opens: func(trimmed string) bool { return opensPart(trimmed) && !strings.HasPrefix(trimmed, "::::") }}
)

func opensPart(trimmed string) bool { return openerPattern().MatchString(trimmed) }

// IsView says whether markdown is a view: it carries the language line, or a
// line outside code opens one of the catalog's parts. Other markdown, such as
// a document that uses ":::note" for its own purpose, is not.
func IsView(markdown string) bool {
	lines := strings.Split(strings.ReplaceAll(markdown, "\r\n", "\n"), "\n")
	if versionPattern().MatchString(strings.TrimSpace(lines[0])) {
		return true
	}
	var fence document.Fence
	for _, line := range lines {
		if !fence.Open() {
			if match := openerPattern().FindStringSubmatch(strings.TrimSpace(line)); match != nil {
				if _, known := catalog.FindPart(match[2]); known {
					return true
				}
			}
		}
		fence.Track(line)
	}
	return false
}

func Check(markdown string) Checked {
	c := &checker{prepared: map[int]out{}, storeFields: map[string]string{}}
	lines := strings.Split(strings.ReplaceAll(markdown, "\r\n", "\n"), "\n")
	language, versioned := c.language(lines[0])
	if language > catalog.Language {
		return Checked{Language: language, Parts: []protocol.ViewPart{}, Problems: c.sorted()}
	}
	if versioned {
		lines[0] = ""
	}
	blocks, refused := viewSyntax.Blocks(strings.Join(lines, "\n"))
	c.prepare(blocks)
	parts := c.blocks(blocks, 0, c.top, true)
	if refused != nil {
		c.lexProblem(refused, 0)
	}
	return Checked{Language: language, Parts: typed(parts), Problems: c.sorted()}
}

type problem struct {
	line int
	text string
	fix  string
}

type checker struct {
	problems    []problem
	top         *shape
	prepared    map[int]out
	storeFields map[string]string
	own         map[string]int
	hasStore    bool
}

func (c *checker) report(line int, fix, format string, args ...any) {
	c.problems = append(c.problems, problem{line: line, text: fmt.Sprintf(format, args...), fix: fix})
}

func (c *checker) sorted() []protocol.ViewProblem {
	sort.SliceStable(c.problems, func(i, j int) bool { return c.problems[i].line < c.problems[j].line })
	problems := make([]protocol.ViewProblem, 0, len(c.problems))
	for _, p := range c.problems {
		problems = append(problems, protocol.ViewProblem{Line: p.line, Text: p.text, Fix: p.fix})
	}
	return problems
}

func (c *checker) language(first string) (int, bool) {
	trimmed := strings.TrimSpace(first)
	match := versionPattern().FindStringSubmatch(trimmed)
	if match == nil {
		return catalog.Language, false
	}
	words := strings.Fields(match[1])
	n, err := 0, error(nil)
	if len(words) == 1 || len(words) == 2 {
		n, err = strconv.Atoi(words[0])
	}
	if _, stamped := parseStamp(words); len(words) == 2 && !stamped {
		c.report(1, "Keep the line as the export wrote it, or remove its second word.", "the language line's second word names no version of a view, as %q does", "sessions@2")
		return catalog.Language, true
	}
	if len(words) < 1 || len(words) > 2 || err != nil || n < 1 {
		c.report(1, fmt.Sprintf("Write it as \"<!-- attn-view %d -->\", or remove the line.", catalog.Language), "the language line names no version")
		return catalog.Language, true
	}
	if n > catalog.Language {
		c.report(1, "Update attn to open it.", "this view is written in language version %d, and this attn reads up to version %d", n, catalog.Language)
	}
	return n, true
}

func (c *checker) lexProblem(refused *document.LexError, offset int) {
	line := refused.Line + offset
	switch refused.Refusal {
	case document.Unclosed:
		o := parseOpener(refused.Directive)
		if part, ok := catalog.FindPart(o.name); ok && part.Container() && o.colons == ":::" {
			c.report(line, fmt.Sprintf("Open it with \"%s\" and close it with \"::::\".", part.Opener()), "%q holds parts, so it opens with four colons", o.name)
			return
		}
		if o.colons == "::::" {
			c.report(line, "Close it with a line that holds only ::::, before the next four-colon part opens.", "%q has no closing ::::", refused.Directive)
			return
		}
		c.report(line, "Close it with a line that holds only :::.", "%q has no closing :::", refused.Directive)
	case document.StrayCloser:
		c.report(line, "Remove the line, or open a part above it.", "%q closes no part", refused.Directive)
	default:
		c.report(line, "Put the part's name right after the colons, such as \":::list items\".", "%q does not open a part", refused.Directive)
	}
}

type opener struct {
	colons string
	name   string
	rest   string
}

func parseOpener(text string) opener {
	m := openerPattern().FindStringSubmatch(text)
	return opener{colons: m[1], name: m[2], rest: m[3]}
}

func (c *checker) prepare(blocks []document.Block) {
	c.top = object("")
	c.own = map[string]int{}
	var unnamed bool
	named := map[string]int{}
	for _, b := range blocks {
		if !b.IsPart() {
			continue
		}
		o := parseOpener(b.Opener)
		part, ok := catalog.FindPart(o.name)
		if !ok || part.Opener() != o.colons+o.name || (part.Name != "data" && part.Name != "commands") {
			continue
		}
		mark := len(c.problems)
		payload, args := c.args(part, o.rest, b.Line, unknown)
		var body any
		if part.Name == "data" {
			name := ""
			if len(c.problems) == mark {
				name = c.source(b.Line, part, args)
			}
			fields, source := c.storeBody(b, args)
			if fields != nil {
				body = fields
			}
			switch {
			case name == "":
				unnamed = true
			case named[name] != 0:
				c.report(b.Line, "Give one of them another name with as=.", "%q already names the data of line %d", name, named[name])
			default:
				named[name] = b.Line
				c.top.fields[name] = source
			}
		} else {
			body = c.ownCommands(b)
		}
		if body != nil {
			payload[bodyField(part.Body)] = body
		}
		c.prepared[b.Line] = c.finish(out{"name": part.Name, "line": b.Line}, part, payload, mark)
	}
	if unnamed {
		c.top = unknown
	}
}

func (c *checker) blocks(blocks []document.Block, offset int, sc *shape, top bool) []out {
	parts := []out{}
	answers := map[string]int{}
	for _, b := range blocks {
		line := b.Line + offset
		if !b.IsPart() {
			parts = append(parts, c.prose(b.Lines, line, sc)...)
			continue
		}
		if prepared, ok := c.prepared[line]; ok && top {
			parts = append(parts, prepared)
			continue
		}
		parts = append(parts, c.part(b, line, sc, top, answers))
	}
	return parts
}

func (c *checker) finish(o out, part catalog.Part, payload out, mark int) out {
	if len(c.problems) > mark {
		first := c.problems[mark]
		o["problem"] = out{"line": first.line, "text": first.text, "fix": first.fix}
		return o
	}
	o[wireName(part.Name)] = payload
	return o
}

func (c *checker) part(b document.Block, line int, sc *shape, top bool, answers map[string]int) out {
	o := parseOpener(b.Opener)
	result := out{"name": o.name, "line": line}
	mark := len(c.problems)
	part, ok := catalog.FindPart(o.name)
	switch {
	case o.name == catalog.ReservedPart:
		c.report(line, "Draw it with the parts a view has, such as \":::list\" or \":::chart\".", "%q is reserved for programs that draw, which are not available", o.name)
	case !ok:
		c.report(line, didYouMean(o.name, partNames(), "Use one of the parts: "+strings.Join(partNames(), ", ")+"."), "%q is not a part", o.name)
	case part.Opener() != o.colons+o.name && part.Container():
		c.report(line, fmt.Sprintf("Open it with \"%s\" and close it with \"::::\".", part.Opener()), "%q holds parts, so it opens with four colons", o.name)
	case part.Opener() != o.colons+o.name:
		c.report(line, fmt.Sprintf("Open it with \"%s\" and close it with \":::\".", part.Opener()), "%q opens with three colons", o.name)
	case part.TopOnly && !top:
		c.report(line, "Move it out of the four-colon part, to the top level of the view.", "%q goes at the top level of a view", ":::"+o.name)
	}
	if len(c.problems) > mark {
		return c.finish(result, part, nil, mark)
	}
	payload, args := c.args(part, o.rest, line, sc)
	items := c.itemScope(line, part, args, sc)
	for _, arg := range part.Args {
		segments, ok := payload[wireName(arg.Name)].([]string)
		switch {
		case ok && arg.Kind == catalog.FieldArg:
			c.resolve(line, segments, sc)
		case ok && arg.Kind == catalog.KeyedFieldArg:
			c.resolve(line, segments, items)
		}
	}
	switch part.Body {
	case catalog.NoBody:
		c.noBody(b, line, part)
	case catalog.QuestionBody, catalog.TextQuestionBody:
		c.question(b, line, part, args, sc, answers, payload)
	case catalog.MediaPairBody:
		payload["media"] = c.mediaPair(b, line, sc)
	case catalog.FactsBody:
		payload["facts"] = c.facts(b, line, sc)
	case catalog.SlotsBody:
		payload["slots"] = c.slots(b, line, part, items, sc)
	case catalog.TableBody:
		payload["columns"] = c.table(b, line, items)
	case catalog.TextBody:
		if text := strings.TrimSpace(b.Text()); text != "" {
			payload["text"] = text
		}
	case catalog.ControlsBody:
		payload["controls"] = c.controls(b, line)
	case catalog.BlocksBody:
		own := len(c.problems)
		inner, refused := innerSyntax.Blocks(strings.Join(b.Lines, "\n"))
		payload["blocks"] = c.blocks(inner, line, items, false)
		if refused != nil {
			c.lexProblem(refused, line)
			if own == mark {
				own, mark = len(c.problems), len(c.problems)-1
			}
		}
		if own > mark {
			first := c.problems[mark]
			result["problem"] = out{"line": first.line, "text": first.text, "fix": first.fix}
			return result
		}
		result[wireName(part.Name)] = payload
		return result
	}
	return c.finish(result, part, payload, mark)
}

func partNames() []string {
	var out []string
	for _, part := range catalog.Parts {
		if part.Colons > 0 {
			out = append(out, part.Name)
		}
	}
	return out
}

func bodyField(body catalog.Body) string {
	switch body {
	case catalog.MediaPairBody:
		return "media"
	case catalog.FieldsBody:
		return "fields"
	case catalog.FactsBody:
		return "facts"
	case catalog.SlotsBody:
		return "slots"
	case catalog.TableBody:
		return "columns"
	case catalog.BlocksBody:
		return "blocks"
	case catalog.TextBody:
		return "text"
	case catalog.ControlsBody:
		return "controls"
	case catalog.CommandsBody:
		return "commands"
	case catalog.TasksBody:
		return "tasks"
	}
	return ""
}

func bodyLines(b document.Block, line int) []numbered {
	var lines []numbered
	for i, text := range b.Lines {
		if strings.TrimSpace(text) != "" {
			lines = append(lines, numbered{line: line + 1 + i, text: strings.TrimSpace(text)})
		}
	}
	return lines
}

type numbered struct {
	line int
	text string
}

func (c *checker) noBody(b document.Block, line int, part catalog.Part) {
	if lines := bodyLines(b, line); len(lines) > 0 {
		c.report(lines[0].line, fmt.Sprintf("Remove the lines between \"%s\" and \":::\".", part.Opener()), "%q takes no body", part.Opener())
	}
}

func (c *checker) args(part catalog.Part, rest string, line int, sc *shape) (out, map[string]string) {
	payload := out{}
	values := map[string]string{}
	tokens, err := tokenize(rest)
	if err != nil {
		c.report(line, "Close each quote, and put a space between arguments.", "%s", err.Error())
		return payload, values
	}
	opener := part.Opener()
	var positional []catalog.Arg
	keyed := map[string]catalog.Arg{}
	flags := map[string]catalog.Arg{}
	for _, arg := range part.Args {
		switch {
		case arg.Kind.Positional():
			positional = append(positional, arg)
		case arg.Kind == catalog.FlagArg:
			flags[arg.Name] = arg
		default:
			keyed[arg.Name] = arg
		}
	}
	argNames := names(part.Args, func(a catalog.Arg) string { return a.Name })
	for i := 0; i < len(tokens); i++ {
		t := tokens[i]
		if t.keyed() {
			if _, ok := keyed[t.key]; !ok {
				c.report(line, didYouMean(t.key, argNames, takes(part)), "%q takes no argument %q", opener, t.key)
				continue
			}
			if _, seen := values[t.key]; seen {
				c.report(line, "Give it once.", "%q is given twice", t.key)
				continue
			}
			values[t.key] = t.value
			continue
		}
		if _, ok := flags[t.value]; ok {
			values[t.value] = "true"
			continue
		}
		if len(positional) == 0 {
			c.report(line, takes(part), "%q does not take %q", opener, t.value)
			continue
		}
		arg := positional[0]
		positional = positional[1:]
		if arg.Kind == catalog.CommandArg {
			words := []string{t.value}
			for _, more := range tokens[i+1:] {
				words = append(words, more.value)
			}
			values[arg.Name] = strings.Join(words, " ")
			i = len(tokens)
			continue
		}
		values[arg.Name] = t.value
	}
	for _, arg := range part.Args {
		value, given := values[arg.Name]
		if !given {
			if arg.Required {
				c.report(line, fmt.Sprintf("Write %s, where %s is %s.", usage(part), arg.Form(), arg.Doc), "%q needs %s", opener, arg.Form())
			}
			continue
		}
		if wire, ok := c.argValue(line, part, arg, value, values, sc); ok {
			payload[wireName(arg.Name)] = wire
		}
	}
	return payload, values
}

func usage(part catalog.Part) string {
	words := []string{part.Opener()}
	for _, arg := range part.Args {
		if arg.Required {
			words = append(words, arg.Form())
		}
	}
	return "\"" + strings.Join(words, " ") + "\""
}

func takes(part catalog.Part) string {
	if len(part.Args) == 0 {
		return fmt.Sprintf("Write %q alone on its line.", part.Opener())
	}
	return fmt.Sprintf("%q takes %s.", part.Opener(), strings.Join(names(part.Args, catalog.Arg.Form), ", "))
}

func (c *checker) argValue(line int, part catalog.Part, arg catalog.Arg, value string, values map[string]string, sc *shape) (any, bool) {
	switch arg.Kind {
	case catalog.ItemsArg:
		if isDataFile(value) {
			if !c.inFolder(line, value) {
				return nil, false
			}
			return out{"file": value}, true
		}
		segments, ok := fieldPath(value)
		if !ok {
			c.report(line, "Name a field, such as \"items\" or \"ci.jobs\", or a .json or .jsonl file.", "%q is neither a field nor a data file", value)
			return nil, false
		}
		return out{"field": segments}, true
	case catalog.FieldArg, catalog.KeyedFieldArg:
		segments, ok := fieldPath(value)
		if !ok {
			c.report(line, "Name a field, such as \"state\" or \"ci.state\".", "%q is not a field", value)
			return nil, false
		}
		return segments, true
	case catalog.NameArg:
		if !namePattern().MatchString(value) {
			c.report(line, "Use lowercase letters, digits and dashes, starting with a letter.", "%q cannot name an answer", value)
			return nil, false
		}
		return value, true
	case catalog.CommandArg:
		return c.call(line, value)
	case catalog.FileArg:
		if !isDataFile(value) {
			c.report(line, "Name a .json or .jsonl file in the view's folder.", "%s=%q is not a JSON or JSON Lines file", arg.Name, value)
			return nil, false
		}
		if !c.inFolder(line, value) {
			return nil, false
		}
		return value, true
	case catalog.FlagArg:
		return true, true
	case catalog.ChoiceArg:
		if !slices.Contains(arg.Choices, value) {
			c.report(line, didYouMean(value, arg.Choices, "Use one of "+quoted(arg.Choices)+"."), "%q is not one of attn's data sets", value)
			return nil, false
		}
		return value, true
	case catalog.DurationArg:
		d, err := time.ParseDuration(value)
		if err != nil || d <= 0 {
			c.report(line, "Write a duration such as 30s, 10m or 1h.", "%s=%q is not a duration", arg.Name, value)
			return nil, false
		}
		return value, true
	}
	if strings.TrimSpace(value) == "" {
		c.report(line, fmt.Sprintf("Give %s= a value, or remove it.", arg.Name), "%s= is empty", arg.Name)
		return nil, false
	}
	return value, true
}

func count(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

func (c *checker) inFolder(line int, file string) bool {
	if path.IsAbs(file) || !filepath.IsLocal(file) {
		c.report(line, "Put the file in the view's folder, and name it from there, such as \"rows.jsonl\" or \"data/rows.jsonl\".", "%q is outside the view's folder", file)
		return false
	}
	return true
}

func isDataFile(value string) bool {
	ext := path.Ext(value)
	return ext == ".json" || ext == ".jsonl"
}

func (c *checker) itemScope(line int, part catalog.Part, values map[string]string, sc *shape) *shape {
	value, ok := values["items"]
	if !ok || isDataFile(value) {
		return unknown
	}
	segments, ok := fieldPath(value)
	if !ok {
		return unknown
	}
	target := c.resolve(line, segments, sc)
	if !target.known {
		return unknown
	}
	if target.item == nil {
		c.report(line, "Name a field that holds a list of items.", "%q is not a list", value)
		return unknown
	}
	return target.item
}

type shape struct {
	known  bool
	name   string
	fields map[string]*shape
	item   *shape
	kind   string
}

var unknown = &shape{}

// ShortestEvery is the most often a run= command reruns. Starting a trivial
// process costs 1.3 ms on ai-sandbox (100 in 132 ms, 2026-11-27), so a second
// keeps attn's own cost near 0.1%; the outside service a command calls is the
// real limiter, and the views reference tells agents its cost.
const ShortestEvery = time.Second

func object(name string) *shape { return &shape{known: true, name: name, fields: map[string]*shape{}} }

func (c *checker) resolve(line int, segments []string, sc *shape) *shape {
	current := sc
	for i, segment := range segments {
		if !current.known {
			return unknown
		}
		at := strings.Join(segments[:i], ".")
		switch {
		case current.fields != nil:
			next, ok := current.fields[segment]
			if !ok {
				c.missingField(line, current, at, segment)
				return unknown
			}
			current = next
		case current.item != nil:
			c.report(line, fmt.Sprintf("Show its items with \":::list %s\", or inside \"::::each %s\".", at, at), "%q is a list, so it has no field %q", at, segment)
			return unknown
		default:
			c.report(line, fmt.Sprintf("Use %q itself.", at), "%q is %s, so it has no field %q", at, current.kind, segment)
			return unknown
		}
	}
	return current
}

func (c *checker) missingField(line int, sc *shape, at, field string) {
	fields := slices.Sorted(maps.Keys(sc.fields))
	owner := at
	if owner == "" {
		owner = sc.name
	}
	switch {
	case owner == "" && len(fields) == 0:
		c.report(line, "Add a \":::data\" part that holds it.", "this view has no data, so it has no field %q", field)
	case owner == "":
		c.report(line, didYouMean(field, fields, "Its fields are "+strings.Join(fields, ", ")+"."), "this view's data has no field %q", field)
	default:
		c.report(line, didYouMean(field, fields, "Its fields are "+strings.Join(fields, ", ")+"."), "%q has no field %q", owner, field)
	}
}

func (c *checker) refs(line int, refs []fieldRef, sc *shape) {
	formats := names(catalog.Formats, func(f catalog.Named) string { return f.Name })
	for _, ref := range refs {
		if ref.format != "" && !slices.Contains(formats, ref.format) {
			c.report(line, didYouMean(ref.format, formats, "Use one of "+quoted(formats)+"."), "%q is not a format", ref.format)
		}
		c.resolve(line, ref.path, sc)
	}
}

func (c *checker) template(line int, text string, sc *shape) []out {
	pieces, refs := template(text)
	c.refs(line, refs, sc)
	return pieces
}

func (c *checker) prose(lines []string, first int, sc *shape) []out {
	var parts []out
	var markdown []string
	var tasks []out
	markdownAt, tasksAt, tasksMark := 0, 0, 0
	var fence document.Fence
	flushMarkdown := func() {
		for len(markdown) > 0 && strings.TrimSpace(markdown[len(markdown)-1]) == "" {
			markdown = markdown[:len(markdown)-1]
		}
		if len(markdown) > 0 {
			mark := len(c.problems)
			text := c.proseTemplate(markdownAt, markdown, sc)
			parts = append(parts, c.finish(out{"name": "markdown", "line": markdownAt}, catalog.Form("markdown"), out{"text": text}, mark))
		}
		markdown = nil
	}
	flushTasks := func() {
		if len(tasks) > 0 {
			parts = append(parts, c.finish(out{"name": "tasks", "line": tasksAt}, catalog.Form("tasks"), out{"tasks": tasks}, tasksMark))
		}
		tasks = nil
	}
	for i, text := range lines {
		line := first + i
		trimmed := strings.TrimSpace(text)
		if !fence.Open() {
			if m := mediaPattern().FindStringSubmatch(trimmed); m != nil {
				flushMarkdown()
				flushTasks()
				parts = append(parts, c.media(line, m[1], m[2], sc))
				continue
			}
			if m := taskPattern().FindStringSubmatch(trimmed); m != nil {
				flushMarkdown()
				if len(tasks) == 0 {
					tasksAt, tasksMark = line, len(c.problems)
				}
				tasks = append(tasks, out{"text": c.template(line, m[2], sc), "checked": m[1] != " "})
				continue
			}
		}
		flushTasks()
		fence.Track(text)
		if len(markdown) == 0 && trimmed == "" {
			continue
		}
		if len(markdown) == 0 {
			markdownAt = line
		}
		markdown = append(markdown, text)
	}
	flushMarkdown()
	flushTasks()
	return parts
}

func (c *checker) proseTemplate(first int, lines []string, sc *shape) []out {
	var pieces []out
	var fence document.Fence
	for i, text := range lines {
		if i > 0 {
			pieces = append(pieces, out{"text": "\n"})
		}
		wasOpen := fence.Open()
		fence.Track(text)
		if wasOpen || fence.Open() {
			pieces = append(pieces, out{"text": text})
			continue
		}
		pieces = append(pieces, c.template(first+i, text, sc)...)
	}
	return joinText(pieces)
}

func joinText(pieces []out) []out {
	joined := []out{}
	for _, piece := range pieces {
		text, isText := piece["text"].(string)
		if isText && len(joined) > 0 {
			if last, ok := joined[len(joined)-1]["text"].(string); ok {
				joined[len(joined)-1] = out{"text": last + text}
				continue
			}
		}
		if isText && text == "" {
			continue
		}
		joined = append(joined, piece)
	}
	return joined
}

func (c *checker) media(line int, label, file string, sc *shape) out {
	mark := len(c.problems)
	o := out{"name": "media", "line": line}
	payload := out{"label": c.template(line, label, sc), "file": c.template(line, file, sc)}
	if slices.Contains(videoTypes, strings.ToLower(path.Ext(file))) {
		c.report(line, "Show a picture or an audio clip instead.", "%q is a video, and a view does not play video", file)
	}
	return c.finish(o, catalog.Form("media"), payload, mark)
}

func (c *checker) question(b document.Block, line int, part catalog.Part, args map[string]string, sc *shape, answers map[string]int, payload out) {
	if name, ok := args["name"]; ok {
		if earlier, taken := answers[name]; taken {
			c.report(line, "Give each question its own name.", "the question on line %d already saves its answer as %q", earlier, name)
		} else {
			answers[name] = line
		}
	}
	body := make([]string, 0, len(b.Lines))
	var fence document.Fence
	for i, text := range b.Lines {
		value, isValue := strings.CutPrefix(strings.TrimSpace(text), "Value:")
		if fence.Open() || !isValue {
			fence.Track(text)
			body = append(body, text)
			continue
		}
		body = append(body, "")
		if part.Body != catalog.TextQuestionBody {
			c.report(line+1+i, "Move the starting text to a \":::question-text\", or remove the line.", "Value: goes only in a \":::question-text\"")
			continue
		}
		payload["value"] = c.template(line+1+i, strings.TrimSpace(value), sc)
	}
	question, after, err := document.Question(document.Block{Line: line, Opener: ":::" + part.Name, Lines: body}, document.QuestionRules{Strict: true})
	if lineErr, ok := err.(*document.LineError); ok {
		c.report(lineErr.Line, "Write the question on the first line of the part, then one \"- [ ] choice\" per line.", "%s", lineErr.Message)
		return
	}
	if after > 0 {
		c.report(line, "Remove the Default: line.", "Default: is for asks; a view waits for the user")
		return
	}
	question.Key = args["name"]
	payload["question"] = question
}

func (c *checker) mediaPair(b document.Block, line int, sc *shape) []out {
	var media []out
	for _, l := range bodyLines(b, line) {
		m := mediaPattern().FindStringSubmatch(l.text)
		if m == nil {
			c.report(l.line, "Write two lines of the form ![label](file), and nothing else.", "%q is not a media line", l.text)
			continue
		}
		if one, ok := c.media(l.line, m[1], m[2], sc)["media"].(out); ok {
			media = append(media, one)
		}
	}
	if len(media) != 2 {
		c.report(line, "Write two lines of the form ![label](file).", "\":::compare\" holds two media lines, and this one holds %d", len(media))
	}
	return media
}

func (c *checker) facts(b document.Block, line int, sc *shape) []out {
	facts := []out{}
	for _, l := range bodyLines(b, line) {
		label, value, ok := strings.Cut(l.text, "|")
		if !ok || strings.TrimSpace(label) == "" {
			c.report(l.line, "Write it as \"label | value\".", "%q is not a fact", l.text)
			continue
		}
		facts = append(facts, out{"label": strings.TrimSpace(label), "value": c.template(l.line, strings.TrimSpace(value), sc)})
	}
	if len(facts) == 0 && len(bodyLines(b, line)) == 0 {
		c.report(line, "Add one \"label | value\" line per fact.", "\":::facts\" holds no facts")
	}
	return facts
}

func (c *checker) slots(b document.Block, line int, part catalog.Part, items, sc *shape) out {
	slots := out{}
	seen := map[string]int{}
	slotNames := names(part.Slots, func(s catalog.Slot) string { return s.Name })
	for _, l := range bodyLines(b, line) {
		name, value, ok := strings.Cut(l.text, ":")
		name, value = strings.TrimSpace(name), strings.TrimSpace(value)
		if !ok || !namePattern().MatchString(name) {
			c.report(l.line, fmt.Sprintf("Write it as \"slot: value\". The slots are %s.", strings.Join(slotNames, ", ")), "%q is not a slot line", l.text)
			continue
		}
		slot, found := findSlot(part, name)
		if !found {
			c.report(l.line, didYouMean(name, slotNames, "The slots are "+strings.Join(slotNames, ", ")+"."), "%q is not a slot of %q", name, part.Opener())
			continue
		}
		if earlier, twice := seen[name]; twice {
			c.report(l.line, "Keep one of them.", "the slot %q is already given on line %d", name, earlier)
			continue
		}
		seen[name] = l.line
		key := wireName(name)
		switch slot.Kind {
		case catalog.TemplateSlot:
			scope := items
			if strings.HasPrefix(name, "group-") {
				scope = sc
			}
			slots[key] = c.template(l.line, value, scope)
		case catalog.TextSlot:
			slots[key] = value
		case catalog.CommandSlot:
			if call, ok := c.call(l.line, value); ok {
				slots[key] = call
			}
		case catalog.CommandsSlot:
			calls := []any{}
			for _, one := range strings.Split(value, ",") {
				if call, ok := c.call(l.line, strings.TrimSpace(one)); ok {
					calls = append(calls, call)
				}
			}
			slots[key] = calls
		}
	}
	return slots
}

func findSlot(part catalog.Part, name string) (catalog.Slot, bool) {
	for _, slot := range part.Slots {
		if slot.Name == name {
			return slot, true
		}
	}
	return catalog.Slot{}, false
}

func cells(row string) []string {
	row = strings.TrimSpace(row)
	row = strings.TrimPrefix(row, "|")
	row = strings.TrimSuffix(row, "|")
	var cells []string
	depth, start := 0, 0
	for i, r := range row {
		switch {
		case r == '{':
			depth++
		case r == '}' && depth > 0:
			depth--
		case r == '|' && depth == 0:
			cells = append(cells, strings.TrimSpace(row[start:i]))
			start = i + 1
		}
	}
	return append(cells, strings.TrimSpace(row[start:]))
}

var separatorPattern = sync.OnceValue(func() *regexp.Regexp { return regexp.MustCompile(`^\|?\s*:?-+:?\s*(\|\s*:?-+:?\s*)*\|?$`) })

func (c *checker) table(b document.Block, line int, items *shape) []out {
	lines := bodyLines(b, line)
	fix := "Write a markdown table of three lines: the headers, a line of dashes, and one row of fields such as \"| {name} | {state} |\"."
	if len(lines) != 3 || !separatorPattern().MatchString(lines[1].text) {
		c.report(line, fix, "\":::table\" holds a header, a line of dashes and one row")
		return nil
	}
	headers, row := cells(lines[0].text), cells(lines[2].text)
	if len(headers) != len(row) {
		c.report(lines[2].line, "Give the row one cell per header.", "the row has %s and the header has %s", count(len(row), "cell"), count(len(headers), "cell"))
		return nil
	}
	columns := make([]out, 0, len(headers))
	for i, header := range headers {
		columns = append(columns, out{"header": header, "cell": c.template(lines[2].line, row[i], items)})
	}
	return columns
}

func (c *checker) controls(b document.Block, line int) []any {
	calls := []any{}
	for _, l := range bodyLines(b, line) {
		if call, ok := c.call(l.line, l.text); ok {
			calls = append(calls, call)
		}
	}
	if len(bodyLines(b, line)) == 0 {
		c.report(line, "Write one command per line, such as \"refresh\".", "\":::controls\" holds no controls")
	}
	return calls
}

func (c *checker) knownCommands() []string {
	known := names(catalog.Commands, func(n catalog.Named) string { return n.Name })
	if c.hasStore {
		known = append(known, names(catalog.StoreCommands, func(s catalog.StoreCommand) string { return s.Name })...)
	}
	return append(known, slices.Sorted(maps.Keys(c.own))...)
}

func storeCommand(name string) (catalog.StoreCommand, bool) {
	for _, command := range catalog.StoreCommands {
		if command.Name == name {
			return command, true
		}
	}
	return catalog.StoreCommand{}, false
}

func (c *checker) call(line int, text string) (any, bool) {
	words := strings.Fields(text)
	if len(words) == 0 {
		c.report(line, "Name a command, such as \"open\".", "a command is missing")
		return nil, false
	}
	name, args := words[0], words[1:]
	known := c.knownCommands()
	if !slices.Contains(known, name) {
		fix := didYouMean(name, known, "Use one of attn's commands ("+strings.Join(names(catalog.Commands, func(n catalog.Named) string { return n.Name }), ", ")+"), or define it in \":::commands\".")
		if _, isStore := storeCommand(name); isStore {
			fix = "Add a \":::data store=\" part that holds the items."
		}
		c.report(line, fix, "%q is not a command of this view", name)
		return nil, false
	}
	call := out{"command": name, "args": slices.Clone(args)}
	store, isStore := storeCommand(name)
	if !isStore {
		if len(args) > 0 {
			c.report(line, fmt.Sprintf("Write %q alone.", name), "%q takes no arguments", name)
			return nil, false
		}
		return call, true
	}
	want := 0
	if store.Field {
		want++
	}
	if store.Value {
		want++
	}
	if len(args) != want {
		c.report(line, fmt.Sprintf("Write %q.", storeUsage(store)), "%q takes %s, and this one has %s", name, count(want, "argument"), count(len(args), "argument"))
		return nil, false
	}
	if !store.Field {
		return call, true
	}
	kind, ok := c.storeFields[args[0]]
	switch {
	case !ok:
		fields := slices.Sorted(maps.Keys(c.storeFields))
		c.report(line, didYouMean(args[0], fields, "The store's fields are "+strings.Join(fields, ", ")+"."), "the store has no field %q", args[0])
		return nil, false
	case store.Type != "" && kind != store.Type:
		c.report(line, fmt.Sprintf("Name a field of type %s.", store.Type), "%q is %s, and %q switches a %s", args[0], kind, name, store.Type)
		return nil, false
	}
	return call, true
}

func storeUsage(command catalog.StoreCommand) string {
	words := []string{command.Name}
	if command.Field {
		words = append(words, "<field>")
	}
	if command.Value {
		words = append(words, "<value>")
	}
	return strings.Join(words, " ")
}

func (c *checker) source(line int, part catalog.Part, args map[string]string) string {
	var sources, given []string
	for _, arg := range part.Args {
		if !arg.Source {
			continue
		}
		sources = append(sources, arg.Name+"=")
		if _, ok := args[arg.Name]; ok {
			given = append(given, arg.Name)
		}
	}
	switch len(given) {
	case 0:
		c.report(line, "Give it one of "+strings.Join(sources, ", ")+".", "\":::data\" names no source")
	case 1:
	default:
		c.report(line, "Give each source its own \":::data\" part.", "\":::data\" names %d sources: %s", len(given), strings.Join(given, ", "))
	}
	if words := strings.Fields(args["run"]); len(words) > 0 && strings.Contains(words[0], "/") && !filepath.IsLocal(words[0]) {
		c.report(line, "Run a program by its name, such as \"gh\", or put it in the view's folder and name it from there, such as \"scripts/status.sh\".", "run= runs %q, which is outside the view's folder", words[0])
	}
	if every, ok := args["every"]; ok && len(given) == 1 && given[0] != "run" {
		c.report(line, "Remove every=, or use run=.", "every= reruns a run= command, and this part has none")
	} else if d, err := time.ParseDuration(every); ok && err == nil && d > 0 && d < ShortestEvery {
		c.report(line, "Write every=\"1s\" or longer.", "every=%q reruns the command more often than once a second", every)
	}
	if name := args["as"]; name != "" {
		return name
	}
	return args["store"]
}

func (c *checker) storeBody(b document.Block, args map[string]string) ([]out, *shape) {
	store, isStore := args["store"]
	lines := bodyLines(b, b.Line)
	if !isStore {
		if len(lines) > 0 {
			c.report(lines[0].line, "Remove the lines, or use store= with them.", "only a store lists fields in \":::data\"")
		}
		return nil, unknown
	}
	shared := strings.Contains(store, "/")
	if shared && len(lines) > 0 {
		c.report(lines[0].line, "Remove the lines; the view that owns the store declares them.", "%q is another view's store, so this view lists no fields", store)
		return nil, unknown
	}
	if shared {
		return nil, unknown
	}
	if len(lines) == 0 {
		c.report(b.Line, "List each field as \"name: type\", such as \"text: text\".", "the store %q lists no fields", store)
		return nil, unknown
	}
	types := names(catalog.StoreTypes, func(n catalog.Named) string { return n.Name })
	item := object(store)
	fields := []out{}
	for _, l := range lines {
		name, kind, ok := strings.Cut(l.text, ":")
		name, kind = strings.TrimSpace(name), strings.TrimSpace(kind)
		if !ok || !segmentPattern().MatchString(name) {
			c.report(l.line, "Write it as \"name: type\".", "%q is not a field", l.text)
			continue
		}
		if !slices.Contains(types, kind) {
			c.report(l.line, didYouMean(kind, types, "Use one of "+quoted(types)+"."), "%q is not a field type", kind)
			continue
		}
		if _, twice := item.fields[name]; twice {
			c.report(l.line, "Keep one of them.", "the field %q is listed twice", name)
			continue
		}
		item.fields[name] = &shape{known: true, kind: kind}
		c.storeFields[name] = kind
		fields = append(fields, out{"name": name, "type": kind})
	}
	c.hasStore = true
	name := args["as"]
	if name == "" {
		name = store
	}
	return fields, &shape{known: true, name: name, item: item}
}

func (c *checker) ownCommands(b document.Block) []out {
	attn := names(catalog.Commands, func(n catalog.Named) string { return n.Name })
	store := names(catalog.StoreCommands, func(s catalog.StoreCommand) string { return s.Name })
	commands := []out{}
	lines := bodyLines(b, b.Line)
	if len(lines) == 0 {
		c.report(b.Line, "Write one command per line, as \"name: program arguments\".", "\":::commands\" defines no commands")
	}
	for _, l := range lines {
		name, rest, ok := strings.Cut(l.text, ":")
		name = strings.TrimSpace(name)
		if !ok || !namePattern().MatchString(name) {
			c.report(l.line, "Write it as \"name: program arguments\", such as \"rerun: scripts/rerun.sh {id}\".", "%q does not define a command", l.text)
			continue
		}
		if slices.Contains(attn, name) || slices.Contains(store, name) {
			c.report(l.line, "Give the command another name.", "%q is one of attn's commands, so the view cannot define it", name)
			continue
		}
		if earlier, twice := c.own[name]; twice {
			c.report(l.line, "Keep one of them.", "the command %q is already defined on line %d", name, earlier)
			continue
		}
		tokens, err := tokenize(rest)
		command := out{"name": name, "written": l.text}
		for err == nil && len(tokens) > 0 && tokens[0].keyed() {
			if tokens[0].key != "confirm" || tokens[0].value == "" {
				err = fmt.Errorf("%q is not how a command asks first", tokens[0].key+"="+tokens[0].value)
				break
			}
			command["confirm"] = c.template(l.line, tokens[0].value, unknown)
			tokens = tokens[1:]
		}
		if err != nil {
			c.report(l.line, "Write it as \"name: program arguments\". To ask the user first, put confirm=\"Question?\" before the program.", "%s", err)
			continue
		}
		if len(tokens) == 0 {
			c.report(l.line, "Write the program after the colon, then its arguments.", "%q names no program", name)
			continue
		}
		args := []any{}
		for _, t := range tokens[1:] {
			word := t.value
			if t.keyed() {
				word = t.key + "=" + t.value
			}
			args = append(args, c.template(l.line, word, unknown))
		}
		c.own[name] = l.line
		command["program"], command["args"] = tokens[0].value, args
		commands = append(commands, command)
	}
	return commands
}

func typed(parts []out) []protocol.ViewPart {
	raw, err := json.Marshal(parts)
	if err != nil {
		panic(fmt.Sprintf("viewlang: %v", err))
	}
	var typed []protocol.ViewPart
	if err := json.Unmarshal(raw, &typed); err != nil {
		panic(fmt.Sprintf("viewlang: a part does not fit the wire types; run make generate-types: %v", err))
	}
	return typed
}

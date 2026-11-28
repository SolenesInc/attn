package daemon_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
)

var (
	shownCommand = regexp.MustCompile("`(attn\\s[^`]+)`|(?m)^\\s*(attn [^\\n]+)$")
	shownCall    = regexp.MustCompile(`\b([a-z]+(?:_[a-z]+)*)\(`)
	typeName     = regexp.MustCompile(`^(?:[a-z]+(?:\|[a-z]+)*|true|\[(?:\.\.\.|"…"|\{\.\.\.\})\]|\{\.\.\.\})$`)
	shownFlag    = regexp.MustCompile("(?:CLI-only )?`--?[a-z][^`]*`(?: \\(CLI only\\))?")
)

type shownArg struct {
	name, value      string
	valued, optional bool
}

func shownArgs(text string) (args []shownArg, partial, closed bool) {
	var entry strings.Builder
	var entries []string
	quoted, depth := false, 0
	for i, r := range text {
		switch {
		case r == '"' && (i == 0 || text[i-1] != '\\'):
			quoted = !quoted
		case quoted:
		case strings.ContainsRune("([{<", r):
			depth++
		case strings.ContainsRune(")]}>", r):
			depth--
		}
		switch {
		case depth < 0:
			closed = true
		case depth == 1 && r == ',' && !quoted && strings.HasPrefix(text, "{"):
			entries = append(entries, entry.String())
			entry.Reset()
			continue
		}
		if closed {
			break
		}
		entry.WriteRune(r)
	}
	if entry.Len() > 0 {
		entries = append(entries, entry.String())
	}
	for i, written := range entries {
		written = strings.TrimSpace(written)
		if i == 0 {
			written = strings.TrimPrefix(written, "{")
		}
		if i == len(entries)-1 {
			written = strings.TrimSuffix(written, "}")
		}
		if written == "…" {
			partial = true
			continue
		}
		name, value, valued := strings.Cut(written, ": ")
		bare, optional := strings.CutSuffix(name, "?")
		args = append(args, shownArg{name: bare, value: value, valued: valued, optional: optional})
	}
	return args, partial, closed
}

func typeOf(written string) string {
	if len(written) > 1 && strings.HasPrefix(written, "'") && strings.HasSuffix(written, "'") {
		return "string"
	}
	var value any
	if json.Unmarshal([]byte(written), &value) != nil {
		switch {
		case strings.HasSuffix(written, ", a JSON object>"):
			return "object"
		case written == "…" || strings.HasPrefix(written, "<"):
			return "integer"
		}
		return "nothing JSON holds"
	}
	switch value.(type) {
	case map[string]any:
		return "object"
	case []any:
		return "array"
	case float64:
		return "integer"
	case bool:
		return "boolean"
	}
	return "string"
}

type toolArguments struct {
	Properties map[string]struct{ Type string }
	Required   []string
}

func callArguments(t *testing.T, agent agentToolsCaller, offered offeredTools) map[string]toolArguments {
	t.Helper()
	data, err := os.ReadFile("agent_tools_generated.json")
	if err != nil {
		t.Fatal(err)
	}
	var generated struct {
		Tools []struct {
			Name        string
			InputSchema toolArguments
		}
	}
	if err := json.Unmarshal(data, &generated); err != nil {
		t.Fatal(err)
	}
	arguments := map[string]toolArguments{}
	for _, tool := range generated.Tools {
		if offered[tool.Name] {
			arguments[tool.Name] = tool.InputSchema
		}
	}
	connected, err := agent.connect(t)
	if err != nil {
		t.Fatal(err)
	}
	served, err := connected.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range served.Tools {
		var schema toolArguments
		data, err := json.Marshal(tool.InputSchema)
		if err == nil {
			err = json.Unmarshal(data, &schema)
		}
		if err != nil {
			t.Fatal(err)
		}
		arguments[tool.Name] = schema
	}
	return arguments
}

func checkShownCalls(t *testing.T, schemas map[string]toolArguments, read, text string) int {
	t.Helper()
	checked := 0
	for _, at := range shownCall.FindAllStringSubmatchIndex(text, -1) {
		name := text[at[2]:at[3]]
		written, _, _ := strings.Cut(text[at[0]:], "\n")
		if _, offered := schemas[name]; !offered {
			if strings.Contains(name, "_") {
				t.Errorf("%s shows %s, which is no function of a script's world: %s", read, name, written)
			}
			continue
		}
		args, partial, closed := shownArgs(strings.TrimPrefix(written, name+"("))
		if !closed {
			t.Errorf("%s shows a call that does not close on its line: %s", read, written)
			continue
		}
		checked++
		tool := schemas[name]
		signature := slices.ContainsFunc(args, func(arg shownArg) bool {
			return arg.optional || !arg.valued || typeName.MatchString(arg.value) && strings.ContainsAny(arg.value, "[{")
		})
		shown := map[string]bool{}
		for _, arg := range args {
			schema, known := tool.Properties[arg.name]
			required := slices.Contains(tool.Required, arg.name)
			switch {
			case !known:
				t.Errorf("%s shows %s with the argument %s, which it does not take: %s", read, name, arg.name, written)
			case shown[arg.name]:
				t.Errorf("%s shows %s with the argument %s twice: %s", read, name, arg.name, written)
			case signature && (arg.optional == required || arg.valued && !typeName.MatchString(arg.value)):
				t.Errorf("%s shows %s in a signature its arguments do not say: %s", read, arg.name, written)
			case !signature:
				if given := typeOf(arg.value); given != schema.Type {
					t.Errorf("%s shows %s with %s as %s, want %s: %s", read, name, arg.name, given, schema.Type, written)
				}
			}
			shown[arg.name] = true
		}
		missing := slices.ContainsFunc(tool.Required, func(arg string) bool { return !shown[arg] })
		if missing != partial {
			t.Errorf("%s shows %s leaving out a required argument: %t, and marks the call partial: %t: %s", read, name, missing, partial, written)
		}
	}
	return checked
}

func offeredToolSpelledAsCommand(tools offeredTools, command string) string {
	var name string
	words := strings.Fields(command)[1:]
	for i, word := range words {
		if strings.ContainsAny(word, "<[-") && !regexp.MustCompile("^[a-z]+(-[a-z]+)*$").MatchString(word) {
			break
		}
		name += strings.ReplaceAll(word, "-", "_")
		if tools[name] && (i == len(words)-1 || !offeredDeeper(tools, name, words[i+1])) {
			return name
		}
		name += "_"
	}
	return ""
}

func offeredDeeper(tools offeredTools, name, word string) bool {
	return tools[name+"_"+strings.ReplaceAll(word, "-", "_")]
}

func TestAReferenceReadThroughAToolTeachesTheToolsAndReadFromTheCLIKeepsItsText(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app := w.App()
	setSetting(t, app, "agent_tools_enabled", "true")
	session := w.Spawn(app, fakeagent.Claude, w.Path("shop"))
	agent := agentToolsCaller{w: w, bearer: terminalCredential(t, w, app.Terminal(session))}
	tools := listOfferedTools(t, agent)
	arguments := callArguments(t, agent, tools)
	calls := 0

	read := func(tool, command string, args map[string]any, want string) string {
		t.Helper()
		result, err := agent.call(t, tool, args)
		if err != nil || result.IsError {
			t.Fatalf("%s %v: %v %q", tool, args, err, resultText(result))
		}
		text := resultText(result)
		if text == "" {
			t.Fatalf("%s %v answered nothing", tool, args)
		}
		for _, match := range shownCommand.FindAllStringSubmatch(text, -1) {
			if offered := offeredToolSpelledAsCommand(tools, match[1]+match[2]); offered != "" {
				t.Errorf("%s %v teaches %q, which the tool %s does", tool, args, match[1]+match[2], offered)
			}
		}
		for _, flag := range shownFlag.FindAllString(text, -1) {
			if !strings.HasSuffix(flag, " (CLI only)") && !strings.HasPrefix(flag, "CLI-only ") {
				t.Errorf("%s %v teaches the flag %s, which no tool takes", tool, args, flag)
			}
		}

		calls += checkShownCalls(t, arguments, fmt.Sprint(tool, " ", args), text)

		var cli string
		if err := w.Client().RunCommand(command, protocol.SessionID(session), args, &cli); err != nil {
			t.Fatal(err)
		}
		if strings.TrimSuffix(cli, "\n") != strings.TrimSuffix(want, "\n") {
			t.Errorf("%s %v from the CLI differs from its source", command, args)
		}
		return text
	}
	source := func(name string) string {
		t.Helper()
		text, err := os.ReadFile(filepath.Join("..", "prompts", "content", filepath.FromSlash(name)))
		if err != nil {
			t.Fatal(err)
		}
		return string(text)
	}

	listed, err := agent.call(t, "skill", map[string]any{"list": true})
	if err != nil {
		t.Fatal(err)
	}
	references := strings.Fields(resultText(listed))
	if !slices.Contains(references, "garden") {
		t.Fatalf("skill list answered %q, want the garden reference among them", references)
	}
	for _, name := range references {
		read("skill", "skill", map[string]any{"reference": name}, source("skills/attn/references/"+name+".md"))
	}
	guide := read("seed_guide", "seed guide", map[string]any{}, source("resources/seed-guide.md"))

	example := regexp.MustCompile("(?s)seed_plot\\(\\{plot: <[^\n]*, a JSON object>\\}\\)\n.*?```json\n(.*?)```").FindStringSubmatch(guide)
	if example == nil {
		t.Fatalf("the seed guide shows no seed_plot call with a payload:\n%s", guide)
	}
	if planted, failed := agent.script(t, "attn_do", "seed_plot({plot: "+example[1]+"})"); failed {
		t.Errorf("seed_plot called as the seed guide shows it: %s", planted)
	}

	if calls == 0 {
		t.Error("no reference shows a call of a tool")
	}

	lines, err := agent.call(t, "skill", map[string]any{"reference": "garden"})
	if err != nil {
		t.Fatal(err)
	}
	if want := "seed_attach({seed, path?, notebook?, url?, mode?: move|copy"; !strings.Contains(resultText(lines), want) {
		t.Errorf("the garden reference does not show the arguments of seed_attach as %q", want)
	}
}

func TestALaunchWithToolsWritesEachCommandAsACallAScriptCanMake(t *testing.T) {
	w := newCrewWorld(t, fakeagent.Claude)
	app := w.App()
	for _, key := range []string{"agent_tools_enabled", "asks_enabled"} {
		setSetting(t, app, key, "true")
	}
	plain := w.Spawn(app, fakeagent.Claude, w.Path("shop"))
	agent := agentToolsCaller{w: w, bearer: terminalCredential(t, w, app.Terminal(plain))}
	tools := listOfferedTools(t, agent)
	arguments := callArguments(t, agent, tools)
	delegated := agent.mustCall(t, "delegate", map[string]any{"brief": "Fix the parser.", "cwd": w.Path("shop"), "model": "default"})
	var started protocol.DelegationOperation
	structuredAs(t, delegated, &started)
	calls := 0
	for name, session := range map[string]string{
		"an agent":            plain,
		"a delegated agent":   string(started.SessionID),
		"a crew member's day": string(startCrew(t, w.Client(), "keel", "").SessionID),
	} {
		text := launchGuidance(t, w, w.Launched(session))
		for _, match := range shownCommand.FindAllStringSubmatch(text, -1) {
			if offered := offeredToolSpelledAsCommand(tools, match[1]+match[2]); offered != "" && name != "a crew member's day" {
				t.Errorf("%s's launch teaches %q, which a script does with %s", name, match[1]+match[2], offered)
			}
		}
		calls += checkShownCalls(t, arguments, name+"'s launch", text)
	}
	if calls == 0 {
		t.Error("no launch shows a call")
	}
}

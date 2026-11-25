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
	shownCall    = regexp.MustCompile("(?m)`?([a-z_]+)`? (?:with )?(\\{.*)")
	shownFlag    = regexp.MustCompile("`--?[a-z][^`]*`(?: \\(CLI only\\))?")
)

type toolArguments struct {
	Properties map[string]any
	Required   []string
}

func offeredArguments(t *testing.T, agent agentToolsCaller) map[string]toolArguments {
	t.Helper()
	session, err := agent.connect(t, "")
	if err != nil {
		t.Fatal(err)
	}
	listed, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	offered := map[string]toolArguments{}
	for _, tool := range listed.Tools {
		data, err := json.Marshal(tool.InputSchema)
		if err != nil {
			t.Fatal(err)
		}
		var args toolArguments
		if err := json.Unmarshal(data, &args); err != nil {
			t.Fatal(err)
		}
		offered[tool.Name] = args
	}
	return offered
}

var signatureWord = regexp.MustCompile(`^[a-z_]+\??(: [^,]*)?$`)

func shownSignature(text string) map[string]bool {
	inner, _, found := strings.Cut(strings.TrimPrefix(text, "{"), "}`")
	if !found {
		return nil
	}
	args := map[string]bool{}
	for _, word := range strings.Split(inner, ", ") {
		if !signatureWord.MatchString(word) {
			return nil
		}
		name, _, _ := strings.Cut(word, ":")
		args[strings.TrimSuffix(name, "?")] = !strings.HasSuffix(name, "?")
	}
	return args
}

func checkShownCalls(t *testing.T, offered map[string]toolArguments, read, text string) int {
	t.Helper()
	checked := 0
	for _, match := range shownCall.FindAllStringSubmatch(text, -1) {
		tool, known := offered[match[1]]
		if !known {
			continue
		}
		var call map[string]any
		if strings.HasPrefix(match[2], "{\"") {
			if err := json.NewDecoder(strings.NewReader(match[2])).Decode(&call); err != nil {
				t.Errorf("%s shows %s with arguments that are not JSON: %s", read, match[1], match[2])
				continue
			}
			checked++
			if name := repeatedArgument(match[2]); name != "" {
				t.Errorf("%s shows %s with the argument %s twice: %s", read, match[1], name, match[2])
			}
			for name := range call {
				if _, takes := tool.Properties[name]; !takes {
					t.Errorf("%s shows %s with the argument %s, which the tool does not take", read, match[1], name)
				}
			}
			continue
		}
		signature := shownSignature(match[2])
		if signature == nil {
			continue
		}
		checked++
		for name, required := range signature {
			if _, takes := tool.Properties[name]; !takes || required != slices.Contains(tool.Required, name) {
				t.Errorf("%s shows %s {%s}, required %t, which its schema does not say", read, match[1], name, required)
			}
		}
		for _, name := range tool.Required {
			if _, shown := signature[name]; !shown {
				t.Errorf("%s shows %s without its required argument %s", read, match[1], name)
			}
		}
	}
	return checked
}

func repeatedArgument(call string) string {
	decoder := json.NewDecoder(strings.NewReader(call))
	names := map[string]bool{}
	if _, err := decoder.Token(); err != nil {
		return ""
	}
	for decoder.More() {
		name, err := decoder.Token()
		if err != nil {
			return ""
		}
		if names[name.(string)] {
			return name.(string)
		}
		names[name.(string)] = true
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return ""
		}
	}
	return ""
}

func offeredToolSpelledAsCommand(tools offeredTools, command string) string {
	var name string
	words := strings.Fields(command)[1:]
	for i, word := range words {
		if strings.ContainsAny(word, "<[-") && !regexp.MustCompile("^[a-z]+(-[a-z]+)*$").MatchString(word) {
			break
		}
		name += strings.ReplaceAll(word, "-", "_")
		if _, offered := tools[name]; offered && (i == len(words)-1 || !offeredDeeper(tools, name, words[i+1])) {
			return name
		}
		name += "_"
	}
	return ""
}

func offeredDeeper(tools offeredTools, name, word string) bool {
	_, deeper := tools[name+"_"+strings.ReplaceAll(word, "-", "_")]
	return deeper
}

func TestAReferenceReadThroughAToolTeachesTheToolsAndReadFromTheCLIKeepsItsText(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	laterReference, err := os.ReadFile(filepath.Join("testdata", "later-reference.md"))
	if err != nil {
		t.Fatal(err)
	}
	w.daemon.ServeReference("later", string(laterReference))
	app := w.App()
	setSetting(t, app, "agent_tools_enabled", "true")
	session := w.Spawn(app, fakeagent.Claude, w.Path("shop"))
	agent := agentToolsCaller{w: w, bearer: terminalCredential(t, w, app.Terminal(session))}
	tools := listOfferedTools(t, agent)
	arguments := offeredArguments(t, agent)
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
			if !strings.HasSuffix(flag, " (CLI only)") {
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
	read("seed_guide", "seed guide", map[string]any{}, source("resources/seed-guide.md"))

	if calls == 0 {
		t.Error("no reference shows a call of a tool")
	}

	lines, err := agent.call(t, "skill", map[string]any{"reference": "garden"})
	if err != nil {
		t.Fatal(err)
	}
	if want := "seed_attach {seed, path?, notebook?, url?, mode?: move|copy"; !strings.Contains(resultText(lines), want) {
		t.Errorf("the garden reference does not show the arguments of seed_attach as %q", want)
	}

	later := read("skill", "skill", map[string]any{"reference": "later"}, string(laterReference))
	for _, line := range []string{
		"git push --force origin main\n",
		"gh pr view --json state\n",
		"    git log --grep seed\n",
		"prints the seed; `section` prints one part\n",
		"`terminal_run` with {\"terminal\": \"<terminal>\", \"command\": \"make test\"} without `--wait --timeout 600` (CLI only)",
		"terminal_run {\"terminal\": \"<terminal>\", \"command\": \"make lint\"} without --wait (CLI only)\n",
		"\"evidence\": [\"a.txt\",\"b.txt\"]",
		"\"evidence\": [\"c.txt\",\"d.txt\"]",
	} {
		if !strings.Contains(later, line) {
			t.Errorf("a later reference read through a tool does not show %q:\n%s", line, later)
		}
	}
}

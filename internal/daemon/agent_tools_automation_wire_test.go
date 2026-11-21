package daemon_test

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/pausepoint"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestEveryAutomationToolAnswersWithTheShapeItsOutputSchemaDescribes(t *testing.T) {
	w := newWorld(t, fakeagent.Claude, fakeagent.Codex)
	if err := os.MkdirAll(w.Path("check"), 0o755); err != nil {
		t.Fatal(err)
	}
	app := w.App()
	setSetting(t, app, "agent_tools_enabled", "true")
	session := w.Spawn(app, fakeagent.Claude, w.Path("check"))
	agent := agentToolsCaller{w: w, bearer: terminalCredential(t, w, app.Terminal(session))}
	tools := &schemaCheckedTools{t: t, agent: agent, tools: listOfferedTools(t, agent), called: map[string]bool{}}

	definition := manualAutomation(w, "Check the shop.")
	if valid := tools.call("automation_validate", map[string]any{"definition": definition}); valid["valid"] != true {
		t.Errorf("automation_validate answered %v, want valid", valid)
	}
	applied := tools.call("automation_apply", map[string]any{"definition": definition, "launch_desktop": "own", "desktop_name": "Checks"})
	id, _ := applied["id"].(float64)
	if id < 1 || applied["name"] != "Manual check" {
		t.Fatalf("automation_apply answered %v, want the saved automation", applied)
	}
	tools.call("automation_list", map[string]any{})
	if shown := tools.call("automation_show", map[string]any{"id": id}); !strings.Contains(fmt.Sprint(shown["spec_yaml"]), "Check the shop.") {
		t.Errorf("automation_show answered %v, want the definition's YAML", shown)
	}
	tools.call("automation_set", map[string]any{"id": id, "launch_desktop": "own"})
	if disabled := tools.call("automation_disable", map[string]any{"id": id}); disabled["enabled"] != false {
		t.Errorf("automation_disable answered %v, want it off", disabled)
	}
	if enabled := tools.call("automation_enable", map[string]any{"id": id}); enabled["enabled"] != true {
		t.Errorf("automation_enable answered %v, want it on", enabled)
	}
	first := tools.call("automation_run", map[string]any{"id": id, "input": map[string]any{}, "request_id": "tool-run"})
	retried := tools.call("automation_run", map[string]any{"id": id, "input": map[string]any{}, "request_id": "tool-run"})
	if first["id"] == nil || retried["id"] != first["id"] {
		t.Errorf("a retried automation_run answered run %v, want the first call's run %v", retried["id"], first["id"])
	}
	if runs := tools.call("automation_runs", map[string]any{"id": id}); len(runs["runs"].([]any)) != 1 {
		t.Errorf("automation_runs answered %v, want the one run", runs)
	}
	tools.call("automation_cleanup", map[string]any{"id": id})
	if deleted := tools.call("automation_delete", map[string]any{"id": id}); deleted["deleted"] != id {
		t.Errorf("automation_delete answered %v, want %v", deleted, id)
	}
	managed := agent.mustCall(t, "automation_apply", map[string]any{"definition": definition + "enabled: true\n"})
	if text := resultText(managed); !managed.IsError || !strings.Contains(text, "`automation_enable`") || strings.Contains(text, "attn automation") {
		t.Errorf("automation_apply with enabled in the spec answered %q, want a refusal that names the automation_enable tool", text)
	}
	tools.report("automation")
}

func TestAnAutomationCallerReadsAndChangesNothingOfAnotherProfile(t *testing.T) {
	w := newWorld(t, fakeagent.Claude, fakeagent.Codex)
	if err := os.MkdirAll(w.Path("check"), 0o755); err != nil {
		t.Fatal(err)
	}
	app := w.App()
	setSetting(t, app, "agent_tools_enabled", "true")
	home := w.Client().WithGardenProfile("Default", "")
	theirs := applyAutomation(t, home, manualAutomation(w, "Check Default's shop."))
	sideApp := w.AppOn(createProfile(app, "Side").ID)
	side := w.Spawn(sideApp, fakeagent.Claude, w.Path("side"))
	agent := agentToolsCaller{w: w, bearer: terminalCredential(t, w, sideApp.Terminal(side))}

	surfaces := map[string]func(command string, args map[string]any) (map[string]any, error){
		"/mcp": func(command string, args map[string]any) (map[string]any, error) {
			result := agent.mustCall(t, strings.ReplaceAll(command, " ", "_"), args)
			if result.IsError {
				return nil, fmt.Errorf("%s", resultText(result))
			}
			answer, _ := result.StructuredContent.(map[string]any)
			return answer, nil
		},
		"run_command": func(command string, args map[string]any) (map[string]any, error) {
			var answer map[string]any
			err := w.Client().RunCommand(command, protocol.SessionID(side), args, &answer)
			return answer, err
		},
	}
	for surface, call := range surfaces {
		mine, err := call("automation apply", map[string]any{"definition": manualAutomation(w, "Check Side's shop from "+surface+".")})
		if err != nil {
			t.Fatalf("%s automation apply from Side: %v", surface, err)
		}
		listed, err := call("automation list", map[string]any{})
		if err != nil {
			t.Fatalf("%s automation list from Side: %v", surface, err)
		}
		ids := map[float64]bool{}
		for _, definition := range listed["definitions"].([]any) {
			ids[definition.(map[string]any)["id"].(float64)] = true
		}
		if ids[float64(theirs.ID)] || !ids[mine["id"].(float64)] {
			t.Errorf("%s automation list from Side holds %v, want its own %v and not Default's %d", surface, ids, mine["id"], theirs.ID)
		}
		unknown := theirs.ID + 1000
		for _, attempt := range []struct {
			command string
			args    func(id int) map[string]any
		}{
			{"automation show", func(id int) map[string]any { return map[string]any{"id": id} }},
			{"automation set", func(id int) map[string]any { return map[string]any{"id": id, "launch_desktop": "own"} }},
			{"automation disable", func(id int) map[string]any { return map[string]any{"id": id} }},
			{"automation enable", func(id int) map[string]any { return map[string]any{"id": id} }},
			{"automation run", func(id int) map[string]any {
				return map[string]any{"id": id, "request_id": fmt.Sprintf("side-%s-%d", surface, id)}
			}},
			{"automation runs", func(id int) map[string]any { return map[string]any{"id": id} }},
			{"automation cleanup", func(id int) map[string]any { return map[string]any{"id": id} }},
			{"automation delete", func(id int) map[string]any { return map[string]any{"id": id} }},
		} {
			foreign, foreignErr := call(attempt.command, attempt.args(theirs.ID))
			absent, absentErr := call(attempt.command, attempt.args(unknown))
			same := func(a, b any) bool {
				return strings.ReplaceAll(fmt.Sprint(b), fmt.Sprint(unknown), fmt.Sprint(theirs.ID)) == fmt.Sprint(a)
			}
			if !same(foreign, absent) || !same(foreignErr, absentErr) {
				t.Errorf("%s %s from Side answers Default's %d with %v, %v; an id that does not exist with %v, %v; want the same answer", surface, attempt.command, theirs.ID, foreign, foreignErr, absent, absentErr)
			}
		}
	}
	after, err := home.AutomationDefinition(theirs.ID)
	if err != nil || !after.Definition.Enabled || after.Definition.Revision != theirs.Revision {
		t.Fatalf("Side's calls changed Default's automation: %+v, %v, want it enabled at revision %d", after, err, theirs.Revision)
	}
	if runs, err := home.AutomationRuns(theirs.ID); err != nil || len(runs.Runs) != 0 {
		t.Errorf("Side's run reached Default's automation: %+v, %v", runs, err)
	}
}

func TestTheAutomationRunToolAnswersOnceTheRunIsRecordedAndTheRunIsDeliveredAfter(t *testing.T) {
	r := newAutomationReviewWorld(t)
	setSetting(t, r.app, "agent_tools_enabled", "true")
	applyAutomation(t, r.cli, automationReviewSpec("manual-review", "manual", automationReviewOverride(r.clone)))
	agent := agentToolsCaller{w: r.w, bearer: terminalCredential(t, r.w, r.app.Terminal(r.w.Spawn(r.app, fakeagent.Claude, r.clone)))}
	input := automationReviewInput(42, r.head)
	started := r.w.PauseAt(pausepoint.AutomationAgentStarted)

	answered := agent.mustCall(t, "automation_run", map[string]any{"id": 2, "input": json.RawMessage(input), "request_id": "from-a-tool"})
	if answered.IsError {
		t.Fatalf("automation_run answered %q, want the recorded run", resultText(answered))
	}
	if run, _ := answered.StructuredContent.(map[string]any); run["state"] != "pending" {
		t.Errorf("automation_run answered %v, want the run pending while its delivery goes on", run)
	}
	started.Await()
	started.Release()

	run, err := r.cli.AutomationRun(2, "from-a-tool", input)
	if err != nil {
		t.Fatal(err)
	}
	reviewer := testworld.AwaitSession(r.app, string(protocol.Deref(run.SessionID)), func(s protocol.Session) bool { return s.Directory != "" })
	if got := strings.TrimSpace(runGit(t, reviewer.Directory, "rev-parse", "HEAD")); got != r.head {
		t.Errorf("the run the tool started checked out %s, want %s", got, r.head)
	}
	r.w.Launched(string(reviewer.ID))
}

func TestEveryAutomationCommandRefusesAnIDBelowOne(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	if err := os.MkdirAll(w.Path("check"), 0o755); err != nil {
		t.Fatal(err)
	}
	app := w.App()
	session := w.Spawn(app, fakeagent.Claude, w.Path("check"))
	for _, attempt := range []struct {
		command string
		args    map[string]any
	}{
		{"automation show", map[string]any{"id": 0}},
		{"automation set", map[string]any{"id": 0, "launch_desktop": "own"}},
		{"automation enable", map[string]any{"id": 0}},
		{"automation disable", map[string]any{"id": -1}},
		{"automation run", map[string]any{"id": 0, "request_id": "zero"}},
		{"automation runs", map[string]any{"id": 0}},
		{"automation cleanup", map[string]any{"id": 0}},
		{"automation delete", map[string]any{"id": 0}},
		{"automation show", map[string]any{}},
	} {
		var answer map[string]any
		err := w.Client().RunCommand(attempt.command, protocol.SessionID(session), attempt.args, &answer)
		if err == nil || !strings.Contains(err.Error(), "must be a positive number") {
			t.Errorf("%s %v answered %v, %v; want a refusal that the id must be a positive number", attempt.command, attempt.args, answer, err)
		}
	}
}

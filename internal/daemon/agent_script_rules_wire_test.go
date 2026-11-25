package daemon_test

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/victorarias/attn/internal/catalog"
	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/harness"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func (s sessionTools) notes(seed string) []string {
	s.shapes.t.Helper()
	read, err := s.w.Client().SeedNotes("", seed, client.SeedNotesQuery{Kind: "note"})
	if err != nil {
		s.shapes.t.Fatal(err)
	}
	var bodies []string
	for _, note := range read.Notes {
		bodies = append(bodies, note.Body)
	}
	slices.Reverse(bodies)
	return bodies
}

func TestAttnReadRefusesEveryCommandThatChangesSomething(t *testing.T) {
	s := newSessionTools(t)
	var changing []string
	for _, cmd := range builtinCatalog(t).Commands {
		if cmd.IsTool() && cmd.Effect != catalog.EffectRead {
			changing = append(changing, cmd.ToolName())
		}
	}
	if !slices.Contains(changing, "browser_cookies") || !slices.Contains(changing, "seed_note") {
		t.Fatalf("the commands that change something are %v, want seed_note and browser_cookies among them", changing)
	}
	answer, failed := s.agent.script(t, "attn_read", fmt.Sprintf(`const ran = [];
for (const name of %q.split(" ")) {
	try { globalThis[name]({}); ran.push(name) } catch (e) { if (e.code !== "changes_something") ran.push(name + " refused " + e.code) }
}
return ran`, strings.Join(changing, " ")))
	if failed || answer != "[]" {
		t.Errorf("attn_read ran, or refused for another reason, %s", answer)
	}
	seed := plantForTools(t, s.w)
	text, failed := s.agent.script(t, "attn_read", "seed_show({seed: '"+seed+"'});\nseed_note({seed: '"+seed+"', text: 'Read only.'});")
	if !failed || !strings.Contains(text, "line 2") || !strings.Contains(text, "seed_note") || !strings.Contains(text, "attn_do") || !strings.Contains(text, "Nothing changed.") {
		t.Errorf("a note through attn_read answered %q, want the line, seed_note and attn_do named, and that nothing changed", text)
	}
	if notes := s.notes(seed); len(notes) != 0 {
		t.Errorf("attn_read wrote %v", notes)
	}
}

func TestARefusalNothingCatchesEndsTheScriptAtItsLineAndTheChangesBeforeItStand(t *testing.T) {
	s := newSessionTools(t)
	seed := plantForTools(t, s.w)
	text, failed := s.agent.script(t, "attn_do", "seed_note({seed: '"+seed+"', text: 'First.'});\nseed_note({seed: 's-zzzzzz', text: 'Lost.'});\nseed_note({seed: '"+seed+"', text: 'Never.'});")
	if notes := s.notes(seed); !slices.Equal(notes, []string{"First."}) {
		t.Errorf("the script left the notes %v, want only the one before the refusal", notes)
	}
	for _, want := range []string{"not_found: ", "seed_note", "Nothing after line 2 ran.", "The changes below stand", "changed:\nseed_note {seed: \"" + seed + "\", text: \"First.\"} -> {"} {
		if !strings.Contains(text, want) {
			t.Errorf("the answer lacks %q:\n%s", want, text)
		}
	}
	if !failed || strings.Contains(text, "Never.") || strings.Contains(strings.SplitAfter(text, "changed:")[1], "Lost.") {
		t.Errorf("want a failed call whose ledger holds only the first note:\n%s", text)
	}

	text, failed = s.agent.script(t, "attn_do", "try { seed_note({seed: 's-zzzzzz', text: 'Lost.'}) } catch (e) { console.log('skipped ' + e.command + ' ' + e.code) }\nseed_note({seed: '"+seed+"', text: 'Second.'});\nreturn 'both tried'")
	if failed || !strings.HasPrefix(text, "skipped seed_note not_found\nboth tried\n\nchanged:\nseed_note {") || !slices.Equal(s.notes(seed), []string{"First.", "Second."}) {
		t.Errorf("a script that catches the refusal answered %q with the notes %v, want it to go on", text, s.notes(seed))
	}

	text, failed = s.agent.script(t, "attn_do", "seed_note({seed: '"+seed+"', text: 'Third.'});\nseed_nots({seed: '"+seed+"'});")
	if !failed || !strings.Contains(text, "seed_notes") || !strings.Contains(text, "Nothing after line 2 ran.") || len(s.notes(seed)) != 3 {
		t.Errorf("a name attn does not offer answered %q, want its line, the nearest name, and the note before it", text)
	}
}

func TestAnArgumentACommandDoesNotTakeIsAnsweredWithTheOnesItTakes(t *testing.T) {
	s := newSessionTools(t)
	text, failed := s.agent.script(t, "attn_do", "seed_note({id: 's-1', text: 'x'})")
	for _, want := range []string{"usage: ", "\"id\"", "seed_note({seed, text, handoff?: true, ring?: true})", "\n- seed: string. ", "\n- text: string. "} {
		if !strings.Contains(text, want) {
			t.Errorf("the answer lacks %q:\n%s", want, text)
		}
	}
	if !failed {
		t.Error("the call did not fail")
	}
	for command, want := range map[string]string{"seed_note": "seed_note({seed, text, handoff?: true, ring?: true})", "seed note": "seed_note({", "seed_check": "seed_check_report({"} {
		if text := resultText(s.agent.mustCall(t, "skill", map[string]any{"command": command})); !strings.Contains(text, want) {
			t.Errorf("skill({command: %q}) answered %q, want it to hold %q", command, text, want)
		}
	}
	if refused := s.agent.mustCall(t, "skill", map[string]any{"command": "seed_nots"}); !refused.IsError || !strings.HasPrefix(resultText(refused), "no_such_command: ") {
		t.Errorf("skill of an unknown command answered %q, want no_such_command", resultText(refused))
	}
}

func TestAScriptPastACallsCommandsIsRefusedTheNextOneWithTheLimitNamed(t *testing.T) {
	s := newSessionTools(t)
	uncaught, failed := s.agent.script(t, "attn_read", "for (let n = 0; ; n++) {\n\tpresence();\n}")
	if !failed || !strings.Contains(uncaught, "command_limit: one call runs at most 1000 commands") || !strings.Contains(uncaught, "Nothing after line 2 ran.") {
		t.Errorf("a script that never stops calling answered %q, want it ended at 1000 commands", uncaught)
	}
	caught, failed := s.agent.script(t, "attn_read", "let ran = 0;\ntry { for (;;) { presence(); ran++ } } catch {}\nreturn 'ran ' + ran")
	if !failed || !strings.HasPrefix(caught, "ran 1000\n\n") || !strings.Contains(caught, "one call runs at most 1000 commands") {
		t.Errorf("a script that catches the limit answered %q, want what it returned and the limit named", caught)
	}
}

func TestACallPastItsTimeEndsWithTheLimitAndTheChangesBeforeItNamed(t *testing.T) {
	t.Setenv("ATTN_BROWSER_HOST_TOKEN", browserHostToken)
	s := newSessionTools(t)
	host := browserHostPeer(s.w, "", browserHostToken)
	s.agent.mustCall(t, "browser_open", map[string]any{"url": "http://localhost:3000"})
	for _, tc := range []struct {
		name, then, want string
		host, kill       bool
	}{
		{"a command that does not end", "browser_back(); AFTER", "one call runs at most 50 s", false, true},
		{"a script that calls no command", "browser_back(); for (;;); AFTER", "one call runs at most 50 s", true, true},
		{"a script that calls on", "browser_back(); AFTER", "call_limit: one call runs at most 50 s, and they have passed", true, false},
		{"a script that catches the limit", "browser_back(); try { AFTER } catch {}", "one call runs at most 50 s. The script went on", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			seed := plantForTools(t, s.w)
			type reply struct {
				result *mcp.CallToolResult
				err    error
			}
			answered := make(chan reply, 1)
			go func() {
				result, err := s.agent.tool(t, "attn_do", map[string]any{"script": "seed_note({seed: '" + seed + "', text: 'Before.'});\n" + strings.Replace(tc.then, "AFTER", "seed_note({seed: '"+seed+"', text: 'After.'});", 1)}, nil)
				answered <- reply{result, err}
			}()
			request := testworld.Await[protocol.BrowserControlRequestMessage](host, protocol.EventBrowserControlRequest, nil)
			s.w.clock.Fire("agent-script-call")
			if tc.host {
				host.Send(protocol.BrowserControlResultMessage{Cmd: protocol.CmdBrowserControlResult, RequestID: request.RequestID, Success: true, Data: protocol.Ptr("{}")})
			}
			if tc.kill {
				s.w.clock.Fire("agent-script-kill")
			}
			got := <-answered
			if got.err != nil {
				t.Fatal(got.err)
			}
			text := resultText(got.result)
			for _, want := range []string{tc.want, "50 s", "The changes below stand", "seed_note {seed: \"" + seed + "\", text: \"Before.\"} -> {"} {
				if !strings.Contains(text, want) {
					t.Errorf("the answer lacks %q:\n%s", want, text)
				}
			}
			if !tc.host && !strings.Contains(text, "browser_back {} -> call_limit: it may have taken effect") {
				t.Errorf("the ledger does not hold the command that had not ended:\n%s", text)
			}
			if notes := s.notes(seed); !slices.Equal(notes, []string{"Before."}) {
				t.Errorf("the call left the notes %v, want only the one before its end", notes)
			}
		})
	}
}

func TestAScriptsAnswerIsWhatItReturnedOrLoggedAndThenItsChanges(t *testing.T) {
	s := newSessionTools(t)
	seed := plantForTools(t, s.w)
	for _, tc := range []struct{ name, tool, script, want string }{
		{"what it returned", "attn_read", "return seed_show({seed: '" + seed + "'}).seed.title", "Fix the flaky checkout test"},
		{"what it logged, then returned", "attn_read", "console.log('one', 2); console.log({three: 3}); return [4]", "one 2\n{\"three\":3}\n[4]"},
		{"nothing", "attn_read", "const unused = seed_ready", "The script ran and returned nothing."},
	} {
		if text, failed := s.agent.script(t, tc.tool, tc.script); failed || text != tc.want {
			t.Errorf("%s: %q answered %q, want %q", tc.name, tc.script, text, tc.want)
		}
	}
	if text, failed := s.agent.script(t, "attn_read", "seed_show({seed: '"+seed+"'})"); failed || !strings.HasPrefix(text, "{") || !strings.Contains(text, "Retry the payment call.") {
		t.Errorf("a script of one read with no return answered %q, want what the read returned", text)
	}

	long := strings.Repeat("x", 300)
	text, failed := s.agent.script(t, "attn_do", "const planted = seed_plant({title: 'Ship the retry', body: '"+long+"'});\nseed_note({seed: planted.id, text: 'Chained.'});\nreturn planted.body.length")
	if failed || !strings.HasPrefix(text, "300\n\nchanged:\nseed_plant {title: \"Ship the retry\", body: \"xxx") || strings.Contains(text, long) || !strings.Contains(text, "…") || !strings.Contains(text, "\nseed_note {") {
		t.Errorf("a script that chains two changes answered %q, want what it returned and one ledger line for each, with its long text cut", text)
	}

	text, _ = s.agent.script(t, "attn_read", "return 'y'.repeat(150000)")
	if length := len([]rune(text)); length > 100_000 || !strings.Contains(text, "of 150000 characters") {
		t.Errorf("a script that returns 150000 characters answered %d of them, ending %q; want at most 100000 and what was left out", length, text[len(text)-200:])
	}
}

func TestATerminalThatShowsNoSessionCanListTheToolsAndCallNone(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	app := w.App()
	setSetting(t, app, "agent_tools_enabled", "true")
	nobodys := harness.ServerCaller{Variable: "CODEX_THREAD_ID", Scope: app.SelectedProfile(), Conversation: "conversation-nobody-holds"}
	agent := agentToolsCaller{w: w, bearer: terminalCredential(t, w, string(nobodys.Terminal()))}
	if session, err := agent.connect(t); err != nil {
		t.Fatal(err)
	} else if listed, err := session.ListTools(context.Background(), nil); err != nil || len(listed.Tools) != 4 {
		t.Fatalf("a terminal that shows no session lists %+v, %v; want the four tools", listed, err)
	}
	for tool, args := range map[string]map[string]any{
		"attn_read": {"script": "return presence()"},
		"attn_do":   {"script": "return presence()"},
		"ask":       {"markdown": "Ship it?"},
		"skill":     {},
	} {
		refused, err := agent.tool(t, tool, args, nil)
		if err != nil {
			t.Fatalf("%s: %v", tool, err)
		}
		if text := resultText(refused); !refused.IsError || !strings.HasPrefix(text, "no_session: ") {
			t.Errorf("%s from a terminal that shows no session answered %q, want the no_session refusal", tool, text)
		}
	}
}

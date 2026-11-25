package daemon_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
)

func TestEveryJournalCommandReturnsToAScriptWhatItsCLIPrints(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	if err := os.MkdirAll(w.Path("notebook"), 0o700); err != nil {
		t.Fatal(err)
	}
	app := w.App()
	setSetting(t, app, "agent_tools_enabled", "true")
	session := w.Spawn(app, fakeagent.Claude, w.Path("shop"))
	agent := agentToolsCaller{w: w, bearer: terminalCredential(t, w, app.Terminal(session))}
	tools := newToolShapes(t, agent, listOfferedTools(t, agent))

	appended := tools.object("journal_append", map[string]any{"text": "Shipped the parser fix.", "date": "2026-07-05"})
	if appended["rel_path"] != "journal/2026-07-05.md" {
		t.Errorf("journal_append answered %v, want journal/2026-07-05.md", appended)
	}
	tools.finish("journal_")
}

func TestEveryCrewCommandReturnsToAScriptWhatItsCLIPrints(t *testing.T) {
	w := newCrewWorld(t, fakeagent.Claude)
	app := w.App()
	setSetting(t, app, "agent_tools_enabled", "true")
	session := w.Spawn(app, fakeagent.Claude, w.Path("shop"))
	agent := agentToolsCaller{w: w, bearer: terminalCredential(t, w, app.Terminal(session))}
	tools := newToolShapes(t, agent, listOfferedTools(t, agent))

	moved := tools.object("crew_move_home", map[string]any{"member": "keel"})
	if member, _ := moved["member"].(map[string]any); member["name"] != "Keel" {
		t.Errorf("crew_move_home answered %v, want keel's new home", moved)
	}
	created := tools.object("crew_create", map[string]any{"name": "scout", "agent": "claude"})
	if created["name"] != "scout" {
		t.Errorf("crew_create answered %v, want the member scout", created)
	}
	tools.object("crew_list", map[string]any{"all": true})
	set := tools.object("crew_set", map[string]any{"member": "scout", "model": "opus", "effort": "", "awareness_dirs": []any{w.Path("shop")}})
	if set["name"] != "scout" {
		t.Errorf("crew_set answered %v, want the member scout", set)
	}
	tools.object("crew_rename", map[string]any{"member": "scout", "name": "ranger"})
	tools.object("crew_apply", map[string]any{"member": "ranger"})
	started := tools.object("crew_start", map[string]any{"member": "ranger"})
	if started["name"] != "ranger" || started["session_id"] == "" {
		t.Errorf("crew_start answered %v, want ranger in a session", started)
	}
	tools.object("crew_close", map[string]any{"member": "ranger"})
	tools.object("crew_prime", map[string]any{})
	tools.object("crew_retire", map[string]any{"member": "alder"})
	retired := agent.mustCall(t, "crew_start", map[string]any{"member": "alder"})
	if text := resultText(retired); !retired.IsError || !strings.Contains(text, "`crew_restore({member: ") || strings.Contains(text, "attn crew") {
		t.Errorf("crew_start of a retired member answered %q, want a refusal that names the crew_restore tool", text)
	}
	tools.object("crew_restore", map[string]any{"member": "alder"})
	tools.finish("crew_")
}

func TestAJournalOrCrewCallerReadsAndChangesNothingOfAnotherProfile(t *testing.T) {
	w := newCrewWorld(t, fakeagent.Claude)
	app := w.App()
	setSetting(t, app, "agent_tools_enabled", "true")
	defaultNotebook := fsNotebookRoot(t, w)
	sideApp := w.AppOn(createProfile(app, "Side").ID)
	sideNotebook := w.Path("side-notebook")
	if err := os.MkdirAll(sideNotebook, 0o755); err != nil {
		t.Fatal(err)
	}
	if saved := notebookSetting(sideApp, "notebook.root", sideNotebook); !protocol.Deref(saved.Success) {
		t.Fatalf("setting Side's Notebook folder: %s", protocol.Deref(saved.Error))
	}
	side := w.Spawn(sideApp, fakeagent.Claude, w.Path("side"))
	agent := agentToolsCaller{w: w, bearer: terminalCredential(t, w, sideApp.Terminal(side))}
	keelBefore := crewRosterMember(t, w.Client().WithGardenProfile("Default", ""), "keel")

	surfaces := map[string]func(command string, args map[string]any) (map[string]any, error){
		"/mcp": func(command string, args map[string]any) (map[string]any, error) {
			result := agent.mustCall(t, strings.NewReplacer(" ", "_", "-", "_").Replace(command), args)
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
		day := map[string]string{"/mcp": "2026-07-06", "run_command": "2026-07-07"}[surface]
		if _, err := call("journal append", map[string]any{"text": "Side's day.", "date": day}); err != nil {
			t.Fatalf("%s journal append from Side: %v", surface, err)
		}
		if _, err := os.Stat(filepath.Join(sideNotebook, "journal", day+".md")); err != nil {
			t.Errorf("%s journal append from Side wrote no entry in Side's Notebook: %v", surface, err)
		}
		if _, err := os.Stat(filepath.Join(defaultNotebook, "journal", day+".md")); !os.IsNotExist(err) {
			t.Errorf("%s journal append from Side reached the Default profile's Notebook: %v", surface, err)
		}
		listed, err := call("crew list", map[string]any{"all": true})
		if err != nil {
			t.Fatalf("%s crew list from Side: %v", surface, err)
		}
		for _, member := range listed["members"].([]any) {
			if id := member.(map[string]any)["id"]; id == "keel" || id == "alder" || id == "trellis" {
				t.Errorf("%s crew list from Side lists the Default profile's member %v", surface, id)
			}
		}
		for _, attempt := range []struct {
			command string
			args    map[string]any
		}{
			{"crew set", map[string]any{"member": "keel", "model": "opus"}},
			{"crew set", map[string]any{"member": "member:keel", "model": "opus"}},
			{"crew rename", map[string]any{"member": "keel", "name": "stolen"}},
			{"crew start", map[string]any{"member": "keel"}},
			{"crew close", map[string]any{"member": "keel"}},
			{"crew apply", map[string]any{"member": "keel"}},
			{"crew move-home", map[string]any{"member": "keel"}},
			{"crew retire", map[string]any{"member": "alder"}},
			{"crew restore", map[string]any{"member": "alder"}},
		} {
			if answer, err := call(attempt.command, attempt.args); err == nil || !strings.Contains(err.Error(), `in profile "Side"`) {
				t.Errorf("%s %s %v from Side answered %v, %v, want a refusal that finds no such member in Side", surface, attempt.command, attempt.args, answer, err)
			}
		}
	}
	keelAfter := crewRosterMember(t, w.Client().WithGardenProfile("Default", ""), "keel")
	if keelAfter.Name != keelBefore.Name || protocol.Deref(keelAfter.Model) != protocol.Deref(keelBefore.Model) || keelAfter.HomeDir != keelBefore.HomeDir || keelAfter.Revision != keelBefore.Revision {
		t.Errorf("Side's calls changed the Default profile's keel: before %+v, after %+v", keelBefore, keelAfter)
	}
	if alder := crewRosterMember(t, w.Client().WithGardenProfile("Default", ""), "alder"); alder.Retired {
		t.Errorf("Side's crew retire retired the Default profile's alder")
	}
}

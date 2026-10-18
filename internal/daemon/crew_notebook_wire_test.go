package daemon_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/crew"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestChiefSetupPicksItsFinalNotebookHomeAndTrustsItsWorkingDirectory(t *testing.T) {
	for _, harness := range []fakeagent.Harness{fakeagent.Claude, fakeagent.Codex} {
		t.Run(string(harness), func(t *testing.T) {
			w := newWorld(t, harness)
			app := w.App()
			root := filepath.Join(w.Dir, "notebook", "chosen")
			result := testworld.Request(app, protocol.CrewSetMessage{Cmd: protocol.CmdCrewSet, RequestID: protocol.Ptr("setup"), Member: "chief", Agent: protocol.Ptr(string(harness)), Model: protocol.Ptr(chiefModel(harness)), NotebookRoot: &root}, protocol.EventCrewSetResult, func(m protocol.CrewSetResultMessage) bool { return m.RequestID == "setup" })
			if !result.Success || result.WokeSessionID == nil {
				t.Fatalf("setup: %+v", result)
			}
			member := result.Member
			if member.HomeDir != filepath.Join(root, "crew", "chief") || protocol.Deref(member.Cwd) != root {
				t.Fatalf("home: %+v", member)
			}
			run := w.Launched(string(*result.WokeSessionID))
			session := queriedSession(t, w.Client(), string(*result.WokeSessionID))
			if session.Directory != root {
				t.Fatalf("launch directory: %s", session.Directory)
			}
			if harness == fakeagent.Codex {
				if !strings.Contains(strings.Join(run.Argv, " "), `projects.`+`"`+root+`"`+`.trust_level="trusted"`) {
					t.Fatalf("Notebook trust: %q", run.Argv)
				}
			} else {
				raw, err := os.ReadFile(filepath.Join(w.Dir, "toolhome", ".claude", ".claude.json"))
				if err != nil {
					t.Fatal(err)
				}
				var config struct {
					Projects map[string]struct {
						Trusted bool `json:"hasTrustDialogAccepted"`
					} `json:"projects"`
				}
				if err := json.Unmarshal(raw, &config); err != nil {
					t.Fatal(err)
				}
				if !config.Projects[root].Trusted {
					t.Fatalf("Notebook not trusted: %s", raw)
				}
			}
			read := notebookAskRead(app, "crew/chief/CHARTER.md")
			if !read.Success || read.Result == nil || !strings.Contains(read.Result.Content, "Chief") {
				t.Fatalf("Notebook charter: %+v", read)
			}
			body := read.Result.Content + "\nA promise added in the Notebook editor.\n"
			written := notebookAskWrite(app, "crew/chief/CHARTER.md", body, read.Result.Hash)
			if !written.Success || written.Result == nil || written.Result.Conflict {
				t.Fatalf("save charter: %+v", written)
			}
			if raw, err := os.ReadFile(member.CharterPath); err != nil || string(raw) != body {
				t.Fatalf("saved charter %q: %v", raw, err)
			}
			run.Prompted()
			run.Reply("Ready. <!-- attn:state=idle -->")
			if _, err := w.Client().CrewRename("chief", "Captain"); err == nil || !strings.Contains(err.Error(), "is awake; put it to sleep first: attn crew sleep") {
				t.Fatalf("awake rename: %v", err)
			}
		})
	}
}

func TestProfilesSharingANotebookGetSeparateChiefHomes(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app := w.App()
	root := filepath.Join(w.Dir, "notebook")
	first := configureChiefOn(t, w, app, fakeagent.Claude, "sonnet")
	w.Launched(first)
	side := createProfile(app, "Side")
	sideApp := w.AppOn(side.ID)
	setSetting(t, sideApp, "notebook.root", root)
	second := configureChiefOn(t, w, sideApp, fakeagent.Claude, "sonnet")
	w.Launched(second)
	for _, c := range []struct{ profile, folder string }{{app.SelectedProfile(), "chief"}, {side.ID, "chief-2"}} {
		for _, m := range crewRoster(t, w.Client().WithRequester(c.profile, "")) {
			if m.Chief && m.HomeDir != filepath.Join(root, "crew", c.folder) {
				t.Fatalf("shared Notebook: %+v", m)
			}
		}
	}
}

func TestLegacyHomesMoveOnlyOnRequestAndNotebookRenamesMoveTheWholeHome(t *testing.T) {
	w := newCrewWorld(t, fakeagent.Claude)
	cli := w.Client()
	before := crewRosterMember(t, cli, "keel")
	nested := writeCrewHomeFile(t, w, "keel", "notes/private.txt", "Everything travels.\n")
	if err := os.Chmod(nested, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("private.txt", filepath.Join(filepath.Dir(nested), "link")); err != nil {
		t.Fatal(err)
	}
	setCrew(t, cli, "keel", protocol.CrewSetMessage{Cwd: protocol.Ptr(filepath.Dir(nested)), AwarenessDirs: []string{filepath.Dir(nested)}})
	w.restart()
	cli = w.Client()
	if got := crewRosterMember(t, cli, "keel"); got.HomeDir != before.HomeDir {
		t.Fatalf("legacy moved at startup: %+v", got)
	}
	awake := wakeCrew(t, cli, "keel", "")
	run := w.Launched(string(awake.SessionID))
	run.Prompted()
	run.Reply("Ready. <!-- attn:state=idle -->")
	if _, err := cli.CrewMoveHome("keel"); err == nil || !strings.Contains(err.Error(), "Keel is awake; put it to sleep first: attn crew sleep Keel") {
		t.Fatalf("awake move: %v", err)
	}
	if _, err := cli.CrewHandoff(awake.SessionID, "A letter that moves with me.", false, protocol.CrewDayCloseSleep); err != nil {
		t.Fatal(err)
	}
	moved, err := cli.CrewMoveHome("keel")
	if err != nil {
		t.Fatal(err)
	}
	if moved.Member.HomeDir != filepath.Join(w.Dir, "notebook", "crew", "keel") || moved.PreviousHome != before.HomeDir {
		t.Fatalf("move: %+v", moved)
	}
	if _, err := cli.CrewRename("keel", "Keeper"); err != nil {
		t.Fatal(err)
	}
	after := crewRosterMember(t, cli, "keel")
	if after.Name != "Keeper" || after.HomeDir != filepath.Join(w.Dir, "notebook", "crew", "keeper") || after.CharterPath != filepath.Join(after.HomeDir, crew.CharterFileName) || protocol.Deref(after.Cwd) != filepath.Join(after.HomeDir, "notes") || len(after.AwarenessDirs) != 1 || after.AwarenessDirs[0] != filepath.Join(after.HomeDir, "notes") {
		t.Fatalf("rename: %+v", after)
	}
	for _, old := range []string{before.HomeDir, moved.Member.HomeDir} {
		if _, err := os.Stat(old); !os.IsNotExist(err) {
			t.Fatalf("old home remains: %s %v", old, err)
		}
	}
	for _, path := range []string{crew.CharterFileName, filepath.Join("handoffs", crewHomeLetters["keel"]), "notes/private.txt"} {
		if _, err := os.Stat(filepath.Join(after.HomeDir, path)); err != nil {
			t.Fatal(err)
		}
	}
	if info, err := os.Stat(filepath.Join(after.HomeDir, "notes/private.txt")); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("file mode: %v %v", info, err)
	}
	if link, err := os.Readlink(filepath.Join(after.HomeDir, "notes/link")); err != nil || link != "private.txt" {
		t.Fatalf("link: %q %v", link, err)
	}
	w.restart()
	if got := crewRosterMember(t, w.Client(), "keel"); got.HomeDir != after.HomeDir {
		t.Fatalf("restart home: %+v", got)
	}
	wake := wakeCrew(t, w.Client(), "Keeper", "")
	w.Launched(string(wake.SessionID))
	prime, err := w.Client().CrewPrime(wake.SessionID)
	if err != nil || !strings.Contains(protocol.Deref(prime.Guidance), "A letter that moves with me.") {
		t.Fatalf("moved letters: %+v %v", prime, err)
	}
}

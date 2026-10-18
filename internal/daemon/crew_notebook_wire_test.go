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

func TestFailedChiefSetupKeepsTheExistingNotebook(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app := w.App()
	if written := notebookAskWrite(app, "knowledge/kept.md", "Existing notes stay visible.\n", ""); !written.Success {
		t.Fatalf("existing note: %+v", written)
	}
	root := filepath.Join(w.Dir, "blocked-notebook")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "crew"), []byte("occupied"), 0o644); err != nil {
		t.Fatal(err)
	}
	result := testworld.Request(app, protocol.CrewSetMessage{Cmd: protocol.CmdCrewSet, RequestID: protocol.Ptr("failed-setup"), Member: "chief", Agent: protocol.Ptr("claude"), Model: protocol.Ptr("sonnet"), NotebookRoot: &root}, protocol.EventCrewSetResult, func(m protocol.CrewSetResultMessage) bool { return m.RequestID == "failed-setup" })
	if result.Success || !strings.Contains(protocol.Deref(result.Error), "crew") {
		t.Fatalf("setup should fail: %+v", result)
	}
	if read := notebookAskRead(app, "knowledge/kept.md"); !read.Success || read.Result == nil || read.Result.Content != "Existing notes stay visible.\n" {
		t.Fatalf("existing note after failed setup: %+v", read)
	}
	for _, chief := range crewRoster(t, w.Client()) {
		if chief.Chief && (chief.HomeDir != "" || protocol.Deref(chief.Agent) != "") {
			t.Fatalf("failed setup configured chief: %+v", chief)
		}
	}
	w.restart()
	if read := notebookAskRead(w.App(), "knowledge/kept.md"); !read.Success || read.Result == nil || read.Result.Content != "Existing notes stay visible.\n" {
		t.Fatalf("existing note after restart: %+v", read)
	}
}

func TestChiefFollowsTheCurrentNotebookWithoutMovingItsHome(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	app, cli := w.App(), w.Client()
	id := configureChiefOn(t, w, app, fakeagent.Codex, chiefModel(fakeagent.Codex))
	run := w.Launched(id)
	run.Prompted()
	run.Reply("Ready. <!-- attn:state=idle -->")
	if _, err := cli.CrewHandoff(protocol.SessionID(id), "Use the current Notebook next time.", false, protocol.CrewDayCloseSleep); err != nil {
		t.Fatal(err)
	}
	before := crewRoster(t, cli)[0]
	unrelated := w.Path("unrelated")
	if err := os.MkdirAll(unrelated, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := cli.CrewSet("chief", &unrelated, nil, nil, nil, nil); err == nil || !strings.Contains(err.Error(), "Chief works from the Notebook root") {
		t.Fatalf("Chief cwd override: %v", err)
	}
	root := filepath.Join(w.Dir, "current-notebook")
	setSetting(t, app, "notebook.root", root)
	wake := wakeCrew(t, cli, "chief", "")
	next := w.Launched(string(wake.SessionID))
	if session := queriedSession(t, cli, string(wake.SessionID)); session.Directory != root {
		t.Fatalf("current Notebook launch: %+v", session)
	}
	if !strings.Contains(strings.Join(next.Argv, " "), `projects."`+root+`".trust_level="trusted"`) {
		t.Fatalf("current Notebook trust: %q", next.Argv)
	}
	after := crewRoster(t, cli)[0]
	if after.HomeDir != before.HomeDir || protocol.Deref(after.Cwd) != root {
		t.Fatalf("Notebook repoint moved home or kept stale cwd: before=%+v after=%+v", before, after)
	}
}

func TestACopiedDatabaseCannotUseAnotherHarnessNotebookHome(t *testing.T) {
	source := newWorld(t, fakeagent.Claude)
	id := configureChief(t, source, fakeagent.Claude, "sonnet")
	run := source.Launched(id)
	run.Prompted()
	run.Reply("Ready. <!-- attn:state=idle -->")
	if _, err := source.Client().CrewHandoff(protocol.SessionID(id), "Rest.", false, protocol.CrewDayCloseSleep); err != nil {
		t.Fatal(err)
	}
	home := crewRoster(t, source.Client())[0].HomeDir
	source.stop()
	copied := &world{World: prepareWorld(t)}
	database, err := os.ReadFile(filepath.Join(source.Dir, "attn.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(copied.Dir, "attn.db"), database, 0o644); err != nil {
		t.Fatal(err)
	}
	copied.start()
	_, err = copied.Client().CrewList()
	crewErrorContains(t, err, home, copied.Dir, "outside harness root")
	_, err = copied.Client().CrewWake("chief", "", "")
	crewErrorContains(t, err, home, copied.Dir, "outside harness root")
}

func TestChiefHandoffStartsItsSuccessorInTheCurrentNotebook(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	app, cli := w.App(), w.Client()
	id := configureChiefOn(t, w, app, fakeagent.Codex, chiefModel(fakeagent.Codex))
	run := w.Launched(id)
	run.Prompted()
	run.Reply("Ready. <!-- attn:state=idle -->")
	before := crewRoster(t, cli)[0]
	root := filepath.Join(w.Dir, "next-day-notebook")
	setSetting(t, app, "notebook.root", root)
	handoff := crewHandoff(t, cli, id, "Continue in the current Notebook.", false, "")
	if handoff.SessionID == nil || handoff.NapError != nil {
		t.Fatalf("Chief successor: %+v", handoff)
	}
	next := w.Launched(string(*handoff.SessionID))
	if session := queriedSession(t, cli, string(*handoff.SessionID)); session.Directory != root {
		t.Fatalf("successor launched outside the current Notebook: %+v", session)
	}
	if !strings.Contains(strings.Join(next.Argv, " "), `projects."`+root+`".trust_level="trusted"`) {
		t.Fatalf("successor Notebook trust: %q", next.Argv)
	}
	after := crewRoster(t, cli)[0]
	if after.HomeDir != before.HomeDir || protocol.Deref(after.BindingSession) != *handoff.SessionID {
		t.Fatalf("successor moved home or lost binding: before=%+v after=%+v", before, after)
	}
	if raw, err := os.ReadFile(handoff.Path); err != nil || !strings.Contains(string(raw), "Continue in the current Notebook.") || !strings.HasPrefix(handoff.Path, before.HomeDir+string(filepath.Separator)) {
		t.Fatalf("handoff left the original home: %s %q %v", handoff.Path, raw, err)
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
	app := w.App()
	read := notebookAskRead(app, "crew/keel/CHARTER.md")
	if !read.Success || read.Result == nil {
		t.Fatalf("charter before rename: %+v", read)
	}
	openCharter, err := os.OpenFile(moved.Member.CharterPath, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer openCharter.Close()
	if _, err := cli.CrewRename("keel", "Keeper"); err != nil {
		t.Fatal(err)
	}
	edit := "# An edit through an already open file.\n"
	if _, err := openCharter.WriteAt([]byte(edit), 0); err != nil {
		t.Fatal(err)
	}
	if raw, err := os.ReadFile(filepath.Join(w.Dir, "notebook", "crew", "keeper", crew.CharterFileName)); err != nil || !strings.HasPrefix(string(raw), edit) {
		t.Fatalf("move lost an edit through an open file: %q %v", raw, err)
	}
	stale := notebookAskWrite(app, "crew/keel/CHARTER.md", "An edit from the old tab.\n", read.Result.Hash)
	if !stale.Success || stale.Result == nil || !stale.Result.Conflict {
		t.Fatalf("stale charter save should conflict: %+v", stale)
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

func TestCrewRenameFailureKeepsTheOriginalHomeAndRecord(t *testing.T) {
	w := newWorld(t)
	cli := w.Client()
	created, err := cli.CrewCreate(protocol.CrewCreateMessage{Name: "Keel"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cli.CrewCreate(protocol.CrewCreateMessage{Name: "Keeper"}); err != nil {
		t.Fatal(err)
	}
	before := created.Member
	charter, err := os.ReadFile(before.CharterPath)
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(before.HomeDir, "notes.txt")
	if err := os.WriteFile(file, []byte("Do not lose this.\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := cli.CrewRename("Keel", "Keeper"); err == nil {
		t.Fatal("rename to an existing member succeeded")
	}
	after := crewRosterMember(t, cli, before.Key)
	if after.Name != before.Name || after.HomeDir != before.HomeDir || after.CharterPath != before.CharterPath {
		t.Fatalf("failed rename changed the member: before=%+v after=%+v", before, after)
	}
	for path, expected := range map[string]string{file: "Do not lose this.\n", before.CharterPath: string(charter)} {
		if raw, err := os.ReadFile(path); err != nil || string(raw) != expected {
			t.Fatalf("file after failed rename %s: %q %v", path, raw, err)
		}
	}
	if _, err := os.Stat(filepath.Join(w.Dir, "notebook", "crew", "keeper-2")); !os.IsNotExist(err) {
		t.Fatalf("failed rename left destination: %v", err)
	}
}

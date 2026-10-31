package daemon_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/crew"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func notebookCrewHome(w *world, folder string) string {
	return filepath.Join(w.Dir, "notebook", crew.HomesDirName, folder)
}

func moveCrewHome(t *testing.T, w *world, member string) *protocol.CrewMoveHomeResult {
	t.Helper()
	moved, err := w.Client().CrewMoveHome(member)
	if err != nil {
		t.Fatalf("move %s's home: %v", member, err)
	}
	return moved
}

func TestLegacyHomesMoveOnlyOnRequestAndNotebookRenamesMoveTheWholeHome(t *testing.T) {
	w := newCrewV2World(t, fakeagent.Claude)
	cli := w.Client()
	before := crewRosterMember(t, cli, "keel")
	nested := writeCrewHomeFile(t, w, "keel", "notes/private.txt", "Everything travels.\n")
	if err := os.Chmod(nested, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("private.txt", filepath.Join(filepath.Dir(nested), "link")); err != nil {
		t.Fatal(err)
	}
	setCrew(t, cli, "Keel", protocol.CrewSetMessage{Cwd: protocol.Ptr(filepath.Dir(nested)), AwarenessDirs: []string{filepath.Dir(nested)}})
	w.restart()
	cli = w.Client()
	if got := crewRosterMember(t, cli, "keel"); got.HomeDir != before.HomeDir {
		t.Fatalf("legacy home moved at startup: %+v", got)
	}
	awake := wakeCrew(t, cli, "Keel", "")
	run := w.Launched(string(awake.SessionID))
	run.Prompted()
	run.Reply("Ready. <!-- attn:state=idle -->")
	_, err := cli.CrewMoveHome("Keel")
	crewErrorContains(t, err, "Keel is awake", "attn crew sleep Keel")
	crewHandoff(t, cli, string(awake.SessionID), "A letter that moves with me.", false)

	moved := moveCrewHome(t, w, "Keel")
	if moved.Member.HomeDir != notebookCrewHome(w, "keel") || moved.PreviousHome != before.HomeDir {
		t.Fatalf("move: %+v", moved)
	}
	_, err = cli.CrewMoveHome("Keel")
	crewErrorContains(t, err, "already outside attn's data folder", moved.Member.HomeDir)
	app := w.App()
	read := notebookAskRead(app, "crew/keel/CHARTER.md")
	if !read.Success || read.Result == nil || !strings.Contains(read.Result.Content, "What I care about.") {
		t.Fatalf("the moved charter in the Notebook: %+v", read)
	}
	openCharter, err := os.OpenFile(moved.Member.CharterPath, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer openCharter.Close()

	renamed, err := cli.CrewRename("Keel", "Keeper")
	if err != nil {
		t.Fatal(err)
	}
	if protocol.Deref(renamed.HomeDir) != notebookCrewHome(w, "keeper") {
		t.Fatalf("rename result: %+v", renamed)
	}
	edit := "# An edit through an already open file.\n"
	if _, err := openCharter.WriteAt([]byte(edit), 0); err != nil {
		t.Fatal(err)
	}
	if raw, err := os.ReadFile(filepath.Join(notebookCrewHome(w, "keeper"), crew.CharterFileName)); err != nil || !strings.HasPrefix(string(raw), edit) {
		t.Fatalf("the move lost an edit through an open file: %q %v", raw, err)
	}
	stale := notebookAskWrite(app, "crew/keel/CHARTER.md", "An edit from a tab opened before the rename.\n", read.Result.Hash)
	if !stale.Success || stale.Result == nil || !stale.Result.Conflict {
		t.Fatalf("a save to the old folder should conflict: %+v", stale)
	}
	if _, err := os.Stat(moved.Member.HomeDir); !os.IsNotExist(err) {
		t.Fatalf("a save to the old folder recreated it: %v", err)
	}
	after := crewRosterMember(t, cli, "keel")
	notes := filepath.Join(after.HomeDir, "notes")
	if after.Name != "Keeper" || after.HomeDir != notebookCrewHome(w, "keeper") || after.CharterPath != filepath.Join(after.HomeDir, crew.CharterFileName) || protocol.Deref(after.Cwd) != notes || len(after.AwarenessDirs) != 1 || after.AwarenessDirs[0] != notes {
		t.Fatalf("rename: %+v", after)
	}
	if _, err := os.Stat(before.HomeDir); !os.IsNotExist(err) {
		t.Fatalf("the legacy home remains: %v", err)
	}
	if info, err := os.Stat(filepath.Join(notes, "private.txt")); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("file mode: %v %v", info, err)
	}
	if link, err := os.Readlink(filepath.Join(notes, "link")); err != nil || link != "private.txt" {
		t.Fatalf("link: %q %v", link, err)
	}
	if _, err := cli.CrewRename("Keeper", "KEEPER"); err != nil {
		t.Fatal(err)
	}
	if got := crewRosterMember(t, cli, "keel"); got.HomeDir != after.HomeDir {
		t.Fatalf("a case-only rename moved the home: %+v", got)
	}

	w.restart()
	cli = w.Client()
	if got := crewRosterMember(t, cli, "keel"); got.HomeDir != after.HomeDir {
		t.Fatalf("home after restart: %+v", got)
	}
	wake := wakeCrew(t, cli, "Keeper", "")
	w.Launched(string(wake.SessionID))
	if session := queriedSession(t, cli, string(wake.SessionID)); session.Directory != notes {
		t.Fatalf("launch directory after the move: %s", session.Directory)
	}
	prime, err := cli.CrewPrime(wake.SessionID)
	if err != nil || !strings.Contains(protocol.Deref(prime.Guidance), "A letter that moves with me.") {
		t.Fatalf("moved letters: %+v %v", prime, err)
	}
}

func TestAMovedHomeTakesTheNextFreeFolderInTheProfilesNotebook(t *testing.T) {
	w := newCrewWorld(t)
	app, cli := w.App(), w.Client()
	root := w.Path("own-notebook")
	setSetting(t, app, "notebook.profile_root", root)
	if err := os.MkdirAll(filepath.Join(root, crew.HomesDirName, "keel"), 0o755); err != nil {
		t.Fatal(err)
	}
	moved := moveCrewHome(t, w, "Keel")
	home := filepath.Join(root, crew.HomesDirName, "keel-2")
	if moved.Member.HomeDir != home {
		t.Fatalf("move into a taken folder: %+v", moved)
	}
	setSetting(t, app, "notebook.profile_root", w.Path("next-notebook"))
	if got := crewRosterMember(t, cli, "keel"); got.HomeDir != home {
		t.Fatalf("changing the Notebook folder moved the home: %+v", got)
	}
	read := testworld.Request(app, protocol.CrewCharterGetMessage{Cmd: protocol.CmdCrewCharterGet, Member: "Keel", RequestID: protocol.Ptr("repointed")}, protocol.EventCrewCharterGetResult, func(r protocol.CrewCharterGetResultMessage) bool { return r.RequestID == "repointed" })
	if !read.Success || read.Charter == nil || !strings.Contains(read.Charter.Content, "What I care about.") {
		t.Fatalf("charter after repointing the Notebook: %+v", read)
	}
}

func TestCrewRenameFailureKeepsTheOriginalHomeAndRecord(t *testing.T) {
	w := newCrewV2World(t)
	cli := w.Client()
	before := moveCrewHome(t, w, "Keel").Member
	moveCrewHome(t, w, "Trellis")
	charter, err := os.ReadFile(before.CharterPath)
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(before.HomeDir, "notes.txt")
	if err := os.WriteFile(file, []byte("Do not lose this.\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err = cli.CrewRename("Keel", "Trellis")
	crewErrorContains(t, err, "already taken")
	after := crewRosterMember(t, cli, "keel")
	if after.Name != before.Name || after.HomeDir != before.HomeDir || after.CharterPath != before.CharterPath {
		t.Fatalf("a failed rename changed the member: before=%+v after=%+v", before, after)
	}
	for path, expected := range map[string]string{file: "Do not lose this.\n", before.CharterPath: string(charter)} {
		if raw, err := os.ReadFile(path); err != nil || string(raw) != expected {
			t.Fatalf("file after a failed rename %s: %q %v", path, raw, err)
		}
	}
	if _, err := os.Stat(notebookCrewHome(w, "trellis-2")); !os.IsNotExist(err) {
		t.Fatalf("a failed rename left its destination: %v", err)
	}
}

func TestAnUnavailableNotebookHomeStopsOnlyItsMember(t *testing.T) {
	w := newCrewWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	member := moveCrewHome(t, w, "Keel").Member
	aside := member.HomeDir + "-aside"
	if err := os.Rename(member.HomeDir, aside); err != nil {
		t.Fatal(err)
	}
	if roster := crewRoster(t, cli); len(roster) != 3 {
		t.Fatalf("an unavailable home hid the roster: %+v", roster)
	}
	read := testworld.Request(app, protocol.CrewCharterGetMessage{Cmd: protocol.CmdCrewCharterGet, Member: "Keel", RequestID: protocol.Ptr("missing-home")}, protocol.EventCrewCharterGetResult, func(r protocol.CrewCharterGetResultMessage) bool { return r.RequestID == "missing-home" })
	if read.Success || !strings.Contains(protocol.Deref(read.Error), member.HomeDir) {
		t.Fatalf("charter of a member whose home is unavailable: %+v", read)
	}
	_, err := cli.CrewWake("Keel", "", "")
	crewErrorContains(t, err, member.HomeDir, "unavailable")
	w.Launched(string(wakeCrew(t, cli, "Trellis", "").SessionID))
	if _, err := os.Stat(member.HomeDir); !os.IsNotExist(err) {
		t.Fatalf("the unavailable home was recreated: %v", err)
	}
	if err := os.Rename(aside, member.HomeDir); err != nil {
		t.Fatal(err)
	}
	w.Launched(string(wakeCrew(t, cli, "Keel", "").SessionID))
}

func TestAMemberWithANotebookHomeKeepsItsProfile(t *testing.T) {
	w := newCrewWorld(t)
	app := w.App()
	original := app.SelectedProfile()
	moved := moveCrewHome(t, w, "Keel")
	createProfile(app, "Keep")
	for _, member := range []string{"alder", "trellis"} {
		if err := os.RemoveAll(crewHome(w, member)); err != nil {
			t.Fatal(err)
		}
	}
	remove := func() protocol.ProfileActionResultMessage {
		var revision int
		for _, profile := range w.AppOn(original).Initial.Profiles {
			if profile.ID == original {
				revision = profile.Revision
			}
		}
		return profileRequest(app, protocol.ProfileDeleteMessage{Cmd: protocol.CmdProfileDelete, RequestID: "delete-notebook-crew-profile", ProfileID: original, ExpectedRevision: revision}, "delete-notebook-crew-profile")
	}
	if refused := remove(); refused.Success || !strings.Contains(protocol.Deref(refused.Error), "1 crew members") {
		t.Fatalf("profile with a Notebook crew member: %+v", refused)
	}
	if err := os.RemoveAll(moved.Member.HomeDir); err != nil {
		t.Fatal(err)
	}
	if deleted := remove(); !deleted.Success {
		t.Fatalf("profile after removing the Notebook home: %+v", deleted)
	}
}

func TestCreatedMembersLiveInTheirProfilesNotebook(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	keel := createCrew(t, cli, "Keel")
	if keel.HomeDir != notebookCrewHome(w, "keel") || keel.CharterPath != filepath.Join(keel.HomeDir, crew.CharterFileName) {
		t.Fatalf("created home: %+v", keel)
	}
	if read := notebookAskRead(app, "crew/keel/CHARTER.md"); !read.Success || read.Result == nil || read.Result.Content != "" {
		t.Fatalf("the new charter in the Notebook: %+v", read)
	}
	wake := wakeCrew(t, cli, "Keel", "")
	w.Launched(string(wake.SessionID))
	if session := queriedSession(t, cli, string(wake.SessionID)); session.Directory != keel.HomeDir {
		t.Fatalf("launch directory: %s", session.Directory)
	}

	shared := createProfile(app, "Shared")
	sharedKeel := createCrew(t, w.Client().WithGardenProfile(shared.ID, ""), "Keel")
	if sharedKeel.HomeDir != notebookCrewHome(w, "keel-2") {
		t.Fatalf("a second Keel in the same Notebook: %+v", sharedKeel)
	}
	own := createProfile(app, "Own")
	root := w.Path("own-notebook")
	setSetting(t, w.AppOn(own.ID), "notebook.profile_root", root)
	ownKeel := createCrew(t, w.Client().WithGardenProfile(own.ID, ""), "Keel")
	if ownKeel.HomeDir != filepath.Join(root, crew.HomesDirName, "keel") {
		t.Fatalf("a Keel in a profile with its own Notebook: %+v", ownKeel)
	}
}

func TestClosedCrewConversationsResumeFromAMovedOrRenamedHome(t *testing.T) {
	for _, relocation := range []string{"move", "rename", "rename while awake"} {
		t.Run(relocation, func(t *testing.T) {
			w := newCrewV2World(t, fakeagent.Claude)
			cli := w.Client()
			if relocation != "move" {
				moveCrewHome(t, w, "Keel")
			}
			home := crewRosterMember(t, cli, "keel").HomeDir
			atHome := sleepAfterADay(t, w, "Keel")
			notes := filepath.Join(home, "notes")
			if err := os.MkdirAll(notes, 0o755); err != nil {
				t.Fatal(err)
			}
			setCrew(t, cli, "Keel", protocol.CrewSetMessage{Cwd: protocol.Ptr(notes)})
			inNotes := sleepAfterADay(t, w, "Keel")

			switch relocation {
			case "move":
				moveCrewHome(t, w, "Keel")
			case "rename":
				if _, err := cli.CrewRename("Keel", "Keeper"); err != nil {
					t.Fatal(err)
				}
			case "rename while awake":
				day := wakeCrew(t, cli, "Keel", "")
				w.Launched(string(day.SessionID)).Prompted()
				if _, err := cli.CrewRename("Keel", "Keeper"); err != nil {
					t.Fatal(err)
				}
				crewHandoff(t, cli, string(day.SessionID), "Renamed today.", false)
				sleepAfterADay(t, w, "Keeper")
			}
			moved := crewRosterMember(t, cli, "keel").HomeDir
			if moved == home {
				t.Fatalf("the %s left the home at %s", relocation, home)
			}
			for directory, day := range map[string]*fakeagent.Run{moved: atHome, filepath.Join(moved, "notes"): inNotes} {
				reopened, err := cli.SessionReopen(client.SessionReopenOptions{SessionID: protocol.SessionID(day.SessionID)})
				if err != nil {
					t.Fatalf("resume %s after the %s: %v", day.SessionID, relocation, err)
				}
				if reopened.Directory != directory {
					t.Fatalf("resumed %s in %s, want %s", day.SessionID, reopened.Directory, directory)
				}
				if resumed := w.Launched(day.SessionID); !resumed.Resumed || resumed.ConversationID != day.ConversationID {
					t.Fatalf("resume after the %s started %+v, want conversation %s", relocation, resumed, day.ConversationID)
				}
			}
		})
	}
}

func sleepAfterADay(t *testing.T, w *world, member string) *fakeagent.Run {
	t.Helper()
	cli := w.Client()
	day := wakeCrew(t, cli, member, "")
	run := w.Launched(string(day.SessionID))
	run.Prompted()
	run.Reply("A day worth keeping. <!-- attn:state=idle -->")
	crewHandoff(t, cli, string(day.SessionID), "Resume this conversation later.", false)
	return run
}

func repointCrewHome(cli *client.Client, member, home string) (*protocol.CrewSetResult, error) {
	return cli.CrewSetMessage(protocol.CrewSetMessage{Member: member, Home: protocol.Ptr(home)})
}

func TestAHomeMovedAwayIsRepointedAndWakesThere(t *testing.T) {
	w := newCrewV2World(t, fakeagent.Claude)
	cli := w.Client()
	home := moveCrewHome(t, w, "Keel").Member.HomeDir
	notes := filepath.Join(home, "notes")
	if err := os.MkdirAll(notes, 0o755); err != nil {
		t.Fatal(err)
	}
	setCrew(t, cli, "Keel", protocol.CrewSetMessage{Cwd: protocol.Ptr(notes), AwarenessDirs: []string{notes}})
	day := sleepAfterADay(t, w, "Keel")

	tidied := w.Path("tidied")
	if err := os.MkdirAll(tidied, 0o755); err != nil {
		t.Fatal(err)
	}
	aside := filepath.Join(tidied, "keel-old")
	if err := os.Rename(home, aside); err != nil {
		t.Fatal(err)
	}
	_, err := cli.CrewWake("Keel", "", "")
	crewErrorContains(t, err, home, "unavailable")

	empty := w.Path("empty")
	if err := os.MkdirAll(empty, 0o755); err != nil {
		t.Fatal(err)
	}
	_, err = repointCrewHome(cli, "Keel", empty)
	crewErrorContains(t, err, empty, "has no CHARTER.md")
	_, err = repointCrewHome(cli, "Keel", notebookCrewHome(w, "alder-missing"))
	crewErrorContains(t, err, notebookCrewHome(w, "alder-missing"), "not there")

	repointed, err := repointCrewHome(cli, "Keel", aside)
	if err != nil {
		t.Fatal(err)
	}
	named := filepath.Join(tidied, "keel")
	movedNotes := filepath.Join(named, "notes")
	if got := repointed.Member; got.HomeDir != named || protocol.Deref(got.Cwd) != movedNotes || len(got.AwarenessDirs) != 1 || got.AwarenessDirs[0] != movedNotes {
		t.Fatalf("repointed member: %+v", got)
	}
	if _, err := os.Stat(aside); !os.IsNotExist(err) {
		t.Fatalf("the repointed folder kept its old name: %v", err)
	}

	reopened, err := cli.SessionReopen(client.SessionReopenOptions{SessionID: protocol.SessionID(day.SessionID)})
	if err != nil || reopened.Directory != movedNotes {
		t.Fatalf("a closed session after the repoint: %+v %v", reopened, err)
	}
	if resumed := w.Launched(day.SessionID); !resumed.Resumed || resumed.ConversationID != day.ConversationID {
		t.Fatalf("resume after the repoint started %+v", resumed)
	}

	wake := wakeCrew(t, cli, "Keel", "")
	w.Launched(string(wake.SessionID))
	if session := queriedSession(t, cli, string(wake.SessionID)); session.Directory != movedNotes {
		t.Fatalf("launch directory after the repoint: %s", session.Directory)
	}
	prime, err := cli.CrewPrime(wake.SessionID)
	if err != nil || !strings.Contains(protocol.Deref(prime.Guidance), "Resume this conversation later.") {
		t.Fatalf("letters after the repoint: %+v %v", prime, err)
	}
	_, err = repointCrewHome(cli, "Keel", home)
	crewErrorContains(t, err, "Keel is awake", "attn crew sleep Keel")
}

func TestARepointBringsAHomeBackIntoTheDataFolder(t *testing.T) {
	w := newCrewWorld(t, fakeagent.Claude)
	cli := w.Client()
	dataHome := crewHome(w, "keel")
	notebook := moveCrewHome(t, w, "Keel").Member.HomeDir
	trellis := moveCrewHome(t, w, "Trellis").Member.HomeDir

	_, err := repointCrewHome(cli, "Keel", trellis)
	crewErrorContains(t, err, "Trellis's home", trellis)
	_, err = repointCrewHome(cli, "Keel", crewHome(w, "alder"))
	crewErrorContains(t, err, "attn's data folder", dataHome)

	if err := os.Rename(notebook, dataHome); err != nil {
		t.Fatal(err)
	}
	if got, err := repointCrewHome(cli, "Keel", dataHome); err != nil || got.Member.HomeDir != dataHome {
		t.Fatalf("repoint into the data folder: %+v %v", got, err)
	}
	w.restart()
	cli = w.Client()
	if roster := crewRoster(t, cli); len(roster) != 3 {
		t.Fatalf("a home back in the data folder joined the roster twice: %+v", roster)
	}
	if moved := moveCrewHome(t, w, "Keel"); moved.Member.HomeDir != notebook || moved.PreviousHome != dataHome {
		t.Fatalf("move-home after coming back: %+v", moved)
	}
}

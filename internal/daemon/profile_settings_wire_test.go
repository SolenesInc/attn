package daemon_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func awaitNotebookRoot(app *testworld.Peer, root string) {
	app.T.Helper()
	testworld.Await(app, protocol.EventSettingsUpdated, func(m protocol.SettingsUpdatedMessage) bool { return m.Settings["notebook.root.effective"] == root })
}

func TestEachProfileKeepsItsOwnNotebook(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	defaultApp, workApp := w.App(), w.App()
	defaultRoot := filepath.Join(w.Dir, "default-notes")
	workRoot := filepath.Join(w.Dir, "work-notes")
	if updated := notebookSetting(defaultApp, "notebook.root", defaultRoot); !protocol.Deref(updated.Success) {
		t.Fatal(protocol.Deref(updated.Error))
	}
	defaultSession := w.Spawn(defaultApp, fakeagent.Claude, w.Path("default-agent"), func(m *protocol.SpawnSessionMessage) { m.ChiefOfStaff = protocol.Ptr(true) })
	work := createProfile(workApp, "Work")
	selectProfile(workApp, work.ID)
	if updated := notebookSetting(workApp, "notebook.root", workRoot); !protocol.Deref(updated.Success) {
		t.Fatal(protocol.Deref(updated.Error))
	}
	workSession := w.Spawn(workApp, fakeagent.Claude, w.Path("work-agent"), func(m *protocol.SpawnSessionMessage) { m.ProfileID = work.ID; m.ChiefOfStaff = protocol.Ptr(true) })
	for _, entry := range []struct {
		session    protocol.SessionID
		root, text string
	}{{protocol.SessionID(defaultSession), defaultRoot, "Default journal"}, {protocol.SessionID(workSession), workRoot, "Work journal"}} {
		result, err := w.Client().AppendJournal(entry.session, "2026-10-10", entry.text)
		if err != nil {
			t.Fatal(err)
		}
		content, err := os.ReadFile(filepath.Join(entry.root, result.RelPath))
		if err != nil || !strings.Contains(string(content), entry.text) {
			t.Fatalf("journal at %s: %s (%v)", entry.root, content, err)
		}
	}

	for _, owned := range []struct {
		session string
		root    string
	}{{defaultSession, defaultRoot}, {workSession, workRoot}} {
		guide, err := w.Client().NotebookGuide(protocol.SessionID(owned.session))
		if err != nil || guide.Root != owned.root || !guide.SessionIsChief {
			t.Fatalf("chief guide: %+v (%v)", guide, err)
		}
		if _, err := os.Stat(filepath.Join(owned.root, "knowledge", "index.md")); err != nil {
			t.Fatal(err)
		}
		seed, err := w.Client().SeedPlant(protocol.SessionID(owned.session), "Profile artifact", "Keep this artifact in its birth profile.", "", "", "")
		if err != nil {
			t.Fatal(err)
		}
		source := filepath.Join(w.Dir, owned.session+".txt")
		if err := os.WriteFile(source, []byte(owned.root), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := w.Client().SeedArtifactTransfer(protocol.SessionID(owned.session), seed.Seed.ID, "copy", source, "proof.txt", "", nil); err != nil {
			t.Fatal(err)
		}
		content, err := os.ReadFile(filepath.Join(owned.root, "seeds", seed.Seed.ID, "proof.txt"))
		if err != nil || string(content) != owned.root {
			t.Fatalf("seed artifact: %s (%v)", content, err)
		}
	}
	listed := notebookAskList(workApp, "")
	if !listed.Success || !slices.Contains(notebookEntryPaths(listed.Entries), "journal/2026-10-10.md") {
		t.Fatalf("Work notebook: %+v", listed)
	}
	read := notebookAskRead(workApp, "journal/2026-10-10.md")
	if !read.Success || strings.Contains(read.Result.Content, "Default journal") || !strings.Contains(read.Result.Content, "Work journal") {
		t.Fatalf("Work journal: %+v", read)
	}
	defaultRead := notebookAskRead(defaultApp, "journal/2026-10-10.md")
	if !defaultRead.Success || strings.Contains(defaultRead.Result.Content, "Work journal") {
		t.Fatalf("Default journal: %+v", defaultRead)
	}
	fsWriteFile(t, filepath.Join(defaultRoot, "external.md"), []byte("# Default edit"))
	fsAwaitChanged(defaultApp, func(m protocol.FsChangedMessage) bool {
		return m.Root == defaultRoot && slices.Contains(m.Paths, "external.md")
	})
	fsAskList(workApp, "", "")
	for _, raw := range workApp.Log() {
		var event protocol.FsChangedMessage
		if err := json.Unmarshal(raw, &event); err != nil {
			t.Fatal(err)
		}
		if event.Event != protocol.EventFsChanged {
			continue
		}
		if event.Root == defaultRoot {
			t.Fatalf("Work heard Default change: %+v", event)
		}
	}
	w.restart()
	got, err := w.Client().Settings(protocol.SessionID(workSession), "", "notebook.root", false)
	if err != nil || protocol.Deref(got.Entries[0].Value) != workRoot {
		t.Fatalf("root after restart: %+v (%v)", got, err)
	}
}

func TestSettingsFollowTheProfileTheAppShows(t *testing.T) {
	w := newWorld(t)
	app := w.App()
	work := createProfile(app, "Work")
	selectProfile(app, work.ID)
	awaitNotebookRoot(app, filepath.Join(w.Dir, "notebook-work"))
	if updated := notebookSetting(app, "theme", "dark"); !protocol.Deref(updated.Success) {
		t.Fatal(protocol.Deref(updated.Error))
	}
	other := w.App()
	snapshot, err := w.Client().Settings("", work.ID, "theme", false)
	if err != nil || protocol.Deref(snapshot.Entries[0].Value) != "dark" {
		t.Fatalf("daemon key: %+v (%v)", snapshot, err)
	}
	other.Send(protocol.GetSettingsMessage{Cmd: protocol.CmdGetSettings})
	testworld.Await(other, protocol.EventSettingsUpdated, func(m protocol.SettingsUpdatedMessage) bool { return m.Settings["theme"] == "dark" })
	for _, key := range []string{"notebook.root.effective", "unknown.setting", "installed_bundled_plugins"} {
		if updated := notebookSetting(app, key, "anything"); protocol.Deref(updated.Success) || !strings.Contains(protocol.Deref(updated.Error), key) {
			t.Fatalf("refusal for %s: %+v", key, updated)
		}
	}
}

func TestNewProfileNotebookDefaultsSurviveRenameAndAvoidCollisions(t *testing.T) {
	w := newWorld(t)
	app := w.App()
	sideRoot := filepath.Join(w.Dir, "notebook-side")
	if err := os.MkdirAll(sideRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	side := createProfile(app, "Side")
	selectProfile(app, side.ID)
	awaitNotebookRoot(app, sideRoot+"-2")
	work := createProfile(app, "Work")
	selectProfile(app, work.ID)
	workRoot := filepath.Join(w.Dir, "notebook-work")
	awaitNotebookRoot(app, workRoot)
	id := uuid.NewString()
	renamed := mustProfileRequest(app, protocol.ProfileRenameMessage{Cmd: protocol.CmdProfileRename, RequestID: id, ProfileID: work.ID, Name: "Office", ExpectedRevision: work.Revision}, id)
	if renamed.Profile.Name != "Office" {
		t.Fatal(renamed)
	}
	got, err := w.Client().Settings("", work.ID, "notebook.root", false)
	if err != nil || protocol.Deref(got.Entries[0].Value) != workRoot {
		t.Fatalf("root after rename: %+v (%v)", got, err)
	}
	if updated := notebookSetting(app, "notebook.root", ""); !protocol.Deref(updated.Success) {
		t.Fatal(protocol.Deref(updated.Error))
	}
	awaitNotebookRoot(app, filepath.Join(w.Dir, "notebook-office"))
}

func TestHarnessNeverWritesNotebookOutsideItsRoot(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app := w.App()
	session := w.Spawn(app, fakeagent.Claude, w.Path("agent"))
	outside := filepath.Join(t.TempDir(), "outside")
	if updated := notebookSetting(app, "notebook.root", outside); !protocol.Deref(updated.Success) {
		t.Fatal(protocol.Deref(updated.Error))
	}
	_, err := w.Client().AppendJournal(protocol.SessionID(session), "2026-10-10", "must not write")
	if err == nil || !strings.Contains(err.Error(), outside) || !strings.Contains(err.Error(), w.Dir) {
		t.Fatalf("harness refusal: %v", err)
	}
	if _, err := os.Stat(outside); !os.IsNotExist(err) {
		t.Fatalf("outside root exists: %v", err)
	}
}

func TestStaleNotebookAndSettingsWritesNeverFollowAProfileOrRootSwitch(t *testing.T) {
	w := newWorld(t)
	app := w.App()
	originalID := protocol.Deref(app.Initial.SelectedProfileID)
	originalRoot := filepath.Join(w.Dir, "original-notes")
	notebookSetting(app, "notebook.root", originalRoot)
	original := notebookAskWrite(app, "same.md", notebookNote("same content"), "")
	work := createProfile(app, "Work")
	selectProfile(app, work.ID)
	notebookAskWrite(app, "same.md", notebookNote("same content"), "")
	refuseWrite := func(profileID, expectedRoot string) {
		result := fsRequest[protocol.FsWriteResultMessage](app, protocol.EventFsWriteResult, func(id *string) any {
			return protocol.FsWriteMessage{Cmd: protocol.CmdFsWrite, RequestID: id, Path: "same.md", Content: notebookNote("stale edit"), BaseHash: original.Result.Hash, ProfileID: protocol.Ptr(profileID), ExpectedNotebookRoot: protocol.Ptr(expectedRoot)}
		})
		if result.Success || result.Error == nil {
			t.Fatalf("stale write accepted: %+v", result)
		}
	}
	refuseWrite(originalID, originalRoot)
	id := uuid.NewString()
	refused := testworld.Request(app, protocol.SetSettingMessage{Cmd: protocol.CmdSetSetting, RequestID: &id, Key: "notebook.root", Value: originalRoot, ProfileID: &originalID}, protocol.EventSettingsUpdated, func(m protocol.SettingsUpdatedMessage) bool { return protocol.Deref(m.RequestID) == id })
	if protocol.Deref(refused.Success) || !strings.Contains(protocol.Deref(refused.Error), originalID) {
		t.Fatalf("stale settings save: %+v", refused)
	}
	if read := notebookAskRead(app, "same.md"); read.Result.Content != notebookNote("same content") {
		t.Fatal(read)
	}
	previousRoot := filepath.Join(w.Dir, "notebook-work")
	notebookSetting(app, "notebook.root", filepath.Join(w.Dir, "work-moved"))
	notebookAskWrite(app, "same.md", notebookNote("same content"), "")
	refuseWrite(work.ID, previousRoot)
	if read := notebookAskRead(app, "same.md"); read.Result.Content != notebookNote("same content") {
		t.Fatal(read)
	}
	remote := fsRemotePeer(w)
	selectProfile(remote, work.ID)
	if listed := fsAskList(remote, "", ""); !listed.Success {
		t.Fatal(protocol.Deref(listed.Error))
	}
	if explicit := fsAskWatch(remote, filepath.Join(w.Dir, "work-moved")); explicit.Success || !strings.Contains(protocol.Deref(explicit.Error), "authenticated") {
		t.Fatalf("remote explicit root: %+v", explicit)
	}
}

func TestTheFirstArtifactCreatesItsProfilesNotebookFolder(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app := w.App()
	work := createProfile(app, "Work")
	selectProfile(app, work.ID)
	session := w.Spawn(app, fakeagent.Claude, w.Path("work-agent"), func(m *protocol.SpawnSessionMessage) { m.ProfileID = work.ID })
	root := filepath.Join(w.Dir, "notebook-work")
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatalf("Notebook already exists before attachment: %v", err)
	}
	seed, err := w.Client().SeedPlant(protocol.SessionID(session), "First artifact", "Keep the result in Work.", "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	source := w.Path("result.txt")
	if err := os.WriteFile(source, []byte("first result"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Client().SeedArtifactTransfer(protocol.SessionID(session), seed.Seed.ID, "copy", source, "result.txt", "", nil); err != nil {
		t.Fatal(err)
	}
	result, err := os.ReadFile(filepath.Join(root, "seeds", seed.Seed.ID, "result.txt"))
	if err != nil || string(result) != "first result" {
		t.Fatalf("artifact: %q (%v)", result, err)
	}
}

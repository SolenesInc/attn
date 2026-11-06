package daemon_test

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/notebook"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func notebookAskBacklinks(p *testworld.Peer, path string) protocol.NotebookBacklinksResultMessage {
	p.T.Helper()
	return fsRequest[protocol.NotebookBacklinksResultMessage](p, protocol.EventNotebookBacklinksResult, func(id *string) any {
		return protocol.NotebookBacklinksMessage{Cmd: protocol.CmdNotebookBacklinks, RequestID: id, Path: path}
	})
}

func notebookEntryPaths(entries []protocol.NotebookEntry) []string {
	paths := make([]string, 0, len(entries))
	for _, entry := range entries {
		paths = append(paths, entry.Path)
	}
	return paths
}

func fsEntryPaths(entries []protocol.FsEntry) []string {
	paths := make([]string, 0, len(entries))
	for _, entry := range entries {
		paths = append(paths, entry.Path)
	}
	return paths
}

func notebookNote(body string) string {
	return "---\ntype: note\n---\n" + body + "\n"
}

func notebookSetting(app *testworld.Peer, key, value string) protocol.SettingsUpdatedMessage {
	app.T.Helper()
	requestID := uuid.NewString()
	return testworld.Request(app, protocol.SetSettingMessage{Cmd: protocol.CmdSetSetting, Key: key, Value: value, RequestID: protocol.Ptr(requestID)},
		protocol.EventSettingsUpdated, func(m protocol.SettingsUpdatedMessage) bool { return protocol.Deref(m.RequestID) == requestID })
}

func TestADaemonOnATemporaryDataFolderKeepsItsDefaultNotebookInside(t *testing.T) {
	w := &world{World: prepareWorld(t)}
	t.Setenv("ATTN_HARNESS_DATA_DIR", "")
	t.Setenv("ATTN_HARNESS_NOTEBOOK_ROOT", "")
	w.start()
	app := w.App()
	root := filepath.Join(w.Dir, "notebook")
	if got := app.Initial.Settings["notebook.root.effective"]; got != root {
		t.Fatalf("notebook.root.effective = %v, want %s", got, root)
	}
	if saved := fsAskWrite(app, "", "note.md", "hello", ""); !saved.Success {
		t.Fatalf("writing to the default Notebook: %s", protocol.Deref(saved.Error))
	}
	if _, err := os.Stat(filepath.Join(root, "note.md")); err != nil {
		t.Fatalf("the write did not land in the data folder's Notebook: %v", err)
	}
}

func TestNotebookBacklinksNameTheNotesThatLinkToANote(t *testing.T) {
	w := newWorld(t)
	app := w.App()
	fsAskWrite(app, "", "knowledge/areas/a.md", notebookNote("body"), "")
	fsAskWrite(app, "", "knowledge/areas/b.md", notebookNote("see [a](/knowledge/areas/a.md)"), "")
	fsAskWrite(app, "", "journal/2026-06-13.md", "---\ntype: journal\n---\nentry\n", "")

	if back := notebookAskBacklinks(app, "/knowledge/areas/a.md"); !back.Success || !slices.Equal(notebookEntryPaths(back.Entries), []string{"knowledge/areas/b.md"}) {
		t.Fatalf("backlinks of a.md = %v, want b.md", notebookEntryPaths(back.Entries))
	}
}

func TestNotebookReportsExternalEditsButNotItsOwnWrites(t *testing.T) {
	w := newWorld(t)
	w.closeWatchWindows()
	app := w.App()
	root := fsNotebookRoot(t, w)
	fsWriteFile(t, filepath.Join(root, "gone.md"), []byte(notebookNote("about to go")))
	fsAskList(app, "", "")

	fsAskWrite(app, "", "own.md", notebookNote("attn wrote this"), "")
	fsWriteFile(t, filepath.Join(root, "ext.md"), []byte(notebookNote("edited externally")))
	if refused := fsAskWrite(app, "", "doc.md", "x", "deadbeef"); refused.Result == nil || !refused.Result.Conflict {
		t.Fatalf("a write against a hash doc.md never had = %+v, want a conflict", refused.Result)
	}
	fsWriteFile(t, filepath.Join(root, "doc.md"), []byte(notebookNote("externally created")))
	fsAskWrite(app, "", "race.md", notebookNote("attn wrote this"), "")
	fsWriteFile(t, filepath.Join(root, "race.md"), []byte(notebookNote("external overwrote it")))

	fsWriteFile(t, filepath.Join(root, "knowledge", "areas", "new.md"), []byte(notebookNote("in a new folder")))
	if err := os.Remove(filepath.Join(root, "gone.md")); err != nil {
		t.Fatal(err)
	}

	heard := map[string]bool{}
	for !heard["ext.md"] || !heard["doc.md"] || !heard["race.md"] || !heard["knowledge/areas/new.md"] || !heard["gone.md"] {
		external := fsAwaitChanged(app, func(m protocol.FsChangedMessage) bool { return m.Root == root && m.Origin == "external" })
		for _, path := range external.Paths {
			heard[path] = true
		}
		if heard["own.md"] {
			t.Fatalf("attn's own write was reported as external: %v", external.Paths)
		}
	}
}

func TestNotebookKeepsItsOwnWriteQuietWhenItsEventsSpanTwoWatchWindows(t *testing.T) {
	w := newWorld(t)
	app := w.App()
	root := fsNotebookRoot(t, w)
	fsAskList(app, "", "")

	own := notebookNote("attn wrote this")
	fsAskWrite(app, "", "own.md", own, "")
	w.awaitTimer(notebook.WatchDebounceTimer)
	w.advance(notebook.WatchDebounce)

	fsWriteFile(t, filepath.Join(root, ".own.md.tmp"), []byte(own))
	if err := os.Rename(filepath.Join(root, ".own.md.tmp"), filepath.Join(root, "own.md")); err != nil {
		t.Fatal(err)
	}
	fsWriteFile(t, filepath.Join(root, "ext.md"), []byte(notebookNote("edited externally")))
	w.closeWatchWindows()
	for heard := false; !heard; {
		external := fsAwaitChanged(app, func(m protocol.FsChangedMessage) bool { return m.Root == root && m.Origin == "external" })
		if slices.Contains(external.Paths, "own.md") {
			t.Fatalf("a later event of attn's own write was reported as external: %v", external.Paths)
		}
		heard = slices.Contains(external.Paths, "ext.md")
	}
}

func TestAttnsOwnNotebookWritesReachEveryOpenView(t *testing.T) {
	w := newWorld(t)
	writer, viewer, cli := w.App(), w.App(), w.Client()
	root := fsNotebookRoot(t, w)
	heard := func(origin, rel string) {
		t.Helper()
		fsAwaitChanged(viewer, func(m protocol.FsChangedMessage) bool {
			return m.Root == root && m.Origin == origin && slices.Contains(m.Paths, rel)
		})
	}

	if _, err := cli.AppendJournal("", "2026-07-05", "shipped the parser"); err != nil {
		t.Fatal(err)
	}
	heard("agent", "journal/2026-07-05.md")

	if written := fsAskWrite(writer, "", "note.md", notebookNote("remember this"), ""); !written.Success {
		t.Fatalf("writing the Notebook: %s", protocol.Deref(written.Error))
	}
	heard("ui", "note.md")
}

func TestTheNotebookGuideScaffoldsOnlyForTheChief(t *testing.T) {
	for _, c := range []struct {
		agent    fakeagent.Harness
		guidance []string
		retired  string
	}{
		{fakeagent.Claude, []string{"Never park a blocking Monitor on attn activity"}, ""},
		{fakeagent.Codex, []string{"`attn seed show <seed-id>`"}, "attn ticket inbox"},
	} {
		t.Run(string(c.agent), func(t *testing.T) {
			w := newWorld(t, c.agent)
			app, cli := w.App(), w.Client()
			root := fsNotebookRoot(t, w)

			worker := w.Spawn(app, c.agent, w.Path("worker"))
			if guide, err := cli.NotebookGuide(protocol.SessionID(worker)); err != nil || guide.SessionIsChief || guide.Guidance == "" || guide.Root != root {
				t.Fatalf("a worker's guide = %+v, %v; want guidance under %s without the chief's role", guide, err, root)
			}
			if listed := fsAskList(app, "", ""); len(listed.Entries) != 0 {
				t.Fatalf("a worker's guide scaffolded %v", fsEntryPaths(listed.Entries))
			}

			chief := w.Spawn(app, c.agent, w.Path("chief"), func(m *protocol.SpawnSessionMessage) { m.ChiefOfStaff = protocol.Ptr(true) })
			guide, err := cli.NotebookGuide(protocol.SessionID(chief))
			if err != nil || !guide.SessionIsChief || guide.Root != root {
				t.Fatalf("the chief's guide = %+v, %v; want the chief's guidance under %s", guide, err, root)
			}
			for _, want := range c.guidance {
				if !strings.Contains(guide.Guidance, want) {
					t.Errorf("the chief's guide does not carry %q", want)
				}
			}
			if c.retired != "" && strings.Contains(guide.Guidance, c.retired) {
				t.Errorf("the chief's guide sends it to the retired %q", c.retired)
			}
			fsAwaitChanged(app, func(m protocol.FsChangedMessage) bool {
				return m.Root == root && m.Origin == "agent" && slices.Contains(m.Paths, "index.md")
			})
			for _, scaffolded := range []string{"index.md", "log.md", "knowledge/index.md"} {
				if read := fsAskRead(app, "", scaffolded); !read.Success {
					t.Fatalf("the chief's guide did not scaffold %s: %s", scaffolded, protocol.Deref(read.Error))
				}
			}
		})
	}
}

func TestNotebookRootSettingIsValidatedAndItsEffectiveValueIsReadOnly(t *testing.T) {
	w := newWorld(t)
	app := w.App()
	root := fsNotebookRoot(t, w)
	custom := w.Path("custom")

	for _, c := range []struct {
		key, value, effective string
		ok                    bool
	}{
		{"notebook.root", "relative/path", "", false},
		{"notebook.root", fsDir(t, "outside"), "", false},
		{"notebook.root", filepath.Join(root, "line\nbreak"), "", false},
		{"notebook.root", custom, custom, true},
		{"notebook.root", "", root, true},
		{"notebook.root.effective", "/tmp/whatever", "", false},
	} {
		if updated := notebookSetting(app, c.key, c.value); protocol.Deref(updated.Success) != c.ok {
			t.Errorf("setting %s to %q succeeded=%v (%s), want %v", c.key, c.value, protocol.Deref(updated.Success), protocol.Deref(updated.Error), c.ok)
		}
		if c.ok {
			testworld.Await(app, protocol.EventSettingsUpdated, func(m protocol.SettingsUpdatedMessage) bool {
				value, _ := m.Settings[c.key].(string)
				return m.RequestID == nil && protocol.Deref(m.ChangedKey) == c.key && value == c.value && m.Settings["notebook.root.effective"] == c.effective
			})
		}
	}
}

func TestANotebookFolderInsideOrAroundAnotherIsRefusedWithNoOutpostPaired(t *testing.T) {
	w, beside := newFsWorldWithAFolderBesideIt(t)
	setup := w.App()
	defaultFolder := fsNotebookRoot(t, w)
	sideID, otherID := createProfile(setup, "Side").ID, createProfile(setup, "Other").ID
	sideApp, otherApp := w.AppOn(sideID), w.AppOn(otherID)
	outer, sideFolder := filepath.Join(beside, "work"), filepath.Join(beside, "work", "side")
	setSetting(t, sideApp, "notebook.root", sideFolder)

	otherFolder := defaultFolder
	refused := func(value string, names ...string) {
		t.Helper()
		result := notebookSetting(otherApp, "notebook.root", value)
		if protocol.Deref(result.Success) {
			t.Fatalf("setting Other's Notebook folder to %s was accepted, want a refusal naming %v", value, names)
		}
		for _, name := range names {
			if !strings.Contains(protocol.Deref(result.Error), name) {
				t.Fatalf("the refusal of %s = %q, want it to name %q", value, protocol.Deref(result.Error), name)
			}
		}
		if got := result.Settings["notebook.root.effective"]; got != otherFolder {
			t.Fatalf("the refused %s moved Other's Notebook folder from %s to %v", value, otherFolder, got)
		}
	}

	refused(filepath.Join(sideFolder, "inner"), "profile Other", filepath.Join(sideFolder, "inner"), "profile Side", sideFolder)
	refused(outer, "profile Other", outer, "profile Side", sideFolder)
	refused(filepath.Join(defaultFolder, "inner"), "profile Other", filepath.Join(defaultFolder, "inner"), "profile Default", defaultFolder)

	setSetting(t, otherApp, "notebook.root", sideFolder+"/")
	notebookEffectiveRoot(otherApp, sideFolder)
	otherFolder = sideFolder
	setSetting(t, setup, "notebook.root", filepath.Join(beside, "moved-default"))

	refused(filepath.Join(defaultFolder, "inner"), "profile Other", "the default Notebook folder", defaultFolder)
	refused(filepath.Dir(defaultFolder), "profile Other", filepath.Dir(defaultFolder), "the default Notebook folder", defaultFolder)

	setSetting(t, otherApp, "notebook.root", "")
	notebookEffectiveRoot(otherApp, defaultFolder)
}

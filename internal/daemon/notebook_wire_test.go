package daemon_test

import (
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

func notebookAskWrite(p *testworld.Peer, path, content, baseHash string) protocol.NotebookWriteResultMessage {
	p.T.Helper()
	return fsRequest[protocol.NotebookWriteResultMessage](p, protocol.EventNotebookWriteResult, func(id *string) any {
		msg := protocol.NotebookWriteMessage{Cmd: protocol.CmdNotebookWrite, RequestID: id, Path: path, Content: content}
		if baseHash != "" {
			msg.BaseHash = protocol.Ptr(baseHash)
		}
		return msg
	})
}

func notebookAskRead(p *testworld.Peer, path string) protocol.NotebookReadResultMessage {
	p.T.Helper()
	return fsRequest[protocol.NotebookReadResultMessage](p, protocol.EventNotebookReadResult, func(id *string) any {
		return protocol.NotebookReadMessage{Cmd: protocol.CmdNotebookRead, RequestID: id, Path: path}
	})
}

func notebookAskList(p *testworld.Peer, prefix string) protocol.NotebookListResultMessage {
	p.T.Helper()
	return fsRequest[protocol.NotebookListResultMessage](p, protocol.EventNotebookListResult, func(id *string) any {
		return protocol.NotebookListMessage{Cmd: protocol.CmdNotebookList, RequestID: id, Prefix: protocol.Ptr(prefix)}
	})
}

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

func notebookNote(body string) string {
	return "---\ntype: note\n---\n" + body + "\n"
}

func notebookSetting(app *testworld.Peer, key, value string) protocol.SettingsUpdatedMessage {
	app.T.Helper()
	requestID := uuid.NewString()
	return testworld.Request(app, protocol.SetSettingMessage{Cmd: protocol.CmdSetSetting, Key: key, Value: value, RequestID: protocol.Ptr(requestID)},
		protocol.EventSettingsUpdated, func(m protocol.SettingsUpdatedMessage) bool { return protocol.Deref(m.RequestID) == requestID })
}

func TestNotebookReadsListsAndBacklinks(t *testing.T) {
	w := newWorld(t)
	app := w.App()
	written := notebookAskWrite(app, "knowledge/areas/a.md", notebookNote("body"), "")
	notebookAskWrite(app, "knowledge/areas/b.md", notebookNote("see [a](/knowledge/areas/a.md)"), "")
	notebookAskWrite(app, "journal/2026-06-13.md", "---\ntype: journal\n---\nentry\n", "")

	read := notebookAskRead(app, "/knowledge/areas/a.md")
	if !read.Success || read.Result == nil || read.Result.Path != "/knowledge/areas/a.md" || read.Result.Content != notebookNote("body") || read.Result.Hash != protocol.Deref(written.Result.Hash) {
		t.Fatalf("reading a.md = %+v (%s), want its content and the hash its write returned", read.Result, protocol.Deref(read.Error))
	}
	if missing := notebookAskRead(app, "/does/not/exist.md"); missing.Success || missing.Error == nil {
		t.Fatalf("reading a missing note = %+v, want a failure", missing)
	}
	if all := notebookAskList(app, ""); !all.Success || !slices.Equal(notebookEntryPaths(all.Entries), []string{"journal/2026-06-13.md", "knowledge/areas/a.md", "knowledge/areas/b.md"}) {
		t.Fatalf("listing the notebook = %v (%s)", notebookEntryPaths(all.Entries), protocol.Deref(all.Error))
	}
	if knowledge := notebookAskList(app, "/knowledge"); !knowledge.Success || !slices.Equal(notebookEntryPaths(knowledge.Entries), []string{"knowledge/areas/a.md", "knowledge/areas/b.md"}) {
		t.Fatalf("listing /knowledge = %v, want only the notes under it", notebookEntryPaths(knowledge.Entries))
	}
	if back := notebookAskBacklinks(app, "/knowledge/areas/a.md"); !back.Success || !slices.Equal(notebookEntryPaths(back.Entries), []string{"knowledge/areas/b.md"}) {
		t.Fatalf("backlinks of a.md = %v, want b.md", notebookEntryPaths(back.Entries))
	}
}

func TestNotebookWritesAreCompareAndSwapWithNormalizedPaths(t *testing.T) {
	w := newWorld(t)
	writer, other := w.App(), w.App()

	created := notebookAskWrite(writer, "/knowledge/areas/foo.md", notebookNote("v1"), "")
	if !created.Success || created.Result == nil || created.Result.Conflict || created.Result.Hash == nil || created.Result.Path != "knowledge/areas/foo.md" {
		t.Fatalf("creating /knowledge/areas/foo.md = %+v (%s), want it saved at the normalized path", created.Result, protocol.Deref(created.Error))
	}
	if changed := testworld.Await[protocol.NotebookChangedMessage](other, protocol.EventNotebookChanged, nil); !slices.Equal(changed.Paths, []string{"knowledge/areas/foo.md"}) || changed.Origin != "ui" {
		t.Fatalf("the other app heard %+v, want the normalized path from the ui", changed)
	}
	hash := *created.Result.Hash

	if stale := notebookAskWrite(writer, "/knowledge/areas/foo.md", "x", "deadbeef"); !stale.Success || stale.Result == nil || !stale.Result.Conflict || protocol.Deref(stale.Result.CurrentHash) != hash {
		t.Fatalf("a write against a stale hash = %+v, want a conflict naming the current hash", stale.Result)
	}
	edited := notebookAskWrite(writer, "/knowledge/areas/foo.md", notebookNote("v2"), hash)
	if !edited.Success || edited.Result == nil || edited.Result.Conflict || edited.Result.Hash == nil {
		t.Fatalf("a write against the current hash = %+v", edited.Result)
	}
	if read := notebookAskRead(writer, "/knowledge/areas/foo.md"); read.Result == nil || read.Result.Hash != *edited.Result.Hash || read.Result.Content != notebookNote("v2") {
		t.Fatalf("reading after the edit = %+v, want v2 at the edit's hash", read.Result)
	}
}

func TestNotebookReportsExternalEditsButNotItsOwnWrites(t *testing.T) {
	w := newWorld(t)
	app := w.App()
	root := fsNotebookRoot(t, w)
	fsWriteFile(t, filepath.Join(root, "gone.md"), []byte(notebookNote("about to go")))
	notebookAskList(app, "")

	notebookAskWrite(app, "own.md", notebookNote("attn wrote this"), "")
	fsWriteFile(t, filepath.Join(root, "ext.md"), []byte(notebookNote("edited externally")))
	if refused := notebookAskWrite(app, "doc.md", "x", "deadbeef"); refused.Result == nil || !refused.Result.Conflict {
		t.Fatalf("a write against a hash doc.md never had = %+v, want a conflict", refused.Result)
	}
	fsWriteFile(t, filepath.Join(root, "doc.md"), []byte(notebookNote("externally created")))
	notebookAskWrite(app, "race.md", notebookNote("attn wrote this"), "")
	fsWriteFile(t, filepath.Join(root, "race.md"), []byte(notebookNote("external overwrote it")))

	ignored := []string{".attn/locks.md", "notes.txt", ".hidden.md", "knowledge/foo.md.tmp.123.456"}
	for _, rel := range ignored {
		fsWriteFile(t, filepath.Join(root, rel), []byte("x"))
	}
	fsWriteFile(t, filepath.Join(root, "knowledge", "areas", "new.md"), []byte(notebookNote("in a new folder")))
	if err := os.Remove(filepath.Join(root, "gone.md")); err != nil {
		t.Fatal(err)
	}

	heard := map[string]bool{}
	for !heard["ext.md"] || !heard["doc.md"] || !heard["race.md"] || !heard["knowledge/areas/new.md"] || !heard["gone.md"] {
		external := testworld.Await(app, protocol.EventNotebookChanged, func(m protocol.NotebookChangedMessage) bool { return m.Origin == "external" })
		for _, path := range external.Paths {
			heard[path] = true
		}
		if heard["own.md"] {
			t.Fatalf("attn's own write was reported as external: %v", external.Paths)
		}
		for _, rel := range ignored {
			if heard[rel] {
				t.Fatalf("%s is not a note but was reported as one: %v", rel, external.Paths)
			}
		}
	}
}

func TestTheNotebookGuideScaffoldsOnlyForTheChief(t *testing.T) {
	for _, c := range []struct {
		agent    fakeagent.Harness
		guidance []string
		retired  string
	}{
		{fakeagent.Claude, []string{"Never keep a blocking Monitor waiting on attn activity"}, ""},
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
			if listed := notebookAskList(app, ""); len(listed.Entries) != 0 {
				t.Fatalf("a worker's guide scaffolded %v", notebookEntryPaths(listed.Entries))
			}

			chief := configureChiefOn(t, w, app, c.agent, chiefModel(c.agent))
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
			if listed := notebookEntryPaths(notebookAskList(app, "").Entries); !slices.Contains(listed, "index.md") || !slices.Contains(listed, "log.md") || !slices.Contains(listed, "knowledge/index.md") {
				t.Fatalf("the chief's guide scaffolded %v, want index.md, log.md and knowledge/index.md", listed)
			}
		})
	}
}

func TestNotebookRootSettingIsValidatedAndItsEffectiveValueIsReadOnly(t *testing.T) {
	w := newWorld(t)
	app := w.App()
	custom := filepath.Join(w.Dir, "custom")
	if updated := notebookSetting(app, "notebook.root", "relative/path"); protocol.Deref(updated.Success) {
		t.Fatal("relative root accepted")
	}
	if updated := notebookSetting(app, "notebook.root.effective", custom); protocol.Deref(updated.Success) || !strings.Contains(protocol.Deref(updated.Error), "notebook.root.effective") {
		t.Fatalf("read-only refusal: %+v", updated)
	}
	if updated := notebookSetting(app, "notebook.root", custom); !protocol.Deref(updated.Success) {
		t.Fatal(protocol.Deref(updated.Error))
	}
	testworld.Await(app, protocol.EventSettingsUpdated, func(m protocol.SettingsUpdatedMessage) bool {
		return m.Settings["notebook.root"] == custom && m.Settings["notebook.root.effective"] == custom
	})
	if written := notebookAskWrite(app, "note.md", notebookNote("new folder"), ""); !written.Success {
		t.Fatal(protocol.Deref(written.Error))
	}
	if _, err := os.Stat(filepath.Join(custom, "note.md")); err != nil {
		t.Fatal(err)
	}
	if updated := notebookSetting(app, "notebook.root", ""); !protocol.Deref(updated.Success) {
		t.Fatal(protocol.Deref(updated.Error))
	}
	testworld.Await(app, protocol.EventSettingsUpdated, func(m protocol.SettingsUpdatedMessage) bool {
		return m.Settings["notebook.root.effective"] == filepath.Join(w.Dir, "notebook-default")
	})
}

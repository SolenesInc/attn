package daemon_test

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/victorarias/attn/internal/config"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/notebook"
	"github.com/victorarias/attn/internal/prompts"
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

func notebookAskSendToChief(p *testworld.Peer, source, selection string) protocol.NotebookSendToChiefResultMessage {
	p.T.Helper()
	return fsRequest[protocol.NotebookSendToChiefResultMessage](p, protocol.EventNotebookSendToChiefResult, func(id *string) any {
		return protocol.NotebookSendToChiefMessage{Cmd: protocol.CmdNotebookSendToChief, RequestID: id, SourcePath: protocol.Ptr(source), Selection: selection}
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
	fsNotebookRoot(t, app)
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
	fsNotebookRoot(t, writer)

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
	first := fsNotebookRoot(t, app)
	notebookAskList(app, "")

	notebookAskWrite(app, "own.md", notebookNote("attn wrote this"), "")
	fsWriteFile(t, filepath.Join(first, "ext.md"), []byte(notebookNote("edited externally")))
	if refused := notebookAskWrite(app, "doc.md", "x", "deadbeef"); refused.Result == nil || !refused.Result.Conflict {
		t.Fatalf("a write against a hash doc.md never had = %+v, want a conflict", refused.Result)
	}
	fsWriteFile(t, filepath.Join(first, "doc.md"), []byte(notebookNote("externally created")))
	notebookAskWrite(app, "race.md", notebookNote("attn wrote this"), "")
	fsWriteFile(t, filepath.Join(first, "race.md"), []byte(notebookNote("external overwrote it")))

	heard := map[string]bool{}
	for !heard["ext.md"] || !heard["doc.md"] || !heard["race.md"] {
		external := testworld.Await(app, protocol.EventNotebookChanged, func(m protocol.NotebookChangedMessage) bool { return m.Origin == "external" })
		for _, path := range external.Paths {
			heard[path] = true
		}
		if heard["own.md"] {
			t.Fatalf("attn's own write was reported as external: %v", external.Paths)
		}
	}

	second := fsDir(t, "second-notebook")
	setSetting(t, app, "notebook.root", second)
	notebookAskList(app, "")
	fsWriteFile(t, filepath.Join(first, "a.md"), []byte("# a\n"))
	fsWriteFile(t, filepath.Join(second, "b.md"), []byte("# b\n"))
	if moved := testworld.Await(app, protocol.EventNotebookChanged, func(m protocol.NotebookChangedMessage) bool {
		return m.Origin == "external" && (slices.Contains(m.Paths, "a.md") || slices.Contains(m.Paths, "b.md"))
	}); !slices.Equal(moved.Paths, []string{"b.md"}) {
		t.Fatalf("after the root moved the notebook heard %v, want only the edit under the new root", moved.Paths)
	}
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
			root := fsNotebookRoot(t, app)

			worker := w.Spawn(app, c.agent, w.Path("worker"))
			if guide, err := cli.NotebookGuide(worker); err != nil || guide.SessionIsChief || guide.Guidance == "" || guide.Root != root {
				t.Fatalf("a worker's guide = %+v, %v; want guidance under %s without the chief's role", guide, err, root)
			}
			if listed := notebookAskList(app, ""); len(listed.Entries) != 0 {
				t.Fatalf("a worker's guide scaffolded %v", notebookEntryPaths(listed.Entries))
			}

			chief := w.Spawn(app, c.agent, w.Path("chief"), func(m *protocol.SpawnSessionMessage) { m.ChiefOfStaff = protocol.Ptr(true) })
			guide, err := cli.NotebookGuide(chief)
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
	custom := fsDir(t, "custom")
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}

	for _, c := range []struct {
		key, value string
		ok         bool
		effective  string
	}{
		{"notebook.root", "relative/path", false, ""},
		{"notebook.root", filepath.Join(w.Dir, "notebook"), false, ""},
		{"notebook.root", custom, true, custom},
		{"notebook.root", "", true, notebook.DefaultRoot(home, config.Instance())},
		{"notebook.root.effective", "/tmp/whatever", false, ""},
	} {
		if updated := notebookSetting(app, c.key, c.value); protocol.Deref(updated.Success) != c.ok {
			t.Errorf("setting %s to %q succeeded=%v (%s), want %v", c.key, c.value, protocol.Deref(updated.Success), protocol.Deref(updated.Error), c.ok)
		}
		if c.effective != "" {
			testworld.Await(app, protocol.EventSettingsUpdated, func(m protocol.SettingsUpdatedMessage) bool {
				return m.RequestID == nil && protocol.Deref(m.ChangedKey) == c.key && m.Settings["notebook.root.effective"] == c.effective
			})
		}
	}
}

func TestSendToChiefAppendsToTheInboxAndRingsOnlyAReadyChief(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	root := fsNotebookRoot(t, app)
	chief := w.Spawn(app, fakeagent.Claude, w.Path("chief"), func(m *protocol.SpawnSessionMessage) { m.ChiefOfStaff = protocol.Ptr(true) })
	agent := w.Launched(chief)
	app.TypeLine(chief, "keep the notebook")
	agent.Prompted()
	agent.Reply("Ready. <!-- attn:state=idle -->")
	testworld.AwaitSession(app, chief, func(s protocol.Session) bool { return s.State == protocol.SessionStateIdle })

	sent := notebookAskSendToChief(app, "/knowledge/index.md", "remember this decision")
	if !sent.Success || sent.Result == nil || sent.Result.Path != "inbox.md" || !sent.Result.Nudged {
		t.Fatalf("sending to an idle chief = %+v (%s), want it in inbox.md and the chief nudged", sent.Result, protocol.Deref(sent.Error))
	}
	if got := agent.Prompted(); !strings.Contains(got, inboxDoorbell) {
		t.Fatalf("the idle chief was prompted with %q, want the inbox doorbell", got)
	}
	wantPrompt := prompts.RenderText("chief", "inbox", prompts.Values{"inbox_path": filepath.Join(root, "inbox.md")})
	if mail, err := cli.AgentInboxBatch(chief, 0); err != nil || len(mail.Items) != 1 || mail.Items[0].Content != wantPrompt {
		t.Fatalf("the chief's inbox = %+v, %v; want the one inbox prompt", mail, err)
	}
	if inbox := notebookAskRead(app, "inbox.md"); inbox.Result == nil || !strings.Contains(inbox.Result.Content, "> remember this decision") || !strings.Contains(inbox.Result.Content, "(/knowledge/index.md)") {
		t.Fatalf("inbox.md = %+v, want the selection blockquoted with a backlink to its source", inbox.Result)
	}

	if queued := notebookAskSendToChief(app, "/index.md", "do not interrupt"); !queued.Success || queued.Result == nil || queued.Result.Nudged {
		t.Fatalf("sending to a working chief = %+v (%s), want it queued without a nudge", queued.Result, protocol.Deref(queued.Error))
	}
	agent.Reply("Read it. <!-- attn:state=idle -->")
	if got := agent.Prompted(); !strings.Contains(got, inboxDoorbell) {
		t.Fatalf("when the chief went idle it was prompted with %q, want the queued doorbell", got)
	}
	if mail, err := cli.AgentInboxBatch(chief, 0); err != nil || len(mail.Items) != 1 || mail.Items[0].Content != wantPrompt {
		t.Fatalf("the chief's inbox after the queued doorbell = %+v, %v; want the inbox prompt", mail, err)
	}

	if err := cli.RecordNotification(chief, "permission_prompt", "Allow edit?"); err != nil {
		t.Fatal(err)
	}
	testworld.AwaitSession(app, chief, func(s protocol.Session) bool { return s.State == protocol.SessionStatePendingApproval })
	if pending := notebookAskSendToChief(app, "/index.md", "wait for approval"); !pending.Success || pending.Result == nil || pending.Result.Nudged {
		t.Fatalf("sending to a chief waiting on an approval = %+v (%s), want it kept without a nudge", pending.Result, protocol.Deref(pending.Error))
	}
}

func TestSendToChiefWithoutAChiefStillLandsAndRefusesBadSelections(t *testing.T) {
	w := newWorld(t)
	app := w.App()
	fsNotebookRoot(t, app)

	for name, selection := range map[string]string{"an empty selection": "   ", "an oversize selection": strings.Repeat("a", 32<<10+1)} {
		if refused := notebookAskSendToChief(app, "/index.md", selection); refused.Success || refused.Result != nil || refused.Error == nil {
			t.Errorf("sending %s = %+v, want a refusal", name, refused)
		}
	}
	if inbox := notebookAskRead(app, "inbox.md"); inbox.Success {
		t.Fatalf("refused selections created inbox.md: %q", inbox.Result.Content)
	}

	sent := notebookAskSendToChief(app, "/knowledge/areas/x.md", "queued for later")
	if !sent.Success || sent.Result == nil || sent.Result.Path != "inbox.md" || sent.Result.Nudged {
		t.Fatalf("sending with no chief = %+v (%s), want it in inbox.md without a nudge", sent.Result, protocol.Deref(sent.Error))
	}
	for source, selection := range map[string]string{
		"/knowledge/areas/Q3 (draft).md":           "x",
		"/knowledge/areas/a.md\n## INJECTED\nb.md": "y",
		"/index.md": "line1\r\nline2",
	} {
		if appended := notebookAskSendToChief(app, source, selection); !appended.Success {
			t.Fatalf("sending from %q: %s", source, protocol.Deref(appended.Error))
		}
	}

	inbox := notebookAskRead(app, "inbox.md").Result.Content
	for _, want := range []string{"> queued for later", "`/knowledge/areas/Q3 (draft).md`", "> line1\n> line2"} {
		if !strings.Contains(inbox, want) {
			t.Errorf("inbox.md does not hold %q:\n%s", want, inbox)
		}
	}
	for _, unwanted := range []string{"\r", "\n## INJECTED", "](/knowledge/areas/Q3"} {
		if strings.Contains(inbox, unwanted) {
			t.Errorf("inbox.md holds %q:\n%s", unwanted, inbox)
		}
	}
	if back := notebookAskBacklinks(app, "/knowledge/areas/x.md"); !slices.Equal(notebookEntryPaths(back.Entries), []string{"inbox.md"}) {
		t.Errorf("backlinks of the clean source = %v, want inbox.md", notebookEntryPaths(back.Entries))
	}
	if back := notebookAskBacklinks(app, "/knowledge/areas/Q3 (draft).md"); len(back.Entries) != 0 {
		t.Errorf("the source with special characters is linked from %v, want it shown as code only", notebookEntryPaths(back.Entries))
	}
}

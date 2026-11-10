package main_test

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/google/uuid"

	"github.com/victorarias/attn/internal/bridge"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

// Both stacks share one disk, so files are named through /proc/self/cwd: each
// daemon resolves that path inside its own working directory. The outpost's
// sent and edited files exist only in its directory, so a stat on the home
// would fail and forget them. Views apply in order, so the sent file's tile
// shows only after the home has applied the earlier edit.
func TestAnOutpostsFilesKeepTheirMachineInRecentsAndDrafts(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("gives each daemon its own file at one path through /proc/self/cwd, which only Linux has")
	}
	home, outpost := testworld.NewStack(t, testworld.WithoutAgentKit()), testworld.NewStack(t)
	files := map[*testworld.Stack]map[string]string{
		home:    {"notes.md": "# home notes\n"},
		outpost: {"notes.md": "# outpost notes\n", "sent.md": "# sent\n", "edited.md": "# edited\n", "gap-sent.md": "# sent in the gap\n", "after.md": "# after the gap\n", "after-restart.md": "# after the restart\n"},
	}
	for stack, named := range files {
		stack.Vars = append(stack.Vars, "SHELL=/bin/bash")
		stack.DaemonDir = stack.Path("daemon-dir")
		if err := os.MkdirAll(stack.DaemonDir, 0o755); err != nil {
			t.Fatal(err)
		}
		for name, body := range named {
			if err := os.WriteFile(filepath.Join(stack.DaemonDir, name), []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	pair := testworld.PrepareOutpostPair(t, home, outpost)
	pair.Start()
	app := pair.App
	shell := home.Spawn(app, fakeagent.Harness(protocol.SessionAgentShell), outpost.Path("shell"), func(m *protocol.SpawnSessionMessage) { m.Host = &pair.Host })
	terminal := protocol.TerminalID(app.Terminal(shell))
	app.TypeLine(shell, "printf '__ready_%s\\n' yes")
	app.AwaitScreen(shell, "__ready_yes")

	const notes, sent, edited = "/proc/self/cwd/notes.md", "/proc/self/cwd/sent.md", "/proc/self/cwd/edited.md"
	if err := outpost.Client().RecordFilesEdited(terminal, []string{edited}); err != nil {
		t.Fatal(err)
	}
	if err := outpost.Client().OpenSentFilesFromTerminal(terminal, []string{sent}); err != nil {
		t.Fatal(err)
	}
	awaitOutpostTileContent(app, pair.Host, "# sent\n")

	recents := func() map[string]protocol.FileActivity {
		result := testworld.Request(app, protocol.RecentFilesMessage{Cmd: protocol.CmdRecentFiles, RequestID: protocol.Ptr(uuid.NewString())},
			protocol.EventRecentFilesResult, func(r protocol.RecentFilesResultMessage) bool { return r.Success })
		byPath := map[string]protocol.FileActivity{}
		for _, file := range result.Files {
			byPath[file.Path] = file
		}
		return byPath
	}
	listed := recents()
	for _, want := range []struct{ path, source string }{{sent, "opened"}, {edited, "edited"}} {
		file, ok := listed[want.path]
		if !ok || protocol.Deref(file.Host) != pair.Host || file.Source != want.source || protocol.Deref(file.SessionID) != protocol.SessionID(shell) {
			t.Fatalf("recent %s = %+v (listed %v), want it %s on %s by %s", want.path, file, ok, want.source, pair.Host, shell)
		}
	}
	again := recents()
	for _, path := range []string{sent, edited} {
		if protocol.Deref(again[path].Host) != pair.Host {
			t.Fatalf("after the home's cleanup, recents = %+v; want %s kept on %s, never checked on the home's disk", again, path, pair.Host)
		}
	}

	app.TypeLine(shell, "attn open "+notes+" && printf '__opened_%s\\n' yes")
	app.AwaitScreen(shell, "__opened_yes")
	awaitOutpostTileContent(app, pair.Host, "# outpost notes\n")
	if err := home.Client().OpenMarkdown(notes, ""); err != nil {
		t.Fatal(err)
	}
	testworld.Await(app, protocol.EventDesktopTileContent, func(m protocol.DesktopTileContentMessage) bool {
		return m.Host == nil && m.Path == notes && m.Content == "# home notes\n"
	})

	outpostURI, homeURI := "attn://file/%2Fproc%2Fself%2Fcwd%2Fnotes.md?host="+pair.Host, "attn://file/%2Fproc%2Fself%2Fcwd%2Fnotes.md"
	documents := []struct {
		host    *string
		uri     string
		comment string
	}{
		{protocol.Ptr(pair.Host), outpostURI, "on the outpost's notes"},
		{nil, homeURI, "on the home's notes"},
	}
	for i, document := range documents {
		mark := protocol.MarkdownAnnotation{ID: uuid.NewString(), Type: "global", Text: protocol.Ptr(document.comment), CreatedAt: i + 1}
		saved := testworld.Request(app, protocol.MarkdownAnnotationsSaveMessage{Cmd: protocol.CmdMarkdownAnnotationsSave, DocumentUri: document.uri, SourceKind: "file",
			Path: protocol.Ptr(notes), Host: document.host, Upserts: []protocol.MarkdownAnnotation{mark}, RemovedIds: []string{}, RequestID: uuid.NewString()},
			protocol.EventMarkdownAnnotationsSaveResult, func(r protocol.MarkdownAnnotationsSaveResultMessage) bool { return r.DocumentUri == document.uri })
		if !saved.Success || len(saved.Annotations) != 1 {
			t.Fatalf("saving the draft %s = %+v", document.comment, saved)
		}
	}
	draftOf := func(host *string, uri string) []protocol.MarkdownAnnotation {
		got := testworld.Request(app, protocol.MarkdownAnnotationsGetMessage{Cmd: protocol.CmdMarkdownAnnotationsGet, DocumentUri: uri, SourceKind: "file",
			Path: protocol.Ptr(notes), Host: host, RequestID: uuid.NewString()},
			protocol.EventMarkdownAnnotationsGetResult, func(r protocol.MarkdownAnnotationsGetResultMessage) bool { return r.DocumentUri == uri })
		if !got.Success {
			t.Fatalf("reading the draft of %s = %+v", uri, got)
		}
		return got.Annotations
	}
	for _, document := range documents {
		if marks := draftOf(document.host, document.uri); len(marks) != 1 || protocol.Deref(marks[0].Text) != document.comment {
			t.Fatalf("the draft of %s = %+v, want only the mark made %s", document.uri, marks, document.comment)
		}
	}

	submitted := testworld.Request(app, protocol.MarkdownAnnotationsSubmitMessage{Cmd: protocol.CmdMarkdownAnnotationsSubmit, DocumentUri: outpostURI, SourceKind: "file",
		Path: protocol.Ptr(notes), Host: protocol.Ptr(pair.Host), TargetSessionID: protocol.Ptr(string(shell)), RequestID: uuid.NewString()},
		protocol.EventMarkdownAnnotationsSubmitResult, func(r protocol.MarkdownAnnotationsSubmitResultMessage) bool { return r.DocumentUri == outpostURI })
	if !submitted.Success || submitted.Status != "delivered" || len(submitted.Annotations) != 0 {
		t.Fatalf("sending the outpost's draft = %+v, want it delivered and spent", submitted)
	}
	app.AwaitScreen(shell, "notes.md on outpost")
	app.AwaitScreen(shell, "on the outpost's notes")
	if marks := draftOf(protocol.Ptr(pair.Host), outpostURI); len(marks) != 0 {
		t.Fatalf("the outpost's draft after sending = %+v, want it empty", marks)
	}
	if marks := draftOf(nil, homeURI); len(marks) != 1 || protocol.Deref(marks[0].Text) != "on the home's notes" {
		t.Fatalf("the home's draft after the outpost's was sent = %+v, want its own mark kept", marks)
	}
	const gapSent, gapEdited, after = "/proc/self/cwd/gap-sent.md", "/proc/self/cwd/gap-edited.md", "/proc/self/cwd/after.md"
	disconnectOutpost(pair)
	if err := outpost.Client().RecordFilesEdited(terminal, []string{gapEdited}); err != nil {
		t.Fatal(err)
	}
	if err := outpost.Client().OpenSentFilesFromTerminal(terminal, []string{gapSent}); err != nil {
		t.Fatal(err)
	}
	outpost.Stop()
	outpost.Start()
	mark := app.EventCount()
	pair.Reconnect()
	stopRetries := home.FireTimers(bridge.RetryTimer)
	defer stopRetries()
	outpostStateSince(app, "outpost", protocol.OutpostStateConnected, mark)
	if err := outpost.Client().OpenSentFilesFromTerminal(terminal, []string{after}); err != nil {
		t.Fatal(err)
	}
	awaitOutpostTileContent(app, pair.Host, "# after the gap\n")
	caughtUp := recents()
	for _, want := range []struct{ path, source string }{{gapSent, "opened"}, {gapEdited, "edited"}} {
		if file := caughtUp[want.path]; protocol.Deref(file.Host) != pair.Host || file.Source != want.source || file.Count != 1 {
			t.Fatalf("recent %s after the gap = %+v, want it %s once on %s", want.path, file, want.source, pair.Host)
		}
	}
	for _, e := range app.Received() {
		if e.Event == protocol.EventDesktopTileContent && protocol.Deref(e.Content) == "# sent in the gap\n" {
			t.Fatalf("a file sent while the outpost was away opened a tile when it came back: %+v", e)
		}
	}
	home.Stop()
	home.Start()
	app = home.App()
	outpostStateOnConnect(app, "outpost", protocol.OutpostStateConnected)
	if err := outpost.Client().OpenSentFilesFromTerminal(terminal, []string{"/proc/self/cwd/after-restart.md"}); err != nil {
		t.Fatal(err)
	}
	awaitOutpostTileContent(app, pair.Host, "# after the restart\n")
	replayed := recents()
	for _, path := range []string{sent, edited, gapSent, gapEdited} {
		if file := replayed[path]; protocol.Deref(file.Host) != pair.Host || file.Count != caughtUp[path].Count {
			t.Fatalf("recent %s after a home restart = %+v, want it as before the restart, %+v: the outpost's view was seen again, not reported again", path, file, caughtUp[path])
		}
	}
	app.Send(protocol.RemoveOutpostMessage{Cmd: protocol.CmdRemoveOutpost, ID: pair.Host})
	testworld.Await(app, protocol.EventOutpostsUpdated, func(m protocol.OutpostsUpdatedMessage) bool { return len(m.Outposts) == 0 })
	outpost.AwaitStopped()
	for path, file := range recents() {
		if file.Host != nil {
			t.Fatalf("recent %s = %+v after its outpost was removed, want it forgotten", path, file)
		}
	}
	if marks := draftOf(nil, homeURI); len(marks) != 1 {
		t.Fatalf("the home's draft after removing the outpost = %+v, want it kept", marks)
	}

	t.Logf("recents: %s opened and %s edited on %s, kept after the home's cleanup; %s sent and %s edited while the outpost was away arrived on reconnect after an outpost restart without a tile, and none counted again after a home restart, and all forgotten when the outpost was removed; drafts of %s on %s and on the home stayed separate, and sending the outpost's spent only its own, naming the outpost to the agent", sent, edited, pair.Host, gapSent, gapEdited, notes, pair.Host)
}

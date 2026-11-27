package daemon_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"

	"github.com/victorarias/attn/internal/bridge"
	"github.com/victorarias/attn/internal/command"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

const farHome = "/home/far"

func TestAnAsksLinksAndCodeBlocksNameTheMachineTheyAreOnAndAMisspelledMachineIsRefusedWithItsLine(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	setSetting(t, app, "asks_enabled", "true")
	far, _ := connectFarOutpost(t, w, app, answerTheFarHome)
	folder := w.Path("shop")
	asker := protocol.SessionID(w.Spawn(app, fakeagent.Claude, folder))
	ask := func(markdown string) (protocol.Ask, error) {
		var opened protocol.Ask
		err := cli.RunCommand("ask", asker, command.AskArgs{Markdown: markdown, BaseDir: folder}, &opened)
		return opened, err
	}

	opened, err := ask("Read [the plan](notes/plan.md) and [its twin](Far:~/notes/plan.md), or [the web](https://example.com/plan.md) and [the bucket](s3://shop/plan.md).\n\n" +
		"```sh far:/srv/shop\nmake test\n```\n\n:::question\nShip it?\n:::\n")
	if err != nil {
		t.Fatalf("ask: %v", err)
	}
	want := []protocol.AskReference{
		{Kind: protocol.AskReferenceKindFile, Text: "notes/plan.md", Path: filepath.Join(folder, "notes/plan.md"), Line: 1},
		{Kind: protocol.AskReferenceKindFile, Text: "Far:~/notes/plan.md", Host: protocol.Ptr(far), Path: farHome + "/notes/plan.md", Line: 1},
		{Kind: protocol.AskReferenceKindCommand, Text: "far:/srv/shop", Host: protocol.Ptr(far), Path: "/srv/shop", Line: 3},
	}
	requireReferences(t, "the opened ask", opened.References, want)
	shown := testworld.Await(app, protocol.EventAsksUpdated, func(m protocol.AsksUpdatedMessage) bool { return len(m.Asks) == 1 })
	requireReferences(t, "the app's ask", shown.Asks[0].References, want)

	var amended protocol.Ask
	if err := cli.RunCommand("ask amend", asker, command.AskAmendArgs{Ask: opened.ID, Markdown: "The log is [here](/var/log/shop.md).", BaseDir: folder}, &amended); err != nil {
		t.Fatalf("ask amend: %v", err)
	}
	want = append(want, protocol.AskReference{Kind: protocol.AskReferenceKindFile, Text: "/var/log/shop.md", Path: "/var/log/shop.md", Line: 1})
	requireReferences(t, "the amended ask", amended.References, want)
	if err := cli.RunCommand("ask amend", asker, command.AskAmendArgs{Ask: opened.ID, Markdown: "And\n[`the build`](far:/srv/build.md).", BaseDir: w.Path("elsewhere")}, &amended); err != nil {
		t.Fatalf("an amendment from another folder that links only by address: %v", err)
	}
	want = append(want, protocol.AskReference{Kind: protocol.AskReferenceKindFile, Text: "far:/srv/build.md", Host: protocol.Ptr(far), Path: "/srv/build.md", Line: 2})
	requireReferences(t, "the ask amended from another folder", amended.References, want)
	err = cli.RunCommand("ask amend", asker, command.AskAmendArgs{Ask: opened.ID, Markdown: "See [the notes](notes.md).", BaseDir: w.Path("elsewhere")}, &amended)
	if err == nil || !strings.Contains(err.Error(), "name each file the amendment links by its absolute path") {
		t.Fatalf("an amendment whose relative link names another file from where it was written: %v, want a refusal", err)
	}

	_, err = ask("Some context.\n\nRead [the plan](fra:~/notes/plan.md).\n")
	if err == nil || !strings.Contains(err.Error(), "line 3") || !strings.Contains(err.Error(), `"fra"`) || !strings.Contains(err.Error(), "home") || !strings.Contains(err.Error(), "far") {
		t.Fatalf("an ask that links fra:~/notes/plan.md: %v, want a refusal with its line and the machines", err)
	}
	t.Logf("an ask that links fra:~/notes/plan.md: %v", err)
}

func TestALinkInAnAsksExplicitSubjectNamesItsMachineAndAMisspelledMachineThereIsRefusedNamingTheSubject(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	setSetting(t, app, "asks_enabled", "true")
	far, _ := connectFarOutpost(t, w, app, answerTheFarHome)
	folder := w.Path("shop")
	asker := protocol.SessionID(w.Spawn(app, fakeagent.Claude, folder))
	ask := func(subject string) (protocol.Ask, error) {
		var opened protocol.Ask
		err := cli.RunCommand("ask", asker, command.AskArgs{Subject: subject, Markdown: "Read [the plan](notes/plan.md).\n", BaseDir: folder}, &opened)
		return opened, err
	}

	opened, err := ask("Ship [the far plan](far:~/plan.md) after [the notes](notes.md)?")
	if err != nil {
		t.Fatalf("ask: %v", err)
	}
	want := []protocol.AskReference{
		{Kind: protocol.AskReferenceKindFile, Text: "far:~/plan.md", Host: protocol.Ptr(far), Path: farHome + "/plan.md", Line: 0},
		{Kind: protocol.AskReferenceKindFile, Text: "notes.md", Path: filepath.Join(folder, "notes.md"), Line: 0},
		{Kind: protocol.AskReferenceKindFile, Text: "notes/plan.md", Path: filepath.Join(folder, "notes/plan.md"), Line: 1},
	}
	requireReferences(t, "the opened ask", opened.References, want)
	shown := testworld.Await(app, protocol.EventAsksUpdated, func(m protocol.AsksUpdatedMessage) bool { return len(m.Asks) == 1 })
	requireReferences(t, "the app's ask", shown.Asks[0].References, want)

	_, err = ask("Ship [the far plan](fra:~/plan.md)?")
	if err == nil || !strings.Contains(err.Error(), "the subject") || strings.Contains(err.Error(), "line ") || !strings.Contains(err.Error(), `"fra"`) || !strings.Contains(err.Error(), "home") || !strings.Contains(err.Error(), "far") {
		t.Fatalf("an ask whose subject links fra:~/plan.md: %v, want a refusal that names the subject and the machines", err)
	}
	t.Logf("an ask whose subject links fra:~/plan.md: %v", err)
}

func requireReferences(t *testing.T, where string, got, want []protocol.AskReference) {
	t.Helper()
	gotJSON, _ := json.Marshal(got)
	wantJSON, _ := json.Marshal(want)
	if string(gotJSON) != string(wantJSON) {
		t.Fatalf("%s references\n%s\nwant\n%s", where, gotJSON, wantJSON)
	}
	t.Logf("%s references %s", where, gotJSON)
}

// answerTheFarHome answers as an outpost whose home folder is farHome.
func answerTheFarHome(_ *bridge.Conn, incoming ssh.NewChannel) {
	bridge.Answer(incoming, func(_ context.Context, raw []byte) (any, error) {
		var request struct{ Cmd string }
		if err := json.Unmarshal(raw, &request); err != nil {
			return nil, err
		}
		if request.Cmd == protocol.CmdTerminalShell {
			return protocol.TerminalShellResult{Shell: "/bin/sh", Home: farHome}, nil
		}
		return struct{}{}, nil
	})
}

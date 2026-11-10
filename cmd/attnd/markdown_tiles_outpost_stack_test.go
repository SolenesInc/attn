package main_test

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

// Both stacks share one disk, so the file is opened through /proc/self/cwd:
// each daemon resolves that path inside its own working directory, which
// gives one path with different content on each machine.
func TestAMarkdownTileFromAnOutpostShowsTheOutpostsFile(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("gives each daemon its own file at one path through /proc/self/cwd, which only Linux has")
	}
	home, outpost := testworld.NewStack(t, testworld.WithoutAgentKit()), testworld.NewStack(t)
	files := map[*testworld.Stack]map[string][]byte{
		home:    {"notes.md": []byte("# home notes\n"), "chart.png": []byte("home image")},
		outpost: {"notes.md": []byte("# outpost notes\n\n![chart](chart.png)\n"), "chart.png": []byte("outpost image"), "sent.md": []byte("# sent from the outpost\n")},
	}
	for stack, named := range files {
		stack.Vars = append(stack.Vars, "SHELL=/bin/bash")
		stack.DaemonDir = stack.Path("daemon-dir")
		if err := os.MkdirAll(stack.DaemonDir, 0o755); err != nil {
			t.Fatal(err)
		}
		for name, body := range named {
			if err := os.WriteFile(filepath.Join(stack.DaemonDir, name), body, 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	pair := testworld.PrepareOutpostPair(t, home, outpost)
	pair.Start()
	app := pair.App
	shell := home.Spawn(app, fakeagent.Harness(protocol.SessionAgentShell), outpost.Path("shell"), func(m *protocol.SpawnSessionMessage) { m.Host = &pair.Host })

	const notes = "/proc/self/cwd/notes.md"
	app.TypeLine(shell, "attn open "+notes+" && printf '__opened_%s\\n' yes")
	app.AwaitScreen(shell, "__opened_yes")
	opened := awaitOutpostTileContent(app, pair.Host, "# outpost notes\n\n![chart](chart.png)\n")
	if opened.Path != notes {
		t.Fatalf("the tile shows %s, want %s", opened.Path, notes)
	}

	image := testworld.Request(app, protocol.TileAssetReadMessage{Cmd: protocol.CmdTileAssetRead, Host: protocol.Ptr(pair.Host), Path: "/proc/self/cwd/chart.png", RequestID: protocol.Ptr(uuid.NewString())},
		protocol.EventTileAssetReadResult, func(r protocol.TileAssetReadResultMessage) bool { return r.RequestID != nil })
	want := "data:image/png;base64," + base64.StdEncoding.EncodeToString([]byte("outpost image"))
	if !image.Success || protocol.Deref(image.DataUri) != want {
		t.Fatalf("the relative image loaded %+v, want the outpost's %s", image, want)
	}

	if err := os.WriteFile(filepath.Join(outpost.DaemonDir, "notes.md"), []byte("# outpost notes, edited\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	awaitOutpostTileContent(app, pair.Host, "# outpost notes, edited\n")

	if err := outpost.Client().OpenSentFilesFromTerminal(protocol.TerminalID(app.Terminal(shell)), []string{"/proc/self/cwd/sent.md"}); err != nil {
		t.Fatal(err)
	}
	awaitOutpostTileContent(app, pair.Host, "# sent from the outpost\n")

	for _, e := range app.Received() {
		if e.Event == protocol.EventDesktopTileContent && strings.Contains(protocol.Deref(e.Content), "home notes") {
			t.Fatalf("an outpost tile showed the home's file at the same path: %+v", e)
		}
	}

	if err := home.Client().OpenMarkdown(notes, ""); err != nil {
		t.Fatal(err)
	}
	testworld.Await(app, protocol.EventDesktopTileContent, func(m protocol.DesktopTileContentMessage) bool {
		return m.Host == nil && m.Path == notes && m.Content == "# home notes\n"
	})
	t.Logf("tile %s shows %s from %s; its image, its edit and a sent file came from the outpost, and the same path opened at home shows the home's file", opened.TileID, opened.Path, pair.Host)
}

func awaitOutpostTileContent(app *testworld.Peer, host, content string) protocol.DesktopTileContentMessage {
	app.T.Helper()
	return testworld.Await(app, protocol.EventDesktopTileContent, func(m protocol.DesktopTileContentMessage) bool {
		return protocol.Deref(m.Host) == host && m.Content == content
	})
}

package daemon_test

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/pausepoint"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestAFileGivenToAViewOnAnOutpostOutlivesAHomeRestartAndTheAgentThereSavesItThroughItsLine(t *testing.T) {
	home := testworld.NewStack(t)
	outpost := testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude))
	recovered := home.PauseEvery(pausepoint.OutpostRecoveryApplied)
	pair := testworld.PrepareOutpostPair(t, home, outpost)
	pair.Start()
	recovered.Await()
	recovered.Release()
	app := pair.App
	awaitOutpostInSync(app, pair.Host)
	setSetting(t, app, "views_enabled", "true")
	asker := home.Spawn(app, fakeagent.Claude, outpost.Path("shop"), func(m *protocol.SpawnSessionMessage) { m.Host = protocol.Ptr(pair.Host) })
	terminal := app.Terminal(asker)
	agent := outpost.LaunchedCarrying(terminal)
	app.TypeLine(asker, "look at my bug")
	agent.Prompted()

	folder := outpost.Path("bug")
	view := filepath.Join(folder, "view.md")
	writeViewFolder(t, folder, map[string]string{"view.md": bugReportView})
	if problems, err := outpost.Client().OpenMarkdown(view, protocol.SessionID(asker)); err != nil || len(problems) != 0 {
		t.Fatalf("the agent on the outpost opened the view with problems %+v, error %v; want none", problems, err)
	}
	opened := awaitViewTile(app, view, func(m protocol.DesktopTileContentMessage) bool {
		return protocol.Deref(m.Host) == pair.Host && m.Answers != nil
	})
	tile := viewTileOf{desktop: opened.DesktopID, tile: opened.TileID}
	agent.Reply("Opened the view for you. <!-- attn:state=waiting_input -->")
	testworld.AwaitSession(app, asker, func(s protocol.Session) bool { return protocol.Deref(s.TurnOwed) })

	screenshot := quickCapturePNG(t)
	chosen := giveViewFile(t, home.TrustedApp(), app, tile, opened.Answers.Owner, "broken screen.png", screenshot)
	home.Stop()
	home.Start()
	recovered.Await()
	recovered.Release()
	app = home.App()
	restored := awaitViewTile(app, view, func(m protocol.DesktopTileContentMessage) bool { return m.Answers != nil })
	if len(restored.Answers.Files) != 1 || restored.Answers.Files[0].ID != chosen {
		t.Fatalf("after the home restarted the tile holds the files %+v, want the screenshot", restored.Answers.Files)
	}

	sent := sendView(t, app, tile)
	if !sent.Told || sent.Responses != filepath.Join(folder, "view.responses.json") {
		t.Fatalf("sending answered %+v, want the agent told and the responses on the outpost beside the view", sent)
	}
	line := screenshotLine(t, sent.Responses)
	found := savesAttachment.FindStringSubmatch(line)
	if found == nil {
		t.Fatalf("the screenshot's line %q on the outpost gives no command that saves it", line)
	}
	saved := outpost.Run(testworld.Invocation{Args: []string{"agent", "attachment", found[1], "--out", "screen.png"}, Terminal: terminal, Dir: outpost.Path("shop")})
	if saved.Code != 0 {
		t.Fatalf("the agent on the outpost saving the screenshot exited %d: %s", saved.Code, saved.Stderr)
	}
	if data, err := os.ReadFile(outpost.Path("shop") + "/screen.png"); err != nil || !bytes.Equal(data, screenshot) {
		t.Errorf("the agent on the outpost saved %d bytes, %v; want the screenshot", len(data), err)
	}
	typed := agent.Prompted()
	t.Logf("typed into the agent on the outpost: %q", typed)
}

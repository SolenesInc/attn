package daemon_test

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/pausepoint"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func awaitOutpostInSync(app *testworld.Peer, host string) {
	app.T.Helper()
	testworld.Await(app, protocol.EventOutpostsUpdated, func(m protocol.OutpostsUpdatedMessage) bool {
		return slices.ContainsFunc(m.Outposts, func(row protocol.OutpostInfo) bool {
			return row.ID == host && row.State == protocol.OutpostStateConnected && row.Notebook != nil && row.Notebook.State == protocol.OutpostNotebookStateInSync
		})
	})
}

func TestAViewOnAnOutpostKeepsItsAnswersThroughAHomeRestartAndSendsThemToTheAgentThere(t *testing.T) {
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
	agent := outpost.LaunchedCarrying(app.Terminal(asker))
	app.TypeLine(asker, "check my transcripts")
	agent.Prompted()

	folder := outpost.Path("check")
	view := filepath.Join(folder, "view.md")
	writeViewFolder(t, folder, map[string]string{"view.md": twelveClipsView, "clips.jsonl": twelveClips()})
	if problems, err := outpost.Client().OpenMarkdown(view, protocol.SessionID(asker)); err != nil || len(problems) != 0 {
		t.Fatalf("the agent on the outpost opened the view with problems %+v, error %v; want none", problems, err)
	}
	opened := awaitViewTile(app, view, func(m protocol.DesktopTileContentMessage) bool { return protocol.Deref(m.Host) == pair.Host })
	tile := viewTileOf{desktop: opened.DesktopID, tile: opened.TileID}
	agent.Reply("Opened the view for you. <!-- attn:state=waiting_input -->")
	testworld.AwaitSession(app, asker, func(s protocol.Session) bool { return protocol.Deref(s.TurnOwed) })

	before, after := twelveClipAnswers()
	for _, answer := range before {
		answerView(t, app, tile, answer)
	}
	home.Stop()
	home.Start()
	recovered.Await()
	recovered.Release()
	app = home.App()
	restored := awaitViewTile(app, view, func(m protocol.DesktopTileContentMessage) bool { return m.Answers != nil })
	if got, want := draftsByKey(restored.Answers.Drafts), draftsByKey(before); !reflect.DeepEqual(got, want) {
		t.Fatalf("after the home restarted the tile holds the answers %+v, want the six given before it %+v", got, want)
	}
	for _, answer := range after {
		answerView(t, app, tile, answer)
	}

	sent := sendView(t, app, tile)
	responses := filepath.Join(folder, "view.responses.json")
	if sent.Responses != responses || !sent.Told {
		t.Fatalf("sending answered %+v, want the agent told and the responses at %s on the outpost", sent, responses)
	}
	if got, want := readResponses(t, responses), wantTwelveClipResponses(view); !reflect.DeepEqual(got, want) {
		gotJSON, _ := json.MarshalIndent(got, "", "  ")
		wantJSON, _ := json.MarshalIndent(want, "", "  ")
		t.Errorf("the responses file on the outpost reads\n%s\nwant\n%s", gotJSON, wantJSON)
	}
	typed := agent.Prompted()
	t.Logf("typed into the agent on the outpost: %q", typed)
	if want := twelveClipsSummary + "\nResponses: " + responses; !strings.Contains(typed, want) {
		t.Errorf("the agent on the outpost was typed %q, want %q", typed, want)
	}
}

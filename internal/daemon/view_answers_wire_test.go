package daemon_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/google/uuid"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

const twelveClipsView = `# Check 12 transcripts

::::one-by-one clips.jsonl
![{id}]({file})

:::question-text transcript
Is this what you said?
Value: {transcript}
:::
::::

:::question-multi platforms
Which platforms did you record on?
- [ ] macOS
- [ ] Linux
:::
`

func twelveClips() string {
	var lines []string
	for n := 1; n <= 12; n++ {
		lines = append(lines, fmt.Sprintf(`{"id": "clip-%02d", "file": "clip-%02d.wav", "transcript": "what clip %d said"}`, n, n, n))
	}
	return strings.Join(lines, "\n") + "\n"
}

type viewTileOf struct{ desktop, tile string }

func awaitViewTile(app *testworld.Peer, path string, holds func(protocol.DesktopTileContentMessage) bool) protocol.DesktopTileContentMessage {
	app.T.Helper()
	return testworld.Await(app, protocol.EventDesktopTileContent, func(m protocol.DesktopTileContentMessage) bool {
		return m.Path == path && m.View != nil && holds(m)
	})
}

func answerView(t *testing.T, app *testworld.Peer, tile viewTileOf, answer protocol.ViewAnswer) {
	t.Helper()
	requestID := uuid.NewString()
	result := testworld.Request(app, protocol.ViewAnswerMessage{Cmd: protocol.CmdViewAnswer, RequestID: requestID, DesktopID: tile.desktop, TileID: tile.tile, Answer: answer},
		protocol.EventViewAnswerResult, func(r protocol.ViewAnswerResultMessage) bool { return r.RequestID == requestID })
	if !result.Success {
		t.Fatalf("answering %+v: %s", answer, protocol.Deref(result.Error))
	}
}

func sendView(t *testing.T, app *testworld.Peer, tile viewTileOf) protocol.ViewSent {
	t.Helper()
	requestID := uuid.NewString()
	result := testworld.Request(app, protocol.ViewSendMessage{Cmd: protocol.CmdViewSend, RequestID: requestID, DesktopID: tile.desktop, TileID: tile.tile},
		protocol.EventViewSendResult, func(r protocol.ViewSendResultMessage) bool { return r.RequestID == requestID })
	if !result.Success || result.Sent == nil {
		t.Fatalf("sending the view: %s", protocol.Deref(result.Error))
	}
	return *result.Sent
}

func kept(row string) protocol.ViewAnswer {
	return protocol.ViewAnswer{Row: protocol.Ptr(row), Step: protocol.Ptr(protocol.ViewStepKept)}
}

func skipped(row string) protocol.ViewAnswer {
	return protocol.ViewAnswer{Row: protocol.Ptr(row), Step: protocol.Ptr(protocol.ViewStepSkipped)}
}

func transcribed(row, text string) protocol.ViewAnswer {
	return protocol.ViewAnswer{Part: protocol.Ptr("transcript"), Row: protocol.Ptr(row), Text: protocol.Ptr(text)}
}

// twelveClipAnswers is how the user goes through the twelve clips: seven kept,
// four changed and one skipped, the first six before the restart.
func twelveClipAnswers() (before, after []protocol.ViewAnswer) {
	for n := 1; n <= 12; n++ {
		row := fmt.Sprintf("clip-%02d", n)
		var given []protocol.ViewAnswer
		switch n {
		case 3, 5, 9, 11:
			given = []protocol.ViewAnswer{transcribed(row, fmt.Sprintf("what clip %d really said", n)), kept(row)}
		case 7:
			given = []protocol.ViewAnswer{skipped(row)}
		default:
			given = []protocol.ViewAnswer{kept(row)}
		}
		if n <= 6 {
			before = append(before, given...)
		} else {
			after = append(after, given...)
		}
	}
	after = append(after,
		protocol.ViewAnswer{Part: protocol.Ptr("platforms"), Picked: []string{"Linux"}},
		protocol.ViewAnswer{Part: protocol.Ptr("a question the view no longer asks"), Picked: []string{"Yes"}},
	)
	return before, after
}

func draftsByKey(drafts []protocol.ViewAnswer) map[string]protocol.ViewAnswer {
	keyed := map[string]protocol.ViewAnswer{}
	for _, draft := range drafts {
		keyed[protocol.Deref(draft.Row)+"/"+protocol.Deref(draft.Part)] = draft
	}
	return keyed
}

const twelveClipsSummary = "The user sent \"Check 12 transcripts\": 7 kept, 4 changed (clip-03, clip-05, clip-09, clip-11), 1 skipped (clip-07), 1 answer besides."

func wantTwelveClipResponses(path string) map[string]any {
	rows := map[string]any{}
	for n := 1; n <= 12; n++ {
		row := fmt.Sprintf("clip-%02d", n)
		switch n {
		case 3, 5, 9, 11:
			rows[row] = map[string]any{"verdict": "changed", "answers": map[string]any{"transcript": fmt.Sprintf("what clip %d really said", n)}}
		case 7:
			rows[row] = map[string]any{"verdict": "skipped", "answers": map[string]any{}}
		default:
			rows[row] = map[string]any{"verdict": "kept", "answers": map[string]any{}}
		}
	}
	return map[string]any{"view": "Check 12 transcripts", "path": path, "answers": map[string]any{"platforms": []any{"Linux"}}, "rows": rows}
}

func readResponses(t *testing.T, path string) map[string]any {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("the responses file: %v", err)
	}
	var responses map[string]any
	if err := json.Unmarshal(content, &responses); err != nil {
		t.Fatalf("the responses file is not JSON: %v\n%s", err, content)
	}
	if sentAt, _ := responses["sent_at"].(string); sentAt == "" {
		t.Errorf("the responses file says no sent_at: %s", content)
	}
	delete(responses, "sent_at")
	return responses
}

func TestAViewsAnswersOutliveARestartAndSendingThemTypesOneLineIntoTheAgentThatOpenedIt(t *testing.T) {
	inBubbleWithAgents(t, func(t *testing.T, w *world) {
		app, cli := w.App(), w.Client()
		setSetting(t, app, "views_enabled", "true")
		asker := w.bubbleClaude(t, app, "shop")
		folder := w.Path("check")
		view := filepath.Join(folder, "view.md")
		writeViewFolder(t, folder, map[string]string{"view.md": twelveClipsView, "clips.jsonl": twelveClips()})
		if problems, err := cli.OpenMarkdown(view, protocol.SessionID(asker.id)); err != nil || len(problems) != 0 {
			t.Fatalf("opening the view answered problems %+v, error %v; want none", problems, err)
		}
		opened := awaitViewTile(app, view, func(protocol.DesktopTileContentMessage) bool { return true })
		tile := viewTileOf{desktop: opened.DesktopID, tile: opened.TileID}
		app.TypeLine(asker.id, "check my transcripts")
		asker.term.Heartbeat("busy", "Claude Code")
		w.advance(time.Second)
		asker.reply("Opened the view for you. <!-- attn:state=waiting_input -->")
		if s := queriedSession(t, asker.cli, asker.id); !protocol.Deref(s.TurnOwed) {
			t.Fatalf("after opening the view the agent is %s with turn owed %v, want the turn open for the user", s.State, protocol.Deref(s.TurnOwed))
		}

		before, after := twelveClipAnswers()
		for _, answer := range before {
			answerView(t, app, tile, answer)
		}
		w.restart()
		app = w.App()
		asker.cli = w.Client()
		w.advance(0)
		restored := awaitViewTile(app, view, func(m protocol.DesktopTileContentMessage) bool { return m.Answers != nil })
		if got, want := draftsByKey(restored.Answers.Drafts), draftsByKey(before); !reflect.DeepEqual(got, want) {
			t.Fatalf("after a restart the tile holds the answers %+v, want the six given before it %+v", got, want)
		}
		if restored.Answers.Sent != nil {
			t.Fatalf("after a restart the tile says the view was sent: %+v", restored.Answers.Sent)
		}
		for _, answer := range after {
			answerView(t, app, tile, answer)
		}

		sent := sendView(t, app, tile)
		responses := filepath.Join(folder, "view.responses.json")
		if sent.Responses != responses || !sent.Told {
			t.Fatalf("sending answered %+v, want the agent told and the responses at %s", sent, responses)
		}
		if got, want := readResponses(t, responses), wantTwelveClipResponses(view); !reflect.DeepEqual(got, want) {
			gotJSON, _ := json.MarshalIndent(got, "", "  ")
			wantJSON, _ := json.MarshalIndent(want, "", "  ")
			t.Errorf("the responses file reads\n%s\nwant\n%s", gotJSON, wantJSON)
		}
		synctest.Wait()
		summary := twelveClipsSummary + "\nResponses: " + responses
		if got := asker.promptsContaining(summary); got != 1 {
			t.Errorf("the summary was typed %d times into the agent, want once: %q", got, asker.prompts())
		}
		t.Logf("typed into the agent: %q", asker.prompts())
		if s := queriedSession(t, asker.cli, asker.id); protocol.Deref(s.TurnOwed) {
			t.Errorf("after the user sent the view the agent still owes the user its turn")
		}
		shown := awaitViewTile(app, view, func(m protocol.DesktopTileContentMessage) bool { return m.Answers != nil && m.Answers.Sent != nil })
		if !shown.Answers.Sent.Told || shown.Answers.Sent.Responses != responses || len(shown.Answers.Drafts) != len(before)+len(after) {
			t.Errorf("after sending the tile holds %+v, want the answers kept and the send told", shown.Answers)
		}
	})
}

func TestSendingAViewWhoseAgentIsGoneWritesTheResponsesAndSaysTheAgentWasNotTold(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	setSetting(t, app, "views_enabled", "true")
	asker := w.Spawn(app, fakeagent.Claude, w.Path("shop"))
	w.Launched(asker)
	folder := w.Path("check")
	view := filepath.Join(folder, "view.md")
	writeViewFolder(t, folder, map[string]string{"view.md": twelveClipsView, "clips.jsonl": twelveClips()})
	if _, err := cli.OpenMarkdown(view, protocol.SessionID(asker)); err != nil {
		t.Fatal(err)
	}
	opened := awaitViewTile(app, view, func(protocol.DesktopTileContentMessage) bool { return true })
	tile := viewTileOf{desktop: opened.DesktopID, tile: opened.TileID}
	closeSession(t, cli, asker, "done checking")
	awaitClosed(app, asker)

	answerView(t, app, tile, transcribed("clip-01", "what clip 1 really said"))
	sent := sendView(t, app, tile)
	responses := filepath.Join(folder, "view.responses.json")
	if sent.Told || sent.Responses != responses {
		t.Fatalf("sending a view whose agent is gone answered %+v, want the responses at %s and the agent not told", sent, responses)
	}
	if got := readResponses(t, responses)["rows"]; !reflect.DeepEqual(got, map[string]any{"clip-01": map[string]any{"verdict": "changed", "answers": map[string]any{"transcript": "what clip 1 really said"}}}) {
		t.Errorf("the responses' rows are %v, want clip-01 changed", got)
	}
}

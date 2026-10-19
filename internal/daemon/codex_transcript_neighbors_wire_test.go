package daemon_test

import (
	"fmt"
	"github.com/victorarias/attn/internal/client"
	"os"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestCodexSessionsSharingADirectoryEachFollowTheirOwnConversation(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	app := w.App()
	cwd := w.Path("shop")
	turn := func(session string, run *fakeagent.Run, prompt, reply string, want protocol.SessionState) {
		t.Helper()
		app.TypeLine(session, prompt)
		run.Prompted()
		run.Reply(reply + " <!-- attn:state=" + string(want) + " -->")
		testworld.AwaitSession(app, session, func(s protocol.Session) bool {
			return s.State == want && protocol.Deref(s.StateReason) == "classifier_verdict"
		})
	}

	older := w.Spawn(app, fakeagent.Codex, cwd)
	olderRun := w.Launched(older)
	turn(older, olderRun, "rename the checkout module", "Keep the old import path as an alias?", protocol.SessionStateWaitingInput)
	newer := w.Spawn(app, fakeagent.Codex, cwd)
	newerRun := w.Launched(newer)
	turn(newer, newerRun, "add a discount field to checkout", "Added the discount field.", protocol.SessionStateIdle)

	if state := queriedSession(t, w.Client(), older).State; state != protocol.SessionStateWaitingInput {
		t.Fatalf("the newer neighbor's reply moved the older session to %s", state)
	}
	turn(older, olderRun, "yes, keep the alias", "Renamed. Should the alias warn on import?", protocol.SessionStateWaitingInput)
	if state := queriedSession(t, w.Client(), newer).State; state != protocol.SessionStateIdle {
		t.Fatalf("the older session's reply moved the newer neighbor to %s", state)
	}
}

func TestAMissingBoundTranscriptNeverUsesANeighborsReply(t *testing.T) {
	inBubbleWithAgents(t, func(t *testing.T, w *world) {
		app, cli := w.App(), w.Client()
		cwd := w.Path("shop")
		makeConversation := func(native string) (string, string) {
			id := w.Spawn(app, fakeagent.Codex, cwd)
			path := w.Path(native + ".jsonl")
			data := fmt.Sprintf(`{"timestamp":"2026-10-18T12:00:00Z","type":"session_meta","payload":{"id":%q,"cwd":%q,"source":"cli"}}
{"timestamp":"2026-10-18T12:00:01Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"Should I delete everything? <!-- attn:state=waiting_input -->"}]}}
`, native, cwd)
			if err := os.WriteFile(path, []byte(data), 0600); err != nil {
				t.Fatal(err)
			}
			if err := cli.ObserveAgentConversation(protocol.TerminalID(w.Terminal(id)), native, path); err != nil {
				t.Fatal(err)
			}
			return id, path
		}
		older, oldPath := makeConversation("own-conversation")
		neighbor, neighborPath := makeConversation("neighbor-conversation")
		if read, err := cli.SessionTranscript(neighbor, ""); err != nil || len(read.Events) == 0 {
			t.Fatalf("neighbor fixture is unreadable: %+v %v", read, err)
		}
		if err := cli.UpdateState(protocol.TerminalID(w.Terminal(older)), protocol.StateWorking); err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(oldPath); err != nil {
			t.Fatal(err)
		}
		if _, err := cli.SessionTranscript(older, ""); err == nil || !strings.Contains(err.Error(), "transcript_unavailable") {
			t.Fatalf("missing own transcript resolved to a neighbor: %v", err)
		}
		if err := cli.SendStop(protocol.TerminalID(w.Terminal(older)), neighborPath, client.StopFacts{}); err != nil {
			t.Fatal(err)
		}
		w.advance(0)
		explained := stateExplainOf(t, cli, older)
		stateExplainShows(t, explained.Observations, stateExplainRow{source: "classifier", claim: protocol.StateUnknown, outcome: "observed", detail: "transcript_parse_error"})
	})
}

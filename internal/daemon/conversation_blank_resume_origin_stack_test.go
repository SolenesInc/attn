package daemon_test

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestABlankCodexTileKeepsItsLaterConversationAcrossARestart(t *testing.T) {
	for _, mode := range []struct {
		name   string
		shared bool
	}{{"shared", true}, {"plain", false}} {
		t.Run(mode.name, func(t *testing.T) {
			s := testworld.NewStack(t, testworld.WithAgents(fakeagent.Codex))
			s.Start()
			app := s.App()
			if mode.shared {
				setSetting(t, app, "codex_shared_enabled", "true")
			}
			first := s.Spawn(app, fakeagent.Codex, s.Path("shop"))
			firstAgent := s.Launched(first)
			firstTerminal := app.Terminal(first)
			app.TypeLine(first, "find the checkout race")
			firstAgent.Prompted()
			original := firstAgent.ConversationID
			firstAgent.Reply("Ready. <!-- attn:state=waiting_input -->")
			testworld.AwaitSession(app, first, func(x protocol.Session) bool { return x.State == protocol.SessionStateWaitingInput })
			app.TypeLine(first, "/new")
			firstAgent.Prompted()
			typeInto(app, firstTerminal, "leave the original conversation\r")
			firstAgent.Prompted()
			if mode.shared {
				testworld.AwaitSession(app, first, func(x protocol.Session) bool { return protocol.Deref(x.Hidden) })
			} else {
				awaitClosed(app, first)
			}

			blank := s.Spawn(app, fakeagent.Codex, s.Path("shop"))
			resumed := s.Launched(blank)
			terminal := app.Terminal(blank)
			resuming := len(app.Log())
			app.TypeLine(blank, "/resume "+original)
			resumed.Prompted()
			typeInto(app, terminal, "continue the original conversation\r")
			resumed.Prompted()
			testworld.AwaitSince(app, resuming, protocol.EventSessionRegistered, func(e protocol.WebSocketEvent) bool {
				return e.Session != nil && string(e.Session.ID) == first
			})
			awaitClosed(app, blank)
			resumed.Reply("Resumed. <!-- attn:state=waiting_input -->")
			testworld.AwaitSessionSince(app, resuming, first, func(x protocol.Session) bool { return x.State == protocol.SessionStateWaitingInput })
			transcripts, err := filepath.Glob(filepath.Join(s.Dir, "toolhome", ".codex", "sessions", "*", "*", "*", "*-"+original+".jsonl"))
			if err != nil || len(transcripts) != 1 {
				t.Fatalf("original conversation transcripts = %v, error = %v", transcripts, err)
			}
			if err := s.Client().ObserveAgentConversation(protocol.TerminalID(terminal), original, transcripts[0]); err != nil {
				t.Fatal(err)
			}
			mark := len(app.Log())
			typeInto(app, terminal, "/new\r")
			resumed.Prompted()
			typeInto(app, terminal, "continue the new conversation\r")
			resumed.Prompted()
			nextConversation := resumed.ConversationID
			var next string
			testworld.AwaitSince(app, mark, protocol.EventProfileArrangementChanged, func(e protocol.ProfileArrangementChangedMessage) bool {
				for _, desktop := range e.Desktops {
					for _, pane := range desktop.Panes {
						if string(pane.RuntimeID) == terminal && string(pane.SessionID) != first {
							next = string(pane.SessionID)
							return true
						}
					}
				}
				return false
			})
			resumed.Reply("Before restart. <!-- attn:state=waiting_input -->")
			before := testworld.AwaitSessionSince(app, mark, next, func(x protocol.Session) bool {
				return x.State == protocol.SessionStateWaitingInput && protocol.Deref(x.TurnOwed)
			})
			s.Stop()
			s.Start()
			app = s.App()
			mark = len(app.Log())
			typeInto(app, terminal, "continue after restart\r")
			if got := resumed.Prompted(); got != "continue after restart" || resumed.ConversationID != nextConversation {
				t.Fatalf("restarted terminal took %q in %s, want the prompt in %s", got, resumed.ConversationID, nextConversation)
			}
			testworld.AwaitSessionSince(app, mark, next, func(x protocol.Session) bool {
				return x.State == protocol.SessionStateWorking && protocol.Deref(x.LastModelRequestAt) != protocol.Deref(before.LastModelRequestAt)
			})
			replyMark := len(app.Log())
			resumed.Reply("After restart. <!-- attn:state=waiting_input -->")
			after := testworld.AwaitSessionSince(app, replyMark, next, func(x protocol.Session) bool {
				return x.State == protocol.SessionStateWaitingInput && protocol.Deref(x.TurnOwed) &&
					protocol.Deref(x.LastModelRequestAt) != protocol.Deref(before.LastModelRequestAt)
			})
			checkArrangement := func(desktops []protocol.Desktop) {
				t.Helper()
				for _, desktop := range desktops {
					for _, pane := range desktop.Panes {
						if string(pane.RuntimeID) == terminal && string(pane.SessionID) != next {
							t.Fatalf("restart moved terminal %s from %s to %s", terminal, next, pane.SessionID)
						}
					}
				}
			}
			checkArrangement(app.Initial.Desktops)
			for _, raw := range app.Log() {
				var e protocol.ProfileArrangementChangedMessage
				if err := json.Unmarshal(raw, &e); err != nil {
					t.Fatal(err)
				}
				if e.Event == protocol.EventProfileArrangementChanged {
					checkArrangement(e.Desktops)
				}
			}
			if got := app.Terminal(next); got != terminal {
				t.Fatalf("after the restarted turn session %s is shown in %s, want %s", next, got, terminal)
			}
			t.Logf("restarted terminal=%s session=%s conversation=%s model_request=%s", terminal, next, nextConversation, protocol.Deref(after.LastModelRequestAt))
		})
	}
}

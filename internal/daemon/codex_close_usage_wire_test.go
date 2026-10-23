package daemon_test

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestClosingSharedCodexKeepsFinalDescendantUsageWithUnrelatedHistory(t *testing.T) {
	for _, visibility := range []string{"visible", "hidden", "hidden-redelivered"} {
		t.Run(visibility, func(t *testing.T) {
			w := newWorld(t, fakeagent.Codex)
			app := w.App()
			setSetting(t, app, "codex_shared_enabled", "true")
			session := w.Spawn(app, fakeagent.Codex, w.Path("shop"))
			codex := w.Launched(session)
			app.TypeLine(session, "price the cart")
			codex.Prompted()
			first := "Priced. <!-- attn:state=idle -->"
			codex.Reply(first)
			awaitUsageTokens(app, session, claudeTokens(first))

			conversation := codex.ConversationID
			home := filepath.Join(w.Dir, "toolhome", ".codex")
			old := filepath.Join(home, "sessions", "2020", "01", "01")
			archive := filepath.Join(home, "archived_sessions")
			for _, dir := range []string{old, archive} {
				if err := os.MkdirAll(dir, 0o700); err != nil {
					t.Fatal(err)
				}
				for i := range 1024 {
					name := fmt.Sprintf("rollout-2020-01-01T00-00-00-unrelated-%d.jsonl", i)
					meta := fmt.Sprintf("{\"type\":\"session_meta\",\"payload\":{\"id\":\"unrelated-%d\",\"source\":\"cli\"}}\n", i)
					if err := os.WriteFile(filepath.Join(dir, name), []byte(meta), 0o600); err != nil {
						t.Fatal(err)
					}
				}
			}

			server := w.CodexServer()
			research, deeper := "The cart needs tax.", "Use the local rate."
			child := server.Subagent(conversation, research)

			if visibility != "visible" {
				moveOn(t, app, codex, app.Terminal(session), "/new", "look at the tax table")
				codex.Reply("Looked. <!-- attn:state=idle -->")
				testworld.AwaitSession(app, session, func(s protocol.Session) bool { return protocol.Deref(s.Hidden) })
				if visibility == "hidden-redelivered" {
					if got := submitSessionAnnotationFeedback(app, session, "finish pricing"); !got.success {
						t.Fatalf("feedback: %+v", got)
					}
					server.Prompted(conversation)
				}
			} else {
				app.TypeLine(session, "finish pricing")
				codex.Prompted()
			}

			server.Subagent(child, deeper)
			want := claudeTokens(first) + claudeTokens(research) + claudeTokens(deeper)
			if visibility != "hidden" {
				last := "Finished. <!-- attn:state=idle -->"
				server.Reply(conversation, last)
				want += claudeTokens(last)
			}

			started := time.Now()
			app.Send(protocol.UnregisterMessage{Cmd: protocol.CmdUnregister, ID: protocol.SessionID(session)})
			testworld.Await(app, protocol.EventSessionUnregistered, func(e protocol.WebSocketEvent) bool { return e.Session != nil && string(e.Session.ID) == session })
			closed := awaitClosed(app, session)
			t.Logf("close with 2048 unrelated rollouts: %s", time.Since(started))
			if closed.Usage == nil || closed.Usage.TotalTokens != want || protocol.Deref(closed.Usage.MeasurementIncomplete) {
				t.Fatalf("closed usage = %+v, want %d complete tokens", closed.Usage, want)
			}
			server.AwaitArchived(conversation)
		})
	}
}

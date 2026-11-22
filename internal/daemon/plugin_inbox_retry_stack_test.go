package daemon_test

import (
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
	"strings"
	"testing"
)

func TestAPluginInboxRetryKeepsTheExactInputAcrossLostRepliesAndRestart(t *testing.T) {
	for _, restart := range []bool{false, true} {
		t.Run(map[bool]string{false: "plugin-reconnect", true: "daemon-restart"}[restart], func(t *testing.T) {
			s := testworld.NewStack(t)
			s.Start()
			app := s.TrustedApp()
			caps := map[string]bool{"state_reporting": true, "message_delivery": true, "attributed_input": true}
			driver := dialDriverAt(t, s.DialUnix, "independent-plugin")
			driver.register("independent", caps)
			awaitDriverAvailable(app, "independent")
			session := s.Spawn(app, "independent", s.Path("shop"))
			run := driver.launched()
			awaitingInput(t, app, driver, session, run)
			reportDriverConversation(driver, run, 2, "native-conversation")
			mailbox := protocol.QuickCaptureMailbox{Kind: protocol.QuickCaptureMailboxKindSession, SessionID: protocol.Ptr(protocol.SessionID(session))}
			first := sendQuickCapture(t, app, mailbox, "first user input")
			var original deliveredMessage
			driver.asked("driver.deliver_message", &original)
			if original.Voice != "user" {
				t.Fatalf("typed user ring voice=%q", original.Voice)
			}
			if restart {
				s.Stop()
				s.Start()
				app = s.TrustedApp()
			} else {
				observer := s.App()
				awaitHarnessOffered(observer, "independent")
				mark := observer.EventCount()
				driver.hangUp()
				testworld.AwaitSince(observer, mark, protocol.EventHarnessCatalog, func(m protocol.HarnessCatalogMessage) bool {
					_, ok := testworld.FindHarness(m.Harnesses, "independent")
					return !ok
				})
			}
			second := sendQuickCapture(t, app, mailbox, "second user input")
			driver = dialDriverAt(t, s.DialUnix, "independent-plugin")
			driver.register("independent", caps)
			var retry deliveredMessage
			held := driver.asked("driver.deliver_message", &retry)
			if retry != original {
				t.Fatalf("ambiguous retry changed input:\noriginal=%+v\nretry=%+v", original, retry)
			}
			if strings.Contains(retry.Text, "second user input") {
				t.Fatal("new mail changed the ambiguous payload")
			}
			driver.answer(held, map[string]bool{"ok": true})
			testworld.Await(app, protocol.EventQuickCaptureRead, func(m protocol.QuickCaptureReadMessage) bool { return m.CaptureID == first.CaptureID })
			var next deliveredMessage
			held = driver.asked("driver.deliver_message", &next)
			if next.InputID == original.InputID || !strings.Contains(next.Text, "second user input") || strings.Contains(next.Text, "first user input") {
				t.Fatalf("next input=%+v", next)
			}
			driver.answer(held, map[string]bool{"ok": true})
			testworld.Await(app, protocol.EventQuickCaptureRead, func(m protocol.QuickCaptureReadMessage) bool { return m.CaptureID == second.CaptureID })
			screenLacks(t, app, session, "first user input")
			screenLacks(t, app, session, "second user input")
			t.Logf("restart=%v original/retry input=%s run=%s conversation=%s; later input=%s; same original payload and zero PTY input", restart, original.InputID, original.RunID, original.ConversationID, next.InputID)
		})
	}
}

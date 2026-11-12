package daemon_test

import (
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestADirectDriverRunSurvivesADaemonRestartWithoutPTYFallback(t *testing.T) {
	for _, backend := range testworld.WorkerAndSharedHost {
		t.Run(string(backend), func(t *testing.T) {
			s := testworld.NewStack(t, testworld.OnPTYBackend(backend))
			s.Start()
			app := s.App()
			driver := dialDriverAt(t, s.DialUnix, "independent-plugin")
			caps := map[string]bool{"resume": true, "state_reporting": true, "message_delivery": true}
			driver.register("independent", caps)
			awaitDriverAvailable(app, "independent")
			session := s.Spawn(app, "independent", s.Path("shop"))
			run := driver.launched()
			awaitingInput(t, app, driver, session, run)
			request := protocol.SessionAnnotationsSubmitMessage{Cmd: protocol.CmdSessionAnnotationsSubmit, RequestID: "surviving-input", SessionID: protocol.SessionID(session), Text: "check the rounding"}
			submit := func() protocol.SessionAnnotationsSubmitResultMessage {
				app.Send(request)
				return feedbackResult(app, request.RequestID)
			}
			observer := s.App()
			driver.hangUp()
			testworld.Await(observer, protocol.EventSettingsUpdated, func(m protocol.SettingsUpdatedMessage) bool { return m.Settings["independent_available"] != "true" })
			if got := submit(); got.Success {
				t.Fatalf("disconnected direct run accepted input: %+v", got)
			}
			screenLacks(t, app, session, request.Text)
			s.Stop()
			s.Start()
			app = s.App()
			if got := submit(); got.Success {
				t.Fatalf("restarted direct run accepted input without plugin: %+v", got)
			}
			screenLacks(t, app, session, request.Text)
			driver = dialDriverAt(t, s.DialUnix, "independent-plugin")
			driver.register("independent", caps)
			if len(driver.registered.ActiveRuns) != 1 || driver.registered.ActiveRuns[0].RunID != run.RunID || driver.registered.ActiveRuns[0].SessionID != run.SessionID {
				t.Fatalf("restored runs=%+v, want %+v", driver.registered.ActiveRuns, run)
			}
			app.Send(request)
			var input deliveredMessage
			driver.answer(driver.asked("driver.deliver_message", &input), map[string]bool{"ok": true})
			if got := feedbackResult(app, request.RequestID); !got.Success {
				t.Fatalf("retry=%+v", got)
			}
			if input.RunID != run.RunID {
				t.Fatalf("retry changed runs: %+v", input)
			}
			if got := submit(); !got.Success {
				t.Fatalf("duplicate=%+v", got)
			}
			screenLacks(t, app, session, request.Text)
			if len(driver.requests) != 0 {
				t.Fatal("retry submitted twice")
			}
			state := s.Attn("session", "show", session)
			if state.Code != 0 || !strings.Contains(state.Stdout, session) {
				t.Fatalf("CLI session show: %+v", state)
			}
			t.Logf("CLI session show:\n%s", state.Stdout)
			t.Logf("backend=%s restored run=%s input=%s once; direct-message PTY bytes=0 before and after daemon restart", backend, run.RunID, input.InputID)
		})
	}
}

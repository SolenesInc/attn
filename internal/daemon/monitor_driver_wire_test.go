package daemon_test

import (
	"testing"
	"time"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestDriverSilenceExpiresAndReportsClearTheAlarm(t *testing.T) {
	for _, report := range []string{"silent", "state", "only_if_unknown", "metadata"} {
		t.Run(report, func(t *testing.T) {
			inBubbleWithAgents(t, func(t *testing.T, w *world) {
				app := w.App()
				driver := connectDriver(t, w, "snipe-plugin", "snipe", map[string]bool{"state_reporting": true})
				session, run := spawnDriven(w, app, driver, w.Path("shop"))
				driver.mustReport("session.report_state", map[string]any{"session_id": run.SessionID, "run_id": run.RunID, "seq": 1, "state": protocol.StateWorking})
				driver.leave(w)
				w.advance(time.Minute)
				if got := listedState(t, w, session).State; got != protocol.SessionStateWorking {
					t.Fatalf("inside the silence grace: %s", got)
				}
				if report != "silent" {
					driver = connectDriver(t, w, "snipe-plugin", "snipe", map[string]bool{"state_reporting": true})
					params := map[string]any{"session_id": run.SessionID, "run_id": run.RunID, "seq": 2}
					if report == "metadata" {
						params["metadata"] = map[string]string{"native_id": "conversation"}
						driver.mustReport("session.report_metadata", params)
					} else {
						params["state"] = protocol.StateWorking
						if report == "only_if_unknown" {
							params["state"], params["only_if_unknown"] = protocol.StateIdle, true
						}
						driver.mustReport("session.report_state", params)
					}
				}
				w.advance(2 * time.Minute)
				want := protocol.SessionStateWorking
				if report == "silent" {
					want = protocol.SessionStateUnknown
				}
				if got := listedState(t, w, session).State; got != want {
					t.Fatalf("after the grace with %s: %s, want %s", report, got, want)
				}
			})
		})
	}
}

func TestADriverRunIgnoresTerminalHeartbeatsAndSurvivesRecoveryWithoutItsPlugin(t *testing.T) {
	inBubbleWithAgents(t, func(t *testing.T, w *world) {
		app := w.App()
		driver := connectDriver(t, w, "snipe-plugin", "snipe", map[string]bool{"state_reporting": true})
		session, run := spawnDriven(w, app, driver, w.Path("shop"))
		driver.mustReport("session.report_state", map[string]any{"session_id": run.SessionID, "run_id": run.RunID, "seq": 1, "state": protocol.StateWaitingInput})
		w.terminal(session).Heartbeat("busy", "worker info")
		w.advance(0)
		if got := listedState(t, w, session).State; got != protocol.SessionStateWaitingInput {
			t.Fatalf("terminal heartbeat changed driver state to %s", got)
		}
		driver.hangUp()
		w.restart()
		w.advance(3 * time.Minute)
		if got := listedState(t, w, session).State; got != protocol.SessionStateUnknown {
			t.Fatalf("recovered driver with no plugin is %s, want unknown after grace", got)
		}
	})
}

func TestSharedCodexThreadsKeepTheirOwnStateEvidence(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	app := w.App()
	setSetting(t, app, "codex_shared_enabled", "true")
	hidden, run, terminal := sharedCodexWaiting(t, w, app)
	thread := run.ConversationID
	moveOn(t, app, run, terminal, "/new", "inspect a second task")
	shown := sessionShownIn(t, w, app, terminal)
	run.Reply("Done here. <!-- attn:state=idle -->")
	testworld.AwaitSession(app, shown, func(s protocol.Session) bool { return s.State == protocol.SessionStateIdle })
	if got := listedState(t, w, hidden).State; got != protocol.SessionStateWaitingInput {
		t.Fatalf("shown thread overwrote hidden thread: %s", got)
	}
	if got := submitSessionAnnotationFeedback(app, hidden, sessionAnnotationFeedback); !got.success {
		t.Fatalf("hidden input: %+v", got)
	}
	server := w.CodexServer()
	server.Prompted(thread)
	testworld.AwaitSession(app, hidden, func(s protocol.Session) bool { return s.State == protocol.SessionStateWorking })
	server.AskQuestion(thread)
	testworld.AwaitSession(app, hidden, func(s protocol.Session) bool { return s.State == protocol.SessionStateWaitingInput })
	if got := listedState(t, w, shown).State; got != protocol.SessionStateIdle {
		t.Fatalf("hidden thread overwrote shown thread: %s", got)
	}
}

func TestSharedCodexLaunchesWaitingForThreadIDsKeepTheirStateSeparate(t *testing.T) {
	w := newTitlingWorld(t, fakeagent.Codex)
	t.Setenv("ATTN_SESSION_AUTO_TITLE", "off")
	app := w.App()
	setSetting(t, app, "codex_shared_enabled", "true")
	warm := w.Spawn(app, fakeagent.Codex, w.Path("warm"))
	w.Launched(warm)
	firstBoot := w.HoldNextBoot()
	first := w.Spawn(app, fakeagent.Codex, w.Path("first"))
	w.AwaitHeldBoot()
	secondBoot := w.HoldNextBoot()
	second := w.Spawn(app, fakeagent.Codex, w.Path("second"))
	w.AwaitHeldBoot()
	firstBoot()
	firstAgent := w.Launched(first)
	secondBoot()
	secondAgent := w.Launched(second)
	app.TypeLine(first, "inspect the first task")
	firstAgent.Prompted()
	firstAgent.Reply("Should I continue?")
	firstVerdict := w.HeadlessTask()
	app.TypeLine(second, "inspect the second task")
	secondAgent.Prompted()
	secondAgent.Reply("Done.")
	secondVerdict := w.HeadlessTask()
	secondVerdict.Answer(`{"verdict":"DONE"}`)
	testworld.AwaitSession(app, second, func(s protocol.Session) bool { return s.State == protocol.SessionStateIdle })
	firstVerdict.Answer(`{"verdict":"WAITING"}`)
	testworld.AwaitSession(app, first, func(s protocol.Session) bool { return s.State == protocol.SessionStateWaitingInput })
}

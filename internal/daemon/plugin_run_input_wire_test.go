package daemon_test

import (
	"fmt"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/pausepoint"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
	"strings"
	"testing"
)

func TestAnArbitraryPlugHarnessTakesDirectInputWithoutTerminalBytes(t *testing.T) {
	t.Setenv(fakeagent.PlugAgentEnv, "independent")
	t.Setenv(fakeagent.PlugCapabilitiesEnv, "message_delivery")
	w := newWorld(t, fakeagent.Plug)
	app := w.App()
	pluginDriverSettings(app, "independent")
	session := w.Spawn(app, fakeagent.Harness("independent"), w.Path("shop"))
	run := w.Launched(session)
	app.TypeLine(session, "hello")
	run.Prompted()
	run.Reply("Ready. <!-- attn:state=waiting_input -->")
	testworld.AwaitSession(app, session, func(s protocol.Session) bool { return s.State == protocol.SessionStateWaitingInput })
	result := feedbackResult(app, sendFeedback(app, session, "check the rounding"))
	if !result.Success || result.Status != "delivered" {
		t.Fatalf("direct input: %+v", result)
	}
	if got := run.Prompted(); got != "check the rounding" {
		t.Fatalf("input=%q", got)
	}
	screenLacks(t, app, session, "check the rounding")
}

func TestADirectRunKeepsItsRouteWhenItsPluginDisappearsOrChangesCapabilities(t *testing.T) {
	inBubbleWithAgents(t, func(t *testing.T, w *world) {
		app := w.App()
		caps := map[string]bool{"state_reporting": true, "message_delivery": true}
		driver := connectDriver(t, w, "independent-plugin", "independent", caps)
		session, run := spawnDriven(w, app, driver, w.Path("shop"))
		awaitingInput(t, app, driver, session, run)
		driver.leave(w)
		request := protocol.SessionAnnotationsSubmitMessage{Cmd: protocol.CmdSessionAnnotationsSubmit, RequestID: "same-input", SessionID: protocol.SessionID(session), Text: "check the rounding"}
		submit := func() protocol.SessionAnnotationsSubmitResultMessage {
			app.Send(request)
			return feedbackResult(app, request.RequestID)
		}
		if r := submit(); r.Success {
			t.Fatal("disconnected direct run accepted input")
		}
		if got := w.terminal(session).Inputs(); len(got) != 0 {
			t.Fatalf("disconnected direct run received PTY bytes: %+v", got)
		}
		driver = connectDriver(t, w, "independent-plugin", "independent", map[string]bool{"state_reporting": true})
		if r := submit(); r.Success {
			t.Fatal("direct run changed to terminal delivery")
		}
		if got := w.terminal(session).Inputs(); len(got) != 0 {
			t.Fatalf("capability withdrawal typed PTY bytes: %+v", got)
		}
		driver.register("independent", caps)
		app.Send(request)
		var input deliveredMessage
		driver.answer(driver.asked("driver.deliver_message", &input), map[string]bool{"ok": true})
		if r := feedbackResult(app, request.RequestID); !r.Success {
			t.Fatalf("retry=%+v", r)
		}
		if input.RunID != run.RunID || input.Text != request.Text {
			t.Fatalf("retry=%+v", input)
		}
		if r := submit(); !r.Success {
			t.Fatalf("duplicate=%+v", r)
		}
		if len(driver.requests) != 0 {
			t.Fatal("duplicate accepted input was submitted twice")
		}
		if got := w.terminal(session).Inputs(); len(got) != 0 {
			t.Fatalf("retry typed PTY bytes: %+v", got)
		}
		t.Logf("same run=%s input=%s delivered once; disconnected, capability withdrawal and retry PTY bytes=0", run.RunID, input.InputID)
	})
}

func TestATerminalRunKeepsTerminalDeliveryAfterItsDriverAddsDirectInput(t *testing.T) {
	inBubbleWithAgents(t, func(t *testing.T, w *world) {
		app := w.App()
		driver := connectDriver(t, w, "independent-plugin", "independent", map[string]bool{"state_reporting": true})
		session, run := spawnDriven(w, app, driver, w.Path("shop"))
		awaitingInput(t, app, driver, session, run)
		driver.register("independent", map[string]bool{"state_reporting": true, "message_delivery": true})
		if r := feedbackResult(app, sendFeedback(app, session, "terminal input")); !r.Success {
			t.Fatalf("terminal input=%+v", r)
		}
		if got := w.terminal(session).Pasted(); len(got) != 1 || got[0] != "terminal input" {
			t.Fatalf("terminal pastes=%q", got)
		}
		if len(driver.requests) != 0 {
			t.Fatal("terminal run switched to direct input")
		}
	})
}

func reportDriverConversation(driver *driverPeer, run driverLaunch, seq uint64, conversation string) {
	driver.mustReport("session.report_metadata", map[string]any{"session_id": run.SessionID, "run_id": run.RunID, "seq": seq, "metadata": map[string]string{"native_id": conversation}, "resume_session_id": conversation})
}

func TestAnArbitraryDriverReceivesBothVoicesAndOnlyOptedInInputDuringApproval(t *testing.T) {
	for _, safe := range []bool{false, true} {
		t.Run(fmt.Sprint(safe), func(t *testing.T) {
			inBubbleWithAgents(t, func(t *testing.T, w *world) {
				app, cli := w.App(), w.Client()
				driver := connectDriver(t, w, "independent-plugin", "independent", map[string]bool{"state_reporting": true, "message_delivery": true, "attributed_input": true, "safe_during_question": safe})
				session, run := spawnDriven(w, app, driver, w.Path("shop"))
				author, _ := spawnDriven(w, app, driver, w.Path("author"))
				reportDriverConversation(driver, run, 1, "native-conversation")
				if err := driver.state(run, 2, protocol.StateWorking); err != nil {
					t.Fatal(err)
				}
				testworld.AwaitSession(app, session, func(s protocol.Session) bool { return s.State == protocol.SessionStateWorking })
				if err := driver.state(run, 3, protocol.StatePendingApproval); err != nil {
					t.Fatal(err)
				}
				testworld.AwaitSession(app, session, func(s protocol.Session) bool { return s.State == protocol.SessionStatePendingApproval })
				request := sendFeedback(app, session, "check the rounding")
				if safe {
					var input deliveredMessage
					driver.answer(driver.asked("driver.deliver_message", &input), map[string]bool{"ok": true})
					if input.Voice != "user" || input.ConversationID != "native-conversation" {
						t.Fatalf("user input=%+v", input)
					}
				}
				result := feedbackResult(app, request)
				if result.Success != safe {
					t.Fatalf("safe=%v result=%+v", safe, result)
				}
				if safe {
					if got := listedState(t, w, session).State; got != protocol.SessionStatePendingApproval {
						t.Fatalf("safe input resolved approval: %s", got)
					}
				}
				if err := driver.state(run, 4, protocol.StateIdle); err != nil {
					t.Fatal(err)
				}
				testworld.AwaitSession(app, session, func(s protocol.Session) bool { return s.State == protocol.SessionStateIdle })
				sent := make(chan error, 1)
				go func() {
					receipt, err := cli.AgentMsg(session, protocol.SessionID(author), "look at the rounding")
					t.Logf("ring receipt=%+v err=%v", receipt, err)
					sent <- err
				}()
				var ring deliveredMessage
				driver.answer(driver.asked("driver.deliver_message", &ring), map[string]bool{"ok": true})
				if err := <-sent; err != nil {
					t.Fatal(err)
				}
				if ring.Voice != "attn" || ring.ConversationID != "native-conversation" || !strings.Contains(ring.Text, inboxDoorbell) {
					t.Fatalf("attributed ring=%+v", ring)
				}
				if got := w.terminal(session).Inputs(); len(got) != 0 {
					t.Fatalf("direct input reached PTY: %+v", got)
				}
				t.Logf("safe_during_question=%v; user voice and attn voice preserved; approval safe input does not resolve approval", safe)
			})
		})
	}
}

func TestAPluginCompactingCloseUsesCorrelatedOutcomes(t *testing.T) {
	for _, outcome := range []string{"completed", "busy", "failed", "unsupported"} {
		t.Run(outcome, func(t *testing.T) {
			inBubbleWithAgents(t, func(t *testing.T, w *world) {
				app, cli := w.App(), w.Client()
				driver := connectDriver(t, w, "independent-plugin", "independent", map[string]bool{"state_reporting": true, "message_delivery": true, "compaction": outcome != "unsupported"})
				session, run := spawnDriven(w, app, driver, w.Path("shop"))
				reportDriverConversation(driver, run, 1, "native-conversation")
				if err := driver.state(run, 2, protocol.StateIdle); err != nil {
					t.Fatal(err)
				}
				testworld.AwaitSession(app, session, func(s protocol.Session) bool { return s.State == protocol.SessionStateIdle })
				if outcome == "unsupported" {
					result, err := cli.AgentCloseCompacting(protocol.SessionID(session), compactReason)
					if err == nil || !strings.Contains(err.Error(), "no way to compact") {
						t.Fatalf("unsupported=%+v err=%v", result, err)
					}
					stillOpen(t, cli, session, "unsupported compaction")
					return
				}
				closing := make(chan struct{})
				go func() { closeCompacting(t, cli, session); close(closing) }()
				var input deliveredMessage
				held := driver.asked("driver.compact", &input)
				report := func(input deliveredMessage, runID, conversation, result string) error {
					return driver.report("session.report_compaction", map[string]any{"session_id": run.SessionID, "run_id": runID, "input_id": input.InputID, "conversation_id": conversation, "outcome": result, "reason": "native result"})
				}
				if err := report(input, "retired-run", input.ConversationID, "completed"); err == nil {
					t.Fatal("retired compact report accepted")
				}
				if err := report(input, run.RunID, "another-conversation", "completed"); err == nil {
					t.Fatal("wrong conversation compact report accepted")
				}
				wrong := input
				wrong.InputID = "another-input"
				if err := report(wrong, run.RunID, input.ConversationID, "completed"); err == nil {
					t.Fatal("wrong input compact report accepted")
				}
				stillOpen(t, cli, session, "before correlated outcome")
				if err := report(input, run.RunID, input.ConversationID, outcome); err != nil {
					t.Fatal(err)
				}
				driver.answer(held, map[string]bool{"ok": true})
				<-closing
				if outcome == "busy" {
					if err := driver.state(run, 3, protocol.StateWorking); err != nil {
						t.Fatal(err)
					}
					testworld.AwaitSession(app, session, func(s protocol.Session) bool { return s.State == protocol.SessionStateWorking })
					if err := driver.state(run, 4, protocol.StateIdle); err != nil {
						t.Fatal(err)
					}
					testworld.AwaitSession(app, session, func(s protocol.Session) bool { return s.State == protocol.SessionStateIdle })
					var retry deliveredMessage
					held = driver.asked("driver.compact", &retry)
					if retry.InputID == input.InputID {
						t.Fatal("busy compact reused its old correlation")
					}
					if err := report(input, run.RunID, input.ConversationID, "completed"); err == nil {
						t.Fatal("late outcome of busy attempt accepted")
					}
					stillOpen(t, cli, session, "while retry runs")
					driver.answer(held, map[string]bool{"ok": true})
					if err := report(retry, run.RunID, retry.ConversationID, "completed"); err != nil {
						t.Fatal(err)
					}
				}
				closed := awaitClosed(app, session)
				reason := protocol.Deref(closed.CloseReason)
				if outcome == "failed" && !strings.Contains(reason, "native result") {
					t.Fatalf("failed compaction close reason=%q", reason)
				}
				if outcome != "failed" && reason != compactReason {
					t.Fatalf("close reason=%q", reason)
				}
				t.Logf("compact outcome=%s run=%s input=%s; wrong run, conversation and input rejected", outcome, run.RunID, input.InputID)
			})
		})
	}
}

func TestOldDriverMessagesKeepTheirAPI7ShapeAndRejectionsNeverReachPTY(t *testing.T) {
	for _, status := range []string{"accepted", "rejected", "target_ended"} {
		t.Run(status, func(t *testing.T) {
			inBubbleWithAgents(t, func(t *testing.T, w *world) {
				app := w.App()
				driver := connectDriver(t, w, "independent-plugin", "independent", map[string]bool{"state_reporting": true, "message_delivery": true})
				session, run := spawnDriven(w, app, driver, w.Path("shop"))
				awaitingInput(t, app, driver, session, run)
				submitted := sendFeedback(app, session, "check the rounding")
				var raw map[string]any
				held := driver.asked("driver.deliver_message", &raw)
				if len(raw) != 4 || raw["voice"] != nil || raw["conversation_id"] != nil {
					t.Fatalf("old API7 message=%v", raw)
				}
				if status == "accepted" {
					driver.answer(held, map[string]bool{"ok": true})
				} else {
					driver.answer(held, map[string]any{"ok": false, "status": status, "reason": "native target refused input"})
				}
				result := feedbackResult(app, submitted)
				if result.Success != (status == "accepted") {
					t.Fatalf("%s=%+v", status, result)
				}
				if status != "accepted" && !strings.Contains(protocol.Deref(result.Error), "native target refused input") {
					t.Fatalf("rejection lost reason: %+v", result)
				}
				if got := w.terminal(session).Inputs(); len(got) != 0 {
					t.Fatalf("input reached PTY: %+v", got)
				}
			})
		})
	}
}

func TestAReplacementDriverRunRejectsTheRetiredRunsReports(t *testing.T) {
	w := newWorld(t)
	app := w.App()
	driver := connectDriver(t, w, "independent-plugin", "independent", map[string]bool{"resume": true, "state_reporting": true, "message_delivery": true})
	session, first := spawnDriven(w, app, driver, w.Path("shop"))
	awaitingInput(t, app, driver, session, first)
	if result := reloadPlug(app, session); !result.Success {
		t.Fatalf("reload=%+v", result)
	}
	second := driver.launched()
	if second.RunID == first.RunID {
		t.Fatal("reload reused the retired run")
	}
	if err := driver.state(first, 99, protocol.StatePendingApproval); err == nil {
		t.Fatal("retired run changed replacement state")
	}
	if err := driver.report("session.report_metadata", map[string]any{"session_id": first.SessionID, "run_id": first.RunID, "seq": 100, "metadata": map[string]string{"native_id": "retired"}, "resume_session_id": "retired"}); err == nil {
		t.Fatal("retired run changed replacement conversation")
	}
	awaitingInput(t, app, driver, session, second)
	submitted := sendFeedback(app, session, "new run input")
	var input deliveredMessage
	driver.answer(driver.asked("driver.deliver_message", &input), map[string]bool{"ok": true})
	if result := feedbackResult(app, submitted); !result.Success || input.RunID != second.RunID {
		t.Fatalf("replacement input=%+v result=%+v", input, result)
	}
}

func TestADirectDriversAvailabilityResumesQueuedMailWithoutAnotherStateChange(t *testing.T) {
	for _, availability := range []string{"reconnect", "conversation"} {
		t.Run(availability, func(t *testing.T) {
			inBubbleWithAgents(t, func(t *testing.T, w *world) {
				app, cli := w.App(), w.Client()
				caps := map[string]bool{"state_reporting": true, "message_delivery": true, "attributed_input": true}
				driver := connectDriver(t, w, "independent-plugin", "independent", caps)
				author, _ := spawnDriven(w, app, driver, w.Path("author"))
				session, run := spawnDriven(w, app, driver, w.Path("shop"))
				awaitingInput(t, app, driver, session, run)
				if availability == "reconnect" {
					reportDriverConversation(driver, run, 2, "native-conversation")
					driver.leave(w)
				}
				receipt := sendAgentMessage(t, cli, author, session, "read the rounding notes")
				if receipt.Status != protocol.AgentMsgStatusQueued {
					t.Fatalf("unavailable input=%+v", receipt)
				}
				if availability == "reconnect" {
					driver = connectDriver(t, w, "independent-plugin", "independent", caps)
				} else {
					reportDriverConversation(driver, run, 2, "native-conversation")
				}
				var input deliveredMessage
				driver.answer(driver.asked("driver.deliver_message", &input), map[string]bool{"ok": true})
				if input.RunID != run.RunID || input.ConversationID != "native-conversation" {
					t.Fatalf("recovered input=%+v", input)
				}
			})
		})
	}
}

func TestAPluginCompactWaitsForItsConversationAndResumesWhenItsDriverReturns(t *testing.T) {
	for _, availability := range []string{"reconnect", "conversation"} {
		t.Run(availability, func(t *testing.T) {
			inBubbleWithAgents(t, func(t *testing.T, w *world) {
				app, cli := w.App(), w.Client()
				caps := map[string]bool{"state_reporting": true, "message_delivery": true, "compaction": true}
				driver := connectDriver(t, w, "independent-plugin", "independent", caps)
				session, run := spawnDriven(w, app, driver, w.Path("shop"))
				awaitingInput(t, app, driver, session, run)
				if availability == "conversation" {
					closeCompacting(t, cli, session)
					if len(driver.requests) != 0 {
						t.Fatal("compact was dispatched without a native conversation")
					}
					reportDriverConversation(driver, run, 2, "native-conversation")
				} else {
					reportDriverConversation(driver, run, 2, "native-conversation")
					placing := w.PauseAt(pausepoint.CompactingClosePlacing)
					requested := make(chan struct{})
					go func() { closeCompacting(t, cli, session); close(requested) }()
					placing.Await()
					driver.leave(w)
					placing.Release()
					<-requested
					driver = connectDriver(t, w, "independent-plugin", "independent", caps)
				}
				var compact deliveredMessage
				driver.answer(driver.asked("driver.compact", &compact), map[string]bool{"ok": true})
				if compact.ConversationID != "native-conversation" {
					t.Fatalf("compact=%+v", compact)
				}
				driver.mustReport("session.report_compaction", map[string]any{"session_id": run.SessionID, "run_id": run.RunID, "input_id": compact.InputID, "conversation_id": compact.ConversationID, "outcome": "completed"})
				awaitClosed(app, session)
			})
		})
	}
}

func TestAPluginRingRejectionOrTargetChangeReleasesTheOriginalInput(t *testing.T) {
	for _, change := range []string{"rejected", "target_ended", "conversation", "retired"} {
		t.Run(change, func(t *testing.T) {
			inBubbleWithAgents(t, func(t *testing.T, w *world) {
				app := w.TrustedApp()
				caps := map[string]bool{"resume": true, "state_reporting": true, "message_delivery": true, "attributed_input": true}
				driver := connectDriver(t, w, "independent-plugin", "independent", caps)
				session, run := spawnDriven(w, app, driver, w.Path("shop"))
				awaitingInput(t, app, driver, session, run)
				reportDriverConversation(driver, run, 2, "native-conversation")
				mailbox := protocol.QuickCaptureMailbox{Kind: protocol.QuickCaptureMailboxKindSession, SessionID: protocol.Ptr(protocol.SessionID(session))}
				capture := sendQuickCapture(t, app, mailbox, "preserve this user input")
				var original deliveredMessage
				held := driver.asked("driver.deliver_message", &original)
				if change == "rejected" || change == "target_ended" {
					driver.answer(held, map[string]any{"ok": false, "status": change, "reason": "target refused input"})
					driver.register("independent", caps)
					var retry deliveredMessage
					held = driver.asked("driver.deliver_message", &retry)
					if retry.InputID == original.InputID {
						t.Fatal("explicit rejection kept an ambiguous attempt")
					}
					driver.answer(held, map[string]bool{"ok": true})
				} else {
					driver.leave(w)
					driver = connectDriver(t, w, "independent-plugin", "independent", map[string]bool{"resume": true, "state_reporting": true})
					if change == "retired" {
						stopDriven(app, driver, session)
						driver.register("independent", caps)
						run = relaunchDriven(w, driver, session, w.Path("shop"))
						awaitingInput(t, app, driver, session, run)
					} else {
						reportDriverConversation(driver, run, 3, "another-conversation")
					}
					driver.register("independent", caps)
					var retry deliveredMessage
					held = driver.asked("driver.deliver_message", &retry)
					if retry.InputID == original.InputID {
						t.Fatal("changed target received ambiguous retry")
					}
					if change == "retired" && retry.RunID == original.RunID {
						t.Fatal("retired run was retried")
					}
					if change == "conversation" && retry.ConversationID != "another-conversation" {
						t.Fatalf("retry=%+v", retry)
					}
					driver.answer(held, map[string]bool{"ok": true})
				}
				testworld.Await(app, protocol.EventQuickCaptureRead, func(m protocol.QuickCaptureReadMessage) bool { return m.CaptureID == capture.CaptureID })
				if got := w.terminal(session).Inputs(); len(got) != 0 {
					t.Fatalf("direct ring reached PTY: %+v", got)
				}
			})
		})
	}
}

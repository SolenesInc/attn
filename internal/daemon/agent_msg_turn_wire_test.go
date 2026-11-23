package daemon_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/claudemod"
	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/pausepoint"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

const (
	peerHeader      = "This message is from another agent, not from your user."
	steerLine       = "The sender chose not to stop your turn."
	interruptLine   = "Your work so far is in this conversation."
	interruptOrigin = "who stopped your turn to send this"
)

func sendPeerMessage(t *testing.T, cli *client.Client, from, to, body string, flags ...string) *protocol.AgentMsgResult {
	t.Helper()
	result, err := peerMessage(cli, from, to, body, flags...)
	if err != nil {
		t.Fatalf("%s messages %s with %v: %v", from, to, flags, err)
	}
	return result
}

func peerMessage(cli *client.Client, from, to, body string, flags ...string) (*protocol.AgentMsgResult, error) {
	args := map[string]any{"target": to, "text": body}
	for _, flag := range flags {
		args[flag] = true
	}
	var result protocol.AgentMsgResult
	err := cli.RunCommand("agent msg", protocol.SessionID(from), args, &result)
	return &result, err
}

func peerMessageStatus(t *testing.T, cli *client.Client, from, id string) *protocol.AgentPeerMessage {
	t.Helper()
	status, err := cli.AgentMsgStatus(id, protocol.SessionID(from))
	if err != nil {
		t.Fatalf("%s asks for the status of %s: %v", from, id, err)
	}
	return status
}

func leadAndWorkingClaude(t *testing.T) (*world, *testworld.Peer, *client.Client, string, *fakeagent.Run) {
	t.Helper()
	w := newWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	registerSessions(t, w, cli, "lead")
	session, run := moddedClaude(t, w, app)
	workingOn(t, app, run, session, "refactor the cache")
	return w, app, cli, session, run
}

func TestASteerReachesAWorkingModdedClaudeAtItsNextToolCallWithoutStoppingTheTurn(t *testing.T) {
	w, _, cli, session, run := leadAndWorkingClaude(t)
	settled := w.PauseAt(pausepoint.InboxRingSettled)

	sent := sendPeerMessage(t, cli, "lead", session, "New code logs through slog, not log.Printf.", "steer")
	if sent.Status != protocol.AgentMsgStatusSteered || !strings.Contains(sent.Detail, "its turn goes on") {
		t.Fatalf("a steer to a working agent = %+v, want steered", sent)
	}
	modEvent(t, run, fakeagent.ModEventHeld, "slog")
	appended := run.ModToolBoundary()
	if len(appended) != 1 || !strings.Contains(appended[0], "New code logs through slog") || !strings.Contains(appended[0], peerHeader) || !strings.Contains(appended[0], steerLine) {
		t.Fatalf("at the next tool call the mod appended %q, want the steer under the peer header", appended)
	}
	settled.Await()
	settled.Release()
	if status := peerMessageStatus(t, cli, "lead", sent.MessageID); protocol.Deref(status.DeliveredAs) != protocol.PeerTurnSteer {
		t.Fatalf("the steer's status = %+v, want delivered as a steer", status)
	}
	run.Reply("Refactored with slog. <!-- attn:state=waiting_input -->")
	nothingElsePrompted(t, w.App(), run, session)
}

func TestAnInterruptStopsAWorkingModdedClaudeAndStartsItsNextTurn(t *testing.T) {
	w, app, cli, session, run := leadAndWorkingClaude(t)
	settled := w.PauseAt(pausepoint.InboxRingSettled)

	shownBefore := len(sessionUpdatesOf(app, session))
	sent := sendPeerMessage(t, cli, "lead", session, "Stop the CacheLayer refactor; the seed asks only for the TTL line.", "interrupt")
	if sent.Status != protocol.AgentMsgStatusInterrupted || !strings.Contains(sent.Detail, "turn stopped") {
		t.Fatalf("an interrupt to a working agent = %+v, want interrupted", sent)
	}
	modEvent(t, run, fakeagent.ModEventHeld, "CacheLayer")
	run.ModAbortLands()
	got := run.Prompted()
	if !strings.Contains(got, "Stop the CacheLayer refactor") || !strings.Contains(got, interruptOrigin) || !strings.Contains(got, peerHeader) || !strings.Contains(got, interruptLine) || !run.ThroughMod || run.AsUser {
		t.Fatalf("after the abort the agent took %q (through the mod: %t, as the user: %t), want the interrupt under the peer header in attn's voice", got, run.ThroughMod, run.AsUser)
	}
	settled.Await()
	settled.Release()
	if status := peerMessageStatus(t, cli, "lead", sent.MessageID); protocol.Deref(status.DeliveredAs) != protocol.PeerTurnInterrupt {
		t.Fatalf("the interrupt's status = %+v, want delivered as an interrupt", status)
	}

	later := sendPeerMessage(t, cli, "lead", session, "The TTL lives in config/cache.yaml.")
	if later.Status != protocol.AgentMsgStatusQueued {
		t.Fatalf("a message during the interrupt's turn = %+v, want queued", later)
	}
	updates := sessionUpdatesOf(app, session)
	for _, shown := range updates[shownBefore:] {
		if shown.State != protocol.SessionStateWorking || protocol.Deref(shown.TurnOpenedAt) != protocol.Deref(updates[shownBefore-1].TurnOpenedAt) {
			t.Fatalf("across the mod's own abort the app was shown %s (%s) with its turn opened at %q, want it working on its next turn with no turn opened", shown.State, protocol.Deref(shown.StateReason), protocol.Deref(shown.TurnOpenedAt))
		}
	}
	run.Reply("Only the TTL line changed. <!-- attn:state=waiting_input -->")
	if got := run.Prompted(); !strings.Contains(got, inboxDoorbell) || !run.ThroughMod {
		t.Fatalf("after the interrupt's turn the agent took %q (through the mod: %t), want the later message's doorbell", got, run.ThroughMod)
	}
}

func TestAHeldSteerOrInterruptGivenBackWaitsAsAQueuedMessage(t *testing.T) {
	for _, flag := range []string{"steer", "interrupt"} {
		for _, back := range []string{"esc", "reload"} {
			t.Run(flag+" at "+back, func(t *testing.T) {
				_, app, cli, session, run := leadAndWorkingClaude(t)
				sent := sendPeerMessage(t, cli, "lead", session, "Rename the flag to ttl_seconds.", flag)
				if sent.Status == protocol.AgentMsgStatusQueued {
					t.Fatalf("the %s to a working agent = %+v, want the mod to hold it", flag, sent)
				}
				modEvent(t, run, fakeagent.ModEventHeld, "ttl_seconds")
				if back == "esc" {
					stoppedByEsc(t, app, run, session)
					workingOn(t, app, run, session, "carry on with the cache")
				} else {
					run.ModReload()
				}
				run.Reply("Carried on. <!-- attn:state=waiting_input -->")
				if got := run.Prompted(); !strings.Contains(got, inboxDoorbell) || strings.Contains(got, "ttl_seconds") || !run.ThroughMod || run.AsUser {
					t.Fatalf("after the give-back the agent took %q (through the mod: %t, as the user: %t), want the doorbell at the turn's end", got, run.ThroughMod, run.AsUser)
				}
				if status := peerMessageStatus(t, cli, "lead", sent.MessageID); status.DeliveredAs != nil {
					t.Fatalf("the given-back %s's status = %+v, want no turn use", flag, status)
				}
			})
		}
	}
}

func TestASteerOrInterruptToAClaudeWithoutTheModQueuesAndIsNeverTyped(t *testing.T) {
	for _, flag := range []string{"steer", "interrupt"} {
		t.Run(flag, func(t *testing.T) {
			w := newWorld(t, fakeagent.Claude)
			app, cli := w.App(), w.Client()
			registerSessions(t, w, cli, "lead")
			session, run := moddedClaude(t, w, app)
			run.ModStopPolling()
			w.awaitTimer(claudemod.ReachTimer)
			w.advance(claudeModReachWindow)
			workingOn(t, app, run, session, "refactor the cache")

			sent := sendPeerMessage(t, cli, "lead", session, "Rename the flag to ttl_seconds.", flag)
			if sent.Status != protocol.AgentMsgStatusQueued || !strings.Contains(sent.Detail, "never types a steer or an interrupt") {
				t.Fatalf("the %s to a Claude without the mod = %+v, want queued with the reason", flag, sent)
			}
			run.Reply("Refactored. <!-- attn:state=waiting_input -->")
			if got := run.Prompted(); !strings.Contains(got, inboxDoorbell) || run.ThroughMod {
				t.Fatalf("at the turn's end the agent took %q (through the mod: %t), want the doorbell typed", got, run.ThroughMod)
			}
			if screen := settledScreen(t, app, session); strings.Contains(screen, "ttl_seconds") {
				t.Fatalf("the %s was typed into the terminal:\n%s", flag, screen)
			}
		})
	}
}

func TestAMessageSentAsTheUserReachesAWorkingModdedClaudeBareAndAsTheUser(t *testing.T) {
	const words = "Stop now and don't reset staging; I'm resetting it myself tonight."
	t.Run("queued", func(t *testing.T) {
		_, _, cli, session, run := leadAndWorkingClaude(t)
		sent := sendPeerMessage(t, cli, "lead", session, words, "as_user")
		if sent.Status != protocol.AgentMsgStatusQueued || !protocol.Deref(sent.AsUser) {
			t.Fatalf("a message as the user to a working agent = %+v, want queued as the user", sent)
		}
		run.Reply("Refactored. <!-- attn:state=waiting_input -->")
		if got := run.Prompted(); got != words || !run.ThroughMod || !run.AsUser {
			t.Fatalf("at the turn's end the agent took %q (through the mod: %t, as the user: %t), want the bare words as the user", got, run.ThroughMod, run.AsUser)
		}
		status := peerMessageStatus(t, cli, "lead", sent.MessageID)
		if !protocol.Deref(status.AsUser) || status.Sender.Ref != "session:lead" {
			t.Fatalf("the status of a message sent as the user = %+v, want its sender and as_user", status)
		}
	})
	t.Run("interrupt", func(t *testing.T) {
		_, _, cli, session, run := leadAndWorkingClaude(t)
		sent := sendPeerMessage(t, cli, "lead", session, words, "interrupt", "as_user")
		if sent.Status != protocol.AgentMsgStatusInterrupted || !strings.Contains(sent.Detail, "as the user's words") {
			t.Fatalf("an interrupt as the user = %+v, want interrupted as the user", sent)
		}
		modEvent(t, run, fakeagent.ModEventHeld, "staging")
		run.ModAbortLands()
		if got := run.Prompted(); got != words || !run.ThroughMod || !run.AsUser {
			t.Fatalf("after the abort the agent took %q (through the mod: %t, as the user: %t), want the bare words as the user", got, run.ThroughMod, run.AsUser)
		}
	})
	t.Run("steer is refused", func(t *testing.T) {
		w := newWorld(t, fakeagent.Claude)
		cli := w.Client()
		registerSessions(t, w, cli, "lead", "builder")
		if _, err := peerMessage(cli, "lead", "builder", words, "steer", "as_user"); client.ErrorCode(err) != "as_user_with_steer" {
			t.Fatalf("a steer as the user = %v, want as_user_with_steer", err)
		}
		if _, err := peerMessage(cli, "lead", "builder", words, "steer", "interrupt"); client.ErrorCode(err) != "steer_and_interrupt" {
			t.Fatalf("a steer that also interrupts = %v, want steer_and_interrupt", err)
		}
	})
}

type shownSince struct{ log, updates int }

func shownNow(app *testworld.Peer, session string) shownSince {
	return shownSince{log: len(app.Log()), updates: len(sessionUpdatesOf(app, session))}
}

func stayedWorkingOnOneTurn(t *testing.T, app *testworld.Peer, session string, before shownSince, endTurn func()) {
	t.Helper()
	endTurn()
	testworld.AwaitSessionSince(app, before.log, session, func(s protocol.Session) bool { return s.State == protocol.SessionStateIdle })
	updates := sessionUpdatesOf(app, session)
	opened := protocol.Deref(updates[before.updates-1].TurnOpenedAt)
	for _, shown := range updates[before.updates : len(updates)-1] {
		if shown.State != protocol.SessionStateWorking || protocol.Deref(shown.TurnOpenedAt) != opened {
			t.Fatalf("across attn's interrupt the app was shown %s (%s) with its turn opened at %q, want it working on the turn opened at %q", shown.State, protocol.Deref(shown.StateReason), protocol.Deref(shown.TurnOpenedAt), opened)
		}
	}
	if last := updates[len(updates)-1]; protocol.Deref(last.StateReason) == "turn_aborted" {
		t.Fatalf("the turn after attn's interrupt ended as %s (%s), want it to end on its own", last.State, protocol.Deref(last.StateReason))
	}
}

func TestASteerReachesAWorkingSharedCodexInItsRunningTurn(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	app, cli := w.App(), w.Client()
	registerSessions(t, w, cli, "lead")
	session, run := sharedCodexInATurn(t, w, app)

	sent := sendPeerMessage(t, cli, "lead", session, "New code logs through slog, not log.Printf.", "steer")
	if sent.Status != protocol.AgentMsgStatusSteered {
		t.Fatalf("a steer to a working Codex = %+v, want steered", sent)
	}
	if got := w.CodexServer().Prompted(run.ConversationID); !strings.Contains(got, "New code logs through slog") || !strings.Contains(got, peerHeader) || !strings.Contains(got, steerLine) {
		t.Fatalf("the running turn took %q, want the steer under the peer header", got)
	}
	run.Reply("Refactored with slog. <!-- attn:state=idle -->")
	testworld.AwaitSession(app, session, func(s protocol.Session) bool { return s.State == protocol.SessionStateIdle })
	if status := peerMessageStatus(t, cli, "lead", sent.MessageID); protocol.Deref(status.DeliveredAs) != protocol.PeerTurnSteer {
		t.Fatalf("the steer's status = %+v, want delivered as a steer", status)
	}
}

func TestAnInterruptStopsAWorkingSharedCodexAndStartsItsNextTurnWithTheMessage(t *testing.T) {
	const words = "Leave the migration for tonight."
	for _, asUser := range []bool{false, true} {
		t.Run(fmt.Sprintf("as the user %t", asUser), func(t *testing.T) {
			w := newWorld(t, fakeagent.Codex)
			app, cli := w.App(), w.Client()
			registerSessions(t, w, cli, "lead")
			session, run := sharedCodexInATurn(t, w, app)
			flags := []string{"interrupt"}
			if asUser {
				flags = append(flags, "as_user")
			}

			before := shownNow(app, session)
			sent := sendPeerMessage(t, cli, "lead", session, words, flags...)
			if sent.Status != protocol.AgentMsgStatusInterrupted {
				t.Fatalf("an interrupt to a working Codex = %+v, want interrupted", sent)
			}
			got := w.CodexServer().Prompted(run.ConversationID)
			if asUser && got != words || !asUser && (!strings.Contains(got, words) || !strings.Contains(got, interruptOrigin) || !strings.Contains(got, peerHeader)) {
				t.Fatalf("the next turn started with %q, want the interrupt (as the user: %t)", got, asUser)
			}
			stayedWorkingOnOneTurn(t, app, session, before, func() { w.CodexServer().Reply(run.ConversationID, "Left it. <!-- attn:state=idle -->") })
			if status := peerMessageStatus(t, cli, "lead", sent.MessageID); protocol.Deref(status.DeliveredAs) != protocol.PeerTurnInterrupt {
				t.Fatalf("the interrupt's status = %+v, want delivered as an interrupt", status)
			}
		})
	}
}

func TestASteerOrInterruptToAnAgentThatIsNotWorkingRingsAtOnceAsAPlainMessage(t *testing.T) {
	const words = "Use the TTL line only."
	for _, flag := range []string{"steer", "interrupt"} {
		for _, mod := range []bool{true, false} {
			t.Run(fmt.Sprintf("%s to Claude, mod %t", flag, mod), func(t *testing.T) {
				w := newWorld(t, fakeagent.Claude)
				app, cli := w.App(), w.Client()
				registerSessions(t, w, cli, "lead")
				session, run := moddedClaude(t, w, app)
				if !mod {
					run.ModStopPolling()
					w.awaitTimer(claudemod.ReachTimer)
					w.advance(claudeModReachWindow)
				}
				sent := sendPeerMessage(t, cli, "lead", session, words, flag)
				if sent.Status != protocol.AgentMsgStatusNotified {
					t.Fatalf("a %s to an idle agent = %+v, want it notified at once", flag, sent)
				}
				if got := run.Prompted(); !strings.Contains(got, inboxDoorbell) || strings.Contains(got, words) || run.ThroughMod != mod || mod && run.AsUser {
					t.Fatalf("the idle agent took %q (through the mod: %t, as the user: %t), want the doorbell, in attn's voice through the mod", got, run.ThroughMod, run.AsUser)
				}
				if status := peerMessageStatus(t, cli, "lead", sent.MessageID); status.DeliveredAs != nil {
					t.Fatalf("msg-status says a running turn took the message as %s, but no turn was running", *status.DeliveredAs)
				}
			})
		}
		t.Run(flag+" to an app server", func(t *testing.T) {
			eachAppServer(t, func(t *testing.T, h fakeagent.Harness) {
				w := newWorld(t, h)
				app, cli := w.App(), w.Client()
				registerSessions(t, w, cli, "lead")
				session, view, server := appServerSession(t, w, app, h)
				testworld.AwaitSession(app, session, func(s protocol.Session) bool { return s.State == protocol.SessionStateIdle })
				sent := sendPeerMessage(t, cli, "lead", session, words, flag)
				if sent.Status != protocol.AgentMsgStatusNotified {
					t.Fatalf("a %s to an idle %s = %+v, want it notified at once", flag, h, sent)
				}
				if submitted := server.Prompted(view.ConversationID); !strings.Contains(submitted.Text, inboxDoorbell) {
					t.Fatalf("the idle %s took %+v, want the doorbell", h, submitted)
				}
			})
		})
	}
}

func TestAnInterruptAsTheUserReachesAnIdleAppServerAsTheUsersInput(t *testing.T) {
	const words = "Leave the migration for tonight."
	eachAppServer(t, func(t *testing.T, h fakeagent.Harness) {
		w := newWorld(t, h)
		app, cli := w.App(), w.Client()
		registerSessions(t, w, cli, "lead")
		session, view, server := appServerSession(t, w, app, h)
		testworld.AwaitSession(app, session, func(s protocol.Session) bool { return s.State == protocol.SessionStateIdle })

		sent := sendPeerMessage(t, cli, "lead", session, words, "interrupt", "as_user")
		if sent.Status != protocol.AgentMsgStatusNotified || !protocol.Deref(sent.AsUser) {
			t.Fatalf("an interrupt as the user to an idle %s = %+v, want it notified at once", h, sent)
		}
		if submitted := server.Prompted(view.ConversationID); submitted.Text != words || submitted.Author == "attn" {
			t.Fatalf("%s took %+v, want the bare words from the user", h, submitted)
		}
	})
}

func TestAnInterruptSentAfterTheUserStoppedClaudeNeverStopsTheUsersNextTurn(t *testing.T) {
	_, app, cli, session, run := leadAndWorkingClaude(t)
	stoppedByEsc(t, app, run, session)

	sent := sendPeerMessage(t, cli, "lead", session, "Rename the flag to ttl_seconds.", "interrupt")
	if sent.Status != protocol.AgentMsgStatusQueued {
		t.Fatalf("an interrupt to a session the user stopped = %+v, want queued", sent)
	}
	workingOn(t, app, run, session, "carry on with the cache")
	run.Reply("Carried on. <!-- attn:state=waiting_input -->")
	if got := run.Prompted(); !strings.Contains(got, inboxDoorbell) || strings.Contains(got, "ttl_seconds") || !run.ThroughMod || run.AsUser {
		t.Fatalf("after the user's turn the agent took %q (through the mod: %t, as the user: %t), want the doorbell, with the user's turn left to end on its own", got, run.ThroughMod, run.AsUser)
	}
	if status := peerMessageStatus(t, cli, "lead", sent.MessageID); status.DeliveredAs != nil {
		t.Fatalf("the interrupt's status = %+v, want no turn use", status)
	}
}

func TestASteerReachesAWorkingSigmaInItsRunningTurn(t *testing.T) {
	w := newWorld(t, fakeagent.Sigma)
	app, cli := w.App(), w.Client()
	registerSessions(t, w, cli, "lead")
	session, view, server := appServerSession(t, w, app, fakeagent.Sigma)
	promptUntilWorking(app, session, view, server, "find the flaky checkout test")

	sent := sendPeerMessage(t, cli, "lead", session, "New code logs through slog, not log.Printf.", "steer")
	if sent.Status != protocol.AgentMsgStatusSteered {
		t.Fatalf("a steer to a working Sigma = %+v, want steered", sent)
	}
	got := server.Prompted(view.ConversationID)
	if !got.Steer || got.Author != "attn" || !strings.Contains(got.Text, "New code logs through slog") || !strings.Contains(got.Text, steerLine) {
		t.Fatalf("Sigma took %+v, want the steer under the peer header, from attn, into the running turn", got)
	}
	if status := peerMessageStatus(t, cli, "lead", sent.MessageID); protocol.Deref(status.DeliveredAs) != protocol.PeerTurnSteer {
		t.Fatalf("the steer's status = %+v, want delivered as a steer", status)
	}
}

func TestAnInterruptStopsAWorkingSigmaAndStartsItsNextRunWithTheMessage(t *testing.T) {
	const words = "Leave the migration for tonight."
	for _, asUser := range []bool{false, true} {
		t.Run(fmt.Sprintf("as the user %t", asUser), func(t *testing.T) {
			w := newWorld(t, fakeagent.Sigma)
			app, cli := w.App(), w.Client()
			registerSessions(t, w, cli, "lead")
			session, view, server := appServerSession(t, w, app, fakeagent.Sigma)
			promptUntilWorking(app, session, view, server, "find the flaky checkout test")
			flags := []string{"interrupt"}
			if asUser {
				flags = append(flags, "as_user")
			}

			before := shownNow(app, session)
			sent := sendPeerMessage(t, cli, "lead", session, words, flags...)
			if sent.Status != protocol.AgentMsgStatusInterrupted {
				t.Fatalf("an interrupt to a working Sigma = %+v, want interrupted", sent)
			}
			server.AwaitInterrupted(view.ConversationID)
			got := server.Prompted(view.ConversationID)
			if got.Steer || asUser && (got.Text != words || got.Author == "attn") || !asUser && (got.Author != "attn" || !strings.Contains(got.Text, words) || !strings.Contains(got.Text, interruptOrigin)) {
				t.Fatalf("after the interrupt Sigma took %+v, want the message as its next run (as the user: %t)", got, asUser)
			}
			stayedWorkingOnOneTurn(t, app, session, before, func() { server.Reply(view.ConversationID, "Left it. <!-- attn:state=idle -->") })
			if status := peerMessageStatus(t, cli, "lead", sent.MessageID); protocol.Deref(status.DeliveredAs) != protocol.PeerTurnInterrupt {
				t.Fatalf("the interrupt's status = %+v, want delivered as an interrupt", status)
			}
		})
	}
}

func TestAnInterruptToSigmaKeepsTheUsersQueuedPromptsForAfterItsMessage(t *testing.T) {
	w := newWorld(t, fakeagent.Sigma)
	app, cli := w.App(), w.Client()
	registerSessions(t, w, cli, "lead")
	session, view, server := appServerSession(t, w, app, fakeagent.Sigma)
	promptUntilWorking(app, session, view, server, "find the flaky checkout test")
	server.QueueFromTile(view.ConversationID, "Then bump the cache TTL.")
	queued := server.Prompted(view.ConversationID)

	sent := sendPeerMessage(t, cli, "lead", session, "Leave the migration for tonight.", "interrupt")
	if sent.Status != protocol.AgentMsgStatusInterrupted {
		t.Fatalf("an interrupt to a working Sigma = %+v, want interrupted", sent)
	}
	server.AwaitInterrupted(view.ConversationID)
	if got := server.Prompted(view.ConversationID); got.Author != "attn" || !strings.Contains(got.Text, "Leave the migration for tonight.") {
		t.Fatalf("after the interrupt Sigma took %+v first, want the interrupt's message to start the next run", got)
	}
	again := server.Prompted(view.ConversationID)
	if again.Text != queued.Text || again.Author != queued.Author || again.AuthorID != queued.AuthorID || again.Steer || again.RequestID == queued.RequestID {
		t.Fatalf("after the message Sigma took %+v, want the user's withdrawn prompt %+v submitted again as theirs, without steer, under a new request id", again, queued)
	}
}

func TestAnInterruptWhoseMessageSigmaRefusesEndsTheRunAndQueuesTheMessage(t *testing.T) {
	w := newWorld(t, fakeagent.Sigma)
	app, cli := w.App(), w.Client()
	registerSessions(t, w, cli, "lead")
	session, view, server := appServerSession(t, w, app, fakeagent.Sigma)
	promptUntilWorking(app, session, view, server, "find the flaky checkout test")
	server.RefuseNextSubmit(view.ConversationID, "TargetUnavailable")

	sent := sendPeerMessage(t, cli, "lead", session, "Leave the migration for tonight.", "interrupt")
	if sent.Status != protocol.AgentMsgStatusQueued || !strings.Contains(sent.Detail, "TargetUnavailable") {
		t.Fatalf("an interrupt whose message Sigma refused = %+v, want it queued with the refusal", sent)
	}
	server.AwaitInterrupted(view.ConversationID)
	testworld.AwaitSession(app, session, func(s protocol.Session) bool { return s.State != protocol.SessionStateWorking })
	if got := server.Prompted(view.ConversationID); got.Author != "attn" || !strings.Contains(got.Text, inboxDoorbell) {
		t.Fatalf("once the interrupted run ended Sigma took %+v, want the inbox doorbell for the queued message", got)
	}
	if status := peerMessageStatus(t, cli, "lead", sent.MessageID); status.DeliveredAs != nil {
		t.Fatalf("the interrupt's status = %+v, want it queued with no turn use", status)
	}
}

func TestASteerOrInterruptToAWorkingServerWithoutTheAbilityQueuesWithTheReason(t *testing.T) {
	for _, flag := range []string{"steer", "interrupt"} {
		t.Run(flag, func(t *testing.T) {
			w := newWorld(t, fakeagent.SampleServer)
			app, cli := w.App(), w.Client()
			registerSessions(t, w, cli, "lead")
			session, view, server := appServerSession(t, w, app, fakeagent.SampleServer)
			promptUntilWorking(app, session, view, server, "find the flaky checkout test")

			sent := sendPeerMessage(t, cli, "lead", session, "Rename the flag to ttl_seconds.", flag)
			if sent.Status != protocol.AgentMsgStatusQueued || !strings.HasSuffix(sent.Detail, "the message waits for the turn to end)") {
				t.Fatalf("a %s to a working server without the ability = %+v, want queued with the reason", flag, sent)
			}
			server.Reply(view.ConversationID, "Found it. <!-- attn:state=idle -->")
			if got := server.Prompted(view.ConversationID); !strings.Contains(got.Text, inboxDoorbell) || strings.Contains(got.Text, "ttl_seconds") {
				t.Fatalf("at the turn's end the server took %+v, want the doorbell", got)
			}
		})
	}
}

func TestASteerOrInterruptToAWorkingPluginDriverQueuesWithTheReason(t *testing.T) {
	for _, flag := range []string{"steer", "interrupt"} {
		t.Run(flag, func(t *testing.T) {
			inBubbleWithAgents(t, func(t *testing.T, w *world) {
				w.finishStartupWork()
				app := w.App()
				driver := connectDriver(t, w, "snipe-plugin", "snipe", map[string]bool{"state_reporting": true, "message_delivery": true})
				awaitDriverAvailable(app, "snipe")
				author, _ := spawnDriven(w, app, driver, w.Path("author"))
				recipient, run := spawnDriven(w, app, driver, w.Path("recipient"))
				if err := driver.state(run, 1, protocol.StateWorking); err != nil {
					t.Fatal(err)
				}
				testworld.AwaitSession(app, recipient, func(s protocol.Session) bool { return s.State == protocol.SessionStateWorking })

				sent := sendPeerMessage(t, w.Client(), author, recipient, "Rename the flag to ttl_seconds.", flag)
				if sent.Status != protocol.AgentMsgStatusQueued || !strings.HasSuffix(sent.Detail, "the message waits for the turn to end)") {
					t.Fatalf("a %s to a working plugin session = %+v, want queued with the reason", flag, sent)
				}
				if err := driver.state(run, 2, protocol.StateIdle); err != nil {
					t.Fatal(err)
				}
				var ring map[string]any
				held := driver.asked("driver.deliver_message", &ring)
				if text := fmt.Sprint(ring); !strings.Contains(text, "attn agent inbox") || strings.Contains(text, "ttl_seconds") {
					t.Fatalf("at the turn's end the driver was asked to deliver %s, want the doorbell", text)
				}
				driver.answer(held, map[string]bool{"ok": true})
			})
		})
	}
}

func TestASteerOrInterruptSentDuringAPermissionQuestionWaitsForItAndTheTurnGoesOnToTakeIt(t *testing.T) {
	approve := func(t *testing.T, w *world, app *testworld.Peer, session string) {
		t.Helper()
		mark := len(app.Log())
		if err := w.Client().UpdateState(protocol.TerminalID(w.Terminal(session)), protocol.StateWorking); err != nil {
			t.Fatalf("approve: %v", err)
		}
		testworld.AwaitSessionSince(app, mark, session, func(s protocol.Session) bool { return s.State == protocol.SessionStateWorking })
	}

	t.Run("steer", func(t *testing.T) {
		w, app, cli, session, run := leadAndWorkingClaude(t)
		settled := w.PauseAt(pausepoint.InboxRingSettled)
		permissionQuestion(t, w, app, session)

		sent := sendPeerMessage(t, cli, "lead", session, "Use the existing cache helper.", "steer")
		if sent.Status != protocol.AgentMsgStatusSteered {
			t.Fatalf("a steer during the turn's permission question = %+v, want the mod to hold it for the turn", sent)
		}
		modEvent(t, run, fakeagent.ModEventHeld, "existing cache helper")
		approve(t, w, app, session)
		appended := run.ModToolBoundary()
		if len(appended) != 1 || !strings.Contains(appended[0], "Use the existing cache helper.") || !strings.Contains(appended[0], steerLine) {
			t.Fatalf("at the first tool call after the approval the mod appended %q, want the steer", appended)
		}
		settled.Await()
		settled.Release()
		if status := peerMessageStatus(t, cli, "lead", sent.MessageID); protocol.Deref(status.DeliveredAs) != protocol.PeerTurnSteer {
			t.Fatalf("the steer's status = %+v, want delivered as a steer", status)
		}
	})

	t.Run("interrupt", func(t *testing.T) {
		w, app, cli, session, run := leadAndWorkingClaude(t)
		settled := w.PauseAt(pausepoint.InboxRingSettled)
		permissionQuestion(t, w, app, session)

		sent := sendPeerMessage(t, cli, "lead", session, "Stop: the cache helper is being removed.", "interrupt")
		if sent.Status != protocol.AgentMsgStatusQueued || !strings.Contains(sent.Detail, "approval") || !strings.Contains(sent.Detail, "interrupt") {
			t.Fatalf("an interrupt during the turn's permission question = %+v, want it kept back until the question closes", sent)
		}
		approve(t, w, app, session)
		modEvent(t, run, fakeagent.ModEventHeld, "cache helper is being removed")
		run.ModAbortLands()
		if got := run.Prompted(); !strings.Contains(got, "Stop: the cache helper is being removed.") || !strings.Contains(got, interruptOrigin) || !run.ThroughMod || run.AsUser {
			t.Fatalf("after the approval the interrupt's abort started a turn with %q (through the mod: %t, as the user: %t), want the interrupt", got, run.ThroughMod, run.AsUser)
		}
		settled.Await()
		settled.Release()
		if status := peerMessageStatus(t, cli, "lead", sent.MessageID); protocol.Deref(status.DeliveredAs) != protocol.PeerTurnInterrupt {
			t.Fatalf("the interrupt's status = %+v, want delivered as an interrupt", status)
		}
	})
}

func TestASteerOrInterruptHeldUntilTheTurnEndsOnItsOwnArrivesAsTheNextPrompt(t *testing.T) {
	for _, flag := range []string{"steer", "interrupt"} {
		t.Run(flag, func(t *testing.T) {
			w, _, cli, session, run := leadAndWorkingClaude(t)
			settled := w.PauseAt(pausepoint.InboxRingSettled)
			sent := sendPeerMessage(t, cli, "lead", session, "Keep the cache key unchanged.", flag)
			if sent.Status == protocol.AgentMsgStatusQueued {
				t.Fatalf("the %s to a working agent = %+v, want the mod to hold it", flag, sent)
			}
			modEvent(t, run, fakeagent.ModEventHeld, "cache key unchanged")
			run.Reply("Checked the cache. <!-- attn:state=waiting_input -->")
			if got := run.Prompted(); !strings.Contains(got, "Keep the cache key unchanged.") || !run.ThroughMod {
				t.Fatalf("after the turn ended on its own the agent took %q (through the mod: %t), want the held %s as its next prompt", got, run.ThroughMod, flag)
			}
			settled.Await()
			settled.Release()
			status := peerMessageStatus(t, cli, "lead", sent.MessageID)
			if status.DeliveredAs != nil || protocol.Deref(status.Turn) != protocol.PeerTurn(flag) {
				t.Fatalf("the %s's status = %+v, want it asked as a %s and taken as the next prompt, not by the running turn", flag, status, flag)
			}
		})
	}
}

func TestASteerOrInterruptToACodexWhoseTurnEndedFirstStartsItsNextTurnAndSaysSo(t *testing.T) {
	for _, flag := range []string{"steer", "interrupt"} {
		t.Run(flag, func(t *testing.T) {
			w := newWorld(t, fakeagent.Codex)
			app, cli := w.App(), w.Client()
			registerSessions(t, w, cli, "lead")
			session, run := sharedCodexInATurn(t, w, app)
			starting := w.PauseAt(pausepoint.CodexTurnStarting)

			type answer struct {
				sent *protocol.AgentMsgResult
				err  error
			}
			answered := make(chan answer, 1)
			go func() {
				sent, err := peerMessage(cli, "lead", session, "Keep the cache key unchanged.", flag)
				answered <- answer{sent, err}
			}()
			starting.Await()
			run.Reply("Checked the cache. <!-- attn:state=idle -->")
			testworld.AwaitSession(app, session, func(s protocol.Session) bool { return s.State == protocol.SessionStateIdle })
			starting.Release()
			got := <-answered
			if got.err != nil {
				t.Fatalf("the %s: %v", flag, got.err)
			}
			if got.sent.Status != protocol.AgentMsgStatusNotified {
				t.Fatalf("a %s that reached Codex after its turn ended = %+v, want notified, not %s", flag, got.sent, flag)
			}
			if prompt := w.CodexServer().Prompted(run.ConversationID); !strings.Contains(prompt, "Keep the cache key unchanged.") {
				t.Fatalf("Codex's next turn started with %q, want the %s", prompt, flag)
			}
			status := peerMessageStatus(t, cli, "lead", got.sent.MessageID)
			if status.DeliveredAs != nil || protocol.Deref(status.Turn) != protocol.PeerTurn(flag) {
				t.Fatalf("the %s's status = %+v, want it asked as a %s and taken as the next prompt", flag, status, flag)
			}
		})
	}
}

func TestAnInterruptToSigmaDuringAnApprovalWaitsForTheApprovalAndThenStopsTheRun(t *testing.T) {
	w := newWorld(t, fakeagent.Sigma)
	app, cli := w.App(), w.Client()
	registerSessions(t, w, cli, "lead")
	session, view, server := appServerSession(t, w, app, fakeagent.Sigma)
	promptUntilWorking(app, session, view, server, "find the flaky checkout test")
	server.AskApproval(view.ConversationID)
	testworld.AwaitSession(app, session, func(s protocol.Session) bool { return s.State == protocol.SessionStatePendingApproval })
	submitted := server.Submissions(view.ConversationID)

	sent := sendPeerMessage(t, cli, "lead", session, "Leave the migration for tonight.", "interrupt")
	if sent.Status != protocol.AgentMsgStatusQueued || !strings.Contains(sent.Detail, "approval") {
		t.Fatalf("an interrupt while Sigma waits on an approval = %+v, want it kept back until the approval closes", sent)
	}
	if got := server.Submissions(view.ConversationID); got != submitted {
		t.Fatalf("Sigma took %d submissions during its approval, want none", got-submitted)
	}
	server.Work(view.ConversationID)
	server.AwaitInterrupted(view.ConversationID)
	if got := server.Prompted(view.ConversationID); got.Steer || !strings.Contains(got.Text, "Leave the migration for tonight.") || !strings.Contains(got.Text, interruptOrigin) {
		t.Fatalf("after the approval Sigma took %+v, want the interrupt as its next run", got)
	}
	sendPeerMessage(t, cli, "lead", session, "The TTL lives in config/cache.yaml.")
	if status := peerMessageStatus(t, cli, "lead", sent.MessageID); protocol.Deref(status.DeliveredAs) != protocol.PeerTurnInterrupt {
		t.Fatalf("the interrupt's status = %+v, want delivered as an interrupt", status)
	}
}

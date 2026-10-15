package daemon_test

import (
	"fmt"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/protocol"
)

func TestUnreadInboxRingsAtMostThreeTimesFiveMinutesApart(t *testing.T) {
	inBubbleWithAgents(t, func(t *testing.T, w *world) {
		app, cli := w.App(), w.Client()
		recipient := w.bubbleClaude(t, app, "shop")
		registerSessions(t, w, cli, "sender")
		sendAgentMessage(t, cli, "sender", recipient.id, "the migration landed")
		for attempt := 1; attempt <= 3; attempt++ {
			recipient.reply("Later. <!-- attn:state=idle -->")
			if got := recipient.promptsContaining(inboxDoorbell); got != attempt {
				t.Fatalf("rings=%d, want %d", got, attempt)
			}
			w.advance(5*time.Minute - time.Second)
			if got := recipient.promptsContaining(inboxDoorbell); got != attempt {
				t.Fatalf("early ring: %q", recipient.prompts())
			}
			w.advance(time.Second)
		}
		w.advance(2 * time.Hour)
		if got := recipient.promptsContaining(inboxDoorbell); got != 3 {
			t.Fatalf("exhausted inbox rang %d times", got)
		}
		sendAgentMessage(t, cli, "sender", recipient.id, "a fresh item")
		if got := recipient.promptsContaining(inboxDoorbell); got != 4 {
			t.Fatalf("fresh item did not ring: %q", recipient.prompts())
		}
		if got := inboxContents(readInbox(t, cli, recipient.id, 0).Items); got != "the migration landed a fresh item" {
			t.Fatalf("inbox=%q", got)
		}
		recipient.reply("Read. <!-- attn:state=idle -->")
		w.advance(10 * time.Minute)
		if got := recipient.promptsContaining(inboxDoorbell); got != 4 {
			t.Fatalf("read inbox rang again: %q", recipient.prompts())
		}
	})
}

func TestMailForAnAgentUnderAFreshDraftRingsOnceTheUserIsQuiet(t *testing.T) {
	inBubbleWithAgents(t, func(t *testing.T, w *world) {
		app, cli := w.App(), w.Client()
		recipient := w.bubbleClaude(t, app, "shop")
		registerSessions(t, w, cli, "sender")

		app.Send(protocol.PtyInputMessage{Cmd: protocol.CmdPtyInput, ID: protocol.TerminalID(recipient.self), Data: "half a thought"})
		w.advance(10 * time.Second)
		held := sendAgentMessage(t, cli, "sender", recipient.id, "the build is green")
		if held.Status != protocol.AgentMsgStatusQueued || !strings.Contains(held.Detail, "typed") {
			t.Fatalf("mail beside a fresh draft = %+v, want it queued behind the user's typing", held)
		}
		w.advance(19 * time.Second)
		if pasted := recipient.term.Pasted(); len(pasted) != 0 {
			t.Fatalf("attn pasted %q into a composer the user typed in %s ago", pasted, 29*time.Second)
		}
		w.advance(time.Second)
		if pasted := recipient.term.Pasted(); len(pasted) != 1 || !strings.Contains(pasted[0], inboxDoorbell) {
			t.Fatalf("once the user was quiet for the window attn pasted %q, want the inbox doorbell", pasted)
		}
	})
}

func TestAReminderHeldByAFreshDraftRingsOnceTheUserIsQuiet(t *testing.T) {
	inBubbleWithAgents(t, func(t *testing.T, w *world) {
		app, cli := w.App(), w.Client()
		recipient := w.bubbleClaude(t, app, "shop")
		registerSessions(t, w, cli, "sender")

		sendAgentMessage(t, cli, "sender", recipient.id, "the build is green")
		recipient.reply("Later. <!-- attn:state=idle -->")
		w.advance(5*time.Minute - 15*time.Second)
		app.Send(protocol.PtyInputMessage{Cmd: protocol.CmdPtyInput, ID: protocol.TerminalID(recipient.self), Data: "half a thought"})

		w.advance(29 * time.Second)
		if got := recipient.promptsContaining(inboxDoorbell); got != 1 || len(recipient.term.Pasted()) != 1 {
			t.Fatalf("the reminder landed on a draft typed %s ago: pasted %q", 29*time.Second, recipient.term.Pasted())
		}
		w.advance(time.Second)
		if pasted := recipient.term.Pasted(); len(pasted) != 2 || !strings.Contains(pasted[1], inboxDoorbell) {
			t.Fatalf("once the user was quiet the reminder did not ring: pasted %q", pasted)
		}
	})
}

func TestMailArrivingAfterAReminderWaitsForTheNextRing(t *testing.T) {
	inBubbleWithAgents(t, func(t *testing.T, w *world) {
		app, cli := w.App(), w.Client()
		recipient := w.bubbleClaude(t, app, "shop")
		registerSessions(t, w, cli, "sender")

		sendAgentMessage(t, cli, "sender", recipient.id, "first")
		recipient.reply("Later. <!-- attn:state=idle -->")
		w.advance(5 * time.Minute)
		recipient.reply("Still later. <!-- attn:state=idle -->")
		if got := recipient.promptsContaining(inboxDoorbell); got != 2 {
			t.Fatalf("want the arrival ring and one reminder, got %q", recipient.prompts())
		}

		for _, body := range []string{"second", "third", "fourth"} {
			w.advance(time.Millisecond)
			sendAgentMessage(t, cli, "sender", recipient.id, body)
		}
		synctest.Wait()
		if got := recipient.promptsContaining(inboxDoorbell); got != 2 {
			t.Fatalf("new mail rang inside the reminder's quiet window: %q", recipient.prompts())
		}
		w.advance(5 * time.Minute)
		if got := recipient.promptsContaining(inboxDoorbell); got != 3 {
			t.Fatalf("after the window the burst rang %d times in all, want one more: %q", got, recipient.prompts())
		}
		if got := inboxContents(readInbox(t, cli, recipient.id, 0).Items); got != "first second third fourth" {
			t.Fatalf("the inbox holds %q", got)
		}
	})
}

func TestAReadReleasesTheOutstandingRingForNewMail(t *testing.T) {
	inBubbleWithAgents(t, func(t *testing.T, w *world) {
		app, cli := w.App(), w.Client()
		recipient := w.bubbleClaude(t, app, "shop")
		registerSessions(t, w, cli, "sender")
		sendAgentMessage(t, cli, "sender", recipient.id, "first")
		recipient.reply("Later. <!-- attn:state=idle -->")
		w.advance(time.Millisecond)
		sendAgentMessage(t, cli, "sender", recipient.id, "second")
		synctest.Wait()
		if got := recipient.promptsContaining(inboxDoorbell); got != 1 {
			t.Fatalf("outstanding ring repeated: %q", recipient.prompts())
		}
		if got := inboxContents(readInbox(t, cli, recipient.id, 0).Items); got != "first second" {
			t.Fatalf("inbox=%q", got)
		}
		sendAgentMessage(t, cli, "sender", recipient.id, "third")
		recipient.reply("Later. <!-- attn:state=idle -->")
		if got := recipient.promptsContaining(inboxDoorbell); got != 2 {
			t.Fatalf("read did not release ring: %q", recipient.prompts())
		}
	})
}

func TestInboxDeliveryWakesAgainOnlyAfterDelayAndFollowsTheMember(t *testing.T) {
	inBubbleWithAgents(t, func(t *testing.T, w *world) {
		writeCrewCharter(t, w, "trellis")
		w.restart()
		app, cli := w.App(), w.Client()
		setSetting(t, app, "crew.heartbeat_enabled", "false")
		setSetting(t, app, "crew.autosleep_enabled", "false")
		registerSessions(t, w, cli, "sender")
		sent := sendAgentMessage(t, cli, "sender", "trellis", "keep this across days")
		if sent.Status != protocol.AgentMsgStatusQueued || protocol.Deref(sent.TargetSessionID) == "" {
			t.Fatalf("asleep send=%+v", sent)
		}
		day := w.bootBubbleClaude(t, string(protocol.Deref(sent.TargetSessionID)))
		day.reply("Ready. <!-- attn:state=idle -->")
		if got := day.promptsContaining(inboxDoorbell); got != 1 {
			t.Fatalf("wake ring=%d", got)
		}
		if _, err := cli.CrewHandoff(protocol.SessionID(day.id), "sleep before reading", false, protocol.CrewDayCloseSleep); err != nil {
			t.Fatal(err)
		}
		w.advance(5*time.Minute - time.Second)
		if binding := crewRosterMember(t, cli, "trellis").BindingSession; binding != nil {
			t.Fatalf("woke early: %s", *binding)
		}
		w.advance(time.Second)
		successorID := protocol.Deref(crewRosterMember(t, cli, "trellis").BindingSession)
		if successorID == "" || string(successorID) == day.id {
			t.Fatalf("no successor: %s", successorID)
		}
		successor := w.bootBubbleClaude(t, string(successorID))
		successor.reply("Ready. <!-- attn:state=idle -->")
		items := readInbox(t, cli, string(successorID), 0).Items
		if len(items) != 1 || items[0].Address != "member:trellis" || items[0].Content != "keep this across days" {
			t.Fatalf("successor inbox=%+v", items)
		}
		if reread, err := cli.AgentInbox(sent.MessageID, successorID); err != nil || string(protocol.Deref(reread.ReadBy)) != string(successorID) {
			t.Fatalf("successor reread=%+v, %v", reread, err)
		}
		if _, err := cli.AgentInbox(sent.MessageID, protocol.SessionID(day.id)); err == nil {
			t.Fatal("old day reread member item")
		}
	})
}

func TestInboxWakeLimitRefusesWakeButKeepsTheItem(t *testing.T) {
	inBubbleWithAgents(t, func(t *testing.T, w *world) {
		writeCrewCharter(t, w, "trellis")
		w.restart()
		app, cli := w.App(), w.Client()
		registerSessions(t, w, cli, "sender")
		setSetting(t, app, "crew.wake_limit", "0")
		sent := sendAgentMessage(t, cli, "sender", "trellis", "read this on your next day")
		if sent.Status != protocol.AgentMsgStatusQueued || !strings.Contains(sent.Detail, "crew.wake_limit=0") {
			t.Fatalf("limit receipt=%+v", sent)
		}
		w.advance(15 * time.Minute)
		if binding := crewRosterMember(t, cli, "trellis").BindingSession; binding != nil {
			t.Fatalf("limit woke %s", *binding)
		}
		setSetting(t, app, "crew.wake_limit", "10")
		w.advance(5 * time.Minute)
		if binding := crewRosterMember(t, cli, "trellis").BindingSession; binding != nil {
			t.Fatalf("exhausted item woke %s", *binding)
		}
		day := w.bootBubbleClaude(t, string(wakeCrew(t, cli, "trellis", "").SessionID))
		day.reply("Ready. <!-- attn:state=idle -->")
		if day.promptsContaining(inboxDoorbell) != 0 {
			t.Fatal("exhausted inbox rang after manual wake")
		}
		if got := inboxContents(readInbox(t, cli, day.id, 0).Items); got != "read this on your next day" {
			t.Fatalf("retained inbox=%q", got)
		}
	})
}

func TestInboxAttemptsSurviveRestartWithoutAFreshBudget(t *testing.T) {
	inBubbleWithAgents(t, func(t *testing.T, w *world) {
		app, cli := w.App(), w.Client()
		recipient := w.bubbleClaude(t, app, "shop")
		registerSessions(t, w, cli, "sender")
		sendAgentMessage(t, cli, "sender", recipient.id, "read after restart")
		recipient.reply("Later. <!-- attn:state=idle -->")
		w.advance(5 * time.Minute)
		recipient.reply("Later. <!-- attn:state=idle -->")
		w.restart()
		app = w.App()
		recipient.cli = w.Client()
		w.advance(0)
		app.TypeLine(recipient.id, "resume without reading")
		recipient.reply("Later. <!-- attn:state=idle -->")
		if got := recipient.promptsContaining(inboxDoorbell); got != 2 {
			t.Fatalf("restart rang early: %q", recipient.prompts())
		}
		w.advance(5 * time.Minute)
		recipient.reply("Later. <!-- attn:state=idle -->")
		if got := recipient.promptsContaining(inboxDoorbell); got != 3 {
			t.Fatalf("third ring=%d", got)
		}
		w.advance(15 * time.Minute)
		if got := recipient.promptsContaining(inboxDoorbell); got != 3 {
			t.Fatalf("restart renewed budget: %q", recipient.prompts())
		}
	})
}

func TestBusyAndApprovalBlockedInboxesKeepTheirFullAttemptBudget(t *testing.T) {
	for _, approval := range []bool{false, true} {
		t.Run(fmt.Sprint(approval), func(t *testing.T) {
			inBubbleWithAgents(t, func(t *testing.T, w *world) {
				app, cli := w.App(), w.Client()
				recipient := w.bubbleClaude(t, app, "shop")
				registerSessions(t, w, cli, "sender")
				app.TypeLine(recipient.id, "work without checking inbox")
				if approval {
					if err := cli.RecordNotification(protocol.TerminalID(w.Terminal(recipient.id)), "permission_prompt", "Allow edit?"); err != nil {
						t.Fatal(err)
					}
				}
				w.advance(0)
				sent := sendAgentMessage(t, cli, "sender", recipient.id, "wait for a safe prompt")
				if sent.Status != protocol.AgentMsgStatusQueued {
					t.Fatalf("blocked send=%+v", sent)
				}
				w.advance(15 * time.Minute)
				if got := recipient.promptsContaining(inboxDoorbell); got != 0 {
					t.Fatalf("blocked rings=%d", got)
				}
				if approval {
					if err := cli.UpdateStateFromHookEvidence(protocol.TerminalID(w.Terminal(recipient.id)), protocol.StateWorking, "", "", ""); err != nil {
						t.Fatal(err)
					}
					w.advance(0)
				}
				recipient.reply("Ready. <!-- attn:state=idle -->")
				for attempt := 1; attempt <= 3; attempt++ {
					if got := recipient.promptsContaining(inboxDoorbell); got != attempt {
						t.Fatalf("ring=%d want %d", got, attempt)
					}
					recipient.reply("Later. <!-- attn:state=idle -->")
					w.advance(5 * time.Minute)
				}
				if got := recipient.promptsContaining(inboxDoorbell); got != 3 {
					t.Fatalf("fourth ring=%d", got)
				}
			})
		})
	}
}

func TestMailForAnUntendedSeedWaitsForItsNextTender(t *testing.T) {
	inBubbleWithAgents(t, func(t *testing.T, w *world) {
		app, cli := w.App(), w.Client()
		registerSessions(t, w, cli, "sender")
		seed := plantSeedAs(t, cli, "sender", "review the build")
		sent := sendAgentMessage(t, cli, "sender", seed, "the deployment is ready")
		if sent.Status != protocol.AgentMsgStatusQueued || protocol.Deref(sent.TargetSessionID) != "" {
			t.Fatalf("untended send=%+v", sent)
		}
		w.advance(2 * time.Hour)
		next := w.bubbleClaude(t, app, "next")
		if _, err := cli.SeedTransition(protocol.SessionID(next.id), seed, "tend", "", false, client.SeedTransitionOptions{}); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		if next.promptsContaining(inboxDoorbell) != 1 {
			t.Fatal("new tender was not rung")
		}
		mail := readInbox(t, cli, next.id, 0).Items
		if len(mail) != 1 || mail[0].Address != protocol.AddressRef("seed:"+seed) || mail[0].Content != "the deployment is ready" {
			t.Fatalf("seed mail=%+v", mail)
		}
		if reread, err := cli.AgentInbox(sent.MessageID, protocol.SessionID(next.id)); err != nil || reread.Content != "the deployment is ready" {
			t.Fatalf("seed reread=%+v, %v", reread, err)
		}
	})
}

func TestMailForASeedFollowsItsNextTenderAfterTheSessionIsRemoved(t *testing.T) {
	inBubble(t, func(t *testing.T, w *world) {
		cli := w.Client()
		registerSessions(t, w, cli, "sender", "tender", "next")
		seed := plantSeedAs(t, cli, "sender", "review the build")
		if _, err := cli.SeedTransition("tender", seed, "tend", "", false, client.SeedTransitionOptions{}); err != nil {
			t.Fatal(err)
		}
		sendAgentMessage(t, cli, "sender", seed, "the deployment is ready")
		if err := cli.Unregister("tender"); err != nil {
			t.Fatal(err)
		}
		w.advance(2 * time.Hour)
		if _, err := cli.SeedTransition("next", seed, "tend", "", false, client.SeedTransitionOptions{}); err != nil {
			t.Fatal(err)
		}
		mail := readInbox(t, cli, "next", 0).Items
		if len(mail) != 1 || mail[0].Address != protocol.AddressRef("seed:"+seed) || mail[0].Content != "the deployment is ready" {
			t.Fatalf("seed mail=%+v", mail)
		}
	})
}

func TestAGardenBellForACaseVariantTenderReachesTheRegisteredMember(t *testing.T) {
	inBubbleWithAgents(t, func(t *testing.T, w *world) {
		cli := w.Client()
		registerSessions(t, w, cli, "sender")
		seed := plantSeedAs(t, cli, "sender", "review the build")

		writeCrewCharter(t, w, "trellis")
		w.restart()
		w.App() // Initial state waits for startup recovery, which drops injected sessions.
		cli = w.Client()
		registerSessions(t, w, cli, "sender")
		if _, err := cli.SeedTransition("", seed, "tend", "", false, client.SeedTransitionOptions{Assignee: "Trellis"}); err != nil {
			t.Fatal(err)
		}
		if _, err := cli.SeedNote("sender", seed, "the deployment is ready", "", true, nil); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		dayID := protocol.Deref(crewRosterMember(t, cli, "trellis").BindingSession)
		if dayID == "" {
			t.Fatal("Garden bell did not wake the registered tender")
		}
		day := w.bootBubbleClaude(t, string(dayID))
		day.reply("Ready. <!-- attn:state=idle -->")
		mail := readInbox(t, cli, day.id, 0).Items
		if len(mail) != 1 || mail[0].Address != "member:trellis" || !strings.Contains(mail[0].Content, seed) {
			t.Fatalf("Garden mail=%+v; want canonical member address and seed update", mail)
		}
	})
}

func TestMailForAMemberTendedSeedWakesTheTender(t *testing.T) {
	inBubbleWithAgents(t, func(t *testing.T, w *world) {
		writeCrewCharter(t, w, "trellis")
		w.restart()
		cli := w.Client()
		registerSessions(t, w, cli, "sender")
		seed := plantSeedAs(t, cli, "sender", "review the build")
		if _, err := cli.SeedTransition("", seed, "tend", "", false, client.SeedTransitionOptions{Assignee: "trellis"}); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		initialID := protocol.Deref(crewRosterMember(t, cli, "trellis").BindingSession)
		initial := w.bootBubbleClaude(t, string(initialID))
		initial.reply("Ready. <!-- attn:state=idle -->")
		readInbox(t, cli, string(initialID), 0)
		if _, err := cli.CrewHandoff(initialID, "Sleep before the message", false, protocol.CrewDayCloseSleep); err != nil {
			t.Fatal(err)
		}
		sent := sendAgentMessage(t, cli, "sender", seed, "the deployment is ready")
		if protocol.Deref(sent.TargetSessionID) == "" || protocol.Deref(sent.TargetSessionID) == initialID {
			t.Fatalf("seed send=%+v", sent)
		}
		day := w.bootBubbleClaude(t, string(protocol.Deref(sent.TargetSessionID)))
		day.reply("Ready. <!-- attn:state=idle -->")
		mail := readInbox(t, cli, day.id, 0).Items
		if len(mail) != 1 || mail[0].Address != protocol.AddressRef("seed:"+seed) || mail[0].Content != "the deployment is ready" {
			t.Fatalf("seed mail=%+v", mail)
		}
	})
}

func TestAnInboxWakeThatPrimesForTenMinutesStillWaitsFiveMinutesBetweenRings(t *testing.T) {
	inBubbleWithAgents(t, func(t *testing.T, w *world) {
		writeCrewCharter(t, w, "trellis")
		w.restart()
		app, cli := w.App(), w.Client()
		setSetting(t, app, "crew.heartbeat_enabled", "false")
		setSetting(t, app, "crew.autosleep_enabled", "false")
		registerSessions(t, w, cli, "sender")
		sent := sendAgentMessage(t, cli, "sender", "trellis", "wait through priming")
		day := w.bootBubbleClaude(t, string(protocol.Deref(sent.TargetSessionID)))
		w.advance(10 * time.Minute)
		day.reply("Ready. <!-- attn:state=idle -->")
		if day.promptsContaining(inboxDoorbell) != 1 {
			t.Fatal("wake did not finish its ring")
		}
		day.reply("Later. <!-- attn:state=idle -->")
		fresh := sendAgentMessage(t, cli, "sender", "trellis", "after the first ring")
		if fresh.Status != protocol.AgentMsgStatusQueued || day.promptsContaining(inboxDoorbell) != 1 {
			t.Fatalf("early ring after priming: %+v, %q", fresh, day.prompts())
		}
		w.advance(5*time.Minute - time.Second)
		if day.promptsContaining(inboxDoorbell) != 1 {
			t.Fatal("rang before five minutes")
		}
		w.advance(time.Second)
		if day.promptsContaining(inboxDoorbell) != 2 {
			t.Fatal("second ring missing")
		}
	})
}

func TestTheThirdInboxWakeCompletesItsRingAndNeverWakesAFourthDay(t *testing.T) {
	inBubbleWithAgents(t, func(t *testing.T, w *world) {
		writeCrewCharter(t, w, "trellis")
		w.restart()
		app, cli := w.App(), w.Client()
		setSetting(t, app, "crew.heartbeat_enabled", "false")
		setSetting(t, app, "crew.autosleep_enabled", "false")
		registerSessions(t, w, cli, "sender")
		sendAgentMessage(t, cli, "sender", "trellis", "ignore across three days")
		for attempt := 1; attempt <= 3; attempt++ {
			id := protocol.Deref(crewRosterMember(t, cli, "trellis").BindingSession)
			if id == "" {
				t.Fatalf("wake %d missing", attempt)
			}
			day := w.bootBubbleClaude(t, string(id))
			day.reply("Ready. <!-- attn:state=idle -->")
			if day.promptsContaining(inboxDoorbell) != 1 {
				t.Fatalf("wake %d did not finish its ring", attempt)
			}
			if _, err := cli.CrewHandoff(id, "sleep unread", false, protocol.CrewDayCloseSleep); err != nil {
				t.Fatal(err)
			}
			w.advance(5 * time.Minute)
		}
		w.advance(30 * time.Minute)
		if bound := crewRosterMember(t, cli, "trellis").BindingSession; bound != nil {
			t.Fatalf("fourth wake=%s", *bound)
		}
	})
}

func TestAQueuedInboxItemNeverReopensAClosedSession(t *testing.T) {
	inBubbleWithAgents(t, func(t *testing.T, w *world) {
		app, cli := w.App(), w.Client()
		recipient := w.bubbleClaude(t, app, "recipient")
		registerSessions(t, w, cli, "sender")
		app.TypeLine(recipient.id, "keep working")
		w.advance(0)
		sent := sendAgentMessage(t, cli, "sender", recipient.id, "read after work")
		if sent.Status != protocol.AgentMsgStatusQueued {
			t.Fatalf("busy send=%+v", sent)
		}
		if _, err := cli.AgentClose(recipient.id, protocol.SessionID(recipient.id), "work cancelled"); err != nil {
			t.Fatal(err)
		}
		w.advance(30 * time.Minute)
		status, err := cli.AgentMsgStatus(sent.MessageID, "sender")
		if err != nil || status.State != protocol.AgentMessageStateQueued || status.NotifiedAt != nil {
			t.Fatalf("closed inbox item=%+v, %v", status, err)
		}
		if got := recipient.promptsContaining(inboxDoorbell); got != 0 {
			t.Fatalf("closed session rang %d times", got)
		}
	})
}

func TestMailArrivingDuringPrimingSharesTheRingAndItsThreeAttemptBudget(t *testing.T) {
	inBubbleWithAgents(t, func(t *testing.T, w *world) {
		writeCrewCharter(t, w, "trellis")
		w.restart()
		app, cli := w.App(), w.Client()
		setSetting(t, app, "crew.heartbeat_enabled", "false")
		setSetting(t, app, "crew.autosleep_enabled", "false")
		registerSessions(t, w, cli, "sender")
		first := sendAgentMessage(t, cli, "sender", "trellis", "before waking")
		day := w.bootBubbleClaude(t, string(protocol.Deref(first.TargetSessionID)))
		w.advance(time.Millisecond)
		second := sendAgentMessage(t, cli, "sender", "trellis", "during priming")
		if second.Status != protocol.AgentMsgStatusQueued || string(protocol.Deref(second.TargetSessionID)) != day.id {
			t.Fatalf("priming send=%+v", second)
		}
		day.reply("Ready. <!-- attn:state=idle -->")
		for attempt := 1; attempt <= 3; attempt++ {
			if day.promptsContaining(inboxDoorbell) != attempt {
				t.Fatalf("ring %d missing: %q", attempt, day.prompts())
			}
			day.reply("Later. <!-- attn:state=idle -->")
			w.advance(5 * time.Minute)
		}
		w.advance(30 * time.Minute)
		if got := day.promptsContaining(inboxDoorbell); got != 3 {
			t.Fatalf("priming batch rang %d times", got)
		}
		if got := inboxContents(readInbox(t, cli, day.id, 0).Items); got != "before waking during priming" {
			t.Fatalf("priming inbox=%q", got)
		}
	})
}

func TestRestartDuringAnInboxWakeWaitsAndSpendsAnotherAttempt(t *testing.T) {
	inBubbleWithAgents(t, func(t *testing.T, w *world) {
		writeCrewCharter(t, w, "trellis")
		w.restart()
		cli := w.Client()
		registerSessions(t, w, cli, "sender")
		sendAgentMessage(t, cli, "sender", "trellis", "read after restarting")
		synctest.Wait()
		dayID := protocol.Deref(crewRosterMember(t, cli, "trellis").BindingSession)
		day := w.bootBubbleClaude(t, string(dayID))
		w.restart()
		day.cli = w.Client()
		day.reply("Ready. <!-- attn:state=idle -->")
		if got := day.promptsContaining(inboxDoorbell); got != 0 {
			t.Fatalf("ring before wake delay=%d", got)
		}
		for want := 1; want <= 2; want++ {
			w.advance(5 * time.Minute)
			if got := day.promptsContaining(inboxDoorbell); got != want {
				t.Fatalf("ring=%d want=%d", got, want)
			}
			day.reply("Later. <!-- attn:state=idle -->")
		}
		w.advance(time.Hour)
		if got := day.promptsContaining(inboxDoorbell); got != 2 {
			t.Fatalf("renewed budget=%d", got)
		}
	})
}

func TestRereadingAnOldPeerMessageDoesNotReleaseANewerOutstandingRing(t *testing.T) {
	inBubbleWithAgents(t, func(t *testing.T, w *world) {
		cli := w.Client()
		day := w.bubbleClaude(t, w.App(), "recipient")
		registerSessions(t, w, cli, "sender")
		first := sendAgentMessage(t, cli, "sender", day.id, "first")
		if _, err := cli.AgentInbox(first.MessageID, protocol.SessionID(day.id)); err != nil {
			t.Fatal(err)
		}
		day.reply("Later. <!-- attn:state=idle -->")
		sendAgentMessage(t, cli, "sender", day.id, "second")
		if _, err := cli.AgentInbox(first.MessageID, protocol.SessionID(day.id)); err != nil {
			t.Fatal(err)
		}
		day.reply("Later. <!-- attn:state=idle -->")
		sendAgentMessage(t, cli, "sender", day.id, "third")
		synctest.Wait()
		if got := day.promptsContaining(inboxDoorbell); got != 2 {
			t.Fatalf("old read released new ring: %d", got)
		}
	})
}

func TestAPartialInboxReadAcknowledgesEveryHeldAddress(t *testing.T) {
	inBubbleWithAgents(t, func(t *testing.T, w *world) {
		writeCrewCharter(t, w, "trellis")
		w.restart()
		cli := w.Client()
		registerSessions(t, w, cli, "sender")
		day := w.bootBubbleClaude(t, string(wakeCrew(t, cli, "trellis", "").SessionID))
		day.reply("Ready. <!-- attn:state=idle -->")
		sendAgentMessage(t, cli, "sender", "session:"+day.id, "session first")
		synctest.Wait()
		day.reply("Later. <!-- attn:state=idle -->")
		w.advance(time.Millisecond)
		sendAgentMessage(t, cli, "sender", "trellis", "member first")
		day.reply("Later. <!-- attn:state=idle -->")
		if got := day.promptsContaining(inboxDoorbell); got != 2 {
			t.Fatalf("separate address rings=%d", got)
		}
		items := readInbox(t, cli, day.id, 1).Items
		if len(items) != 1 || items[0].Address != protocol.AddressRef("session:"+day.id) {
			t.Fatalf("partial read=%+v", items)
		}
		synctest.Wait()
		if got := day.promptsContaining(inboxDoorbell); got != 3 {
			t.Fatalf("unread member address was not acknowledged: %d", got)
		}
	})
}

func TestAnInboxRingsAfterASelectorClearsWithoutAnotherTurn(t *testing.T) {
	inBubbleWithAgents(t, func(t *testing.T, w *world) {
		app := w.App()
		recipient := w.bubbleClaude(t, app, "recipient")
		cli := w.Client()
		registerSessions(t, w, cli, "sender")
		recipient.term.PaintScreen("Which should I keep?\n❯ 1. The old import path\nEnter to select · Esc to cancel")
		sent := sendAgentMessage(t, cli, "sender", recipient.id, "read after dismissing")
		if sent.Status != protocol.AgentMsgStatusQueued {
			t.Fatalf("selector send=%+v", sent)
		}
		w.advance(15 * time.Minute)
		if got := recipient.promptsContaining(inboxDoorbell); got != 0 {
			t.Fatalf("selector rings=%d", got)
		}
		app.Send(protocol.PtyInputMessage{Cmd: protocol.CmdPtyInput, ID: protocol.TerminalID(recipient.self), Data: "\x1b"})
		w.advance(0)
		recipient.term.PaintScreen("❯ ")
		w.advance(29 * time.Second)
		if got := recipient.promptsContaining(inboxDoorbell); got != 0 {
			t.Fatalf("ring inside quiet window=%d", got)
		}
		w.advance(time.Second)
		if got := recipient.promptsContaining(inboxDoorbell); got != 1 {
			t.Fatalf("cleared selector rings=%d", got)
		}
		for want := 2; want <= 3; want++ {
			recipient.reply("Later. <!-- attn:state=idle -->")
			w.advance(5 * time.Minute)
			if got := recipient.promptsContaining(inboxDoorbell); got != want {
				t.Fatalf("ring=%d want=%d", got, want)
			}
		}
	})
}

func TestAMessageToACrewDaysSessionIDFollowsTheMemberIntoItsNextDay(t *testing.T) {
	inBubbleWithAgents(t, func(t *testing.T, w *world) {
		writeCrewCharter(t, w, "trellis")
		w.restart()
		cli := w.Client()
		registerSessions(t, w, cli, "sender")
		first := w.bootBubbleClaude(t, string(wakeCrew(t, cli, "trellis", "").SessionID))
		first.reply("Ready. <!-- attn:state=idle -->")
		sent := sendAgentMessage(t, cli, "sender", first.id, "Reply for the next day")
		if _, err := cli.CrewHandoff(protocol.SessionID(first.id), "sleep unread", false, protocol.CrewDayCloseSleep); err != nil {
			t.Fatal(err)
		}
		next := w.bootBubbleClaude(t, string(wakeCrew(t, cli, "trellis", "").SessionID))
		items := readInbox(t, cli, next.id, 0).Items
		if len(items) != 1 || items[0].ItemID != sent.MessageID || items[0].Address != "member:trellis" {
			t.Fatalf("successor inbox=%+v", items)
		}
	})
}

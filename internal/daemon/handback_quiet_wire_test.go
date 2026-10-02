package daemon_test

import (
	"fmt"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestAHandedBackReviewHeldByTheUsersTypingLandsOnceTheyAreQuiet(t *testing.T) {
	inBubbleWithAgents(t, func(t *testing.T, w *world) {
		app, cli := w.App(), w.Client()
		presenter := w.bubbleClaude(t, app, "presenter")
		repo := newRepo(t, "shop")
		manifest := fmt.Sprintf("version: 1\nkind: changes\ntitle: \"Checkout\"\nframe:\n  repo: %q\n  base: HEAD\n  head: HEAD\n", repo)
		opened, err := cli.PresentOpen(presenter.id, manifest, "")
		if err != nil {
			t.Fatalf("present: %v", err)
		}

		app.Send(protocol.PtyInputMessage{Cmd: protocol.CmdPtyInput, ID: presenter.id, Data: "half a thought"})
		approved := testworld.Request(app, protocol.PresentSubmitRoundMessage{Cmd: protocol.CmdPresentSubmitRound, RoundID: opened.RoundID, Verdict: "approved", Handback: true},
			protocol.EventPresentSubmitRoundResult, func(r protocol.PresentSubmitRoundResultMessage) bool { return r.RoundID == opened.RoundID })
		if !approved.Success {
			t.Fatalf("approve the round: %s", protocol.Deref(approved.Error))
		}
		w.advance(29 * time.Second)
		if pasted := presenter.term.Pasted(); len(pasted) != 0 {
			t.Fatalf("the handback pasted %q into a composer the user typed in 29s ago", pasted)
		}
		w.advance(time.Second)
		if pasted := presenter.term.Pasted(); len(pasted) != 1 || !strings.Contains(pasted[0], inboxDoorbell) {
			t.Fatalf("once the user was quiet the handback pasted %q, want the inbox doorbell", pasted)
		}
	})
}

func TestANotebookEntryForAChiefHeldByTheUsersTypingLandsOnceTheyAreQuiet(t *testing.T) {
	inBubbleWithAgents(t, func(t *testing.T, w *world) {
		app := w.App()
		chief := w.bubbleClaude(t, app, "chief")
		if set := setChiefOfStaff(app, chief.id, true); !set.Success {
			t.Fatalf("make %s the chief: %s", chief.id, protocol.Deref(set.Error))
		}
		synctest.Wait()
		chief = w.bootBubbleClaude(t, chief.id)

		app.Send(protocol.PtyInputMessage{Cmd: protocol.CmdPtyInput, ID: chief.id, Data: "half a thought"})
		sent := notebookAskSendToChief(app, "notes/today.md", "follow up on the release")
		if !sent.Success || sent.Result == nil || sent.Result.Nudged {
			t.Fatalf("sending to a chief the user is typing to = %+v, want it written and not nudged yet", sent)
		}
		w.advance(29 * time.Second)
		if pasted := chief.term.Pasted(); len(pasted) != 0 {
			t.Fatalf("the chief nudge pasted %q into a composer the user typed in 29s ago", pasted)
		}
		w.advance(time.Second)
		if pasted := chief.term.Pasted(); len(pasted) != 1 || !strings.Contains(pasted[0], inboxDoorbell) {
			t.Fatalf("once the user was quiet the chief was nudged with %q, want the inbox doorbell", pasted)
		}
	})
}

func TestAPresentationHandbackReachesItsMemberAfterThePresentingDayEnds(t *testing.T) {
	inBubbleWithAgents(t, func(t *testing.T, w *world) {
		writeCrewCharter(t, w, "trellis")
		w.restart()
		cli, app := w.Client(), w.App()
		first := w.bootBubbleClaude(t, wakeCrew(t, cli, "trellis", "").SessionID)
		first.reply("Ready. <!-- attn:state=idle -->")
		opened, err := cli.PresentOpen(first.id, presentationManifest("Checkout", newRepo(t, "shop"), "HEAD", "HEAD", ""), "")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := cli.CrewHandoff(first.id, "Review tomorrow", false, protocol.CrewDayCloseSleep); err != nil {
			t.Fatal(err)
		}
		if err := cli.Unregister(first.id); err != nil {
			t.Fatal(err)
		}
		approved := testworld.Request(app, protocol.PresentSubmitRoundMessage{Cmd: protocol.CmdPresentSubmitRound, RoundID: opened.RoundID, Verdict: "approved", Handback: true}, protocol.EventPresentSubmitRoundResult, func(r protocol.PresentSubmitRoundResultMessage) bool { return r.RoundID == opened.RoundID })
		if !approved.Success {
			t.Fatal(protocol.Deref(approved.Error))
		}
		synctest.Wait()
		nextID := protocol.Deref(crewRosterMember(t, cli, "trellis").BindingSession)
		if nextID == "" || nextID == first.id {
			t.Fatalf("no successor: %q", nextID)
		}
		next := w.bootBubbleClaude(t, nextID)
		next.reply("Ready. <!-- attn:state=idle -->")
		items := readInbox(t, cli, next.id, 0).Items
		if len(items) != 1 || items[0].Address != "member:trellis" || !strings.Contains(items[0].Content, "attn present feedback") {
			t.Fatalf("handback=%+v", items)
		}
	})
}

func TestANotebookSendCoveredByTheChiefsOutstandingRingReportsNudged(t *testing.T) {
	inBubbleWithAgents(t, func(t *testing.T, w *world) {
		app := w.App()
		chief := w.bubbleClaude(t, app, "chief")
		if set := setChiefOfStaff(app, chief.id, true); !set.Success {
			t.Fatal(protocol.Deref(set.Error))
		}
		synctest.Wait()
		chief = w.bootBubbleClaude(t, chief.id)
		first := notebookAskSendToChief(app, "notes/today.md", "first selection")
		if !first.Success || first.Result == nil || !first.Result.Nudged {
			t.Fatalf("first=%+v", first)
		}
		second := notebookAskSendToChief(app, "notes/today.md", "second selection")
		if !second.Success || second.Result == nil || !second.Result.Nudged {
			t.Fatalf("covered send=%+v", second)
		}
		if got := chief.promptsContaining(inboxDoorbell); got != 1 {
			t.Fatalf("rings=%d", got)
		}
	})
}

package daemon_test

import (
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func branchClaude(app *testworld.Peer, claude *fakeagent.Run, session string) protocol.Session {
	app.T.Helper()
	app.TypeLine(session, "/branch")
	if got := claude.Prompted(); got != "/branch" {
		app.T.Fatalf("claude received %q, want /branch", got)
	}
	return awaitSuccessor(app, session)
}

func TestABranchOffersItsClosedOriginalAndOpenReopensItAsItselfWithItsSeedAndMailBesideTheBranch(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	delegated := seedResumeDelegate(t, w, fakeagent.Claude, "api")
	original, seed := string(delegated.SessionID), delegated.SeedID
	claude := w.Launched(original)
	claude.Prompted()
	claude.Reply("Looking into it. <!-- attn:state=idle -->")
	conversation := claude.ConversationID
	profileID := app.SelectedProfile()
	desktop, _ := viewProfile(t, w, profileID).paneOf(t, original)
	row := rowDesktop{t: t, w: w, app: app, profileID: profileID, desktopID: desktop.ID}
	before := rowOf(t, row.desktop())

	branch := branchClaude(app, claude, original)
	if protocol.Deref(branch.ForkOffer) != delegated.SessionID {
		t.Fatalf("the branch %s first appeared offering %v, want %s", branch.ID, branch.ForkOffer, original)
	}
	awaitClosed(app, original)
	row.requireRow(strings.Replace(before, original, string(branch.ID), 1))
	if sent := sendAgentMessage(t, cli, string(branch.ID), seed, "the deployment is ready"); sent.Status != protocol.AgentMsgStatusQueued {
		t.Fatalf("mail for the closed original's seed = %+v, want it queued", sent)
	}

	mustOpenOriginal(app, string(branch.ID))
	back := w.Launched(original)
	if !back.Resumed || back.ConversationID != conversation {
		t.Fatalf("opening the original ran claude %q, want it resuming %s", back.Argv, conversation)
	}
	row.requireRow(strings.Replace(before, original, string(branch.ID)+" "+original, 1))
	row.requireActive(original)
	awaitNoForkOffer(app, string(branch.ID))
	if tender := tenderSessionOf(lifeShow(t, cli, seed).Seed); tender != delegated.SessionID {
		t.Errorf("after Open, seed %s is tended by %q, want the original %s", seed, tender, original)
	}
	if prompt := back.Prompted(); !strings.Contains(prompt, inboxDoorbell) {
		t.Fatalf("the reopened original was prompted with %q, want the inbox doorbell", prompt)
	}
	if mail := readInbox(t, cli, original, 0).Items; len(mail) != 1 || mail[0].Content != "the deployment is ready" {
		t.Errorf("the reopened original's inbox = %+v, want the mail that queued while it was closed", mail)
	}
}

func TestClearAndResumeInAClaudeTerminalOfferNothingWhileBranchDoes(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app := w.App()
	first := w.Spawn(app, fakeagent.Claude, w.Path("shop"))
	claude := w.Launched(first)
	app.TypeLine(first, "add a discount field")
	claude.Prompted()
	claude.Reply("Added. <!-- attn:state=idle -->")
	original := claude.ConversationID

	cleared := clearClaude(app, claude, first)
	if cleared.ForkOffer != nil {
		t.Errorf("/clear opened %s offering %s", cleared.ID, *cleared.ForkOffer)
	}
	resumeClaude(app, claude, string(cleared.ID), original)
	if back := awaitSuccessor(app, string(cleared.ID)); string(back.ID) != first || back.ForkOffer != nil {
		t.Errorf("/resume %s showed %s offering %v, want %s offering nothing", original, back.ID, back.ForkOffer, first)
	}
	if offer := queriedSession(t, w.Client(), first).ForkOffer; offer != nil {
		t.Errorf("after /clear and /resume, %s offers %s", first, *offer)
	}

	if branch := branchClaude(app, claude, first); protocol.Deref(branch.ForkOffer) != protocol.SessionID(first) {
		t.Errorf("/branch opened %s offering %v, want %s", branch.ID, branch.ForkOffer, first)
	}
}

func TestAForkStartForTheConversationTheTerminalAlreadyHoldsOffersNothing(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	first := w.Spawn(app, fakeagent.Claude, w.Path("shop"))
	terminal := protocol.TerminalID(app.Terminal(first))
	claude := w.Launched(first)
	transcript := w.Path(claude.ConversationID + ".jsonl")
	forkStart := func(when string) {
		t.Helper()
		if err := cli.ObserveAgentConversation(terminal, claude.ConversationID, transcript, "fork"); err != nil {
			t.Fatalf("SessionStart fork %s: %v", when, err)
		}
		if ids := queriedIDs(t, cli, ""); !slices.Equal(ids, []string{first}) {
			t.Errorf("after a SessionStart fork %s the sessions are %v, want only %s", when, ids, first)
		}
		if offer := queriedSession(t, cli, first).ForkOffer; offer != nil {
			t.Errorf("after a SessionStart fork %s, %s offers %s", when, first, *offer)
		}
	}

	forkStart("for the terminal's first conversation")
	app.TypeLine(first, "add a discount field")
	claude.Prompted()
	claude.Reply("Added. <!-- attn:state=idle -->")
	forkStart("repeating the conversation it holds")
}

func TestASessionStartForkFromACodexTerminalMovesItWithoutAnOffer(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	app := w.App()
	original, _, terminal := sharedCodexWaiting(t, w, app)
	side := uuid.NewString()
	rollout := w.Path(side + ".jsonl")
	meta := fmt.Sprintf("{\"timestamp\":\"2026-11-27T12:00:00Z\",\"type\":\"session_meta\",\"payload\":{\"id\":%q,\"cwd\":%q,\"source\":\"cli\"}}\n", side, w.Path("shop"))
	if err := os.WriteFile(rollout, []byte(meta), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := w.Client().ObserveAgentConversation(protocol.TerminalID(terminal), side, rollout, "fork"); err != nil {
		t.Fatalf("SessionStart fork: %v", err)
	}
	if moved := awaitSuccessor(app, original); moved.ForkOffer != nil {
		t.Errorf("a Codex SessionStart fork opened %s offering %s", moved.ID, *moved.ForkOffer)
	}
}

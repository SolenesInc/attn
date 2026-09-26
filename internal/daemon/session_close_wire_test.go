package daemon_test

import (
	"slices"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestAClosedSessionLeavesEveryLiveViewAndKeepsItsFirstClose(t *testing.T) {
	w := newWorld(t)
	app := w.App()
	cli := w.Client()
	registerSessions(t, w, cli, "live", "gone")

	closeSession(t, cli, "gone", "work finished")
	closed := awaitClosed(app, "gone")
	if protocol.Deref(closed.ClosedBy) != "gone" || protocol.Deref(closed.CloseReason) != "work finished" {
		t.Errorf("closed row = %+v, want gone closing itself because the work finished", closed)
	}
	if err := cli.Unregister("gone"); err != nil {
		t.Fatalf("a second close: %v", err)
	}
	if again := showSession(t, cli, "gone"); protocol.Deref(again.ClosedBy) != "gone" || protocol.Deref(again.CloseReason) != "work finished" ||
		protocol.Deref(again.ClosedAt) != protocol.Deref(closed.ClosedAt) {
		t.Errorf("after a second close the row = %+v, want the first close kept", again)
	}
	if err := cli.Register("gone", "gone", w.Path("gone")); err == nil {
		t.Error("registering over the closed session was accepted")
	}
	if ids := queriedIDs(t, cli, ""); !slices.Equal(ids, []string{"live"}) {
		t.Errorf("query = %v, want only the live session", ids)
	}

	w.restart()
	if got := ledgerIDs(ledger(t, w.Client(), client.SessionListOptions{Closed: true})); !slices.Equal(got, []string{"gone"}) {
		t.Errorf("closed ledger after a restart = %v, want the closed row", got)
	}
}

func TestALateReportCannotRewriteAClosedSession(t *testing.T) {
	inBubble(t, func(t *testing.T, w *world) {
		cli := w.Client()
		registerSessions(t, w, cli, "s1")
		if err := cli.UpdateState("s1", protocol.StateWorking); err != nil {
			t.Fatalf("report working: %v", err)
		}
		closeSession(t, cli, "s1", "brief delivered")
		atClose := showSession(t, cli, "s1")

		w.advance(time.Minute)
		_ = cli.UpdateState("s1", protocol.StateWaitingInput)
		_ = cli.UpdateStateFromHookEvidence("s1", protocol.StateWaitingInput, "", "Stop", "")
		_ = cli.UpdateTodos("s1", []string{"late todo"})
		_ = cli.RenameSession("s1", "renamed after the close")
		if err := cli.Register("s1", "s1", w.Path("elsewhere")); err == nil {
			t.Error("a late register recreated the closed session")
		}
		w.advance(time.Minute)

		after := showSession(t, cli, "s1")
		if after.State != atClose.State || after.LastSeen != atClose.LastSeen || after.Label != atClose.Label ||
			after.WorkspaceID != atClose.WorkspaceID || after.Directory != atClose.Directory {
			t.Errorf("closed row after late reports:\n got=%+v\nwant=%+v", after, atClose)
		}
		if ids := queriedIDs(t, cli, ""); len(ids) != 0 {
			t.Errorf("query = %v, want no live session after the close", ids)
		}
	})
}

func TestAReopenedSessionComesBackWithItsDraftPullRequestsCostTurnAndLaunch(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app := w.App()
	cli := w.Client()

	kept := w.Spawn(app, fakeagent.Claude, w.Path("shop"), func(m *protocol.SpawnSessionMessage) {
		m.Model = protocol.Ptr("sonnet")
		m.Effort = protocol.Ptr("high")
		m.YoloMode = protocol.Ptr(true)
	})
	first := w.Launched(kept)
	app.TypeLine(kept, "price the cart")
	first.Prompted()
	question := "Which currency? <!-- attn:state=waiting_input -->"
	first.Reply(question)
	owing := testworld.AwaitSession(app, kept, func(s protocol.Session) bool {
		return protocol.Deref(s.TurnOwed) && s.Usage != nil && s.Usage.TotalTokens == claudeTokens(question)
	})
	spent := owing.Usage.TotalTokens
	if saved := saveSessionAnnotations(app, kept, 1, []protocol.SessionAnnotation{}, "the tax is wrong"); !saved.Success {
		t.Fatalf("save annotation draft: %s", protocol.Deref(saved.Error))
	}
	if err := cli.RecordPullRequestCreated(kept, "https://github.com/acme/shop/pull/7"); err != nil {
		t.Fatalf("record pull request: %v", err)
	}
	testworld.AwaitSession(app, kept, func(s protocol.Session) bool { return len(s.PullRequests) == 1 })

	dropped := w.Spawn(app, fakeagent.Claude, w.Path("blog"))
	other := w.Launched(dropped)
	app.TypeLine(dropped, "draft a post")
	other.Prompted()
	other.Reply("Drafted. <!-- attn:state=idle -->")
	testworld.AwaitSession(app, dropped, func(s protocol.Session) bool { return s.State == protocol.SessionStateIdle })

	closeSession(t, cli, kept, "priced")
	closeSession(t, cli, dropped, "drafted")
	awaitClosed(app, kept)
	awaitClosed(app, dropped)
	_ = cli.UpdateState(kept, protocol.StateWorking)
	_ = cli.UpdateStateFromHookEvidence(kept, protocol.StateIdle, "", "Stop", "")

	w.restart()
	app = w.App()
	cli = w.Client()
	if _, err := cli.SessionReopen(client.SessionReopenOptions{SessionID: kept}); err != nil {
		t.Fatalf("reopen: %v", err)
	}
	resumed := w.Launched(kept)
	if closedAt := protocol.Deref(showSession(t, cli, kept).ClosedAt); closedAt != "" {
		t.Errorf("the reopened session's row still reads closed at %s", closedAt)
	}
	model, _ := flagValue(resumed.Argv, "--model")
	effort, _ := flagValue(resumed.Argv, "--effort")
	if !resumed.Resumed || resumed.ConversationID != first.ConversationID || model != "sonnet" || effort != "high" ||
		!slices.Contains(resumed.Argv, "--dangerously-skip-permissions") {
		t.Fatalf("reopen ran claude %q, want -r %s on sonnet at high effort skipping permissions", resumed.Argv, first.ConversationID)
	}
	back := queriedSession(t, cli, kept)
	if protocol.Deref(back.TurnOpenedAt) != protocol.Deref(owing.TurnOpenedAt) || !protocol.Deref(back.TurnOwed) {
		t.Errorf("reopened turn opened at %q, owed %v; want the turn opened at %q still owed despite the reports after the close",
			protocol.Deref(back.TurnOpenedAt), protocol.Deref(back.TurnOwed), protocol.Deref(owing.TurnOpenedAt))
	}
	if back.Usage == nil || back.Usage.TotalTokens != spent || len(back.PullRequests) != 1 {
		t.Errorf("reopened session usage %+v with %d pull requests, want the %d tokens and one pull request it had before the close",
			back.Usage, len(back.PullRequests), spent)
	}
	if note := protocol.Deref(getSessionAnnotations(app, kept).Note); note != "the tax is wrong" {
		t.Errorf("annotation draft after the reopen = %q, want the one saved before the close", note)
	}

	app.TypeLine(kept, "add shipping")
	resumed.Prompted()
	reply := "Shipping added. <!-- attn:state=idle -->"
	resumed.Reply(reply)
	awaitUsageTokens(app, kept, spent+claudeTokens(reply))

	w.restart()
	app = w.App()
	states := map[string]protocol.SessionState{}
	for _, s := range app.Initial.Sessions {
		states[s.ID] = s.State
	}
	if states[kept] != protocol.SessionStateRecoverable {
		t.Errorf("the reopened session came back %q after a restart, want recoverable", states[kept])
	}
	if _, ok := states[dropped]; ok {
		t.Error("the session left closed came back after a restart")
	}
}

func closeSession(t *testing.T, cli *client.Client, id, reason string) {
	t.Helper()
	if _, err := cli.AgentClose(id, id, reason); err != nil {
		t.Fatalf("%s closes itself: %v", id, err)
	}
}

func awaitClosed(app *testworld.Peer, id string) protocol.SessionLedgerEntry {
	app.T.Helper()
	closed := testworld.Await(app, protocol.EventSessionClosed, func(e protocol.WebSocketEvent) bool {
		return e.SessionLedgerEntry != nil && e.SessionLedgerEntry.ID == id
	})
	return *closed.SessionLedgerEntry
}

func showSession(t *testing.T, cli *client.Client, id string) protocol.SessionLedgerEntry {
	t.Helper()
	shown, err := cli.SessionShow(id)
	if err != nil {
		t.Fatalf("session show %s: %v", id, err)
	}
	return shown.Entry
}

func queriedIDs(t *testing.T, cli *client.Client, filter string) []string {
	t.Helper()
	sessions, err := cli.Query(filter)
	if err != nil {
		t.Fatalf("query %q: %v", filter, err)
	}
	ids := make([]string, 0, len(sessions))
	for _, s := range sessions {
		ids = append(ids, s.ID)
	}
	return ids
}

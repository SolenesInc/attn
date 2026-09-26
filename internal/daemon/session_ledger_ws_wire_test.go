package daemon_test

import (
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestTheAppReadsTheLedgerOverTheWebSocket(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app := w.App()
	panes := spawnPanes(w, app, w.Path("live"), w.Path("closed"))
	live, closed := panes[0], panes[1]
	closePane(app, closed)
	if row := awaitClosed(app, closed.session); protocol.Deref(row.ClosedBy) != "user" {
		t.Errorf("session_closed row closed by %q, want user", protocol.Deref(row.ClosedBy))
	}

	both := ledgerListOverTheWebSocket(app, "both", protocol.SessionListMessage{All: protocol.Ptr(true)})
	if !both.Success || both.Result == nil {
		t.Fatalf("session_list all = %+v, want a page", both)
	}
	want := []string{live.session, closed.session}
	slices.Sort(want)
	if got := sortedLedgerIDs(both.Result); !slices.Equal(got, want) {
		t.Errorf("session_list all = %v, want the live and the closed session %v", got, want)
	}
	if facets := both.Result.Facets; facets == nil || len(facets.Workspaces) != 2 {
		t.Errorf("facets = %+v, want a workspace choice per session", facets)
	}

	first := ledgerListOverTheWebSocket(app, "first", protocol.SessionListMessage{All: protocol.Ptr(true), Limit: protocol.Ptr(1)})
	if first.Result == nil || first.Result.NextBefore == nil {
		t.Fatalf("first page = %+v, want a cursor onto the older row", first.Result)
	}
	second := ledgerListOverTheWebSocket(app, "second", protocol.SessionListMessage{All: protocol.Ptr(true), Limit: protocol.Ptr(1), Before: first.Result.NextBefore})
	if second.Result == nil || len(second.Result.Entries) != 1 || second.Result.Facets != nil {
		t.Errorf("second page = %+v, want the next row without facets", second.Result)
	}

	inverted := ledgerListOverTheWebSocket(app, "inverted", protocol.SessionListMessage{
		Since: protocol.Ptr("2026-09-05T00:00:00Z"), Until: protocol.Ptr("2026-09-01T00:00:00Z"),
	})
	if inverted.Success || !strings.Contains(protocol.Deref(inverted.Error), "half-open") {
		t.Errorf("an inverted window = %+v, want a refusal explaining the half-open window", inverted)
	}
	vague := ledgerListOverTheWebSocket(app, "vague", protocol.SessionListMessage{Since: protocol.Ptr("yesterday")})
	if vague.Success || !strings.Contains(protocol.Deref(vague.Error), "RFC3339") {
		t.Errorf("since yesterday = %+v, want a refusal naming the format", vague)
	}

	shown := ledgerShowOverTheWebSocket(app, closed.session)
	if !shown.Success || shown.Entry == nil || shown.Entry.ID != closed.session {
		t.Errorf("session_show %s = %+v, want its ledger row", closed.session, shown)
	}
	if unknown := ledgerShowOverTheWebSocket(app, "elsewhere"); unknown.Success || !strings.Contains(protocol.Deref(unknown.Error), "elsewhere") {
		t.Errorf("session_show elsewhere = %+v, want a refusal naming the session", unknown)
	}
}

func TestTheAppIsToldWhyAReopenIsRefusedAndWhatIsOffered(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app := w.App()
	dir := w.Path("scratch")
	session := w.Spawn(app, fakeagent.Claude, dir)
	run := w.Launched(session)
	app.TypeLine(session, "sketch the plan")
	run.Prompted()
	run.Reply("Sketched. <!-- attn:state=idle -->")
	testworld.AwaitSession(app, session, func(s protocol.Session) bool { return s.State == protocol.SessionStateIdle })
	closeSession(t, w.Client(), session, "sketched")
	awaitClosed(app, session)
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}

	refused := reopenOverTheWebSocket(app, session)
	if refused.Success || !strings.Contains(protocol.Deref(refused.Error), "Offered instead: start_fresh_elsewhere") {
		t.Errorf("reopening in a deleted directory = %+v, want a refusal naming the action offered instead", refused)
	}
	if refused.Reopen == nil || !slices.Equal(refused.Reopen.Actions, []protocol.SessionReopenAction{protocol.SessionReopenActionStartFreshElsewhere}) {
		t.Errorf("reopen verdict = %+v, want it to offer start_fresh_elsewhere", refused.Reopen)
	}

	if unknown := reopenOverTheWebSocket(app, "never-ran"); unknown.Success || !strings.Contains(protocol.Deref(unknown.Error), "no ledger row") {
		t.Errorf("reopening a session that never ran = %+v, want the no-ledger-row refusal", unknown)
	}
}

func ledgerListOverTheWebSocket(app *testworld.Peer, requestID string, msg protocol.SessionListMessage) protocol.SessionListResultMessage {
	app.T.Helper()
	msg.Cmd, msg.RequestID = protocol.CmdSessionList, protocol.Ptr(requestID)
	return testworld.Request(app, msg, protocol.EventSessionListResult, func(r protocol.SessionListResultMessage) bool { return r.RequestID == requestID })
}

func ledgerShowOverTheWebSocket(app *testworld.Peer, session string) protocol.SessionShowResultMessage {
	app.T.Helper()
	requestID := "show-" + session
	return testworld.Request(app, protocol.SessionShowMessage{Cmd: protocol.CmdSessionShow, RequestID: protocol.Ptr(requestID), SessionID: session},
		protocol.EventSessionShowResult, func(r protocol.SessionShowResultMessage) bool { return r.RequestID == requestID })
}

func reopenOverTheWebSocket(app *testworld.Peer, session string, action ...protocol.SessionReopenAction) protocol.SessionReopenResultMessage {
	app.T.Helper()
	requestID := uuid.NewString()
	msg := protocol.SessionReopenMessage{Cmd: protocol.CmdSessionReopen, RequestID: protocol.Ptr(requestID), SessionID: session}
	for _, a := range action {
		msg.Action = protocol.Ptr(a)
	}
	return testworld.Request(app, msg, protocol.EventSessionReopenResult, func(r protocol.SessionReopenResultMessage) bool { return r.RequestID == requestID })
}

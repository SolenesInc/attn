package daemon_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func registerDelegationCaller(t *testing.T, w *world, cli *client.Client, id string) string {
	t.Helper()
	registerSessions(t, w, cli, id)
	cwd := w.Path(id, "work")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	return cwd
}

func plantDelegationSeed(t *testing.T, cli *client.Client, sessionID, title string) string {
	t.Helper()
	planted, err := cli.SeedPlant(sessionID, title, "Work on "+strings.ToLower(title)+".", "", "", "")
	if err != nil {
		t.Fatalf("plant %q: %v", title, err)
	}
	return planted.Seed.ID
}

func moveDelegationSeed(t *testing.T, cli *client.Client, sessionID, seedID, verb, reason string) {
	t.Helper()
	if _, err := cli.SeedTransition(sessionID, seedID, verb, reason, "", false, client.SeedTransitionOptions{}); err != nil {
		t.Fatalf("%s moves %s to %s: %v", sessionID, seedID, verb, err)
	}
}

func delegateAtSeed(source, cwd, seedID string) protocol.DelegateMessage {
	return protocol.DelegateMessage{
		Cmd: protocol.CmdDelegate, Cwd: cwd, SourceSessionID: protocol.Ptr(source), Agent: protocol.Ptr("codex"),
		Assignment: protocol.DelegateAssignment{Kind: protocol.DelegateAssignmentKindSeed, SeedID: protocol.Ptr(seedID)},
	}
}

func TestDelegatingAtASeedHandsItToTheDelegate(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	cli := w.Client()
	cwd := registerDelegationCaller(t, w, cli, "caller")
	registerSessions(t, w, cli, "holder", "gone")
	open := plantDelegationSeed(t, cli, "caller", "Open plot")
	abandoned := plantDelegationSeed(t, cli, "caller", "Abandoned plot")
	moveDelegationSeed(t, cli, "gone", abandoned, "tend", "")
	if err := cli.Unregister("gone"); err != nil {
		t.Fatal(err)
	}
	own := plantDelegationSeed(t, cli, "caller", "The caller's plot")
	moveDelegationSeed(t, cli, "caller", own, "tend", "")
	held := plantDelegationSeed(t, cli, "caller", "Somebody else's plot")
	moveDelegationSeed(t, cli, "holder", held, "tend", "")
	harvested := plantDelegationSeed(t, cli, "caller", "Finished plot")
	moveDelegationSeed(t, cli, "caller", harvested, "tend", "")
	moveDelegationSeed(t, cli, "caller", harvested, "harvest", "done")
	repo := newRepo(t, "shop")
	planted, err := cli.SeedList("", false, 0)
	if err != nil {
		t.Fatal(err)
	}

	for i, row := range []struct {
		name, seed  string
		cwd         string
		checkout    *protocol.DelegateCheckout
		handover    bool
		refusal     []string
		predecessor string
	}{
		{name: "an untended seed", seed: open},
		{name: "a seed whose tender is gone", seed: abandoned},
		{name: "the caller's own seed without a handover", seed: own, refusal: []string{own, "is tended by the source session; use --handover"}},
		{name: "the caller's own seed into a new worktree without a handover", seed: own, cwd: repo,
			checkout: delegateNewWorktree("feat/unexpected", "main"), refusal: []string{own, "use --handover"}},
		{name: "the caller's own seed, handed over", seed: own, handover: true, predecessor: "caller"},
		{name: "a seed a live session tends", seed: held, refusal: []string{held, "is being tended by holder"}},
		{name: "a harvested seed", seed: harvested, refusal: []string{harvested, "replant"}},
	} {
		request := delegateAtSeed("caller", cwd, row.seed)
		if row.cwd != "" {
			request.Cwd, request.Checkout = row.cwd, row.checkout
		}
		request.Label = protocol.Ptr("delegate-" + string(rune('a'+i)))
		if row.handover {
			request.Assignment.Handover = &protocol.DelegateHandover{}
		}
		result, err := cli.Delegate(request)
		if row.refusal != nil {
			for _, want := range row.refusal {
				if err == nil || !strings.Contains(err.Error(), want) {
					t.Errorf("%s: delegating = %+v, %v; want a refusal naming %q", row.name, result, err, want)
				}
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: %v", row.name, err)
			continue
		}
		shown, err := cli.SeedShow("", row.seed)
		if err != nil || result.SeedID != row.seed || shown.Seed.TenderSession != result.SessionID {
			t.Errorf("%s: the delegation bound seed %s and left %+v, %v; want %s tended by %s", row.name, result.SeedID, shown, err, row.seed, result.SessionID)
		}
		if predecessor := protocol.Deref(result.PredecessorSessionID); predecessor != row.predecessor {
			t.Errorf("%s: the delegation reports predecessor %q; want %q", row.name, predecessor, row.predecessor)
		}
	}

	if _, err := os.Stat(filepath.Join(filepath.Dir(repo), "shop--feat-unexpected")); !os.IsNotExist(err) {
		t.Errorf("the refused dispatch into a new worktree created it: %v", err)
	}
	if branches := runGit(t, repo, "branch", "--list", "feat/unexpected"); strings.TrimSpace(branches) != "" {
		t.Errorf("the refused dispatch into a new worktree created its branch")
	}
	if after, err := cli.SeedList("", false, 0); err != nil || after.Total != planted.Total {
		t.Errorf("delegating at seeds changed the garden from %d to %+v, %v seeds; want nothing planted", planted.Total, after, err)
	}
	if sessions, err := cli.Query(""); err != nil || len(sessions) != 5 {
		t.Errorf("sessions after the delegations are %+v, %v; want caller, holder and the three delegates that were not refused", sessions, err)
	}
}

func subscribeToDelegatedSeedNotes(app *testworld.Peer, seedID string) string {
	app.T.Helper()
	subscription := subscribeOverTheWire(app, protocol.DocumentQuery{Namespace: "core/garden", Collection: "notes", Filters: []protocol.DocumentFilter{where("seed", "eq", seedID)}})
	awaitDelivery(app, subscription, func(m protocol.DocSubscriptionDeliveryMessage) bool { return m.Delivery == 1 })
	return subscription
}

func TestADelegatesTicketReportsLandOnItsSeed(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	app, cli := w.App(), w.Client()
	cwd := registerDelegationCaller(t, w, cli, "caller")
	worker, err := cli.Delegate(delegateFrom("caller", cwd, "Migrate the store", fakeagent.Codex))
	if err != nil {
		t.Fatal(err)
	}
	peerRequest := delegateFrom("caller", cwd, "Something else entirely", fakeagent.Codex)
	peerRequest.Label = protocol.Ptr("peer")
	peer, err := cli.Delegate(peerRequest)
	if err != nil {
		t.Fatal(err)
	}
	workerNotes := subscribeToDelegatedSeedNotes(app, worker.SeedID)
	createTicket(t, cli, "caller", "Migrate the store", "migrate")
	if _, err := cli.TakeTicket(worker.SessionID, "migrate", true); err != nil {
		t.Fatalf("the worker takes the ticket: %v", err)
	}

	reportTicket(t, cli, peer.SessionID, "migrate", protocol.DispatchWorkStateInProgress, "nudging")
	reports := []struct {
		state          protocol.DispatchWorkState
		comment        string
		heldForCatchUp bool
	}{
		{protocol.DispatchWorkStateInProgress, "reading the store layer", true},
		{protocol.DispatchWorkStateReadyForReview, "PR #1 is up", false},
		{protocol.DispatchWorkStateCompleted, "merged", false},
	}
	for i, report := range reports {
		if report.heldForCatchUp {
			if held, err := cli.SetTicketStatus(worker.SessionID, string(report.state), report.comment, "migrate"); err != nil || held.Applied {
				t.Fatalf("the worker's report of %s after the peer's = %+v, %v; want it held for catch-up", report.state, held, err)
			}
		}
		reportTicket(t, cli, worker.SessionID, "migrate", report.state, report.comment)
		awaitDelivery(app, workerNotes, func(m protocol.DocSubscriptionDeliveryMessage) bool { return len(m.Order) == i+1 })
	}

	notes, err := cli.SeedNotes("", worker.SeedID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(notes.Notes) != len(reports) {
		t.Fatalf("the worker's seed holds notes %+v; want one per report", notes.Notes)
	}
	for _, report := range reports {
		found := false
		for _, note := range notes.Notes {
			if strings.Contains(note.Body, string(report.state)) && strings.Contains(note.Body, report.comment) && note.AuthorSession == worker.SessionID {
				found = true
			}
		}
		if !found {
			t.Errorf("no note by %s reports %s with %q; the seed holds %+v", worker.SessionID, report.state, report.comment, notes.Notes)
		}
	}
	if shown, err := cli.SeedShow("", worker.SeedID); err != nil || shown.Seed.Status != "growing" || shown.Seed.TenderSession != worker.SessionID {
		t.Errorf("after the reports the worker's seed is %+v, %v; want it still growing under %s", shown, err, worker.SessionID)
	}
	if peerLog, err := cli.SeedNotes("", peer.SeedID, 10); err != nil || len(peerLog.Notes) != 0 {
		t.Errorf("reporting on somebody else's ticket left notes %+v, %v on the peer's seed", peerLog, err)
	}
}

func TestAMessageToASeedReachesItsTenderOrIsRefusedByName(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	cli := w.Client()
	cwd := registerDelegationCaller(t, w, cli, "caller")
	delegated, err := cli.Delegate(delegateFrom("caller", cwd, "Migrate the store", fakeagent.Codex))
	if err != nil {
		t.Fatal(err)
	}
	untended := plantSeedAs(t, cli, "caller", "Nobody has this")

	sendAgentMessage(t, cli, "caller", delegated.SeedID, "the schema moved")
	if inbox := inboxContents(readInbox(t, cli, delegated.SessionID, 0).Items); !strings.Contains(inbox, "the schema moved") {
		t.Errorf("the seed's tender received %q; want the message sent to its seed", inbox)
	}
	_, err = cli.AgentMsg(untended, "caller", "anyone there?")
	for _, want := range []string{"nobody is tending", untended, "attn seed note"} {
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("messaging untended seed %s = %v; want a refusal naming %q", untended, err, want)
		}
	}
}

func TestNestedDelegatesCarryTheirSeedAndDispatcher(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	app, cli := w.App(), w.Client()
	cwd := registerDelegationCaller(t, w, cli, "caller")
	observer := w.Spawn(app, fakeagent.Codex, w.Path("observer"))
	first, err := cli.Delegate(delegateFrom("caller", cwd, "Plan the migration", fakeagent.Codex))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cli.SeedWatch(observer, first.SeedID, false); err != nil {
		t.Fatal(err)
	}
	nestedRequest := delegateFrom(first.SessionID, cwd, "Migrate the first table", fakeagent.Codex)
	nestedRequest.Label = protocol.Ptr("first table")
	nested, err := cli.Delegate(nestedRequest)
	if err != nil {
		t.Fatalf("the delegate delegates in turn: %v", err)
	}

	shown, err := cli.SeedShow("", nested.SeedID)
	if err != nil {
		t.Fatal(err)
	}
	partOfFirst := false
	for _, edge := range shown.Seed.Edges {
		partOfFirst = partOfFirst || edge.Kind == "part-of" && edge.To == first.SeedID
	}
	if !partOfFirst || shown.Seed.PlanterSession != first.SessionID || shown.Seed.TenderSession != nested.SessionID {
		t.Errorf("the nested delegation planted %+v; want it part of %s, planted by %s and tended by %s", shown.Seed, first.SeedID, first.SessionID, nested.SessionID)
	}
	for _, want := range []struct{ session, seed, dispatcher string }{
		{first.SessionID, first.SeedID, "caller"},
		{nested.SessionID, nested.SeedID, first.SessionID},
	} {
		testworld.AwaitSession(app, want.session, func(s protocol.Session) bool {
			return protocol.Deref(s.SeedID) == want.seed && protocol.Deref(s.DispatcherSessionID) == want.dispatcher
		})
	}
	if caller := sessionOfDelegate(t, w, "caller"); caller.SeedID != nil || caller.DispatcherSessionID != nil {
		t.Errorf("the caller reads as %+v; want no seed and no dispatcher", caller)
	}

	if prompt := w.Launched(observer).Prompted(); !strings.Contains(prompt, inboxDoorbell) {
		t.Fatalf("the plot's watcher was prompted %q; want the inbox doorbell", prompt)
	}
	bells := readInbox(t, cli, observer, 0).Items
	if len(bells) != 1 || protocol.Deref(bells[0].Hint) != "tended" || !strings.Contains(bells[0].Content, nested.SeedID) {
		t.Errorf("the plot's watcher received %q; want one bell that %s is tended", inboxContents(bells), nested.SeedID)
	}
	for _, directlyPrompted := range []string{first.SessionID, nested.SessionID} {
		if items := readInbox(t, cli, directlyPrompted, 0).Items; len(items) != 0 {
			t.Errorf("%s, who planned or was prompted with the work, received %q", directlyPrompted, inboxContents(items))
		}
	}
}

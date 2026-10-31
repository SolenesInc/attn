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
	planted, err := cli.SeedPlant(protocol.SessionID(sessionID), title, "Work on "+strings.ToLower(title)+".", "", "")
	if err != nil {
		t.Fatalf("plant %q: %v", title, err)
	}
	return planted.Seed.ID
}

func moveDelegationSeed(t *testing.T, cli *client.Client, sessionID, seedID, verb, reason string) {
	t.Helper()
	if _, err := cli.SeedTransition(protocol.SessionID(sessionID), seedID, verb, reason, false, client.SeedTransitionOptions{}); err != nil {
		t.Fatalf("%s moves %s to %s: %v", sessionID, seedID, verb, err)
	}
}

func delegateAtSeed(source, cwd, seedID string) protocol.DelegateMessage {
	return protocol.DelegateMessage{
		Cmd: protocol.CmdDelegate, Cwd: cwd, SourceSessionID: protocol.Ptr(protocol.SessionID(source)), Agent: protocol.Ptr("codex"),
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
		if err != nil || result.SeedID != row.seed || tenderSessionOf(shown.Seed) != result.SessionID {
			t.Errorf("%s: the delegation bound seed %s and left %+v, %v; want %s tended by %s", row.name, result.SeedID, shown, err, row.seed, result.SessionID)
		}
		if predecessor := protocol.Deref(result.PredecessorSessionID); string(predecessor) != row.predecessor {
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

func TestADelegationOpeningNamesTheSeedsChecksOrSaysItHasNone(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	cli := w.Client()
	cwd := registerDelegationCaller(t, w, cli, "caller")
	plot, err := cli.SeedPlot("caller", protocol.SeedPlotMessage{
		Title:  "search moves to the daemon",
		Checks: []protocol.SeedCheckDefinition{{ID: protocol.Ptr("C5"), Text: "Landed on main.", Shared: protocol.Ptr(true)}},
		Children: []protocol.SeedPlotChild{{Title: "daemon search endpoint", Body: protocol.Ptr("Serve search from the daemon."),
			Checks: []protocol.SeedCheckDefinition{{ID: protocol.Ptr("C1"), Text: "Wire tests cover the queries."}}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	leaf := plot.Children[0].ID
	for _, id := range []string{"C3", "C4"} {
		add := checkMove(leaf, id, "add", "")
		add.Text = protocol.Ptr("Covers " + id + ".")
		checkMoveOK(t, cli, add)
	}
	checkMoveOK(t, cli, checkMove(leaf, "C4", "remove", ""))

	request := delegateAtSeed("caller", cwd, leaf)
	request.Label = protocol.Ptr("leaf")
	withChecks, err := cli.Delegate(request)
	if err != nil {
		t.Fatal(err)
	}
	prompt := w.Launched(string(withChecks.SessionID)).Prompted()
	if !strings.Contains(prompt, "Report C1, C3 and the plot's shared C5 with `attn seed check report`") || strings.Contains(prompt, "C4") {
		t.Errorf("the delegate on a seed with checks was prompted %q; want C1, C3 and the shared C5 named, the removed C4 left out", prompt)
	}

	request = delegateFrom("caller", cwd, "Tidy the README", fakeagent.Codex)
	request.Label = protocol.Ptr("readme")
	without, err := cli.Delegate(request)
	if err != nil {
		t.Fatal(err)
	}
	prompt = w.Launched(string(without.SessionID)).Prompted()
	if !strings.Contains(prompt, "The seed has no checks, so record how you verified the outcome in a note.") || strings.Contains(prompt, "check report") {
		t.Errorf("the delegate on a seed without checks was prompted %q; want the no-checks sentence and no check reporting", prompt)
	}
}

func TestASeedBeingDelegatedRefusesOtherClaimsWhileItsDelegateBoots(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	app, cli := w.App(), w.Client()
	cwd := registerDelegationCaller(t, w, cli, "caller")
	registerSessions(t, w, cli, "contender")
	seed := plantDelegationSeed(t, cli, "caller", "Reserved work")
	request := delegateAtSeed("caller", cwd, seed)
	request.RequestID = "reserve"
	boot := w.HoldNextBoot()
	accepted, err := cli.StartDelegation(request)
	if err != nil {
		t.Fatal(err)
	}
	testworld.AwaitSession(app, string(accepted.SessionID), func(protocol.Session) bool { return true })

	if _, err := cli.SeedTransition("contender", seed, "tend", "", false, client.SeedTransitionOptions{}); err == nil || !strings.Contains(err.Error(), string("being tended by "+accepted.SessionID)) {
		t.Errorf("claiming the seed while its delegate boots = %v, want it refused naming the delegate", err)
	}
	boot()
	if result, err := cli.Delegate(request); err != nil || result.SessionID != accepted.SessionID {
		t.Fatalf("the delegation = %+v, %v; want it to finish as %s", result, err, accepted.SessionID)
	}
	if shown := lifeShow(t, cli, seed); tenderSessionOf(shown.Seed) != accepted.SessionID {
		t.Errorf("the seed is tended by %q, want the delegate %s", tenderSessionOf(shown.Seed), accepted.SessionID)
	}
}

func TestAMessageToASeedReachesItsCurrentOrNextTender(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	cli := w.Client()
	cwd := registerDelegationCaller(t, w, cli, "caller")
	delegated, err := cli.Delegate(delegateFrom("caller", cwd, "Migrate the store", fakeagent.Codex))
	if err != nil {
		t.Fatal(err)
	}
	untended := plantSeedAs(t, cli, "caller", "Nobody has this")

	sendAgentMessage(t, cli, "caller", delegated.SeedID, "the schema moved")
	if inbox := inboxContents(readInbox(t, cli, string(delegated.SessionID), 0).Items); !strings.Contains(inbox, "the schema moved") {
		t.Errorf("the seed's tender received %q; want the message sent to its seed", inbox)
	}
	sendAgentMessage(t, cli, "caller", untended, "anyone there?")
	if _, err := cli.SeedTransition(delegated.SessionID, untended, "tend", "", false, client.SeedTransitionOptions{}); err != nil {
		t.Fatal(err)
	}
	if mail := inboxContents(readInbox(t, cli, string(delegated.SessionID), 0).Items); !strings.Contains(mail, "anyone there?") {
		t.Fatalf("next tender inbox=%q", mail)
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
	if _, err := cli.SeedWatch(protocol.SessionID(observer), first.SeedID, false); err != nil {
		t.Fatal(err)
	}
	nestedRequest := delegateFrom(string(first.SessionID), cwd, "Migrate the first table", fakeagent.Codex)
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
	if !partOfFirst || planterSessionOf(shown.Seed) != first.SessionID || tenderSessionOf(shown.Seed) != nested.SessionID {
		t.Errorf("the nested delegation planted %+v; want it part of %s, planted by %s and tended by %s", shown.Seed, first.SeedID, first.SessionID, nested.SessionID)
	}
	for _, want := range []struct{ session, seed, dispatcher string }{
		{string(first.SessionID), first.SeedID, "caller"},
		{string(nested.SessionID), nested.SeedID, string(first.SessionID)},
	} {
		testworld.AwaitSession(app, want.session, func(s protocol.Session) bool {
			return protocol.Deref(s.SeedID) == want.seed && string(protocol.Deref(s.DispatcherSessionID)) == want.dispatcher
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
	for _, directlyPrompted := range []string{string(first.SessionID), string(nested.SessionID)} {
		if items := readInbox(t, cli, directlyPrompted, 0).Items; len(items) != 0 {
			t.Errorf("%s, who planned or was prompted with the work, received %q", directlyPrompted, inboxContents(items))
		}
	}
}

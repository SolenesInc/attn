package daemon_test

import (
	"maps"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestTheLedgerPagesClosedSessionsNewestFirst(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app := w.App()
	cli := w.Client()
	shop := w.Path("shop")
	panes := spawnPanes(w, app, shop, shop, shop, shop, shop, shop)
	live, a, b, c, d, e := panes[0].session, panes[1].session, panes[2].session, panes[3].session, panes[4].session, panes[5].session
	for _, closing := range panes[1:] {
		closePane(app, closing)
	}

	first := ledger(t, cli, client.SessionListOptions{Closed: true, Limit: 2})
	if got := ledgerIDs(first); !slices.Equal(got, []string{e, d}) || first.Omitted != 3 || protocol.Deref(first.NextBefore) != d {
		t.Fatalf("first page = %v omitting %d before %q, want [%s %s] omitting 3 before %s", got, first.Omitted, protocol.Deref(first.NextBefore), e, d, d)
	}
	second := ledger(t, cli, client.SessionListOptions{Closed: true, Limit: 2, Before: d})
	if got := ledgerIDs(second); !slices.Equal(got, []string{c, b}) || second.Omitted != 1 {
		t.Fatalf("second page = %v omitting %d, want [%s %s] omitting 1", got, second.Omitted, c, b)
	}
	last := ledger(t, cli, client.SessionListOptions{Closed: true, Limit: 2, Before: protocol.Deref(second.NextBefore)})
	if got := ledgerIDs(last); !slices.Equal(got, []string{a}) || last.Omitted != 0 || last.NextBefore != nil {
		t.Fatalf("last page = %v omitting %d before %q, want [%s] with nothing left", got, last.Omitted, protocol.Deref(last.NextBefore), a)
	}

	if got := ledgerIDs(ledger(t, cli, client.SessionListOptions{})); !slices.Equal(got, []string{live}) {
		t.Errorf("live scope = %v, want only the open session %s", got, live)
	}
	if got := ledgerIDs(ledger(t, cli, client.SessionListOptions{All: true})); len(got) != 6 || !slices.Contains(got, live) || !slices.Contains(got, a) {
		t.Errorf("all scope = %v, want the open session and every closed one", got)
	}

	_, err := cli.SessionList(client.SessionListOptions{Limit: 1001})
	if err == nil || !strings.Contains(err.Error(), "1001") || !strings.Contains(err.Error(), "1000") {
		t.Errorf("an over-limit page = %v, want a refusal naming the ask and the limit", err)
	}
	_, err = cli.SessionList(client.SessionListOptions{Before: "never-existed"})
	if err == nil || !strings.Contains(err.Error(), "never-existed") {
		t.Errorf("paging from an unknown row = %v, want a refusal naming the cursor", err)
	}
}

func TestTheLedgerWindowIsHalfOpenOverEachRowsInstant(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app := w.App()
	cli := w.Client()

	shop := w.Path("shop")
	seenBeforeTheWindow := spawnPanes(w, app, shop, shop)
	closedInside := seenBeforeTheWindow[1]
	inside := spawnPanes(w, app, shop)[0]
	since := showSession(t, cli, inside.session).LastSeen
	closedAfter := spawnPanes(w, app, shop)[0]
	closePane(app, closedInside)
	atUntil := spawnPanes(w, app, shop)[0]
	until := showSession(t, cli, atUntil.session).LastSeen
	closePane(app, closedAfter)

	page := ledger(t, cli, client.SessionListOptions{All: true, Since: since, Until: until})
	want := []string{closedInside.session, inside.session}
	slices.Sort(want)
	if got := sortedLedgerIDs(page); !slices.Equal(got, want) {
		t.Errorf("window [%s, %s) = %v, want the row seen at its start and the one closed inside it %v", since, until, got, want)
	}
}

func TestTheLedgerFiltersByWorkspaceAndRepositoryAndCountsEveryChoice(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app := w.App()
	cli := w.Client()
	attn := newRepo(t, "attn")
	other := newRepo(t, "other")
	panes := spawnPanes(w, app, attn, filepath.Join(attn, "app"), other)
	attnOne, attnTwo, elsewhere := panes[0], panes[1], panes[2]

	byRepository := ledger(t, cli, client.SessionListOptions{Repository: attn})
	want := []string{attnOne.session, attnTwo.session}
	slices.Sort(want)
	if got := sortedLedgerIDs(byRepository); !slices.Equal(got, want) {
		t.Errorf("repository %s = %v, want both sessions in it %v", attn, got, want)
	}
	if got := ledgerIDs(ledger(t, cli, client.SessionListOptions{WorkspaceID: attnTwo.workspace})); !slices.Equal(got, []string{attnTwo.session}) {
		t.Errorf("%s = %v, want its one session %s", attnTwo.workspace, got, attnTwo.session)
	}
	if got := ledgerIDs(ledger(t, cli, client.SessionListOptions{WorkspaceID: elsewhere.workspace, Repository: attn})); len(got) != 0 {
		t.Errorf("%s in repository %s = %v, want nothing matching both", elsewhere.workspace, attn, got)
	}

	requestID := "facets"
	faceted := testworld.Request(app, protocol.SessionListMessage{
		Cmd: protocol.CmdSessionList, RequestID: protocol.Ptr(requestID), Repository: protocol.Ptr(attn),
	}, protocol.EventSessionListResult, func(r protocol.SessionListResultMessage) bool { return r.RequestID == requestID })
	if !faceted.Success || faceted.Result == nil || faceted.Result.Facets == nil {
		t.Fatalf("faceted list = %+v, want a page with its facets", faceted)
	}
	facets := faceted.Result.Facets
	if got := facetCounts(facets.Workspaces); !maps.Equal(got, map[string]int{attnOne.workspace: 1, attnTwo.workspace: 1, elsewhere.workspace: 1}) {
		t.Errorf("workspace facets = %v, want every workspace the repository filter hides too", got)
	}
	if got := facetCounts(facets.Repositories); !maps.Equal(got, map[string]int{attn: 2, other: 1}) {
		t.Errorf("repository facets = %v, want every repository the filter hides too", got)
	}
}

type sessionPane struct {
	session, workspace, pane string
}

func spawnPanes(w *world, app *testworld.Peer, dirs ...string) []sessionPane {
	app.T.Helper()
	panes := make([]sessionPane, 0, len(dirs))
	for _, dir := range dirs {
		spawned, workspace, pane := w.RequestSpawn(app, fakeagent.Claude, dir)
		if !spawned.Success {
			app.T.Fatalf("spawn in %s: %s", dir, protocol.Deref(spawned.Error))
		}
		panes = append(panes, sessionPane{session: spawned.ID, workspace: workspace, pane: pane})
	}
	for _, p := range panes {
		w.Launched(p.session)
		testworld.AwaitSession(app, p.session, func(s protocol.Session) bool { return s.State == protocol.SessionStateIdle })
	}
	return panes
}

func closePane(app *testworld.Peer, p sessionPane) {
	app.T.Helper()
	closed := testworld.Request(app, protocol.WorkspaceLayoutClosePaneMessage{
		Cmd: protocol.CmdWorkspaceLayoutClosePane, WorkspaceID: p.workspace, PaneID: p.pane,
	}, protocol.EventWorkspaceLayoutActionResult, func(r protocol.WorkspaceLayoutActionResultMessage) bool {
		return r.Action == protocol.CmdWorkspaceLayoutClosePane && protocol.Deref(r.PaneID) == p.pane
	})
	if !closed.Success {
		app.T.Fatalf("close the pane of %s: %s", p.session, protocol.Deref(closed.Error))
	}
}

func ledger(t *testing.T, cli *client.Client, opts client.SessionListOptions) *protocol.SessionListResult {
	t.Helper()
	page, err := cli.SessionList(opts)
	if err != nil {
		t.Fatalf("session list %+v: %v", opts, err)
	}
	return page
}

func sortedLedgerIDs(page *protocol.SessionListResult) []string {
	ids := ledgerIDs(page)
	slices.Sort(ids)
	return ids
}

func facetCounts(facets []protocol.SessionLedgerFacet) map[string]int {
	counts := make(map[string]int, len(facets))
	for _, facet := range facets {
		counts[facet.Value] = facet.Count
	}
	return counts
}

func ledgerIDs(page *protocol.SessionListResult) []string {
	ids := make([]string, 0, len(page.Entries))
	for _, entry := range page.Entries {
		ids = append(ids, entry.ID)
	}
	return ids
}

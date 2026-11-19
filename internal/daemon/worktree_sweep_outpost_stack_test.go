package daemon_test

import (
	"slices"
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestASeedTendedOnAnOutpostDoesNotHoldTheHomesWorktreeAtTheSamePath(t *testing.T) {
	home := testworld.NewStack(t)
	outpost := testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude))
	home.Vars = append(home.Vars, "ATTN_WORKTREE_SWEEP_IDLE_DAYS=0")
	pair := testworld.PrepareOutpostPair(t, home, outpost)
	pair.Start()
	app, cli := pair.App, home.Client()
	testworld.Await(app, protocol.EventOutpostsUpdated, func(m protocol.OutpostsUpdatedMessage) bool {
		return slices.ContainsFunc(m.Outposts, func(row protocol.OutpostInfo) bool {
			return row.ID == pair.Host && row.Notebook != nil && row.Notebook.State == protocol.OutpostNotebookStateInSync
		})
	})
	shop := newRepo(t, "shop")
	checkout := createWorktree(t, app, shop, "feat-shared-path")

	onOutpost := home.Spawn(app, fakeagent.Claude, checkout, func(m *protocol.SpawnSessionMessage) {
		m.Host = protocol.Ptr(pair.Host)
	})
	outpost.LaunchedCarrying(app.Terminal(onOutpost))
	seed := plantSeedAs(t, cli, onOutpost, "Work in the outpost's checkout")
	moveDelegationSeed(t, cli, onOutpost, seed, "tend", "")

	refreshWorktrees(t, cli)
	decided := testworld.AwaitEvent(app, "the sweep's decision on the home's worktree", func(e protocol.WebSocketEvent) bool {
		switch e.Event {
		case protocol.EventWorktreeSwept:
			return e.SweepEntry != nil && e.SweepEntry.Path == checkout
		case protocol.EventWorktreeStateChanged:
			return len(e.Worktrees) == 1 && e.Worktrees[0].Path == checkout && protocol.Deref(e.Worktrees[0].SweepStatus) == "kept_open_seed"
		}
		return false
	})
	if decided.Event != protocol.EventWorktreeSwept || decided.SweepEntry.Action != "removed" {
		t.Errorf("the sweep kept the home's worktree for the seed open on the outpost; want it removed, since that seed's checkout is on the outpost")
	}
}

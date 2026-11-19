package daemon_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/bridge"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/pausepoint"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestAnOpenSeedKeepsItsOutpostBranchAfterRecoveryReapsItsTender(t *testing.T) {
	home := testworld.NewStack(t)
	outpost := testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude))
	outpost.Vars = append(outpost.Vars, "ATTN_WORKTREE_SWEEP_IDLE_DAYS=0")
	recovered := home.PauseEvery(pausepoint.OutpostRecoveryApplied)
	pair := testworld.PrepareOutpostPair(t, home, outpost)
	pair.Start()
	recovered.Await()
	recovered.Release()
	app, cli := pair.App, home.Client()
	testworld.Await(app, protocol.EventOutpostsUpdated, func(m protocol.OutpostsUpdatedMessage) bool {
		return slices.ContainsFunc(m.Outposts, func(row protocol.OutpostInfo) bool {
			return row.ID == pair.Host && row.Notebook != nil && row.Notebook.State == protocol.OutpostNotebookStateInSync
		})
	})
	shop := newRepo(t, "shop")
	const branch = "feat-reaped-tender"
	checkout := shop + "--" + branch
	created := testworld.Request(app, protocol.CreateWorktreeMessage{
		Cmd: protocol.CmdCreateWorktree, MainRepo: shop, Branch: branch, Path: protocol.Ptr(checkout), Host: protocol.Ptr(pair.Host),
	}, protocol.EventCreateWorktreeResult, func(r protocol.CreateWorktreeResultMessage) bool { return protocol.Deref(r.Path) == checkout })
	if !created.Success {
		t.Fatalf("create the outpost's worktree: %s", protocol.Deref(created.Error))
	}

	tender := home.Spawn(app, fakeagent.Claude, checkout, func(m *protocol.SpawnSessionMessage) {
		m.Host = protocol.Ptr(pair.Host)
	})
	outpost.LaunchedCarrying(app.Terminal(tender))
	seed := plantSeedAs(t, cli, tender, "Work in the outpost's checkout")
	moveDelegationSeed(t, cli, tender, seed, "tend", "")

	mark := len(app.Log())
	pair.Disconnect()
	awaitOutpostState(app, mark, pair.Host, protocol.OutpostStateLost)
	outpost.Reboot()
	outpost.Start()
	pair.Reconnect()
	stopRetries := home.FireTimers(bridge.RetryTimer)
	recovered.Await()
	recovered.Release()
	stopRetries()
	listed, err := cli.Query("")
	if err != nil {
		t.Fatalf("query sessions: %v", err)
	}
	if slices.ContainsFunc(listed, func(s protocol.Session) bool { return string(s.ID) == tender }) {
		t.Fatalf("recovery kept tender %s after its outpost lost the terminal it ran in", tender)
	}

	onOutpost := outpost.App()
	refreshWorktrees(t, outpost.Client())
	swept := testworld.AwaitEvent(onOutpost, "the outpost sweep's decision on the reaped tender's worktree", func(e protocol.WebSocketEvent) bool {
		switch e.Event {
		case protocol.EventWorktreeSwept:
			return e.SweepEntry != nil && e.SweepEntry.Path == checkout
		case protocol.EventWorktreeStateChanged:
			return slices.ContainsFunc(e.Worktrees, func(w protocol.Worktree) bool {
				return w.Path == checkout && protocol.Deref(w.SweepStatus) == "kept_open_seed"
			})
		}
		return false
	})
	if swept.Event == protocol.EventWorktreeSwept {
		t.Fatalf("the outpost's sweep %s the worktree open seed %s can still continue in after its tender was reaped", swept.SweepEntry.Action, seed)
	}

	deleted := testworld.Request(app, protocol.DeleteWorktreeMessage{
		Cmd: protocol.CmdDeleteWorktree, Path: checkout, Host: protocol.Ptr(pair.Host), Force: protocol.Ptr(true),
	}, protocol.EventDeleteWorktreeResult, func(r protocol.DeleteWorktreeResultMessage) bool { return r.Path == checkout })
	if !deleted.Success {
		t.Fatalf("delete the outpost's worktree: %s", protocol.Deref(deleted.Error))
	}
	if branches := runGit(t, shop, "branch", "--list", branch); strings.TrimSpace(branches) == "" {
		t.Errorf("removing the outpost's worktree deleted branch %s, which open seed %s can still continue from after its tender was reaped", branch, seed)
	}
}

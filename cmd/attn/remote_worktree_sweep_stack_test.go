package main_test

import (
	"os"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestTheSweepOnAnOutpostKeepsWhatTheHomesOpenSeedsOwn(t *testing.T) {
	home, outpost := testworld.NewStack(t), testworld.NewStack(t)
	outpost.Vars = append(outpost.Vars, "SHELL=/bin/bash", "ATTN_WORKTREE_SWEEP_IDLE_DAYS=0")
	homesOwnCheckout := home.Path("home-checkout")
	gitRepo(t, homesOwnCheckout)
	giveTheHomeItsOwnCheckoutAt(t, home, "*/shop--*", homesOwnCheckout)
	pair := testworld.PrepareOutpostPair(t, home, outpost)
	pair.Start()
	app, host := pair.App, pair.Host
	onOutpost := protocol.Ptr(host)

	repo := outpost.Path("shop")
	gitRepo(t, repo)
	createOnOutpost := func(branch string) string {
		t.Helper()
		path := outpost.Path("shop--" + branch)
		id := "create-" + branch
		created := testworld.Request(app, protocol.CreateWorktreeMessage{Cmd: protocol.CmdCreateWorktree, Host: onOutpost, MainRepo: repo, Branch: branch, Path: protocol.Ptr(path), RequestID: protocol.Ptr(id)},
			protocol.EventCreateWorktreeResult, func(r protocol.CreateWorktreeResultMessage) bool { return protocol.Deref(r.RequestID) == id })
		if !created.Success {
			t.Fatalf("create %s on the outpost: %s", branch, protocol.Deref(created.Error))
		}
		return path
	}
	owned := createOnOutpost("owned")
	loose := createOnOutpost("loose")

	shell := home.Spawn(app, fakeagent.Harness(protocol.SessionAgentShell), owned, func(m *protocol.SpawnSessionMessage) { m.Host = onOutpost })
	app.TypeLine(shell, "echo up-$((1+1))")
	app.AwaitScreen(shell, "up-2")
	testworld.AwaitSession(app, shell, func(s protocol.Session) bool { return protocol.Deref(s.Branch) == "owned" })
	cli := home.Client()
	planted, err := cli.SeedPlant(protocol.SessionID(shell), "Continue the owned work", "Finish it.", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cli.SeedTransition(protocol.SessionID(shell), planted.Seed.ID, "tend", "", false, client.SeedTransitionOptions{}); err != nil {
		t.Fatal(err)
	}
	app.TypeLine(shell, "exit")
	testworld.Await(app, protocol.EventSessionUnregistered, func(e protocol.WebSocketEvent) bool {
		return e.Session != nil && string(e.Session.ID) == shell
	})

	onTheOutpost := outpost.App()
	if refreshed, err := outpost.Client().WorktreeRefresh(); err != nil || !refreshed.Queued {
		t.Fatalf("worktree refresh on the outpost = %+v, %v", refreshed, err)
	}
	swept := testworld.Await(onTheOutpost, protocol.EventWorktreeSwept, func(e protocol.WebSocketEvent) bool {
		return e.SweepEntry != nil && e.SweepEntry.Path == loose
	}).SweepEntry
	if swept.Action != "removed" {
		t.Errorf("the sweep recorded %s for the worktree no seed owns; want it removed", swept.Action)
	}
	kept := testworld.Await(onTheOutpost, protocol.EventWorktreeStateChanged, func(e protocol.WebSocketEvent) bool {
		return len(e.Worktrees) == 1 && e.Worktrees[0].Path == owned && e.Worktrees[0].SweepStatus != nil
	}).Worktrees[0]
	if protocol.Deref(kept.SweepStatus) != "kept_open_seed" || !strings.Contains(protocol.Deref(kept.SweepReason), planted.Seed.ID) {
		t.Errorf("the outpost's sweep decided the seed's worktree is %s (%s); want kept_open_seed naming %s",
			protocol.Deref(kept.SweepStatus), protocol.Deref(kept.SweepReason), planted.Seed.ID)
	}
	if _, err := os.Stat(owned); err != nil {
		t.Errorf("the sweep removed the worktree the home's open seed owns: %v", err)
	}

	stranded := createOnOutpost("stranded")
	lost := app.EventCount()
	pair.Disconnect()
	outpostStateSince(app, "outpost", protocol.OutpostStateLost, lost)
	if refreshed, err := outpost.Client().WorktreeRefresh(); err != nil || !refreshed.Queued {
		t.Fatalf("worktree refresh on the outpost without its home = %+v, %v", refreshed, err)
	}
	undecided := testworld.Await(onTheOutpost, protocol.EventWorktreeStateChanged, func(e protocol.WebSocketEvent) bool {
		return len(e.Worktrees) == 1 && e.Worktrees[0].Path == stranded && strings.Contains(protocol.Deref(e.Worktrees[0].SweepReason), "home")
	}).Worktrees[0]
	if protocol.Deref(undecided.SweepStatus) != "" {
		t.Errorf("without its home the outpost's sweep decided %s (%s); want it undecided", protocol.Deref(undecided.SweepStatus), protocol.Deref(undecided.SweepReason))
	}
	if _, err := os.Stat(stranded); err != nil {
		t.Errorf("the sweep removed a worktree while the home's Garden was out of reach: %v", err)
	}
	t.Logf("removed %s; kept %s: %s; without the home: %s", loose, owned, protocol.Deref(kept.SweepReason), protocol.Deref(undecided.SweepReason))
}

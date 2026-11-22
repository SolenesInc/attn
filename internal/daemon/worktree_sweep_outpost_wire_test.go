package daemon_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"testing"

	_ "github.com/mattn/go-sqlite3"

	"golang.org/x/crypto/ssh"

	"github.com/victorarias/attn/internal/bridge"
	"github.com/victorarias/attn/internal/daemon"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/pausepoint"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
	"github.com/victorarias/attn/internal/testworld"
)

func connectFarOutpost(t *testing.T, w *world, app *testworld.Peer, callHandler func(*bridge.Conn, ssh.NewChannel)) (string, *bridge.Conn) {
	t.Helper()
	connected := make(chan *bridge.Conn, 1)
	recovered := w.PauseAt(pausepoint.OutpostRecoveryApplied)
	if callHandler == nil {
		callHandler = answerBridgeCall
	}
	startFarOutpostWith(t, w, func(conn *bridge.Conn) { connected <- conn }, bridge.Handlers{daemon.BridgeCall: callHandler})
	setSetting(t, app, "outposts_enabled", "true")
	app.Send(protocol.AddOutpostMessage{Cmd: protocol.CmdAddOutpost, Name: "far", Destination: outpostAddress.String()})
	host := testworld.Await(app, protocol.EventOutpostsUpdated, func(m protocol.OutpostsUpdatedMessage) bool {
		return len(m.Outposts) == 1 && m.Outposts[0].State == protocol.OutpostStateConnected
	}).Outposts[0].ID
	recovered.Await()
	recovered.Release()
	return host, <-connected
}

func TestTheHomeNeverFollowsItsOwnSymlinksForAnOutpostsWorktrees(t *testing.T) {
	w := newWorld(t)
	app, cli := w.App(), w.Client()

	root := t.TempDir()
	other := filepath.Join(root, "other")
	realRepo := filepath.Join(root, "real-repo")
	for _, dir := range []string{other, realRepo} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	alias := filepath.Join(root, "alias")
	repoAlias := filepath.Join(root, "repo-alias")
	if err := os.Symlink(other, alias); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(realRepo, repoAlias); err != nil {
		t.Fatal(err)
	}

	host, peer := connectFarOutpost(t, w, app, func(_ *bridge.Conn, incoming ssh.NewChannel) {
		bridge.Answer(incoming, func(_ context.Context, payload []byte) (any, error) {
			var envelope struct {
				Cmd string `json:"cmd"`
			}
			if err := json.Unmarshal(payload, &envelope); err == nil && envelope.Cmd == protocol.CmdCheckoutInspect {
				return protocol.CheckoutInspection{
					Exists:      true,
					IsDirectory: true,
					Cwd:         alias,
					RepoRoot:    alias,
					MainRepo:    repoAlias,
					Branch:      "feat-other",
				}, nil
			}
			return struct{}{}, nil
		})
	})

	const onOutpost = "outpost-symlink-session"
	if err := w.InjectSessionOn(host, onOutpost, "far work", alias, "claude"); err != nil {
		t.Fatal(err)
	}
	seed := plantSeedAs(t, cli, onOutpost, "Work at alias on the outpost")
	moveDelegationSeed(t, cli, onOutpost, seed, "tend", "")

	ctx, cancel := context.WithTimeout(t.Context(), fakeagent.HangGuard)
	defer cancel()

	var otherOwners protocol.WorktreeSeedOwners
	err := peer.Call(ctx, daemon.BridgeCall, protocol.WorktreeSeedOwnersMessage{
		Cmd:      protocol.CmdWorktreeSeedOwners,
		MainRepo: realRepo,
		Worktrees: []protocol.WorktreeCheckout{
			{Path: other, Branch: "feat-other"},
		},
	}, &otherOwners)
	if err != nil {
		t.Fatalf("worktree_seed_owners for realRepo/other from outpost: %v", err)
	}
	if len(otherOwners.OwnedPaths) != 0 {
		t.Errorf("worktree_seed_owners(realRepo, other) = %v; want empty (the home followed its own symlinks %s -> %s / %s -> %s)", otherOwners.OwnedPaths, alias, other, repoAlias, realRepo)
	}

	var aliasOwners protocol.WorktreeSeedOwners
	err = peer.Call(ctx, daemon.BridgeCall, protocol.WorktreeSeedOwnersMessage{
		Cmd:      protocol.CmdWorktreeSeedOwners,
		MainRepo: repoAlias,
		Worktrees: []protocol.WorktreeCheckout{
			{Path: other, Branch: "feat-other"},
			{Path: alias, Branch: "feat-alias"},
		},
	}, &aliasOwners)
	if err != nil {
		t.Fatalf("worktree_seed_owners for repoAlias from outpost: %v", err)
	}
	if !slices.Equal(aliasOwners.OwnedPaths, []string{other, alias}) {
		t.Errorf("worktree_seed_owners(repoAlias) = %v; want [%s %s] (branch matches repoAlias and cwd matches alias)", aliasOwners.OwnedPaths, other, alias)
	}

	var otherPlan protocol.WorktreeRemovalPlan
	err = peer.Call(ctx, daemon.BridgeCall, protocol.WorktreeRemovalPrepareMessage{
		Cmd:      protocol.CmdWorktreeRemovalPrepare,
		MainRepo: realRepo,
		Path:     other,
		Branch:   "feat-other",
	}, &otherPlan)
	if err != nil {
		t.Fatalf("worktree_removal_prepare(realRepo, other) from outpost: %v", err)
	}
	if len(otherPlan.Seeds) != 0 || otherPlan.KeepBranch {
		t.Errorf("worktree_removal_prepare(realRepo, other) = %+v; want no seeds and KeepBranch=false (the home followed its own symlinks %s -> %s / %s -> %s)", otherPlan, alias, other, repoAlias, realRepo)
	}

	var aliasPlan protocol.WorktreeRemovalPlan
	err = peer.Call(ctx, daemon.BridgeCall, protocol.WorktreeRemovalPrepareMessage{
		Cmd:      protocol.CmdWorktreeRemovalPrepare,
		MainRepo: repoAlias,
		Path:     alias,
		Branch:   "feat-other",
	}, &aliasPlan)
	if err != nil {
		t.Fatalf("worktree_removal_prepare(repoAlias, alias) from outpost: %v", err)
	}
	if !slices.Equal(aliasPlan.Seeds, []string{seed}) || !aliasPlan.KeepBranch {
		t.Errorf("worktree_removal_prepare(repoAlias, alias) = %+v; want Seeds=[%s] and KeepBranch=true", aliasPlan, seed)
	}
}

func TestAClosedPreMachineOutpostSeedStillNamesItsOutpostAndDoesNotHoldTheHomesWorktree(t *testing.T) {
	t.Setenv("ATTN_WORKTREE_SWEEP_IDLE_DAYS", "0")
	w := newWorld(t)
	app, cli := w.App(), w.Client()
	host, _ := connectFarOutpost(t, w, app, nil)

	shop := newRepo(t, "shop")
	checkout := createWorktree(t, app, shop, "feat-legacy-outpost")
	alias := filepath.Join(t.TempDir(), "alias-to-home-checkout")
	if err := os.Symlink(checkout, alias); err != nil {
		t.Fatal(err)
	}

	const closedOutpost = "closed-pre-machine-outpost"
	const orphanOutpost = "reaped-pre-machine-outpost"
	if err := w.InjectSessionOn(host, closedOutpost, "closed outpost work", checkout, "claude"); err != nil {
		t.Fatal(err)
	}
	if err := w.InjectSessionOn(host, orphanOutpost, "orphaned outpost work", alias, "claude"); err != nil {
		t.Fatal(err)
	}
	closedSeed := plantSeedAs(t, cli, closedOutpost, "Pre-machine closed outpost seed")
	moveDelegationSeed(t, cli, closedOutpost, closedSeed, "tend", "")
	orphanSeed := plantSeedAs(t, cli, orphanOutpost, "Pre-machine reaped outpost seed")
	moveDelegationSeed(t, cli, orphanOutpost, orphanSeed, "tend", "")

	for _, id := range []string{closedOutpost, orphanOutpost} {
		if err := cli.Unregister(protocol.SessionID(id)); err != nil {
			t.Fatalf("close %s: %v", id, err)
		}
		testworld.Await(app, protocol.EventSessionUnregistered, func(e protocol.WebSocketEvent) bool {
			return e.Session != nil && string(e.Session.ID) == id
		})
	}

	setSetting(t, app, "outposts_enabled", "false")
	w.stop()
	simulatePreMachineOutpostDispatches(t, filepath.Join(w.Dir, "attn.db"), []string{closedOutpost, orphanOutpost}, orphanOutpost)
	w.start()

	app, cli = w.App(), w.Client()
	shown := lifeShow(t, cli, closedSeed).Seed
	if shown.Continuation == nil {
		t.Fatalf("seed %s has no continuation after restart", closedSeed)
	}
	if got := protocol.Deref(shown.Continuation.Outpost); got != "far" {
		t.Errorf("closed pre-machine outpost seed continuation outpost = %q, want %q", got, "far")
	}
	if shown.Continuation.DirectoryState == "present" {
		t.Errorf("closed pre-machine outpost seed read the home's own folder at %s as DirectoryState=%q", checkout, shown.Continuation.DirectoryState)
	}

	refreshWorktrees(t, cli)
	decided := testworld.AwaitEvent(app, "the home sweep's decision on its own worktree", func(e protocol.WebSocketEvent) bool {
		switch e.Event {
		case protocol.EventWorktreeSwept:
			return e.SweepEntry != nil && e.SweepEntry.Path == checkout
		case protocol.EventWorktreeStateChanged:
			return len(e.Worktrees) == 1 && e.Worktrees[0].Path == checkout && protocol.Deref(e.Worktrees[0].SweepStatus) == "kept_open_seed"
		}
		return false
	})
	if decided.Event != protocol.EventWorktreeSwept || decided.SweepEntry.Action != "removed" {
		t.Errorf("the home's sweep kept %s (%+v); want it removed, because the closed pre-machine outpost seed belongs to far and the orphaned dispatch must not follow the home's symlink", checkout, decided)
	}
}

func simulatePreMachineOutpostDispatches(t *testing.T, dbPath string, stripMachineIDs []string, deleteSessionID string) {
	t.Helper()
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	var tableID int
	if err := db.QueryRow(`SELECT id FROM document_collections WHERE namespace = 'core/garden' AND collection = 'dispatches'`).Scan(&tableID); err != nil {
		t.Fatalf("locate dispatches collection: %v", err)
	}
	table := "doc_" + strconv.Itoa(tableID)
	for _, id := range stripMachineIDs {
		var raw string
		if err := db.QueryRow(`SELECT body FROM `+table+` WHERE id = ?`, id).Scan(&raw); err != nil {
			t.Fatalf("read dispatch %s: %v", id, err)
		}
		var doc map[string]any
		if err := json.Unmarshal([]byte(raw), &doc); err != nil {
			t.Fatalf("decode dispatch %s: %v", id, err)
		}
		delete(doc, "machine")
		updated, err := json.Marshal(doc)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`UPDATE `+table+` SET body = ? WHERE id = ?`, string(updated), id); err != nil {
			t.Fatalf("strip machine on dispatch %s: %v", id, err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	s, err := store.NewWithDB(dbPath)
	if err != nil {
		t.Fatalf("open store for reaped session cleanup: %v", err)
	}
	s.Remove(protocol.SessionID(deleteSessionID))
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
}

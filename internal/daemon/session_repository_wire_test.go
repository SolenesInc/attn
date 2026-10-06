package daemon_test

import (
	"slices"
	"testing"

	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestASpawnedSessionIsListedUnderItsRepositoryEvenWhenClosedAtOnce(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app := w.App()
	shop := newRepo(t, "shop")
	id := w.Spawn(app, fakeagent.Claude, shop)
	w.Launched(id)
	testworld.AwaitSession(app, id, func(s protocol.Session) bool {
		return protocol.Deref(s.Branch) == "main" && !protocol.Deref(s.IsWorktree)
	})
	if closed := testworld.Request(app, protocol.UnregisterMessage{Cmd: protocol.CmdUnregister, ID: protocol.SessionID(id)},
		protocol.EventSessionCloseResult, func(r protocol.SessionCloseResultMessage) bool { return string(r.SessionID) == id }); !closed.Accepted {
		t.Fatalf("closing %s was refused: %s", id, protocol.Deref(closed.Error))
	}

	page, err := w.Client().SessionList(client.SessionListOptions{All: true, Repository: shop})
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, entry := range page.Entries {
		ids = append(ids, string(entry.ID))
	}
	if !slices.Equal(ids, []string{id}) {
		t.Errorf("the ledger under repository %s lists %q, want the session spawned there", shop, ids)
	}

	feature := shop + "--feature"
	runGit(t, shop, "worktree", "add", "-q", "-b", "feature", feature)
	inWorktree := w.Spawn(app, fakeagent.Claude, feature)
	w.Launched(inWorktree)
	testworld.AwaitSession(app, inWorktree, func(s protocol.Session) bool {
		return protocol.Deref(s.Branch) == "feature" && protocol.Deref(s.IsWorktree) && protocol.Deref(s.MainRepo) == shop
	})
}

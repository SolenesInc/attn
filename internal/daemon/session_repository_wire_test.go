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
	if closed := testworld.Request(app, protocol.UnregisterMessage{Cmd: protocol.CmdUnregister, ID: id},
		protocol.EventSessionCloseResult, func(r protocol.SessionCloseResultMessage) bool { return r.SessionID == id }); !closed.Accepted {
		t.Fatalf("closing %s was refused: %s", id, protocol.Deref(closed.Error))
	}

	page, err := w.Client().SessionList(client.SessionListOptions{All: true, Repository: shop})
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, entry := range page.Entries {
		ids = append(ids, entry.ID)
	}
	if !slices.Equal(ids, []string{id}) {
		t.Errorf("the ledger under repository %s lists %q, want the session spawned there", shop, ids)
	}
}

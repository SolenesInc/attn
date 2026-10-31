package daemon_test

import (
	"testing"

	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
)

func TestASeedContinuationFollowsItsTendersConversationMovePastClose(t *testing.T) {
	for _, harness := range []fakeagent.Harness{fakeagent.Claude, fakeagent.Pi} {
		t.Run(string(harness), func(t *testing.T) {
			w := newWorld(t, harness)
			app, cli := w.App(), w.Client()
			awaitAgentAvailable(app, string(harness))
			cwd := w.Path("shop")

			other := w.Spawn(app, harness, cwd)
			elsewhere := w.Launched(other)
			if harness == fakeagent.Claude {
				talk(app, elsewhere, other, "plan the release")
			}

			spawned, workspace, pane := w.RequestSpawn(app, harness, cwd)
			tender := string(spawned.ID)
			agent := w.Launched(tender)
			seed := plantSeedAs(t, cli, tender, "price the cart")
			if _, err := cli.SeedTransition(spawned.ID, seed, "tend", "", false, client.SeedTransitionOptions{}); err != nil {
				t.Fatal(err)
			}
			if got := continuationConversation(t, cli, seed); got != agent.ConversationID {
				t.Fatalf("before the move the seed resumes %q, want %q, the conversation its tender launched", got, agent.ConversationID)
			}

			moved := len(app.Log())
			app.TypeLine(tender, "/resume "+elsewhere.ConversationID)
			agent.Prompted()
			awaitSharedWith(app, moved, tender, other)
			if got := continuationConversation(t, cli, seed); got != elsewhere.ConversationID {
				t.Errorf("after the move the seed resumes %q, want %q", got, elsewhere.ConversationID)
			}

			closePane(app, sessionPane{session: tender, desktop: workspace, pane: pane})
			awaitClosed(app, tender)
			if got := continuationConversation(t, cli, seed); got != elsewhere.ConversationID {
				t.Errorf("after its tender closed the seed resumes %q, want %q", got, elsewhere.ConversationID)
			}
		})
	}
}

func continuationConversation(t *testing.T, cli *client.Client, seed string) string {
	t.Helper()
	continuation := lifeShow(t, cli, seed).Seed.Continuation
	if continuation == nil {
		t.Fatalf("seed %s has no continuation", seed)
	}
	return protocol.Deref(continuation.NativeConversationID)
}

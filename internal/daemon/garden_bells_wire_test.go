package daemon_test

import (
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/protocol"
)

func TestAnUnreadSeedBellSaysTheSeedWasUnblocked(t *testing.T) {
	inBubble(t, func(t *testing.T, w *world) {
		cli := w.Client()
		registerSessions(t, w, cli, "watcher", "worker")
		release := plantSeed(t, cli, "worker", "Ship the release")
		build := plantSeed(t, cli, "worker", "Fix the build")
		if _, err := cli.SeedLink(build, "blocks", release, false); err != nil {
			t.Fatalf("the build blocks the release: %v", err)
		}
		if _, err := cli.SeedWatch("watcher", release, false); err != nil {
			t.Fatalf("watch the release: %v", err)
		}

		if _, err := cli.SeedNote("worker", release, "waiting on the build", "", "", true, nil); err != nil {
			t.Fatalf("ring the watcher with a note: %v", err)
		}
		if _, err := cli.SeedTransition("worker", build, "harvest", "fixed", "", false, client.SeedTransitionOptions{}); err != nil {
			t.Fatalf("harvest the build: %v", err)
		}
		w.advance(0)

		items := readInbox(t, cli, "watcher", 0).Items
		if len(items) != 1 {
			t.Fatalf("the watcher's inbox = %q, want the note and the unblock coalesced into one update", inboxContents(items))
		}
		if hint := protocol.Deref(items[0].Hint); hint != "unblocked" || !strings.Contains(items[0].Content, release+" moved: unblocked") {
			t.Errorf("the coalesced update = %+v, want it to say the release was unblocked", items[0])
		}
	})
}

func plantSeed(t *testing.T, cli *client.Client, sessionID, title string) string {
	t.Helper()
	planted, err := cli.SeedPlant(sessionID, title, "", "", "", "")
	if err != nil {
		t.Fatalf("plant %q: %v", title, err)
	}
	return planted.Seed.ID
}

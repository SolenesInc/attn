package daemon_test

import (
	"os"
	"reflect"
	"testing"

	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestADelegatedSessionWatchesItsSeedAndAResumeKeepsItsUnwatch(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	registerSessions(t, w, cli, "planner", "planter")
	crown, child, leaf := gardenNudgePlot(t, cli)

	atChild := gardenSubscriptionDelegate(t, w, app, "child", child)
	gardenNudgeWatch(t, cli, atChild, crown, false)
	gardenNudgeWatch(t, cli, atChild, crown, true)
	inherited := gardenNudgeWatch(t, cli, atChild, leaf, true)
	if inherited.Changed || !inherited.Watching || !reflect.DeepEqual(inherited.WatchingVia, []string{child}) {
		t.Errorf("the child's delegate unwatching the leaf = %+v, want still watching via %s", inherited, child)
	}
	if shown, err := cli.SeedShow(atChild, leaf); err != nil || !reflect.DeepEqual(shown.WatchingVia, []string{child}) {
		t.Errorf("show of the leaf = %+v (%v), want watching via %s", shown, err, child)
	}

	atCrown := gardenSubscriptionDelegate(t, w, app, "crown", crown)
	if !gardenSubscriptionWatching(t, cli, atCrown, crown) {
		t.Fatal("the delegate does not watch the crown its delegation bound")
	}
	if unwatched := gardenNudgeWatch(t, cli, atCrown, crown, true); !unwatched.Changed || unwatched.Watching {
		t.Fatalf("unwatching the delegated crown = %+v", unwatched)
	}
	respawn(w, app, fakeagent.Claude, atCrown, w.Path("crown"))
	if gardenSubscriptionWatching(t, cli, atCrown, crown) {
		t.Error("resuming the delegate restored the watch it removed")
	}
}

func gardenSubscriptionDelegate(t *testing.T, w *world, app *testworld.Peer, label, seedID string) string {
	t.Helper()
	cwd := w.Path(label)
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	delegated := gardenPlotDelegate(app, fakeagent.Claude, label, seedID, cwd)
	if !delegated.Success {
		t.Fatalf("delegating %s: %s", seedID, protocol.Deref(delegated.Error))
	}
	w.Launched(delegated.Result.SessionID)
	return delegated.Result.SessionID
}

func gardenSubscriptionWatching(t *testing.T, cli *client.Client, session, seedID string) bool {
	t.Helper()
	shown, err := cli.SeedShow(session, seedID)
	if err != nil {
		t.Fatalf("show %s: %v", seedID, err)
	}
	return shown.Watching
}

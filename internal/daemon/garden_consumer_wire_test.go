package daemon_test

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/bus"
	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestAGardenBellRetriesAfterACrewRosterPathFailureIsRepaired(t *testing.T) {
	inBubbleWithAgents(t, func(t *testing.T, w *world) {
		cli := w.Client()
		registerSessions(t, w, cli, "sender")
		seed := plantSeedAs(t, cli, "sender", "review the build")

		writeCrewCharter(t, w, "trellis")
		writeCrewCharter(t, w, "alder")
		w.restart()
		w.App() // Initial state waits for startup recovery, which drops injected sessions.
		cli = w.Client()
		registerSessions(t, w, cli, "sender")
		first := wakeCrew(t, cli, "trellis", "").SessionID
		w.bootBubbleClaude(t, string(first))
		if _, err := cli.SeedTransition(first, seed, "tend", "", false, client.SeedTransitionOptions{}); err != nil {
			t.Fatal(err)
		}
		crewHandoff(t, cli, string(first), "Wake when the build is ready.", false, protocol.CrewDayCloseSleep)
		home := filepath.Join(w.Dir, "crew", "trellis")
		saved := home + "-saved"
		if err := os.Rename(home, saved); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(t.TempDir(), home); err != nil {
			t.Fatal(err)
		}
		if _, err := cli.SeedNote("sender", seed, "the deployment is ready", "", true, nil); err != nil {
			t.Fatal(err)
		}
		w.advance(0)
		if err := os.Remove(home); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(saved, home); err != nil {
			t.Fatal(err)
		}
		w.advance(bus.DefaultPollInterval)
		dayID := protocol.Deref(crewRosterMember(t, cli, "trellis").BindingSession)
		if dayID == "" {
			t.Fatal("Garden event was lost during the roster failure")
		}
		day := w.bootBubbleClaude(t, string(dayID))
		day.reply("Ready. <!-- attn:state=idle -->")
		if mail := readInbox(t, cli, day.id, 0).Items; len(mail) != 1 || !strings.Contains(mail[0].Content, seed) {
			t.Fatalf("recovered Garden mail=%+v; want the failed event delivered", mail)
		}
	})
}

func TestALateSeedBellRingsWhoeverTendsTheSeedNow(t *testing.T) {
	inBubble(t, func(t *testing.T, w *world) {
		app, cli := w.App(), w.Client()
		registerSessions(t, w, cli, "first", "second", "third")
		seed := plantSeedAs(t, cli, "", "dispatch the current tender")

		seedBellsConsumerEnabled(t, app, false)
		lifeMove(t, cli, "first", seed, "tend", "", "")
		for _, taker := range []string{"second", "third"} {
			if _, err := cli.SeedTransition(protocol.SessionID(taker), seed, "tend", "", true, client.SeedTransitionOptions{}); err != nil {
				t.Fatalf("%s takes the seed: %v", taker, err)
			}
		}
		seedBellsConsumerEnabled(t, app, true)
		w.advance(bus.DefaultPollInterval)

		for _, earlier := range []string{"first", "second"} {
			if items := readInbox(t, cli, earlier, 0).Items; len(items) != 0 {
				t.Errorf("%s, who no longer tends the seed, was rung with %q", earlier, inboxContents(items))
			}
		}
		if items := readInbox(t, cli, "third", 0).Items; len(items) != 1 || !strings.Contains(items[0].Content, seed+" moved: tended") {
			t.Errorf("the current tender holds %q, want one tended bell about %s", inboxContents(items), seed)
		}
	})
}

func TestEditingAWatchedSeedRingsNoOne(t *testing.T) {
	inBubble(t, func(t *testing.T, w *world) {
		cli := w.Client()
		registerSessions(t, w, cli, "watcher", "writer")
		seed := plantSeedAs(t, cli, "writer", "quietly edited")
		if _, err := cli.SeedWatch("watcher", seed, false); err != nil {
			t.Fatal(err)
		}

		if _, err := cli.SeedEdit(seed, "a better body"); err != nil {
			t.Fatal(err)
		}
		w.advance(0)
		if items := readInbox(t, cli, "watcher", 0).Items; len(items) != 0 {
			t.Fatalf("a body edit rang the watcher with %q", inboxContents(items))
		}

		if _, err := cli.SeedNote("writer", seed, "please look", "", true, nil); err != nil {
			t.Fatal(err)
		}
		w.advance(0)
		if items := readInbox(t, cli, "watcher", 0).Items; len(items) != 1 {
			t.Errorf("a ringing note after the edit left the watcher %q, want one bell", inboxContents(items))
		}
	})
}

func seedBellsConsumerEnabled(t *testing.T, app *testworld.Peer, enabled bool) {
	t.Helper()
	requestID := "garden-seed-bells-" + strconv.FormatBool(enabled)
	result := testworld.Request(app, protocol.BusSetConsumerEnabledMessage{
		Cmd: protocol.CmdBusSetConsumerEnabled, RequestID: requestID, Consumer: "garden-seed-bells", Enabled: enabled,
	}, protocol.EventBusSetConsumerEnabledResult, func(m protocol.BusSetConsumerEnabledResultMessage) bool { return m.RequestID == requestID })
	if !result.Success {
		t.Fatalf("set garden-seed-bells enabled=%t: %s", enabled, protocol.Deref(result.Error))
	}
}

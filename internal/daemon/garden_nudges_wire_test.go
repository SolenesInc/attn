package daemon_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestTheAppsSeedEditsNeverRingTheirSource(t *testing.T) {
	inBubble(t, func(t *testing.T, w *world) {
		app, cli := w.App(), w.Client()
		registerSessions(t, w, cli, "planter", "source", "watcher")
		seed := plantSeedAs(t, cli, "planter", "delivery proof")
		gardenNudgeWatch(t, cli, "source", seed, false)
		gardenNudgeWatch(t, cli, "watcher", seed, false)

		moved := testworld.Request(app, protocol.SeedTransitionMessage{
			Cmd: protocol.CmdSeedTransition, RequestID: protocol.Ptr("move"),
			SourceSessionID: protocol.Ptr("source"), SeedID: seed, Verb: "tend",
		}, protocol.EventSeedTransitionResult, func(m protocol.SeedTransitionResultMessage) bool { return m.RequestID == "move" })
		if !moved.Success {
			t.Fatalf("the app's tend: %s", protocol.Deref(moved.Error))
		}
		w.advance(0)
		gardenNudgeInboxIsEmpty(t, cli, "source", "after the app tended for it")
		gardenNudgeOneBell(t, cli, "watcher", seed, "tended")

		noted := testworld.Request(app, protocol.SeedNoteMessage{
			Cmd: protocol.CmdSeedNote, RequestID: protocol.Ptr("note"),
			SourceSessionID: protocol.Ptr("source"), SeedID: seed, Body: "look now", Ring: protocol.Ptr(true),
		}, protocol.EventSeedNoteResult, func(m protocol.SeedNoteResultMessage) bool { return m.RequestID == "note" })
		if !noted.Success {
			t.Fatalf("the app's ringing note: %s", protocol.Deref(noted.Error))
		}
		w.advance(0)
		gardenNudgeInboxIsEmpty(t, cli, "source", "after the app noted for it")
		gardenNudgeOneBell(t, cli, "watcher", seed, "note.added")

		gardenNudgeMove(t, cli, "source", seed, "park")
		gardenNudgeNote(t, cli, "source", seed, "my own words", true)
		w.advance(0)
		gardenNudgeInboxIsEmpty(t, cli, "source", "after its own move and note")
	})
}

func TestWatchingAPlotHearsItsWholeTreeUntilUnwatched(t *testing.T) {
	inBubble(t, func(t *testing.T, w *world) {
		cli := w.Client()
		registerSessions(t, w, cli, "planter", "watcher", "bystander", "worker")
		crown, _, leaf := gardenNudgePlot(t, cli, "planter")
		gardenNudgeWatch(t, cli, "watcher", crown, false)
		gardenNudgeWatch(t, cli, "bystander", crown, false)

		gardenNudgeMove(t, cli, "worker", leaf, "tend")
		w.advance(0)
		gardenNudgeOneBell(t, cli, "watcher", leaf, "tended")
		gardenNudgeOneBell(t, cli, "bystander", leaf, "tended")

		gardenNudgeNote(t, cli, "worker", leaf, "queued before unwatch", true)
		w.advance(0)
		if unwatched := gardenNudgeWatch(t, cli, "watcher", crown, true); unwatched.Watching || !unwatched.Changed || len(unwatched.WatchingVia) != 0 {
			t.Fatalf("unwatch = %+v, want a change to not watching", unwatched)
		}
		gardenNudgeInboxIsEmpty(t, cli, "watcher", "after it unwatched with a bell pending")
		gardenNudgeOneBell(t, cli, "bystander", leaf, "note.added")

		gardenNudgeNote(t, cli, "worker", leaf, "after unwatch", true)
		gardenNudgeMove(t, cli, "worker", leaf, "park")
		future := gardenNudgePlant(t, cli, "planter", "future child", crown)
		gardenNudgeMove(t, cli, "worker", future, "tend")
		w.advance(0)
		gardenNudgeInboxIsEmpty(t, cli, "watcher", "after it unwatched the plot")

		gardenNudgeWatch(t, cli, "watcher", crown, false)
		gardenNudgeNote(t, cli, "planter", future, "rewatched", true)
		w.advance(0)
		gardenNudgeOneBell(t, cli, "watcher", future, "note.added")
	})
}

func TestUnreadBellsRingByChoiceAndCoalesceUntilTheSeedIsRead(t *testing.T) {
	inBubble(t, func(t *testing.T, w *world) {
		cli := w.Client()
		registerSessions(t, w, cli, "planter", "watcher", "worker")
		seed := plantSeedAs(t, cli, "planter", "delivery proof")
		gardenNudgeWatch(t, cli, "watcher", seed, false)

		gardenNudgeNote(t, cli, "worker", seed, "ordinary progress", false)
		w.advance(0)
		gardenNudgeInboxIsEmpty(t, cli, "watcher", "after a note that did not ask to ring")

		gardenNudgeNote(t, cli, "worker", seed, "please look", true)
		gardenNudgeMove(t, cli, "worker", seed, "tend")
		w.advance(0)
		gardenNudgeOneBell(t, cli, "watcher", seed, "note.added")

		gardenNudgeNote(t, cli, "worker", seed, "before the notes", true)
		w.advance(0)
		if _, err := cli.SeedNotes("watcher", seed, 0); err != nil {
			t.Fatalf("seed notes from the watcher: %v", err)
		}
		gardenNudgeInboxIsEmpty(t, cli, "watcher", "after it read the notes")
		gardenNudgeNote(t, cli, "worker", seed, "after the notes", true)
		w.advance(0)
		gardenNudgeOneBell(t, cli, "watcher", seed, "note.added")

		gardenNudgeNote(t, cli, "worker", seed, "before the show", true)
		w.advance(0)
		if shown, err := cli.SeedShow("watcher", seed); err != nil || !shown.Watching {
			t.Fatalf("show from the watcher = %+v, %v; want it watching", shown, err)
		}
		gardenNudgeInboxIsEmpty(t, cli, "watcher", "after it showed the seed")
		gardenNudgeNote(t, cli, "worker", seed, "after the show", true)
		w.advance(0)
		gardenNudgeOneBell(t, cli, "watcher", seed, "note.added")
	})
}

func TestAnUnwatchDropsOnlyWhatNoOtherRoleCovers(t *testing.T) {
	inBubble(t, func(t *testing.T, w *world) {
		cli := w.Client()
		registerSessions(t, w, cli, "planter", "recipient", "bystander", "worker", "successor")

		tended := plantSeedAs(t, cli, "planter", "tended and watched")
		gardenNudgeMove(t, cli, "recipient", tended, "tend")
		gardenNudgeWatch(t, cli, "recipient", tended, false)
		gardenNudgeNote(t, cli, "worker", tended, "while tender", true)
		w.advance(0)
		if unwatched := gardenNudgeWatch(t, cli, "recipient", tended, true); unwatched.Watching || !unwatched.Changed {
			t.Fatalf("unwatch = %+v", unwatched)
		}
		gardenNudgeOneBell(t, cli, "recipient", tended, "note.added")

		parked := plantSeedAs(t, cli, "planter", "parked but watched")
		gardenNudgeMove(t, cli, "recipient", parked, "tend")
		gardenNudgeWatch(t, cli, "recipient", parked, false)
		gardenNudgeNote(t, cli, "worker", parked, "while both", true)
		w.advance(0)
		gardenNudgeMove(t, cli, "recipient", parked, "park")
		gardenNudgeOneBell(t, cli, "recipient", parked, "note.added")

		abandoned := plantSeedAs(t, cli, "planter", "every role dropped")
		gardenNudgeMove(t, cli, "recipient", abandoned, "tend")
		gardenNudgeWatch(t, cli, "recipient", abandoned, false)
		gardenNudgeNote(t, cli, "worker", abandoned, "while both", true)
		w.advance(0)
		gardenNudgeMove(t, cli, "recipient", abandoned, "park")
		gardenNudgeWatch(t, cli, "recipient", abandoned, true)
		gardenNudgeWatch(t, cli, "recipient", abandoned, false)
		gardenNudgeInboxIsEmpty(t, cli, "recipient", "after it lost its last role and regained one")

		blocker := plantSeedAs(t, cli, "planter", "the blocker")
		blocked := plantSeedAs(t, cli, "planter", "waiting on the blocker")
		if _, err := cli.SeedLink(blocker, "blocks", blocked, false); err != nil {
			t.Fatal(err)
		}
		gardenNudgeMove(t, cli, "recipient", blocked, "tend")
		gardenNudgeMove(t, cli, "worker", blocker, "tend")
		if harvested := gardenNudgeMove(t, cli, "worker", blocker, "harvest"); len(harvested.Unblocked) != 1 || harvested.Unblocked[0].ID != blocked {
			t.Fatalf("harvesting the blocker unblocked %+v, want %s", harvested.Unblocked, blocked)
		}
		w.advance(0)
		gardenNudgeMove(t, cli, "recipient", blocked, "park")
		gardenNudgeMove(t, cli, "successor", blocked, "tend")
		w.advance(0)
		gardenNudgeInboxIsEmpty(t, cli, "recipient", "after someone else took over the unblocked seed")

		crown, child, leaf := gardenNudgePlot(t, cli, "planter")
		gardenNudgeWatch(t, cli, "recipient", crown, false)
		gardenNudgeWatch(t, cli, "recipient", child, false)
		gardenNudgeWatch(t, cli, "bystander", crown, false)
		sendAgentMessage(t, cli, "worker", "recipient", "unrelated mail")
		gardenNudgeNote(t, cli, "worker", leaf, "covered by the child", true)
		gardenNudgeNote(t, cli, "worker", crown, "covered only by the plot", true)
		w.advance(0)
		gardenNudgeWatch(t, cli, "recipient", crown, true)
		if got := readInbox(t, cli, "recipient", 0).Items; len(got) != 2 || !strings.Contains(inboxContents(got), "unrelated mail") || !strings.Contains(inboxContents(got), leaf+" moved: note.added") {
			t.Errorf("after unwatching the crown the inbox holds %q, want the mail and the child-covered bell", inboxContents(got))
		}
		if got := readInbox(t, cli, "bystander", 0).Items; len(got) != 2 {
			t.Errorf("the other watcher's inbox holds %q, want both bells", inboxContents(got))
		}
		inherited := gardenNudgeWatch(t, cli, "recipient", leaf, true)
		if inherited.Changed || !inherited.Watching || !reflect.DeepEqual(inherited.WatchingVia, []string{child}) {
			t.Errorf("unwatching the leaf itself = %+v, want still watching via %s", inherited, child)
		}
		if shown, err := cli.SeedShow("recipient", leaf); err != nil || !reflect.DeepEqual(shown.WatchingVia, []string{child}) {
			t.Errorf("show of the leaf = %+v (%v), want watching via %s", shown, err, child)
		}
	})
}

func TestBellsFromATreeASeedLeftAreDiscarded(t *testing.T) {
	inBubble(t, func(t *testing.T, w *world) {
		cli := w.Client()
		registerSessions(t, w, cli, "planter", "watcher", "direct", "worker")
		crown, child, leaf := gardenNudgePlot(t, cli, "planter")
		gardenNudgeWatch(t, cli, "watcher", crown, false)
		gardenNudgeWatch(t, cli, "direct", leaf, false)
		gardenNudgeNote(t, cli, "worker", leaf, "rang through the old tree", true)
		w.advance(0)

		if _, err := cli.SeedLink(child, "part-of", crown, true); err != nil {
			t.Fatalf("take the child out of the plot: %v", err)
		}
		gardenNudgeInboxIsEmpty(t, cli, "watcher", "after the seed left the plot it watched")
		gardenNudgeOneBell(t, cli, "direct", leaf, "note.added")
	})
}

func gardenNudgePlot(t *testing.T, cli *client.Client, planter string) (crown, child, leaf string) {
	t.Helper()
	crown = gardenNudgePlant(t, cli, planter, "ship seed nudges", "")
	child = gardenNudgePlant(t, cli, planter, "daemon mechanics", crown)
	leaf = gardenNudgePlant(t, cli, planter, "delivery proof", child)
	return crown, child, leaf
}

func gardenNudgePlant(t *testing.T, cli *client.Client, planter, title, partOf string) string {
	t.Helper()
	planted, err := cli.SeedPlant(planter, title, "Work through "+title+".", partOf, "", "")
	if err != nil {
		t.Fatalf("plant %q under %s: %v", title, partOf, err)
	}
	return planted.Seed.ID
}

func gardenNudgeWatch(t *testing.T, cli *client.Client, session, seedID string, unwatch bool) *protocol.SeedWatchResult {
	t.Helper()
	result, err := cli.SeedWatch(session, seedID, unwatch)
	if err != nil {
		t.Fatalf("%s watch %s (unwatch %v): %v", session, seedID, unwatch, err)
	}
	return result
}

func gardenNudgeMove(t *testing.T, cli *client.Client, session, seedID, verb string) *protocol.SeedTransitionResult {
	t.Helper()
	reason := ""
	if verb == "harvest" || verb == "wither" {
		reason = "done"
	}
	result, err := cli.SeedTransition(session, seedID, verb, reason, "", false, client.SeedTransitionOptions{})
	if err != nil {
		t.Fatalf("%s %s %s: %v", session, verb, seedID, err)
	}
	return result
}

func gardenNudgeNote(t *testing.T, cli *client.Client, session, seedID, body string, ring bool) {
	t.Helper()
	if _, err := cli.SeedNote(session, seedID, body, "", "", ring, nil); err != nil {
		t.Fatalf("%s notes %q on %s: %v", session, body, seedID, err)
	}
}

func gardenNudgeInboxIsEmpty(t *testing.T, cli *client.Client, session, when string) {
	t.Helper()
	if items := readInbox(t, cli, session, 0).Items; len(items) != 0 {
		t.Errorf("%s's inbox %s holds %q, want nothing", session, when, inboxContents(items))
	}
}

func gardenNudgeOneBell(t *testing.T, cli *client.Client, session, seedID, event string) {
	t.Helper()
	items := readInbox(t, cli, session, 0).Items
	if len(items) != 1 || !strings.Contains(items[0].Content, seedID+" moved: "+event) {
		t.Errorf("%s's inbox holds %q, want one %s bell for %s", session, inboxContents(items), event, seedID)
	}
}

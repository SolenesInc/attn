package daemon_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
)

func TestClosingASeedReportsExactlyTheSeedsItUnblocked(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	closer := spawnPanes(w, app, w.Path("closer"))[0].session

	t.Run("the one dependent", func(t *testing.T) {
		blocker, dependent := rippleChain(t, cli, "lay the pipe", "run water through it")
		lifeMove(t, cli, closer, blocker, "tend", "", "")
		rippleUnblocks(t, cli, closer, blocker, "harvest", dependent)
	})
	t.Run("every dependent", func(t *testing.T) {
		blocker, dependent := rippleChain(t, cli, "lay the pipe", "run water through it")
		second := plantSeedAs(t, cli, "", "paint the wall")
		edgeLink(t, cli, blocker, "blocks", second)
		rippleUnblocks(t, cli, closer, blocker, "harvest", dependent, second)
	})
	t.Run("only when the last blocker closes", func(t *testing.T) {
		blocker, dependent := rippleChain(t, cli, "lay the pipe", "run water through it")
		other := plantSeedAs(t, cli, "", "pour the slab")
		edgeLink(t, cli, other, "blocks", dependent)
		rippleUnblocks(t, cli, closer, blocker, "harvest")
		rippleUnblocks(t, cli, closer, other, "harvest", dependent)
	})
	t.Run("nothing when the seed blocked nobody", func(t *testing.T) {
		_, dependent := rippleChain(t, cli, "lay the pipe", "run water through it")
		rippleUnblocks(t, cli, closer, dependent, "wither")
	})
	t.Run("the same when withered", func(t *testing.T) {
		blocker, dependent := rippleChain(t, cli, "lay the pipe", "run water through it")
		rippleUnblocks(t, cli, closer, blocker, "wither", dependent)
	})
}

func TestTheTenderOfAnUnblockedSeedIsRung(t *testing.T) {
	inBubble(t, func(t *testing.T, w *world) {
		w.finishStartupWork()
		writeCrewCharter(t, w, "trellis")
		w.restart()
		cli := w.Client()
		registerSessions(t, w, cli, "harvester", "tender", "bystander")
		if err := cli.RegisterAsMember("bound", "bound", w.Path("bound"), "", "trellis"); err != nil {
			t.Fatalf("bind trellis: %v", err)
		}
		everyone := []string{"harvester", "tender", "bystander", "bound"}

		blocker, _ := rippleChain(t, cli, "lay the pipe", "run water through it")
		lifeMove(t, cli, "harvester", blocker, "harvest", "pipe laid", "")
		w.advance(0)
		rippleRung(t, cli, "a seed nobody tends", "", everyone...)

		blocker, dependent := rippleChain(t, cli, "lay the pipe", "run water through it")
		lifeMove(t, cli, "tender", dependent, "tend", "", "")
		lifeMove(t, cli, "harvester", blocker, "harvest", "pipe laid", "")
		w.advance(0)
		rippleRung(t, cli, "a seed the tender holds", dependent, "tender")
		rippleRung(t, cli, "a seed the tender holds", "", "harvester", "bystander", "bound")

		blocker, dependent = rippleChain(t, cli, "lay the pipe", "run water through it")
		lifeMove(t, cli, "tender", dependent, "tend", "", "")
		if _, err := cli.SeedWatch("tender", dependent, false); err != nil {
			t.Fatal(err)
		}
		if _, err := cli.SeedNote("bystander", dependent, "the valve is in", "", "", true, nil); err != nil {
			t.Fatal(err)
		}
		lifeMove(t, cli, "harvester", blocker, "harvest", "pipe laid", "")
		w.advance(0)
		unwatched, err := cli.SeedWatch("tender", dependent, true)
		if err != nil || unwatched.Watching || !unwatched.Changed {
			t.Fatalf("unwatch = %+v, %v", unwatched, err)
		}
		w.advance(0)
		rippleRung(t, cli, "a seed the tender stopped watching", dependent, "tender")

		blocker, dependent = rippleChain(t, cli, "lay the pipe", "run water through it")
		lifeMove(t, cli, "harvester", dependent, "tend", "", "")
		lifeMove(t, cli, "harvester", blocker, "harvest", "pipe laid", "")
		w.advance(0)
		rippleRung(t, cli, "a seed the harvester holds", "", everyone...)

		blocker, dependent = rippleChain(t, cli, "lay the pipe", "run water through it")
		lifeMove(t, cli, "", dependent, "tend", "", "trellis")
		w.advance(0)
		readInbox(t, cli, "bound", 0)
		lifeMove(t, cli, "harvester", blocker, "harvest", "pipe laid", "")
		w.advance(0)
		rippleRung(t, cli, "a seed a member tends", dependent, "bound")
	})
}

func TestAMergedPullRequestUnblocksLikeAnyOtherClose(t *testing.T) {
	github := newHarvestGitHub(t)
	inBubble(t, func(t *testing.T, w *world) {
		cli := w.Client()
		registerSessions(t, w, cli, "shipper", "tender")

		blocker, dependent := rippleChain(t, cli, "lay the pipe", "run water through it")
		lifeMove(t, cli, "tender", dependent, "tend", "", "")
		armed := harvestArm(t, cli, "shipper", blocker, github.open(71, "Lay the pipe"))
		if len(armed.Unblocked) != 0 {
			t.Fatalf("arming on an open pull request announced %v", lifeSeedIDs(armed.Unblocked))
		}
		github.merge(71, "Lay the pipe")
		w.advance(protocol.HeatHotInterval)
		rippleRung(t, cli, "a merge that harvests the blocker", dependent, "tender")

		blocker, dependent = rippleChain(t, cli, "lay the pipe", "run water through it")
		merged := github.merge(72, "Lay the other pipe")
		if err := cli.RecordPullRequestCreated("shipper", merged); err != nil {
			t.Fatal(err)
		}
		w.advance(protocol.HeatHotInterval)
		if got := lifeSeedIDs(harvestArm(t, cli, "shipper", blocker, merged).Unblocked); !slices.Equal(got, []string{dependent}) {
			t.Errorf("arming on a merged pull request announced %v, want %s", got, dependent)
		}
	})
}

func rippleChain(t *testing.T, cli *client.Client, blockerTitle, dependentTitle string) (blocker, dependent string) {
	t.Helper()
	blocker = plantSeedAs(t, cli, "", blockerTitle)
	dependent = plantSeedAs(t, cli, "", dependentTitle)
	edgeLink(t, cli, blocker, "blocks", dependent)
	return blocker, dependent
}

func rippleUnblocks(t *testing.T, cli *client.Client, closer, seed, verb string, want ...string) {
	t.Helper()
	moved, err := cli.SeedTransition(closer, seed, verb, "closed", "", true, client.SeedTransitionOptions{})
	if err != nil {
		t.Fatalf("%s %s: %v", verb, seed, err)
	}
	got := lifeSeedIDs(moved.Unblocked)
	slices.Sort(got)
	want = slices.Clone(want)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Errorf("%s %s announced unblocked %v, want %v", verb, seed, got, want)
	}
}

func rippleRung(t *testing.T, cli *client.Client, what, seed string, sessions ...string) {
	t.Helper()
	for _, session := range sessions {
		items := readInbox(t, cli, session, 0).Items
		if seed == "" {
			if len(items) != 0 {
				t.Errorf("after unblocking %s, %s was rung with %q", what, session, inboxContents(items))
			}
			continue
		}
		if len(items) != 1 || protocol.Deref(items[0].Hint) != "unblocked" || !strings.Contains(items[0].Content, seed+" moved: unblocked") {
			t.Errorf("after unblocking %s, %s holds %q, want one bell saying %s was unblocked", what, session, inboxContents(items), seed)
		}
	}
}

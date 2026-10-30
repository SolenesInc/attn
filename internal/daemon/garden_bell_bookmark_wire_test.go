package daemon_test

import (
	"database/sql"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"testing"

	"github.com/victorarias/attn/internal/client"
)

var bellSince = regexp.MustCompile("read what changed with `attn seed show (s-[a-z0-9]+) --since ([0-9]+)`")

func TestABellHandsOverTheSinceReadOfEverythingItCovers(t *testing.T) {
	inBubble(t, func(t *testing.T, w *world) {
		cli := w.Client()
		registerSessions(t, w, cli, "planter", "watcher", "worker")
		seed := plantSeedAs(t, cli, "planter", "bookmark proof")
		gardenNudgeWatch(t, cli, "watcher", seed, false)
		before, err := cli.SeedShow("watcher", seed)
		if err != nil {
			t.Fatalf("watcher's first read: %v", err)
		}

		gardenNudgeNote(t, cli, "worker", seed, "please look", true)
		gardenNudgeMove(t, cli, "worker", seed, "tend")
		w.advance(0)
		items := readInbox(t, cli, "watcher", 0).Items
		if len(items) != 1 {
			t.Fatalf("watcher's inbox holds %q, want one bell", inboxContents(items))
		}
		match := bellSince.FindStringSubmatch(items[0].Content)
		if match == nil || match[1] != seed {
			t.Fatalf("bell = %q, want it to hand over `attn seed show %s --since N`", items[0].Content, seed)
		}
		after, _ := strconv.Atoi(match[2])
		if after < before.Bookmark {
			t.Fatalf("bell since %d is before the watcher's bookmark %d, taken before the note", after, before.Bookmark)
		}

		read := sinceEntries(t, cli, seed, after)
		if len(read) != 2 || read[0] != "move" || read[1] != "note: please look" {
			t.Fatalf("show --since %d = %q, want the note and the tend that coalesced into the bell", after, read)
		}
		if later := sinceEntries(t, cli, seed, after+1); len(later) != 1 || later[0] != "move" {
			t.Fatalf("show --since %d = %q, want only the tend: the bell's number sits just before the note", after+1, later)
		}
	})
}

func TestABellStoredBeforeBellsKeptAPositionReadsTheWholeSeed(t *testing.T) {
	w := &world{World: prepareWorld(t)}
	w.start()
	cli := w.Client()
	registerSessions(t, w, cli, "planter", "watcher", "worker")
	seed := plantSeedAs(t, cli, "planter", "older bell")
	gardenNudgeWatch(t, cli, "watcher", seed, false)
	gardenNudgeNote(t, cli, "worker", seed, "rang before the upgrade", true)
	w.stop()
	asTheMigrationLeftIt(t, w)
	w.start()

	items := readInbox(t, w.Client(), "watcher", 0).Items
	want := seed + " moved: note.added — read it with `attn seed show " + seed + "`."
	if len(items) != 1 || items[0].Content != want {
		t.Fatalf("watcher's inbox holds %q, want %q", inboxContents(items), want)
	}
}

func asTheMigrationLeftIt(t *testing.T, w *world) {
	t.Helper()
	db, err := sql.Open("sqlite3", filepath.Join(w.Dir, "attn.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`UPDATE inbox_items SET after_seq = 0`); err != nil {
		t.Fatal(err)
	}
}

func sinceEntries(t *testing.T, cli *client.Client, seed string, after int) []string {
	t.Helper()
	shown, err := cli.SeedShowSince("planter", seed, after)
	if err != nil || shown.Since == nil {
		t.Fatalf("show %s --since %d = %+v, %v", seed, after, shown, err)
	}
	kinds := make([]string, len(shown.Since.Entries))
	for i, entry := range shown.Since.Entries {
		kinds[i] = entry.Kind
		if entry.Kind == "note" {
			kinds[i] += ": " + entry.Body
		}
	}
	slices.Sort(kinds)
	return kinds
}

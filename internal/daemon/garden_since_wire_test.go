package daemon_test

import (
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/bus"
	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/protocol"
)

func TestASinceReadReturnsExactlyWhatWasSavedAfterItsBookmark(t *testing.T) {
	inBubble(t, func(t *testing.T, w *world) {
		cli := w.Client()
		registerSessions(t, w, cli, "reader", "worker")
		seed := plantSeedAs(t, cli, "worker", "A seed on its own")
		plot := plantSeedAs(t, cli, "worker", "A plot")
		child := plantPartAs(t, cli, "worker", "A child", plot)
		grandchild := plantPartAs(t, cli, "worker", "A grandchild", child)

		if _, err := cli.SeedWatch("reader", seed, false); err != nil {
			t.Fatalf("watch the seed: %v", err)
		}
		sinceNote(t, cli, seed, "ring the reader", true)
		w.advance(0)
		if items := readInbox(t, cli, "reader", 0).Items; len(items) != 1 {
			t.Fatalf("the reader's inbox = %q, want the seed's bell for the read to consume", inboxContents(items))
		}
		before := sinceShow(t, cli, "reader", seed, nil).Bookmark
		after := sinceShow(t, cli, "reader", seed, nil).Bookmark
		if before == 0 || after != before {
			t.Fatalf("two reads with nothing saved between printed bookmarks %d and %d; a read must print the log head and write nothing to it", before, after)
		}
		if items := readInbox(t, cli, "reader", 0).Items; len(items) != 0 {
			t.Errorf("the read left the bell unread: %q", inboxContents(items))
		}

		seedMark := sinceShow(t, cli, "", seed, nil).Bookmark
		plotMark := sinceShow(t, cli, "", plot, nil).Bookmark
		onSeed := sinceNote(t, cli, seed, "a note on the seed", false)
		first := sinceNote(t, cli, grandchild, "the grandchild's first note", false)
		second := sinceNote(t, cli, grandchild, "the grandchild's second note", false)

		seedSince := sinceShow(t, cli, "", seed, &seedMark)
		requireSince(t, "the seed", seedSince.Since, seedMark, onSeed)
		plotSince := sinceShow(t, cli, "", plot, &plotMark)
		requireSince(t, "the plot", plotSince.Since, plotMark, second, first)
		if plotSince.Bookmark <= plotMark || seedSince.Bookmark <= seedMark {
			t.Errorf("since-reads printed bookmarks %d (seed) and %d (plot), want each past %d and %d", seedSince.Bookmark, plotSince.Bookmark, seedMark, plotMark)
		}
		notesSince, err := cli.SeedNotes("", plot, client.SeedNotesQuery{Since: &plotMark})
		if err != nil {
			t.Fatalf("notes --since on the plot: %v", err)
		}
		requireEntries(t, "notes --since on the plot", notesSince.Notes, second, first)
		if notesSince.Bookmark != plotSince.Bookmark || notesSince.Expired != nil {
			t.Errorf("notes --since printed bookmark %d (expired %+v), want %d like show", notesSince.Bookmark, notesSince.Expired, plotSince.Bookmark)
		}

		requireSince(t, "the seed again", sinceShow(t, cli, "", seed, &seedSince.Bookmark).Since, seedSince.Bookmark)
		requireSince(t, "the plot again", sinceShow(t, cli, "", plot, &plotSince.Bookmark).Since, plotSince.Bookmark)

		const written = 20
		mark := plotSince.Bookmark
		seen := map[string]int{}
		done := make(chan struct{})
		go func() {
			defer close(done)
			for i := range written {
				sinceNote(t, cli, child, fmt.Sprintf("written while the plot is read, %d", i), false)
			}
		}()
		read := func() {
			shown := sinceShow(t, cli, "", plot, &mark)
			for _, entry := range shown.Since.Entries {
				seen[entry.ID]++
			}
			mark = shown.Bookmark
		}
		for writing := true; writing; {
			select {
			case <-done:
				writing = false
			default:
				read()
			}
		}
		read()
		if len(seen) != written {
			t.Errorf("chained since-reads saw %d of the %d entries written between them", len(seen), written)
		}
		for id, times := range seen {
			if times != 1 {
				t.Errorf("entry %s appeared in %d since-reads, want exactly one", id, times)
			}
		}
	})
}

func TestASinceReadOlderThanTheEventLogIsAFullRead(t *testing.T) {
	t.Setenv(bus.RetentionEnv, "1h")
	inBubble(t, func(t *testing.T, w *world) {
		cli := w.Client()
		registerSessions(t, w, cli, "worker")
		seed := plantSeedAs(t, cli, "worker", "A seed whose bookmark ages out")
		old := sinceShow(t, cli, "", seed, nil).Bookmark
		lost := sinceNote(t, cli, seed, "saved after the bookmark, then trimmed from the event log", false)

		w.advance(2*bus.DefaultTrimInterval + time.Minute)
		emptied := sinceShow(t, cli, "", seed, &old)
		if emptied.Since.Expired == nil || !slices.ContainsFunc(emptied.Notes, noteWithID(lost.ID)) {
			t.Fatalf("a since-read from %d after retention emptied the event log answered %+v with notes %+v, want the full read with the trimmed note", old, emptied.Since, emptied.Notes)
		}
		if emptied.Bookmark <= old {
			t.Errorf("the emptied log printed bookmark %d, want the head past %d", emptied.Bookmark, old)
		}
		emptiedNotes, err := cli.SeedNotes("", seed, client.SeedNotesQuery{Since: &old})
		if err != nil {
			t.Fatalf("notes --since %d on the emptied log: %v", old, err)
		}
		if emptiedNotes.Expired == nil || emptiedNotes.Bookmark != emptied.Bookmark || !slices.ContainsFunc(emptiedNotes.Notes, noteWithID(lost.ID)) {
			t.Errorf("notes --since %d on the emptied log answered expired %+v, bookmark %d, notes %+v; want the full read at bookmark %d", old, emptiedNotes.Expired, emptiedNotes.Bookmark, emptiedNotes.Notes, emptied.Bookmark)
		}
		requireSince(t, "a bookmark at the emptied log's head", sinceShow(t, cli, "", seed, &emptied.Bookmark).Since, emptied.Bookmark)

		late := sinceNote(t, cli, seed, "after the bookmark aged out", false)

		shown := sinceShow(t, cli, "", seed, &old)
		expired := shown.Since.Expired
		if expired == nil {
			t.Fatalf("a since-read from %d after the log was trimmed answered %+v, want it to say the bookmark is too old", old, shown.Since)
		}
		if expired.Earliest <= old+1 || expired.RetentionSeconds != int(time.Hour/time.Second) {
			t.Errorf("expired = %+v, want the earliest kept save past %d and the 1h retention", *expired, old+1)
		}
		if len(shown.Since.Entries) != 0 || !slices.ContainsFunc(shown.Notes, noteWithID(late.ID)) || !slices.ContainsFunc(shown.Notes, noteWithID(lost.ID)) {
			t.Errorf("the fallback carried since entries %d and full-read notes %+v, want the full read with both notes", len(shown.Since.Entries), shown.Notes)
		}

		notes, err := cli.SeedNotes("", seed, client.SeedNotesQuery{Since: &old})
		if err != nil {
			t.Fatalf("notes --since %d: %v", old, err)
		}
		if notes.Expired == nil || *notes.Expired != *expired || len(notes.Notes) != 2 {
			t.Errorf("notes --since %d answered expired %+v with %d notes, want %+v and the full log", old, notes.Expired, len(notes.Notes), *expired)
		}

		kept := expired.Earliest - 1
		requireSince(t, "a bookmark at the oldest kept save", sinceShow(t, cli, "", seed, &kept).Since, kept, late)
	})
}

func plantPartAs(t *testing.T, cli *client.Client, sessionID, title, plot string) string {
	t.Helper()
	planted, err := cli.SeedPlant(protocol.SessionID(sessionID), title, "", plot, "", "")
	if err != nil {
		t.Fatalf("plant %q under %s: %v", title, plot, err)
	}
	return planted.Seed.ID
}

func sinceNote(t *testing.T, cli *client.Client, seedID, body string, ring bool) protocol.SeedNote {
	t.Helper()
	result, err := cli.SeedNote("worker", seedID, body, "", "", ring, nil)
	if err != nil {
		t.Errorf("note %q on %s: %v", body, seedID, err)
		return protocol.SeedNote{}
	}
	return result.Note
}

func sinceShow(t *testing.T, cli *client.Client, sessionID protocol.SessionID, seedID string, since *int) *protocol.SeedShowResult {
	t.Helper()
	var (
		shown *protocol.SeedShowResult
		err   error
	)
	if since == nil {
		shown, err = cli.SeedShow(sessionID, seedID)
	} else {
		shown, err = cli.SeedShowSince(sessionID, seedID, *since)
	}
	if err != nil {
		t.Fatalf("show %s: %v", seedID, err)
	}
	if since != nil && shown.Since == nil {
		t.Fatalf("show %s --since %d answered without a since", seedID, *since)
	}
	return shown
}

func requireSince(t *testing.T, what string, since *protocol.SeedSince, after int, want ...protocol.SeedNote) {
	t.Helper()
	if since.After != after || since.Expired != nil {
		t.Errorf("the since-read of %s answered after %d (expired %+v), want after %d", what, since.After, since.Expired, after)
	}
	requireEntries(t, "the since-read of "+what, since.Entries, want...)
}

func requireEntries(t *testing.T, what string, got []protocol.SeedNote, want ...protocol.SeedNote) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s returned %d entries %+v, want %d", what, len(got), got, len(want))
	}
	for i := range want {
		if got[i].ID != want[i].ID || got[i].SeedID != want[i].SeedID || got[i].Body != want[i].Body || protocol.Deref(got[i].Seq) <= olderSeq(got, i) {
			t.Errorf("%s entry %d = %+v, want %+v labeled by its seed with a save number, newest first", what, i, got[i], want[i])
		}
	}
}

func olderSeq(entries []protocol.SeedNote, i int) int {
	if i+1 < len(entries) {
		return protocol.Deref(entries[i+1].Seq)
	}
	return 0
}

func noteWithID(id string) func(protocol.SeedNote) bool {
	return func(note protocol.SeedNote) bool { return note.ID == id }
}

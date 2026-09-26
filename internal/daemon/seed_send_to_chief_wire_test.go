package daemon_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/protocol"
)

func TestSendingASeedToTheChiefHandsItOverUnlessItChanged(t *testing.T) {
	inBubble(t, func(t *testing.T, w *world) {
		app, cli := w.App(), w.Client()
		setSetting(t, app, "notebook.root", filepath.Join(t.TempDir(), "notebook"))
		registerSessions(t, w, cli, "chief", "sender", "observer")
		if made := setChiefOfStaff(app, "chief", true); !made.Success {
			t.Fatalf("making chief the Chief: %s", protocol.Deref(made.Error))
		}
		seed := plantSeedAs(t, cli, "sender", "Place this work")
		tended := lifeMove(t, cli, "sender", seed, "tend", "", "")
		for _, watcher := range []string{"sender", "observer"} {
			if _, err := cli.SeedWatch(watcher, seed, false); err != nil {
				t.Fatal(err)
			}
		}

		stale := tended
		stale.Rev--
		_, err := cli.SeedSendToChief("sender", stale, "")
		lifeRefusal(t, "sending a seed changed since it was opened", err, seed, "changed since you opened it")
		if still := lifeShow(t, cli, seed).Seed; still.TenderSession != "sender" || still.Rev != tended.Rev {
			t.Fatalf("the refused send moved the seed: %+v", still)
		}

		sent, err := cli.SeedSendToChief("sender", tended, "Use branch feature/special in /tmp/special.")
		if err != nil {
			t.Fatal(err)
		}
		if sent.ChiefSessionID != "chief" || sent.Seed.TenderSession != "chief" {
			t.Errorf("sending to the Chief answered %+v, want the chief tending it", sent)
		}
		shown := lifeShow(t, cli, seed)
		if got := shown.Seed; got.TenderSession != "chief" || protocol.Deref(got.LastExecutionID) != protocol.Deref(tended.LastExecutionID) {
			t.Errorf("after the send the seed is tended by %q in execution %q, want chief in %q",
				got.TenderSession, protocol.Deref(got.LastExecutionID), protocol.Deref(tended.LastExecutionID))
		}
		if len(shown.Notes) == 0 || !strings.Contains(shown.Notes[0].Body, "Sent to Chief") || !strings.Contains(shown.Notes[0].Body, "feature/special") {
			t.Errorf("the seed's log leads with %+v, want a Sent to Chief note carrying the guidance", shown.Notes)
		}

		w.advance(0)
		if bells := readInbox(t, cli, "observer", 0).Items; len(bells) != 1 || protocol.Deref(bells[0].Hint) != "tended" || !strings.Contains(bells[0].Content, seed) {
			t.Errorf("the observer received %q, want one bell that %s is tended", inboxContents(bells), seed)
		}
		if bells := readInbox(t, cli, "sender", 0).Items; len(bells) != 0 {
			t.Errorf("the sender received %q, want no bell for its own move", inboxContents(bells))
		}
		if items := readInbox(t, cli, "chief", 0).Items; len(items) != 1 || protocol.Deref(items[0].Hint) == "tended" || !strings.Contains(items[0].Content, "attn seed show "+seed) {
			t.Errorf("the chief received %q, want only the assignment pointing at %s and no tended bell", inboxContents(items), seed)
		}
	})
}

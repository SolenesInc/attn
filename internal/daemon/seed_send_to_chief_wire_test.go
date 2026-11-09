package daemon_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/protocol"
)

func TestSendingASeedToTheChiefHandsItOverUnlessItChanged(t *testing.T) {
	inBubbleWithAgents(t, func(t *testing.T, w *world) {
		cli := w.Client()
		registerSessions(t, w, cli, "sender", "observer")
		setCrew(t, cli, "chief", protocol.CrewSetMessage{Agent: protocol.Ptr("claude")})
		chief := w.bootBubbleClaude(t, string(startCrew(t, cli, "chief", "").SessionID))
		chief.reply("Ready. <!-- attn:state=idle -->")
		member := chiefOfRoster(t, crewRoster(t, cli)).ID
		seed := plantSeedAs(t, cli, "sender", "Place this work")
		tended := lifeMove(t, cli, "sender", seed, "tend", "", "")
		// Drain the setup move before its late bell can reach the next tender or new watchers.
		w.advance(0)
		for _, watcher := range []string{"sender", "observer"} {
			if _, err := cli.SeedWatch(protocol.SessionID(watcher), seed, false); err != nil {
				t.Fatal(err)
			}
		}

		stale := tended
		stale.Rev--
		_, err := cli.SeedSendToChief("sender", stale, "")
		lifeRefusal(t, "sending a seed changed since it was opened", err, seed, "changed since you opened it")
		if still := lifeShow(t, cli, seed).Seed; tenderSessionOf(still) != "sender" || still.Rev != tended.Rev {
			t.Fatalf("the refused send moved the seed: %+v", still)
		}

		sent, err := cli.SeedSendToChief("sender", tended, "Use branch feature/special in /tmp/special.")
		if err != nil {
			t.Fatal(err)
		}
		if string(sent.ChiefSessionID) != chief.id || tenderMemberOf(sent.Seed) != member {
			t.Errorf("sending to the Chief answered %+v, want the Chief member tending it from %s", sent, chief.id)
		}
		shown := lifeShow(t, cli, seed)
		if got := shown.Seed; tenderMemberOf(got) != member || protocol.Deref(got.LastExecutionID) != protocol.Deref(tended.LastExecutionID) {
			t.Errorf("after the send the seed is tended by %+v in execution %q, want the Chief member in %q",
				got.Tender, protocol.Deref(got.LastExecutionID), protocol.Deref(tended.LastExecutionID))
		}
		if !slices.ContainsFunc(shown.Notes, func(note protocol.SeedNote) bool {
			return strings.Contains(note.Body, "Sent to Chief") && strings.Contains(note.Body, "feature/special")
		}) {
			t.Errorf("the seed's log = %+v, want a Sent to Chief note carrying the guidance", shown.Notes)
		}

		w.advance(0)
		if bells := readInbox(t, cli, "observer", 0).Items; len(bells) != 1 || protocol.Deref(bells[0].Hint) != "tended" || !strings.Contains(bells[0].Content, seed) {
			t.Errorf("the observer received %q, want one bell that %s is tended", inboxContents(bells), seed)
		}
		if bells := readInbox(t, cli, "sender", 0).Items; len(bells) != 0 {
			t.Errorf("the sender received %q, want no bell for its own move", inboxContents(bells))
		}
		if items := readInbox(t, cli, chief.id, 0).Items; len(items) != 1 || protocol.Deref(items[0].Hint) == "tended" || !strings.Contains(items[0].Content, "attn seed show "+seed) {
			t.Errorf("the chief received %q, want only the assignment pointing at %s and no tended bell", inboxContents(items), seed)
		}
	})
}

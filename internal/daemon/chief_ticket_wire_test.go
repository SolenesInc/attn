package daemon_test

import (
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

func TestALegacyDelegationTicketReachesItsCreatorAndTheChief(t *testing.T) {
	chiefTicketUpgrade(t, func(t *testing.T, w *world) {
		app, cli := w.App(), w.Client()
		ticketNudgeReadySessions(t, w, cli, "creator", "chief", "worker")
		setChiefOfStaff(app, "chief", true)
		for _, session := range []string{"creator", "chief", "worker"} {
			inboxLines(t, cli, session)
		}

		reportTicket(t, cli, "worker", "ordinary", protocol.DispatchWorkStateReadyForReview, "take a look")
		for _, observer := range []string{"creator", "chief"} {
			if got := inboxLines(t, cli, observer); !slices.Equal(got, []string{"ordinary status_changed take a look"}) {
				t.Errorf("%s read %q, want the worker's report once", observer, got)
			}
		}
		commentOnTicket(t, cli, "creator", "ordinary", "one more thing to check")
		if got := inboxLines(t, cli, "worker"); !slices.Equal(got, []string{"ordinary commented one more thing to check"}) {
			t.Errorf("the worker read %q, want the creator's comment", got)
		}
	})
}

func TestAChiefOwnedLegacyTicketFollowsTheRoleToTheNextChief(t *testing.T) {
	chiefTicketUpgrade(t, func(t *testing.T, w *world) {
		app, cli := w.App(), w.Client()
		ticketNudgeReadySessions(t, w, cli, "chief-a", "chief-b", "worker")
		setChiefOfStaff(app, "chief-a", true)
		for _, session := range []string{"chief-a", "chief-b", "worker"} {
			inboxLines(t, cli, session)
		}
		w.advance(ticketNudgeBundleWindow)

		reportTicket(t, cli, "worker", "chiefs", protocol.DispatchWorkStateNeedsInput, "need a decision")
		ticketNudgeAwaitDeadline(t, w, app, "chief-a", time.Now().Add(ticketNudgeCountdown))
		w.advance(ticketNudgeCountdown)
		ticketNudgeAwaitDelivered(t, w, app, cli, "chief-a")
		if got := inboxLines(t, cli, "chief-a"); !slices.Equal(got, []string{"chiefs status_changed need a decision"}) {
			t.Errorf("the chief read %q, want the report once", got)
		}
		if nudged := readInbox(t, cli, "worker", 0); len(nudged.Items) != 0 {
			t.Errorf("the reporting worker was nudged %q about its own report", inboxContents(nudged.Items))
		}

		w.advance(ticketNudgeBundleWindow)
		reportTicket(t, cli, "worker", "chiefs", protocol.DispatchWorkStateReadyForReview, "ready now")
		ticketNudgeAwaitDeadline(t, w, app, "chief-a", time.Now().Add(ticketNudgeCountdown))
		setChiefOfStaff(app, "chief-b", true)
		ticketNudgeAwaitDeadline(t, w, app, "chief-b", time.Now().Add(ticketNudgeCountdown))
		if latest := ticketLatestSession(t, app, "chief-a"); latest.NudgeFiresAt != nil || protocol.Deref(latest.TicketUnread) {
			t.Errorf("the former chief still shows unread %v with a countdown to %s", protocol.Deref(latest.TicketUnread), protocol.Deref(latest.NudgeFiresAt))
		}
		w.advance(ticketNudgeCountdown)
		ticketNudgeAwaitDelivered(t, w, app, cli, "chief-b")
		if got := inboxLines(t, cli, "chief-b"); !slices.Equal(got, []string{"chiefs status_changed ready now"}) {
			t.Errorf("the new chief read %q, want only the report the former chief left unread", got)
		}
		if nudged := readInbox(t, cli, "chief-a", 0); len(nudged.Items) != 0 {
			t.Errorf("the former chief was nudged %q", inboxContents(nudged.Items))
		}
		if got := inboxLines(t, cli, "chief-a"); len(got) != 0 {
			t.Errorf("the former chief read %q, want nothing of the role's ticket", got)
		}
	})
}

func TestAChiefSubscribedToItsRolesTicketReadsEachEventOnce(t *testing.T) {
	chiefTicketUpgrade(t, func(t *testing.T, w *world) {
		app, cli := w.App(), w.Client()
		ticketNudgeReadySessions(t, w, cli, "chief-a", "worker")
		setChiefOfStaff(app, "chief-a", true)
		if _, err := cli.SubscribeTicket("chief-a", "chiefs"); err != nil {
			t.Fatal(err)
		}
		inboxLines(t, cli, "chief-a")

		reportTicket(t, cli, "worker", "chiefs", protocol.DispatchWorkStateReadyForReview, "ready")
		if got := inboxLines(t, cli, "chief-a"); !slices.Equal(got, []string{"chiefs status_changed ready"}) {
			t.Errorf("the chief read %q, want the report once", got)
		}
		if again := inboxLines(t, cli, "chief-a"); len(again) != 0 {
			t.Errorf("a second read returned %q", again)
		}
	})
}

func chiefTicketUpgrade(t *testing.T, script func(t *testing.T, w *world)) {
	t.Helper()
	legacyRecoveryUpgrade(t, func(string) {
		database := filepath.Join(t.TempDir(), "attn.db")
		t.Setenv("ATTN_DB_PATH", database)
		older, err := store.NewWithDB(database)
		if err != nil {
			t.Fatal(err)
		}
		now := time.Date(2026, 1, 10, 0, 0, 0, 0, time.UTC)
		chief := store.TicketRoleIdentity(store.TicketRoleChiefOfStaff)
		if _, err := older.CreateTicketWithSubscribers(store.Ticket{ID: "ordinary", Title: "Ordinary", Description: "Plain delegated task.", Status: store.TicketStatusWorking, Assignee: "worker"},
			"creator", "", []string{"creator", chief}, now); err != nil {
			t.Fatal(err)
		}
		if _, err := older.CreateTicketWithSubscribers(store.Ticket{ID: "chiefs", Title: "Chiefs", Description: "Migrate the store to X.", Status: store.TicketStatusWorking, Assignee: "worker"},
			"chief-a", store.TicketRoleChiefOfStaff, nil, now); err != nil {
			t.Fatal(err)
		}
		if err := older.Close(); err != nil {
			t.Fatal(err)
		}
	}, script)
}

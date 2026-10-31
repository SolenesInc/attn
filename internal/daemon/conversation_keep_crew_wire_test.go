package daemon_test

import (
	"os"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestConversationKeepSurvivesAnUnavailableCrewHome(t *testing.T) {
	w := newCrewWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	home := crewHome(w, "keel")
	if err := os.RemoveAll(home); err != nil {
		t.Fatal(err)
	}
	delegated := seedResumeDelegate(t, w, fakeagent.Claude, "api")
	run := w.Launched(string(delegated.SessionID))
	run.Prompted()
	run.Reply("source answer <!-- attn:state=waiting_input -->")
	closePane(app, sessionPane{session: string(delegated.SessionID)})
	testworld.AwaitTaskDone(app, "conversation_keep")
	keepPassAfterAnotherSessionCloses(t, w)
	rows, err := cli.KeptConversationList(false)
	if err != nil || rows.Count != 1 {
		t.Fatalf("seed-referenced conversation must be kept and listed: %+v, %v", rows, err)
	}
	warnings := 0
	for _, notification := range listNotifications(app).Notifications {
		if strings.Contains(notification.Detail, home) {
			warnings++
			if notification.Severity != protocol.NotificationSeverityWarning || !strings.Contains(notification.Body, "expiry") {
				t.Fatalf("missing-home notification: %+v", notification)
			}
		}
	}
	if warnings != 1 {
		t.Fatalf("warnings naming the unavailable home = %d, want one across keep passes", warnings)
	}
	sessionRecoveryDeleteTranscript(t, run.ConversationID)
	if result := seedResumeRequest(app, delegated.SeedID); !result.Success {
		t.Fatalf("resume kept conversation after harness pruning: %+v", result)
	}
	seedResumeContinues(t, w, run, string(delegated.SessionID))
	_, err = cli.CrewWake("Keel", "", "")
	crewErrorContains(t, err, home, "unavailable")
}

func TestConversationKeepPausesExpiryUntilCrewHomesAreReadable(t *testing.T) {
	w := newCrewWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	delegated := seedResumeDelegate(t, w, fakeagent.Claude, "api")
	run := w.Launched(string(delegated.SessionID))
	run.Prompted()
	run.Reply("keep until released <!-- attn:state=waiting_input -->")
	closePane(app, sessionPane{session: string(delegated.SessionID)})
	testworld.AwaitTaskDone(app, "conversation_keep")
	lifeMove(t, cli, "", delegated.SeedID, "wither", "finished", "")
	testworld.AwaitTaskDone(app, "conversation_keep")
	if kept := lifeShow(t, cli, delegated.SeedID).Seed.Continuation.KeptConversation; kept == nil || kept.DeleteAfter == nil {
		t.Fatalf("released conversation: %+v", kept)
	}
	home := crewHome(w, "keel")
	if err := os.Rename(home, home+"-saved"); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ATTN_CONVERSATION_KEEP_GRACE_DAYS", "0")
	keepPassAfterAnotherSessionCloses(t, w)
	if kept := lifeShow(t, cli, delegated.SeedID).Seed.Continuation.KeptConversation; kept == nil || kept.DeletedAt != nil {
		t.Fatalf("expired copy deleted with unreadable crew references: %+v", kept)
	}
	if err := os.Rename(home+"-saved", home); err != nil {
		t.Fatal(err)
	}
	keepPassAfterAnotherSessionCloses(t, w)
	keepPassAfterAnotherSessionCloses(t, w)
	if kept := lifeShow(t, cli, delegated.SeedID).Seed.Continuation.KeptConversation; kept == nil || kept.DeletedAt == nil {
		t.Fatalf("expiry did not resume after restoring the home: %+v", kept)
	}
}

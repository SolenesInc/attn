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
	for _, retired := range []bool{false, true} {
		name := "active"
		if retired {
			name = "retired"
		}
		t.Run(name, func(t *testing.T) {
			w := newWorld(t, fakeagent.Claude)
			app, cli := w.App(), w.Client()
			created, err := cli.CrewCreate(protocol.CrewCreateMessage{Name: "Keel"})
			if err != nil {
				t.Fatal(err)
			}
			if retired {
				if _, err := cli.CrewRetire("Keel"); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.RemoveAll(created.Member.HomeDir); err != nil {
				t.Fatal(err)
			}
			delegated := seedResumeDelegate(t, w, fakeagent.Claude, "api")
			run := w.Launched(string(delegated.SessionID))
			run.Prompted()
			run.Reply("source answer <!-- attn:state=waiting_input -->")
			closePane(app, sessionPane{session: string(delegated.SessionID)})
			testworld.AwaitTaskDone(app, "conversation_keep")
			rows, err := cli.KeptConversationList(false)
			if err != nil || rows.Count != 1 {
				t.Fatalf("seed-referenced conversation must be kept and listed: %+v, %v", rows, err)
			}
			warnings := 0
			for _, notification := range listNotifications(app).Notifications {
				if strings.Contains(notification.Detail, created.Member.HomeDir) {
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
			if !retired {
				_, err := cli.CrewWake("Keel", "", "")
				crewErrorContains(t, err, "recorded home", "restore the home")
				result := testworld.Request(app, protocol.CrewCharterSetMessage{Cmd: protocol.CmdCrewCharterSet, Member: "Keel", Content: "Updated charter", ExpectedToken: "previous", RequestID: protocol.Ptr("missing-home")},
					protocol.EventCrewCharterSetResult, func(r protocol.CrewCharterSetResultMessage) bool { return r.RequestID == "missing-home" })
				if result.Success || !strings.Contains(protocol.Deref(result.Error), "restore the home") {
					t.Fatalf("editing a member with a missing home must refuse: %+v", result)
				}
			}
		})
	}
}

func TestConversationKeepPausesExpiryUntilCrewHomesAreReadable(t *testing.T) {
	w := newCrewWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	setSetting(t, app, "crew_memory_enabled", "true")
	day := wakeCrew(t, cli, "trellis", "")
	w.Launched(string(day.SessionID))
	closed, err := cli.CrewHandoff(day.SessionID, "Keep this letter.", false, protocol.CrewDayCloseSleep, "Remember this day.")
	if err != nil || protocol.Deref(closed.Outcome) != protocol.CrewDayCloseSleep {
		t.Fatalf("close crew day: %+v, %v", closed, err)
	}
	testworld.AwaitTaskDone(app, "conversation_keep")
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
	rows, err := cli.KeptConversationList(false)
	if err != nil || rows.Count != 2 {
		t.Fatalf("kept conversations while a home is missing: %+v, %v", rows, err)
	}
	healthyDays := 0
	for _, row := range rows.Rows {
		healthyDays += len(row.CrewDays)
	}
	if healthyDays != 1 {
		t.Fatalf("readable crew day references = %d, want 1", healthyDays)
	}
	lifeMove(t, cli, "", delegated.SeedID, "replant", "", "")
	testworld.AwaitTaskDone(app, "conversation_keep")
	if kept := lifeShow(t, cli, delegated.SeedID).Seed.Continuation.KeptConversation; kept == nil || kept.DeleteAfter != nil {
		t.Fatalf("replant must retain a copy with another home missing: %+v", kept)
	}
	lifeMove(t, cli, "", delegated.SeedID, "wither", "finished again", "")
	testworld.AwaitTaskDone(app, "conversation_keep")
	if err := os.Rename(home+"-saved", home); err != nil {
		t.Fatal(err)
	}
	keepPassAfterAnotherSessionCloses(t, w)
	keepPassAfterAnotherSessionCloses(t, w)
	if kept := lifeShow(t, cli, delegated.SeedID).Seed.Continuation.KeptConversation; kept == nil || kept.DeletedAt == nil {
		t.Fatalf("expiry did not resume after restoring the home: %+v", kept)
	}
}

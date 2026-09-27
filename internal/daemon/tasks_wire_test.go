package daemon_test

import (
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestTheTaskListShowsTheNewestUpdatedTaskFirstWithinASecond(t *testing.T) {
	inBubble(t, func(t *testing.T, w *world) {
		app, cli := w.App(), w.Client()
		sessions := []string{"s0", "s1234", "s12345", "s5"}
		registerSessions(t, w, cli, sessions...)
		for _, id := range sessions {
			if err := cli.UpdateState(id, protocol.StateWaitingInput); err != nil {
				t.Fatalf("report %s waiting: %v", id, err)
			}
			testworld.AwaitSession(app, id, func(s protocol.Session) bool { return protocol.Deref(s.TurnOwed) })
		}

		until := time.Now().Add(time.Hour).Format(time.RFC3339Nano)
		for i, step := range []time.Duration{0, 123400 * time.Microsecond, 50 * time.Microsecond, 376550 * time.Microsecond} {
			w.advance(step)
			app.Send(protocol.SnoozeTurnMessage{Cmd: protocol.CmdSnoozeTurn, SessionID: sessions[i], Until: until})
			testworld.AwaitSession(app, sessions[i], func(s protocol.Session) bool { return protocol.Deref(s.TurnSnoozedUntil) != "" })
		}

		requestID := uuid.NewString()
		listed := testworld.Request(app, protocol.TaskListMessage{Cmd: protocol.CmdTaskList, RequestID: protocol.Ptr(requestID)},
			protocol.EventTaskListResult, func(r protocol.TaskListResultMessage) bool { return r.RequestID == requestID })
		var subjects []string
		for _, task := range listed.Tasks {
			if task.Kind == "session_snooze_wake" {
				subjects = append(subjects, task.Subject)
			}
		}
		if want := []string{"s5", "s12345", "s1234", "s0"}; !slices.Equal(subjects, want) {
			t.Errorf("snooze wake tasks = %v, want the newest-updated first", subjects)
		}
	})
}

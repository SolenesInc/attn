package daemon_test

import (
	"slices"
	"testing"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestQueryFiltersByStateAndOrdersByLabelThenID(t *testing.T) {
	inBubble(t, func(t *testing.T, w *world) {
		app := w.App()
		cli := w.Client()
		for _, s := range []struct{ id, label, state string }{
			{"b-id", "dup", protocol.StateWaitingInput},
			{"a-id", "dup", protocol.StateWorking},
			{"c-id", "zzz", protocol.StateWaitingInput},
		} {
			if err := cli.Register(s.id, s.label, w.Path(s.id)); err != nil {
				t.Fatalf("register %s: %v", s.id, err)
			}
			if err := cli.UpdateState(s.id, s.state); err != nil {
				t.Fatalf("report %s for %s: %v", s.state, s.id, err)
			}
			testworld.AwaitSession(app, s.id, func(got protocol.Session) bool { return string(got.State) == s.state })
		}

		if got := queriedIDs(t, cli, ""); !slices.Equal(got, []string{"a-id", "b-id", "c-id"}) {
			t.Errorf("query = %v, want label then id order [a-id b-id c-id]", got)
		}
		if got := queriedIDs(t, cli, protocol.StateWaitingInput); !slices.Equal(got, []string{"b-id", "c-id"}) {
			t.Errorf("query waiting_input = %v, want [b-id c-id]", got)
		}
		if got := queriedIDs(t, cli, protocol.StateWorking); !slices.Equal(got, []string{"a-id"}) {
			t.Errorf("query working = %v, want [a-id]", got)
		}
	})
}

func TestReportedTodosAndARenameReachTheSession(t *testing.T) {
	inBubble(t, func(t *testing.T, w *world) {
		app := w.App()
		cli := w.Client()
		registerSessions(t, w, cli, "s1")
		before := testworld.AwaitSession(app, "s1", func(protocol.Session) bool { return true })

		if err := cli.UpdateTodos("s1", []string{"write the migration", "run the suite"}); err != nil {
			t.Fatalf("report todos: %v", err)
		}
		testworld.AwaitSession(app, "s1", func(s protocol.Session) bool {
			return slices.Equal(s.Todos, []string{"write the migration", "run the suite"})
		})

		if err := cli.RenameSession("s1", "checkout"); err != nil {
			t.Fatalf("rename: %v", err)
		}
		renamed := testworld.AwaitSession(app, "s1", func(s protocol.Session) bool { return s.Label == "checkout" })
		if renamed.Directory != before.Directory || renamed.WorkspaceID != before.WorkspaceID || renamed.Agent != before.Agent {
			t.Errorf("rename changed more than the label: before=%+v after=%+v", before, renamed)
		}
	})
}

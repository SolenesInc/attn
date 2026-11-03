package daemon_test

import (
	"slices"
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestQueryFiltersByStateAndOrdersByLabelThenID(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app := w.App()
	cli := w.Client()
	sessions := []struct{ id, label, state string }{
		{"b-id", "dup", protocol.StateWaitingInput},
		{"a-id", "dup", protocol.StateWorking},
		{"c-id", "zzz", protocol.StateWaitingInput},
	}
	for _, s := range sessions {
		w.Spawn(app, fakeagent.Claude, w.Path("shop"), func(m *protocol.SpawnSessionMessage) {
			m.ID = protocol.SessionID(s.id)
			m.Label = protocol.Ptr(s.label)
		})
	}
	for _, s := range sessions {
		run := w.Launched(s.id)
		app.TypeLine(s.id, "tune the query")
		run.Prompted()
		if s.state == protocol.StateWaitingInput {
			run.Reply("Which index? <!-- attn:state=waiting_input -->")
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
}

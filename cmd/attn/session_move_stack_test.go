package main_test

import (
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestSessionMoveTakesTheCallingSessionToTheNamedDesktop(t *testing.T) {
	t.Parallel()
	s := testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude))
	s.Start()
	app := s.App()
	notes := s.Path("notes")
	if err := os.MkdirAll(notes, 0o755); err != nil {
		t.Fatal(err)
	}
	source := s.Spawn(app, fakeagent.Claude, notes)
	s.Launched(source)
	id := uuid.NewString()
	created := testworld.Request(app, protocol.DesktopCreateMessage{Cmd: protocol.CmdDesktopCreate, RequestID: id, ProfileID: app.SelectedProfile(), Name: protocol.Ptr("Ops")},
		protocol.EventProfileActionResult, func(r protocol.ProfileActionResultMessage) bool { return r.RequestID == id })
	if !created.Success {
		t.Fatalf("creating the Ops desktop: %s", protocol.Deref(created.Error))
	}
	ops := created.Desktops[0].ID

	moved := s.Run(testworld.Invocation{Session: source, Args: []string{"session", "move", "ops", "--json"}})
	if moved.Code != 0 {
		t.Fatalf("attn session move ops exited %d: %s", moved.Code, moved.Stderr)
	}
	var result protocol.DesktopMoveSessionResult
	moved.JSON(t, &result)
	if result.SessionID != source || result.DesktopID != ops {
		t.Errorf("session move printed %+v; want %s on Ops %s", result, source, ops)
	}

	for _, tc := range []struct {
		args []string
		code int
		want string
	}{
		{args: []string{"session", "move", "nope"}, code: 1, want: `unknown desktop "nope"`},
		{args: []string{"session", "move"}, code: 2, want: "exactly one desktop is required"},
	} {
		got := s.Run(testworld.Invocation{Session: source, Args: tc.args})
		if got.Code != tc.code || !strings.Contains(got.Stderr, tc.want) {
			t.Errorf("%v exited %d with stderr %q; want %d and %q", tc.args, got.Code, got.Stderr, tc.code, tc.want)
		}
	}
}

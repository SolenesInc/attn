package main_test

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestAnOutpostSessionsSeedCommandsRunOnTheHomeAsThatSession(t *testing.T) {
	home, outpost := testworld.NewStack(t, testworld.WithoutAgentKit()), testworld.NewStack(t)
	for _, stack := range []*testworld.Stack{home, outpost} {
		stack.Vars = append(stack.Vars, "SHELL=/bin/bash")
	}
	pair := testworld.PrepareOutpostPair(t, home, outpost)
	pair.Start()
	seed := plant(t, home, "Work done on the outpost")
	id := home.Spawn(pair.App, fakeagent.Harness(protocol.SessionAgentShell), outpost.Path("shell"), func(m *protocol.SpawnSessionMessage) { m.Host = &pair.Host })
	terminal := pair.App.Terminal(id)
	onOutpost := func(args ...string) testworld.Result {
		return outpost.Run(testworld.Invocation{Args: append([]string{"seed"}, args...), Terminal: terminal})
	}

	requireStdout(t, onOutpost("note", seed.ID, "-m", "Noted from the outpost"), "noted on "+seed.ID)
	shown := showSeed(t, home, "", seed.ID)
	if len(shown.Notes) == 0 || shown.Notes[0].Body != "Noted from the outpost" || stackAuthorSession(shown.Notes[0]) != protocol.SessionID(id) {
		t.Fatalf("the home's newest note is not the outpost session's: %+v", shown.Notes)
	}
	requireStdout(t, onOutpost("show", seed.ID), "Noted from the outpost")

	home.Stop()
	query, err := json.Marshal(protocol.QueryMessage{Cmd: protocol.CmdQuery})
	if err != nil {
		t.Fatal(err)
	}
	run, err := json.Marshal(protocol.RunCommandMessage{Cmd: protocol.CmdRunCommand, Command: "seed show", Args: protocol.RecordUnknown{"seed": seed.ID}})
	if err != nil {
		t.Fatal(err)
	}
	name, err := os.Hostname()
	if err != nil {
		t.Fatal(err)
	}
	forwardFailed := func(text string) bool {
		return text == name+" is unreachable; this needs it now" || text == "lost "+name+" during this call; it may have run"
	}
	socketForward, commandForward := outpostReply(t, outpost, query), outpostReply(t, outpost, run)
	if !forwardFailed(protocol.Deref(socketForward.Error)) || !forwardFailed(protocol.Deref(commandForward.Error)) {
		t.Fatalf("run_command forwards through the call every home command uses, so both fail as Forward fails: query %q, run_command %q", protocol.Deref(socketForward.Error), protocol.Deref(commandForward.Error))
	}
	for _, verb := range []string{"note", "show"} {
		args := []string{verb, seed.ID}
		if verb == "note" {
			args = append(args, "-m", "Lost on the way")
		}
		offline := onOutpost(args...)
		prefix := "seed " + verb + ": daemon error: "
		if offline.Code != 1 || offline.Stdout != "" || !strings.HasPrefix(offline.Stderr, prefix) || !forwardFailed(strings.TrimSuffix(strings.TrimPrefix(offline.Stderr, prefix), "\n")) {
			t.Errorf("seed %s with the home down: %+v, want exit 1 and %q with Forward's failure", verb, offline, prefix)
		}
	}
	t.Logf("with the home down: query %q, run_command %q", protocol.Deref(socketForward.Error), protocol.Deref(commandForward.Error))
}

func outpostReply(t *testing.T, outpost *testworld.Stack, frame []byte) protocol.Response {
	t.Helper()
	conn, err := outpost.DialUnix()
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := conn.SetDeadline(time.Now().Add(fakeagent.HangGuard)); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Write(append(frame, '\n')); err != nil {
		t.Fatal(err)
	}
	var reply protocol.Response
	if err := json.NewDecoder(conn).Decode(&reply); err != nil {
		t.Fatal(err)
	}
	return reply
}

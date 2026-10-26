package main_test

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestTheAgentWrapperForwardsEverySignalUnchanged(t *testing.T) {
	for _, backend := range []string{"worker", "shared host"} {
		t.Run(backend, func(t *testing.T) {
			s := testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude))
			executable := filepath.Join(s.Dir, "signal-agent")
			if err := os.WriteFile(executable, []byte(`#!/bin/sh
trap 'printf "forwarded-TERM\n"' TERM
trap 'printf "forwarded-HUP\n"; exit 7' HUP
printf 'signal-ready\n'
while :; do read hold; done
`), 0o700); err != nil {
				t.Fatal(err)
			}
			if backend == "shared host" {
				host := os.Getenv("ATTN_TEST_PTY_HOST")
				if host == "" {
					t.Skip("set ATTN_TEST_PTY_HOST to verify wrapper signals on the shared host")
				}
				s.Vars = append(s.Vars, "ATTN_PTY_BACKEND=migrating", "ATTN_PTY_HOST_BINARY="+host)
			}
			s.Start()
			app := s.App()
			if backend == "shared host" {
				enableSharedHost(t, app)
			}
			id := s.Spawn(app, fakeagent.Claude, s.Path("signals"), func(m *protocol.SpawnSessionMessage) { m.Executable = protocol.Ptr(executable) })
			app.AwaitScreen(id, "signal-ready")
			terminal := protocol.TerminalID(app.Terminal(id))
			s.SignalAgentWrapper(id, syscall.SIGTERM)
			app.AwaitScreen(id, "forwarded-TERM")
			s.SignalAgentWrapper(id, syscall.SIGHUP)
			app.AwaitScreen(id, "forwarded-HUP")
			testworld.Await(app, protocol.EventSessionExited, func(event protocol.SessionExitedMessage) bool {
				return string(event.SessionID) == id && event.ExitCode == 7
			})
			final := testworld.Request(app, protocol.AttachSessionMessage{Cmd: protocol.CmdAttachSession, ID: terminal, AttachPolicy: protocol.Ptr(protocol.AttachPolicySameAppRemount)}, protocol.EventAttachResult, func(result protocol.AttachResultMessage) bool { return result.ID == terminal })
			if !final.Success || final.Exit == nil || final.Exit.Code != 7 || final.Screen == nil || !strings.Contains(final.Screen.Text, "forwarded-HUP") {
				t.Fatalf("final output after exit: %+v", final)
			}
			t.Log("the wrapper forwarded TERM, then HUP, with both signal identities preserved")
		})
	}
}

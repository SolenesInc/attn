package main_test

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/ptyworker"
	"github.com/victorarias/attn/internal/testworld"
)

func TestTheAgentWrapperForwardsEverySignalUnchanged(t *testing.T) {
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
	s.Start()
	app := s.App()
	id := s.Spawn(app, fakeagent.Claude, s.Path("signals"), func(m *protocol.SpawnSessionMessage) { m.Executable = protocol.Ptr(executable) })
	app.AwaitScreen(id, "signal-ready")
	paths, err := filepath.Glob(filepath.Join(s.Dir, "workers", "*", "registry", app.Terminal(id)+".json"))
	if err != nil || len(paths) != 1 {
		t.Fatalf("terminal registry: %v, %v", paths, err)
	}
	entry, err := ptyworker.ReadRegistry(paths[0])
	if err != nil {
		t.Fatal(err)
	}
	if err := syscall.Kill(entry.ChildPID, syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	app.AwaitScreen(id, "forwarded-TERM")
	if err := syscall.Kill(entry.ChildPID, syscall.SIGHUP); err != nil {
		t.Fatal(err)
	}
	app.AwaitScreen(id, "forwarded-HUP")
	t.Log("the wrapper forwarded TERM, then HUP, with both signal identities preserved")
}

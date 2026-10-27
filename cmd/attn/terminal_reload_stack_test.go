package main_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/pausepoint"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestTerminalRunWaitsForTheFirstPromptAfterReload(t *testing.T) {
	for _, placement := range []string{"local", "outpost"} {
		t.Run(placement, func(t *testing.T) {
			home := testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude))
			machine := home
			if placement == "outpost" {
				machine = testworld.NewStack(t)
			}
			waiting := machine.PauseEvery(pausepoint.PtyWaitBlock)
			gate, release, ready := machine.Path("hold"), machine.Path("release"), machine.Path("ready")
			shell := machine.Path("bin", "bash")
			if err := os.MkdirAll(filepath.Dir(shell), 0o700); err != nil {
				t.Fatal(err)
			}
			for _, fifo := range []string{release, ready} {
				if err := syscall.Mkfifo(fifo, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			unblock, err := os.OpenFile(release, os.O_RDWR, 0o600)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _, _ = unblock.WriteString("go\n"); _ = unblock.Close() })
			script := fmt.Sprintf("#!/bin/sh\nif [ \"$#\" = 1 ] && [ \"$1\" = -l ] && [ -f %q ]; then printf ready >%q; read hold <%q; fi\nexec /bin/bash \"$@\"\n", gate, ready, release)
			if err := os.WriteFile(shell, []byte(script), 0o700); err != nil {
				t.Fatal(err)
			}
			machine.Vars = append(machine.Vars, "SHELL="+shell)
			var host string
			var app *testworld.Peer
			if placement == "outpost" {
				agent := testworld.NewSSHAgent(t)
				for _, stack := range []*testworld.Stack{machine, home} {
					agent.Authorize(stack.World)
					stack.Start()
				}
				address := bridgeAddress(t)
				requireStdout(t, machine.Attn("settings", "set", "outpost_listen", address), address)
				app = enableOutposts(t, home)
				app.Send(protocol.AddOutpostMessage{Cmd: protocol.CmdAddOutpost, Name: "outpost", Address: address})
				host = outpostState(app, "outpost", protocol.OutpostStateConnected).ID
			} else {
				home.Start()
				app = home.App()
			}
			caller := home.Spawn(app, fakeagent.Claude, home.Path("caller"))
			home.Launched(caller)
			id := home.Spawn(app, fakeagent.Harness(protocol.SessionAgentShell), machine.Path("shell"), func(m *protocol.SpawnSessionMessage) {
				if host != "" {
					m.Host = &host
				}
			})
			app.TypeLine(id, "printf 'initial-%s\\n' ready")
			app.AwaitScreen(id, "initial-ready")
			if err := os.WriteFile(gate, nil, 0o600); err != nil {
				t.Fatal(err)
			}
			result := testworld.Request(app, protocol.ReloadSessionMessage{Cmd: protocol.CmdReloadSession, ID: protocol.SessionID(id), Cols: 80, Rows: 24}, protocol.EventReloadSessionResult, func(r protocol.ReloadSessionResultMessage) bool { return string(r.ID) == id })
			if !result.Success {
				t.Fatalf("reload: %s", protocol.Deref(result.Error))
			}
			held := openHeldPipe(t, ready)
			readyText := make([]byte, len("ready"))
			if _, err := held.Read(readyText); err != nil {
				t.Fatal(err)
			}
			_ = held.Close()
			run := home.Launch(testworld.Invocation{Session: caller, Args: []string{"terminal", "run", id, "printf 'after-%s\\n' reload; (exit 7)", "--wait"}})
			waiting.Await()
			waiting.Release()
			if _, err := unblock.WriteString("go\n"); err != nil {
				t.Fatal(err)
			}
			waiting.Await()
			waiting.Release()
			if got := run.Wait(); got.Code != 7 || strings.TrimSpace(got.Stdout) != "after-reload" {
				t.Fatalf("run after reload: %+v", got)
			}
			t.Log("reload returned before the first prompt; run --wait waited and returned output with exit code 7")
		})
	}
}

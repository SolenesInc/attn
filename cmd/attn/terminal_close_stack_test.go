package main_test

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestTerminalCloseStopsEveryShellPromptly(t *testing.T) {
	for _, name := range []string{"bash", "zsh", "fish"} {
		for _, placement := range []string{"local", "outpost"} {
			t.Run(name+"/"+placement, func(t *testing.T) {
				realShell, err := exec.LookPath(name)
				if err != nil {
					t.Skipf("%s unavailable: %v", name, err)
				}
				home := testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude))
				machine := home
				if placement == "outpost" {
					machine = testworld.NewStack(t)
				}
				shell, fifo := machine.Path("bin", name), machine.Path("shell-held")
				if err := os.MkdirAll(filepath.Dir(shell), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := syscall.Mkfifo(fifo, 0o600); err != nil {
					t.Fatal(err)
				}
				script := fmt.Sprintf("#!/bin/sh\nif [ \"$1\" = -l ] && { [ \"$#\" = 1 ] || [ \"$2\" = -C ]; }; then exec 3>%q; trap '' HUP; fi\nexec %q \"$@\"\n", fifo, realShell)
				if err := os.WriteFile(shell, []byte(script), 0o700); err != nil {
					t.Fatal(err)
				}
				machine.Vars = append(machine.Vars, "SHELL="+shell)
				var host string
				var app *testworld.Peer
				if placement == "outpost" {
					a := testworld.NewSSH(t)
					for _, stack := range []*testworld.Stack{machine, home} {
						a.Use(stack.World)
						stack.Start()
					}
					address := bridgeAddress(t)
					a.Route(address, machine)
					app = enableOutposts(t, home)
					app.Send(protocol.AddOutpostMessage{Cmd: protocol.CmdAddOutpost, Name: "outpost", Destination: address})
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
				held := openHeldPipe(t, fifo)
				app.TypeLine(id, "trap '' HUP TERM; printf 'close-%s\\n' ready")
				app.AwaitScreen(id, "close-ready")
				got := home.Run(testworld.Invocation{Session: caller, Args: []string{"terminal", "close", id}})
				if got.Code != 0 {
					t.Fatalf("close: %+v", got)
				}
				machine.AwaitTimer(shellCloseGrace)
				machine.Advance(shellCloseGrace)
				awaitPipeReleased(t, held, name)
			})
		}
	}
}

const shellCloseGrace = 500 * time.Millisecond

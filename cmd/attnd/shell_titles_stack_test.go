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

func TestShellTitlesFollowProgramsFoldersNamesAndDaemonRestart(t *testing.T) {
	t.Parallel()
	for _, backend := range testworld.WorkerAndSharedHost {
		t.Run(string(backend), func(t *testing.T) {
			t.Parallel()
			s := testworld.NewStack(t, testworld.OnPTYBackend(backend))
			if err := os.MkdirAll(s.Path("zsh-config"), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(s.Path("warehouse"), 0700); err != nil {
				t.Fatal(err)
			}
			s.Vars = append(s.Vars, "SHELL=/bin/zsh", "ZDOTDIR="+s.Path("zsh-config"))
			s.Start()
			app := s.App()
			shell := s.Spawn(app, fakeagent.Harness(protocol.SessionAgentShell), s.Path("shop"))
			await := func(title string) protocol.Session {
				return testworld.AwaitSession(app, shell, func(x protocol.Session) bool { return protocol.Deref(x.Title) == title })
			}
			first := await("shop")
			if first.Label != "" {
				t.Fatalf("new shell name=%q, want unnamed", first.Label)
			}
			release := filepath.Join(s.Dir, "release")
			if err := syscall.Mkfifo(release, 0600); err != nil {
				t.Fatal(err)
			}
			app.TypeLine(shell, `sh -c 'printf "\033]0;building the shop\007"; cat `+release+`'`)
			await("building the shop")
			s.Stop()
			s.Start()
			app = s.App()
			recovered := initialSession(t, app, shell)
			if protocol.Deref(recovered.Title) != "building the shop" {
				t.Fatalf("restarted title=%q", protocol.Deref(recovered.Title))
			}
			if got := s.Attn("session", "rename", "my server", "--session", shell); got.Code != 0 {
				t.Fatal(got.Stderr)
			}
			testworld.AwaitSession(app, shell, func(x protocol.Session) bool {
				return x.Label == "my server" && protocol.Deref(x.Title) == "building the shop"
			})
			if got := s.Attn("session", "rename", "", "--session", shell); got.Code != 0 {
				t.Fatal(got.Stderr)
			}
			testworld.AwaitSession(app, shell, func(x protocol.Session) bool { return x.Label == "" && protocol.Deref(x.Title) == "building the shop" })
			if err := os.WriteFile(release, []byte("done\n"), 0600); err != nil {
				t.Fatal(err)
			}
			await("shop")
			app.TypeLine(shell, "cd "+s.Path("warehouse"))
			await("warehouse")
		})
	}
}

func TestUnmarkedShellDropsAProgramsTitleWhenItsForegroundGroupReturns(t *testing.T) {
	t.Parallel()
	for _, backend := range testworld.WorkerAndSharedHost {
		t.Run(string(backend), func(t *testing.T) {
			t.Parallel()
			s := testworld.NewStack(t, testworld.OnPTYBackend(backend))
			s.Vars = append(s.Vars, "SHELL=/bin/sh")
			s.Start()
			app := s.App()
			shell := s.Spawn(app, fakeagent.Harness(protocol.SessionAgentShell), s.Path("shop"))
			testworld.AwaitSession(app, shell, func(x protocol.Session) bool { return protocol.Deref(x.Title) == "shop" })
			release := filepath.Join(s.Dir, "release")
			if err := syscall.Mkfifo(release, 0600); err != nil {
				t.Fatal(err)
			}
			app.TypeLine(shell, `sh -c 'printf "\033]0;unmarked program\007"; cat `+release+`'`)
			testworld.AwaitSession(app, shell, func(x protocol.Session) bool { return protocol.Deref(x.Title) == "unmarked program" })
			if err := os.WriteFile(release, []byte("done\n"), 0600); err != nil {
				t.Fatal(err)
			}
			testworld.AwaitSession(app, shell, func(x protocol.Session) bool { return protocol.Deref(x.Title) == "shop" })
		})
	}
}

func TestShellProgramTitleInsideHeavyOutputIsKept(t *testing.T) {
	t.Parallel()
	for _, backend := range testworld.WorkerAndSharedHost {
		t.Run(string(backend), func(t *testing.T) {
			t.Parallel()
			s := testworld.NewStack(t, testworld.OnPTYBackend(backend))
			s.Vars = append(s.Vars, "SHELL=/bin/sh")
			s.Start()
			app := s.App()
			shell := s.Spawn(app, fakeagent.Harness(protocol.SessionAgentShell), s.Path("shop"))
			testworld.AwaitSession(app, shell, func(x protocol.Session) bool { return protocol.Deref(x.Title) == "shop" })
			line := strings.Repeat("x", 1023) + "\n"
			heavy := strings.Repeat(line, 256)
			output := filepath.Join(s.Dir, "output")
			if err := os.WriteFile(output, []byte(heavy+"\x1b]0;title amid output\x07"+heavy+"output-done\n"), 0600); err != nil {
				t.Fatal(err)
			}
			release := filepath.Join(s.Dir, "release")
			if err := syscall.Mkfifo(release, 0600); err != nil {
				t.Fatal(err)
			}
			app.TypeLine(shell, `sh -c 'cat `+output+`; cat `+release+`'`)
			app.AwaitScreen(shell, "output-done")
			testworld.AwaitSession(app, shell, func(x protocol.Session) bool { return protocol.Deref(x.Title) == "title amid output" })
			if err := os.WriteFile(release, []byte("done\n"), 0600); err != nil {
				t.Fatal(err)
			}
			testworld.AwaitSession(app, shell, func(x protocol.Session) bool { return protocol.Deref(x.Title) == "shop" })
		})
	}
}

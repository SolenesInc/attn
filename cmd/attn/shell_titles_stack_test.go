package main_test

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestShellTitlesFollowProgramsFoldersNamesAndDaemonRestart(t *testing.T) {
	t.Parallel()
	for _, shared := range []bool{false, true} {
		name := "worker"
		if shared {
			name = "shared host"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			s := testworld.NewStack(t)
			if err := os.MkdirAll(s.Path("zsh-config"), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(s.Path("warehouse"), 0700); err != nil {
				t.Fatal(err)
			}
			s.Vars = append(s.Vars, "SHELL=/bin/zsh", "ZDOTDIR="+s.Path("zsh-config"))
			if shared {
				host := os.Getenv("ATTN_TEST_PTY_HOST")
				if host == "" {
					t.Skip("set ATTN_TEST_PTY_HOST to run shared host titles")
				}
				s.Vars = append(s.Vars, "ATTN_PTY_BACKEND=migrating", "ATTN_PTY_HOST_BINARY="+host)
			}
			s.Start()
			if shared {
				enableSharedHost(t, s.App())
			}
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
	for _, shared := range []bool{false, true} {
		name := "worker"
		if shared {
			name = "shared host"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			s := testworld.NewStack(t)
			s.Vars = append(s.Vars, "SHELL=/bin/sh")
			if shared {
				host := os.Getenv("ATTN_TEST_PTY_HOST")
				if host == "" {
					t.Skip("set ATTN_TEST_PTY_HOST to run shared host titles")
				}
				s.Vars = append(s.Vars, "ATTN_PTY_BACKEND=migrating", "ATTN_PTY_HOST_BINARY="+host)
			}
			s.Start()
			if shared {
				enableSharedHost(t, s.App())
			}
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

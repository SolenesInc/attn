package main_test

import (
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestAgentsStartAsTheirTerminalRuntimesChildOnBothPTYBackends(t *testing.T) {
	for _, backend := range testworld.WorkerAndSharedHost {
		t.Run(string(backend), func(t *testing.T) {
			s := testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude, fakeagent.Codex), testworld.OnPTYBackend(backend))
			s.Start()
			app := s.App()
			for _, h := range []fakeagent.Harness{fakeagent.Claude, fakeagent.Codex} {
				session := s.Spawn(app, h, s.Path(string(h)), func(m *protocol.SpawnSessionMessage) {
					m.InitialPrompt = protocol.Ptr("literal 'quote' \\\\path\n$HOME and λ")
				})
				run := s.Launched(session)
				if got := run.Prompted(); got != "literal 'quote' \\\\path\n$HOME and λ" {
					t.Fatalf("%s received prompt %q", h, got)
				}
				pid := strconv.Itoa(run.PID)
				out, err := exec.Command("ps", "-p", pid, "-o", "ppid=").Output()
				if err != nil {
					t.Fatalf("read %s's parent: %v", h, err)
				}
				parent := strings.TrimSpace(string(out))
				out, err = exec.Command("ps", "-p", parent, "-o", "args=").Output()
				if err != nil {
					t.Fatalf("read parent %s: %v", parent, err)
				}
				want := " pty-worker "
				if backend == testworld.SharedHostPTY {
					want = "attn-pty-host"
				}
				if !strings.Contains(string(out), want) {
					t.Errorf("%s's parent is %s, want %s", h, out, want)
					for read := range 3 {
						states, err := exec.Command("ps", "-o", "pid,ppid,stat,etime,comm", "-p", parent+","+pid).CombinedOutput()
						t.Logf("process states, read %d (err %v):\n%s", read+1, err, states)
					}
					t.Logf("%s terminal:\n%s", h, s.TerminalEvidence(session))
					t.FailNow()
				}
				receipt, err := exec.Command("ps", "-p", fmt.Sprintf("%s,%s", pid, parent), "-o", "pid,ppid,rss,args").Output()
				if err != nil {
					t.Fatal(err)
				}
				receipt = regexp.MustCompile(`(--control-token )\S+`).ReplaceAll(receipt, []byte("${1}<redacted>"))
				t.Logf("%s %s process receipt:\n%s", backend, h, receipt)
			}

		})
	}
}

package daemon_test

import (
	"fmt"
	"os"
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/ptyworker"
	"github.com/victorarias/attn/internal/testworld"
)

func TestAnAutomationAgentGetsItsLaunchContractOnEveryPtyBackend(t *testing.T) {
	for _, backend := range []string{"embedded", "worker"} {
		t.Run(backend, func(t *testing.T) {
			if backend == "worker" {
				t.Setenv("ATTN_PTY_BACKEND", "worker")
				t.Setenv("ATTN_PTY_WORKER_BINARY", testworld.AttnBinary(t))
			}
			w := newWorld(t, fakeagent.Claude)
			if backend == "worker" {
				t.Cleanup(func() { ptyworker.ReapDataDir(w.Dir) })
			}
			app, cli := w.App(), w.Client()
			if err := os.MkdirAll(w.Path("check"), 0o755); err != nil {
				t.Fatal(err)
			}
			applyAutomation(t, cli, fmt.Sprintf(`api_version: attn.dev/automations/v1alpha1
name: Nightly check
trigger: {type: manual}
prompt: Report.
launch: {driver: claude, model: sonnet, effort: high}
location: {type: directory, path: %q}
`, w.Path("check")))
			awaitAutomationChanged(app, 1)
			run := testworld.Request(app, protocol.AutomationRunMessage{Cmd: protocol.CmdAutomationRun, DefinitionID: 1, RequestID: "now"},
				protocol.EventAutomationRunResult, automationAnswer[protocol.AutomationRunResultMessage]("now"))
			if !run.Success || protocol.Deref(run.Run.SessionID) == "" {
				t.Fatalf("automation_run = %+v, want a delivered run with a session", run)
			}
			agent := w.Launched(protocol.Deref(run.Run.SessionID))
			for _, flag := range [][]string{{"--model", "sonnet"}, {"--effort", "high"}, {"--permission-mode", "auto"}} {
				if !containsAutomationFlag(agent.Argv, flag[0], flag[1]) {
					t.Errorf("the agent was launched with %q, want %s %s", agent.Argv, flag[0], flag[1])
				}
			}
		})
	}
}

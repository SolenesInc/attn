package daemon_test

import (
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/ptyworker"
	"github.com/victorarias/attn/internal/testworld"
)

func TestAShellIsWorkingOnlyWhileACommandRunsWhateverItsWorkerClaims(t *testing.T) {
	t.Setenv("ATTN_PTY_BACKEND", "worker")
	t.Setenv("ATTN_PTY_WORKER_BINARY", testworld.AttnBinary(t))
	w := newWorld(t)
	t.Cleanup(func() { ptyworker.ReapDataDir(w.Dir) })
	app, cli := w.App(), w.Client()
	shell := w.Spawn(app, workspaceShell, w.Path("shop"))
	testworld.AwaitSession(app, shell, func(s protocol.Session) bool { return s.State == protocol.SessionStateIdle })
	attached := testworld.Request(app, protocol.AttachSessionMessage{Cmd: protocol.CmdAttachSession, ID: shell},
		protocol.EventAttachResult, func(r protocol.AttachResultMessage) bool { return r.ID == shell })
	if !attached.Success {
		t.Fatalf("attach %s refused: %s", shell, protocol.Deref(attached.Error))
	}
	beforeCommand := len(sessionUpdatesOf(app, shell))

	app.TypeLine(shell, "cat")
	testworld.AwaitSession(app, shell, func(s protocol.Session) bool { return s.State == protocol.SessionStateWorking })
	app.Send(protocol.PtyInputMessage{Cmd: protocol.CmdPtyInput, ID: shell, Data: "\x04"})
	testworld.AwaitSession(app, shell, func(s protocol.Session) bool {
		return s.State == protocol.SessionStateIdle && protocol.Deref(s.StateReason) == "at_prompt"
	})

	updates := sessionUpdatesOf(app, shell)
	for _, update := range updates[:beforeCommand] {
		if update.State != protocol.SessionStateIdle {
			t.Errorf("the shell left idle before any command ran:%s", describeUpdates(updates))
			break
		}
	}
	var stretches []protocol.SessionState
	for _, update := range updates {
		if protocol.Deref(update.TurnOwed) {
			t.Errorf("the shell owed the user a turn:%s", describeUpdates(updates))
			break
		}
		if update.State == protocol.SessionStateWorking && !strings.HasPrefix(protocol.Deref(update.StateReason), "heartbeat_busy") {
			t.Errorf("the shell worked for a reason other than its foreground command:%s", describeUpdates(updates))
		}
		if len(stretches) == 0 || stretches[len(stretches)-1] != update.State {
			stretches = append(stretches, update.State)
		}
	}
	if len(stretches) < 2 || stretches[len(stretches)-2] != protocol.SessionStateWorking || stretches[len(stretches)-1] != protocol.SessionStateIdle ||
		countShellStateStretches(stretches, protocol.SessionStateWorking) != 1 {
		t.Errorf("the shell went through %v, want one working stretch while cat ran, idle otherwise:%s", stretches, describeUpdates(updates))
	}

	observations := stateExplainOf(t, cli, shell).Observations
	workerClaims := 0
	for _, obs := range observations {
		if obs.Source != "worker_info" || obs.Claim != string(protocol.StateWorking) {
			continue
		}
		workerClaims++
		if obs.Outcome != "vetoed" || protocol.Deref(obs.Reason) != "resolver_owned" {
			t.Errorf("the worker's working claim was %s (%s), want vetoed as resolver_owned:\n%s", obs.Outcome, protocol.Deref(obs.Reason), describeStateExplain(observations))
		}
	}
	if workerClaims == 0 {
		t.Errorf("the worker never claimed the shell was working:\n%s", describeStateExplain(observations))
	}
	exitWorkspaceShells(app, shell)
}

func countShellStateStretches(stretches []protocol.SessionState, state protocol.SessionState) int {
	count := 0
	for _, s := range stretches {
		if s == state {
			count++
		}
	}
	return count
}

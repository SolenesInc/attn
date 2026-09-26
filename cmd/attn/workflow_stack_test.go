package main_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

type workflowResult struct {
	Status       string          `json:"status"`
	Result       json.RawMessage `json:"result"`
	Error        string          `json:"error"`
	CallsTotal   *int            `json:"calls_total"`
	CallsDone    *int            `json:"calls_done"`
	CallsRunning *int            `json:"calls_running"`
}

func writeWorkflowScript(t *testing.T, s *testworld.Stack, name, body string) string {
	t.Helper()
	path := s.Path("scripts", name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func awaitWorkflowRun(app *testworld.Peer, match func(protocol.WorkflowRun) bool) protocol.WorkflowRun {
	app.T.Helper()
	return testworld.Await(app, protocol.EventWorkflowRunUpdated, func(m protocol.WorkflowRunUpdatedMessage) bool {
		return match(m.Run)
	}).Run
}

func finishedWorkflow(t *testing.T, r testworld.Result) workflowResult {
	t.Helper()
	var out workflowResult
	r.JSON(t, &out)
	if out.CallsTotal == nil || out.CallsDone == nil || out.CallsRunning == nil {
		t.Fatalf("workflow result lacks its call counts:\n%s", r.Stdout)
	}
	if len(out.Result) > 0 {
		var compact bytes.Buffer
		if err := json.Compact(&compact, out.Result); err != nil {
			t.Fatal(err)
		}
		out.Result = compact.Bytes()
	}
	return out
}

func TestWorkflowRunRecordsEachRunWithTheDaemonAndReportsHowItEnded(t *testing.T) {
	t.Parallel()
	s := testworld.NewStack(t, testworld.WithAgents(fakeagent.Codex, fakeagent.Claude))
	echo := writeWorkflowScript(t, s, "echo.js", "return args;")
	argsFile := writeWorkflowScript(t, s, "args.json", `{"b":2}`)

	for _, tc := range []struct {
		args   []string
		stderr string
	}{
		{[]string{"--wait"}, "missing <script.js> argument"},
		{[]string{echo, "--args", "{}", "--args-file", argsFile}, "--args and --args-file are mutually exclusive"},
		{[]string{echo, "--args", "{not json"}, "args is not valid JSON"},
	} {
		r := s.Attn(append([]string{"workflow", "run"}, tc.args...)...)
		if r.Code != 2 || !strings.Contains(r.Stderr, tc.stderr) {
			t.Errorf("workflow run %q exited %d: %s", tc.args, r.Code, r.Stderr)
		}
	}

	s.Start()
	app := s.App()
	if r := s.Attn("workflow", "run", echo, "--wait"); r.Code != 1 || !strings.Contains(r.Stderr, "workflows are disabled") {
		t.Errorf("a run with workflows disabled exited %d: %s", r.Code, r.Stderr)
	}
	requestID := uuid.NewString()
	enabled := testworld.Request(app, protocol.SetSettingMessage{Cmd: protocol.CmdSetSetting, Key: "workflows_enabled", Value: "true", RequestID: protocol.Ptr(requestID)},
		protocol.EventSettingsUpdated, func(m protocol.SettingsUpdatedMessage) bool { return protocol.Deref(m.RequestID) == requestID })
	if !protocol.Deref(enabled.Success) {
		t.Fatalf("enable workflows: %s", protocol.Deref(enabled.Error))
	}

	inline := s.Run(testworld.Invocation{Args: []string{"workflow", "run", echo, "--wait", "--args", `{"a":1}`}, Session: "sess-env"})
	if out := finishedWorkflow(t, inline); inline.Code != 0 || out.Status != "completed" || string(out.Result) != `{"a":1}` || *out.CallsTotal != 0 {
		t.Errorf("a completed run exited %d and printed:\n%s", inline.Code, inline.Stdout)
	}
	first := awaitWorkflowRun(app, func(r protocol.WorkflowRun) bool {
		return protocol.Deref(r.ArgsJson) == `{"a":1}` && r.Status == protocol.WorkflowRunStatusCompleted
	})
	if protocol.Deref(first.SessionID) != "sess-env" || protocol.Deref(first.Harness) != "codex" || protocol.Deref(first.ResultJson) != `{"a":1}` || first.ScriptPath != echo {
		t.Errorf("the daemon recorded %+v, want the caller's session, the codex harness and the result", first)
	}

	fromFile := s.Run(testworld.Invocation{Args: []string{"workflow", "run", echo, "--wait", "--args-file", argsFile, "--session", "sess-flag", "--harness", "claude"}, Session: "sess-env"})
	if out := finishedWorkflow(t, fromFile); fromFile.Code != 0 || string(out.Result) != `{"b":2}` {
		t.Errorf("a run with --args-file exited %d and printed:\n%s", fromFile.Code, fromFile.Stdout)
	}
	second := awaitWorkflowRun(app, func(r protocol.WorkflowRun) bool {
		return protocol.Deref(r.ArgsJson) == `{"b":2}` && r.Status == protocol.WorkflowRunStatusCompleted
	})
	if protocol.Deref(second.SessionID) != "sess-flag" || protocol.Deref(second.Harness) != "claude" {
		t.Errorf("the daemon recorded session %q and harness %q, want --session and --harness to win", protocol.Deref(second.SessionID), protocol.Deref(second.Harness))
	}

	boom := writeWorkflowScript(t, s, "boom.js", `throw new Error("boom");`)
	failed := s.Run(testworld.Invocation{Args: []string{"workflow", "run", boom, "--wait"}, Session: "sess-env"})
	if out := finishedWorkflow(t, failed); failed.Code != 1 || out.Status != "failed" || !strings.Contains(out.Error, "boom") || strings.Contains(failed.Stdout, `"result"`) {
		t.Errorf("a throwing run exited %d and printed:\n%s", failed.Code, failed.Stdout)
	}
	third := awaitWorkflowRun(app, func(r protocol.WorkflowRun) bool {
		return r.ScriptPath == boom && r.Status == protocol.WorkflowRunStatusFailed
	})

	resumed := s.Run(testworld.Invocation{Args: []string{"workflow", "run", echo, "--wait", "--resume", first.RunID, "--args", `{"a":3}`}, Session: "sess-env"})
	if out := finishedWorkflow(t, resumed); resumed.Code != 0 || string(out.Result) != `{"a":3}` {
		t.Errorf("a resumed run exited %d and printed:\n%s", resumed.Code, resumed.Stdout)
	}
	if again := awaitWorkflowRun(app, func(r protocol.WorkflowRun) bool {
		return protocol.Deref(r.ArgsJson) == `{"a":3}` && r.Status == protocol.WorkflowRunStatusCompleted
	}); again.RunID != first.RunID {
		t.Errorf("--resume %s ran as %s", first.RunID, again.RunID)
	}

	for _, tc := range []struct {
		runID  string
		code   int
		status string
	}{
		{first.RunID, 0, "completed"},
		{third.RunID, 1, "failed"},
	} {
		r := s.Attn("workflow", "result", tc.runID)
		if out := finishedWorkflow(t, r); r.Code != tc.code || out.Status != tc.status {
			t.Errorf("workflow result %s exited %d with status %q, want %d and %q", tc.runID, r.Code, out.Status, tc.code, tc.status)
		}
	}

	var shown struct {
		RunID    string `json:"run_id"`
		Status   string `json:"status"`
		Script   string `json:"script"`
		Progress struct {
			CallsTotal int    `json:"calls_total"`
			Summary    string `json:"summary"`
		} `json:"progress"`
		Calls     []json.RawMessage `json:"calls"`
		Resumable bool              `json:"resumable"`
		Error     string            `json:"error"`
	}
	s.Attn("workflow", "show", third.RunID).JSON(t, &shown)
	if shown.RunID != third.RunID || shown.Status != "failed" || shown.Script != boom || shown.Progress.Summary != "0/0 done" || shown.Calls == nil || !shown.Resumable || !strings.Contains(shown.Error, "boom") {
		t.Errorf("workflow show = %+v", shown)
	}
	if r := s.Attn("workflow", "show", "wf-missing"); r.Code != 1 || !strings.Contains(r.Stderr, `run "wf-missing" not found`) {
		t.Errorf("workflow show of an unknown run exited %d: %s", r.Code, r.Stderr)
	}

	listed := func(inv testworld.Invocation) []string {
		t.Helper()
		var entries []struct {
			RunID     string `json:"run_id"`
			Status    string `json:"status"`
			Script    string `json:"script"`
			CreatedAt string `json:"created_at"`
			Resumable bool   `json:"resumable"`
		}
		s.Run(inv).JSON(t, &entries)
		var got []string
		for _, e := range entries {
			if e.Script == "" || e.CreatedAt == "" || !e.Resumable {
				t.Errorf("workflow list entry %+v lacks its script, creation time or resumability", e)
			}
			got = append(got, e.RunID+"/"+e.Status)
		}
		slices.Sort(got)
		return got
	}
	envRuns := []string{first.RunID + "/completed", third.RunID + "/failed"}
	slices.Sort(envRuns)
	if got := listed(testworld.Invocation{Args: []string{"workflow", "list"}, Session: "sess-env"}); !slices.Equal(got, envRuns) {
		t.Errorf("workflow list in sess-env = %q, want %q", got, envRuns)
	}
	if got := listed(testworld.Invocation{Args: []string{"workflow", "list", "--session", "sess-flag"}, Session: "sess-env"}); !slices.Equal(got, []string{second.RunID + "/completed"}) {
		t.Errorf("workflow list --session sess-flag = %q", got)
	}

	spin := writeWorkflowScript(t, s, "spin.js", "while (true) {}")
	s.Launch(testworld.Invocation{Args: []string{"workflow", "run", spin, "--wait"}})
	spinning := awaitWorkflowRun(app, func(r protocol.WorkflowRun) bool {
		return r.ScriptPath == spin && r.Status == protocol.WorkflowRunStatusRunning
	})
	var canceled protocol.WorkflowRun
	cancel := s.Attn("workflow", "cancel", spinning.RunID)
	cancel.JSON(t, &canceled)
	if cancel.Code != 0 || canceled.Status != protocol.WorkflowRunStatusCanceled {
		t.Errorf("workflow cancel exited %d with %+v", cancel.Code, canceled)
	}
	interrupted := awaitWorkflowRun(app, func(r protocol.WorkflowRun) bool {
		return r.RunID == spinning.RunID && r.LastError != nil
	})
	if interrupted.Status != protocol.WorkflowRunStatusCanceled || !strings.Contains(protocol.Deref(interrupted.LastError), "cancel") {
		t.Errorf("the canceled run ended %s: %s", interrupted.Status, protocol.Deref(interrupted.LastError))
	}
	if r := s.Attn("workflow", "result", spinning.RunID); r.Code != 1 || finishedWorkflow(t, r).Status != "canceled" {
		t.Errorf("workflow result of the canceled run exited %d and printed:\n%s", r.Code, r.Stdout)
	}
}

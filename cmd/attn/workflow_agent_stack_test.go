package main_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestAWorkflowAgentReturnsItsResultThroughTheToolAndIsolatedWorkKeepsOnlyWhatChanged(t *testing.T) {
	t.Parallel()
	s := testworld.NewStack(t, testworld.WithAgents(fakeagent.Codex))
	repo := s.Path("shop")
	gitRepo(t, repo)
	script := writeWorkflowScript(t, s, "agents.js", `
const schema = {type: "object", additionalProperties: false, required: ["answer"], properties: {answer: {type: "string"}}};
const edited = await agent("edit the readme", {schema, isolation: "worktree", model: "gpt-override"});
const looked = await agent("only look", {schema, isolation: "worktree"});
const nudged = await agent("answer after a nudge", {schema});
const failed = await agent("always fail", {schema});
return {edited, looked, nudged, failed};
`)

	var mu sync.Mutex
	tasks := map[string][]fakeagent.HeadlessTask{}
	s.AnswerHeadlessTasks(func(task *fakeagent.HeadlessTask) {
		mu.Lock()
		brief := ""
		for _, b := range []string{"edit the readme", "only look", "answer after a nudge", "always fail"} {
			if strings.Contains(task.Prompt, b) {
				brief = b
			}
		}
		tasks[brief] = append(tasks[brief], *task)
		attempt := len(tasks[brief])
		mu.Unlock()
		switch {
		case brief == "edit the readme":
			if err := os.WriteFile(filepath.Join(task.Dir, "README.md"), []byte("edited\n"), 0o644); err != nil {
				task.Fail(err.Error())
				return
			}
			task.CallTool(`{"answer":"edited"}`)
		case brief == "only look":
			task.CallToolAndFail(`{"answer":"looked"}`, "exited after returning its result")
		case brief == "answer after a nudge" && attempt == 1:
			task.Answer("I think the answer is yes")
		case brief == "answer after a nudge" && attempt == 2:
			task.CallTool(`{"answer":1}`)
		case brief == "answer after a nudge":
			task.CallTool(`{"answer":"after a nudge"}`)
		default:
			task.Fail("the model is unavailable")
		}
	})

	s.Start()
	app := s.App()
	requestID := uuid.NewString()
	testworld.Request(app, protocol.SetSettingMessage{Cmd: protocol.CmdSetSetting, Key: "workflows_enabled", Value: "true", RequestID: protocol.Ptr(requestID)},
		protocol.EventSettingsUpdated, func(m protocol.SettingsUpdatedMessage) bool { return protocol.Deref(m.RequestID) == requestID })

	run := s.Run(testworld.Invocation{Args: []string{"workflow", "run", script, "--wait"}, Dir: repo, Env: []string{"ATTN_HEADLESS_TASKS=on"}})
	out := finishedWorkflow(t, run)
	if want := `{"edited":{"answer":"edited"},"failed":null,"looked":{"answer":"looked"},"nudged":{"answer":"after a nudge"}}`; run.Code != 0 || string(out.Result) != want {
		t.Fatalf("the workflow exited %d with %s, want %s\n%s", run.Code, out.Result, want, run.Stderr)
	}

	mu.Lock()
	defer mu.Unlock()
	edited, looked, nudged := tasks["edit the readme"][0], tasks["only look"][0], tasks["answer after a nudge"]
	if edited.Model != "gpt-override" {
		t.Errorf("the isolated call ran model %q, want its own gpt-override", edited.Model)
	}
	if body, err := os.ReadFile(filepath.Join(edited.Dir, "README.md")); err != nil || string(body) != "edited\n" || !listedWorktree(t, repo, edited.Dir) {
		t.Errorf("the worktree the agent changed at %s is gone or lost its change (%q, %v)", edited.Dir, body, err)
	}
	if body, _ := os.ReadFile(filepath.Join(repo, "README.md")); string(body) != "shop\n" {
		t.Errorf("the isolated edit reached the working tree: %q", body)
	}
	if _, err := os.Stat(looked.Dir); !os.IsNotExist(err) || listedWorktree(t, repo, looked.Dir) || looked.Dir == edited.Dir {
		t.Errorf("the worktree the agent left clean at %s is still there (%v)", looked.Dir, err)
	}
	if len(nudged) != 3 || !sameDir(nudged[0].Dir, repo) {
		t.Errorf("the unisolated call ran %d attempts in %v, want three in the working tree %s", len(nudged), nudged, repo)
	}
	if failed := tasks["always fail"]; len(failed) != 3 {
		t.Errorf("a call that always fails ran %d attempts, want 3", len(failed))
	}
}

func listedWorktree(t *testing.T, repo, dir string) bool {
	t.Helper()
	out, err := exec.Command("git", "-C", repo, "worktree", "list", "--porcelain").Output()
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(out), "\n") {
		if path, ok := strings.CutPrefix(line, "worktree "); ok && sameDir(path, dir) {
			return true
		}
	}
	return false
}

func sameDir(a, b string) bool {
	left, lerr := os.Stat(a)
	right, rerr := os.Stat(b)
	return lerr == nil && rerr == nil && os.SameFile(left, right)
}

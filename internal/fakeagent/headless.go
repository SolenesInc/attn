package fakeagent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"
)

const (
	methodHeadless      = "headless"
	headlessTaskBacklog = 64
)

type headlessTask struct {
	Harness Harness `json:"harness"`
	Prompt  string  `json:"prompt"`
	Model   string  `json:"model,omitempty"`
	Effort  string  `json:"effort,omitempty"`
	Refusal string  `json:"refusal,omitempty"`
	launchedAs
}

type launchedAs struct {
	Argv []string `json:"argv"`
	Env  []string `json:"env"`
	Dir  string   `json:"dir"`
}

type headlessAnswer struct {
	Text    string          `json:"text"`
	Failure string          `json:"failure,omitempty"`
	Tool    json.RawMessage `json:"tool,omitempty"`
}

type HeadlessTask struct {
	Harness Harness
	Prompt  string
	Model   string
	Effort  string
	Argv    []string
	Env     []string
	Dir     string
	answer  chan headlessAnswer
}

func (task *HeadlessTask) Answer(text string) {
	task.answer <- headlessAnswer{Text: text}
}

func (task *HeadlessTask) Fail(message string) {
	task.answer <- headlessAnswer{Failure: message}
}

func (k *Kit) HeadlessTask() *HeadlessTask {
	k.t.Helper()
	select {
	case task := <-k.headless:
		return task
	case <-time.After(HangGuard):
		k.t.Fatalf("the daemon ran no headless task within %s%s", HangGuard, k.failureSummary())
		return nil
	}
}

func (k *Kit) receiveHeadlessTask(f *fake, params json.RawMessage) (headlessAnswer, error) {
	var asked headlessTask
	if err := json.Unmarshal(params, &asked); err != nil {
		return headlessAnswer{}, err
	}
	if asked.Refusal != "" {
		k.fail(fmt.Sprintf("fake %s cannot script this headless task: %s", asked.Harness, asked.Refusal))
		return headlessAnswer{Failure: asked.Refusal}, nil
	}
	task := &HeadlessTask{Harness: asked.Harness, Prompt: asked.Prompt, Model: asked.Model, Effort: asked.Effort,
		Argv: asked.Argv, Env: asked.Env, Dir: asked.Dir, answer: make(chan headlessAnswer, 1)}
	if answerer := k.headlessAnswerer(); answerer != nil {
		answerer(task)
		return <-task.answer, nil
	}
	select {
	case k.headless <- task:
	case <-f.peer.done:
		return headlessAnswer{}, errPeerClosed
	}
	select {
	case answer := <-task.answer:
		return answer, nil
	case <-f.peer.done:
		return headlessAnswer{}, errPeerClosed
	}
}

func (k *Kit) failUnansweredHeadlessTasks() {
	for {
		select {
		case task := <-k.headless:
			k.fail(fmt.Sprintf("the daemon ran a headless %s task the test never answered: %q", task.Harness, task.Prompt))
			task.Fail("the test never answered this task")
		default:
			return
		}
	}
}

type headlessRun struct {
	harness Harness
	prompt  string
	model   string
	effort  string
	refusal string
	tools   map[string]*toolServer
	answer  func(text string) error
	fail    func(message string)
}

func (run headlessRun) serve(cfg config) int {
	control, err := dialControl(cfg, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "fake %s: %v\n", run.harness, err)
		return 1
	}
	defer control.close()
	var answer headlessAnswer
	dir, _ := os.Getwd()
	asked := headlessTask{Harness: run.harness, Prompt: run.prompt, Model: run.model, Effort: run.effort, Refusal: run.refusal,
		launchedAs: launchedAs{Argv: os.Args, Env: os.Environ(), Dir: dir}}
	if err := control.start().call(context.Background(), methodHeadless, asked, &answer); err != nil {
		fmt.Fprintf(os.Stderr, "fake %s: %v\n", run.harness, err)
		return 1
	}
	if answer.Tool != nil {
		if err := callTool(run.tools, answer.Tool); err != nil {
			fmt.Fprintf(os.Stderr, "fake %s: %v\n", run.harness, err)
			return 1
		}
	}
	if answer.Failure != "" {
		run.fail(answer.Failure)
		return 1
	}
	if err := run.answer(answer.Text); err != nil {
		fmt.Fprintf(os.Stderr, "fake %s: %v\n", run.harness, err)
		return 1
	}
	return 0
}

func joinSystemPrompt(system, prompt string) string {
	if system = strings.TrimSpace(system); system == "" {
		return prompt
	}
	return system + "\n\n" + prompt
}

func printJSONLines(lines ...any) error {
	for _, line := range lines {
		data, err := json.Marshal(line)
		if err != nil {
			return err
		}
		if _, err := fmt.Fprintln(os.Stdout, string(data)); err != nil {
			return err
		}
	}
	return nil
}

package daemon_test

import (
	"os"
	"slices"
	"strings"
	"testing"
	"testing/synctest"

	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func inBubbleWithAgents(t *testing.T, script func(t *testing.T, w *world)) {
	t.Helper()
	prepared := prepareWorld(t)
	t.Setenv("ATTN_PTY_BACKEND", "embedded")
	synctest.Test(t, func(t *testing.T) {
		bubbled := *prepared
		bubbled.T = t
		w := &world{World: &bubbled, bubbled: true, terms: testworld.NewTerminals()}
		w.start()
		script(t, w)
	})
}

type bubbleClaude struct {
	t          *testing.T
	id         string
	cli        *client.Client
	term       *testworld.Terminal
	transcript *fakeagent.ClaudeTranscript
}

func (w *world) bubbleClaude(t *testing.T, app *testworld.Peer, dir string) *bubbleClaude {
	t.Helper()
	id := w.Spawn(app, fakeagent.Claude, w.Path(dir))
	agent := w.bootBubbleClaude(t, id)
	if booted := queriedSession(t, agent.cli, id); booted.State != protocol.SessionStateIdle {
		t.Fatalf("%s booted %s, want idle", id, booted.State)
	}
	return agent
}

func (w *world) bootBubbleClaude(t *testing.T, id string) *bubbleClaude {
	t.Helper()
	term := w.terms.Terminal(id)
	if term == nil {
		t.Fatalf("session %s has no terminal", id)
	}
	cwd := term.Options.CWD
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	agent := &bubbleClaude{t: t, id: id, cli: w.Client(), term: term, transcript: fakeagent.WriteClaudeTranscript(t, cwd)}
	term.OnSubmit(agent.take)
	if err := agent.cli.ObserveAgentConversation(id, agent.transcript.ConversationID, agent.transcript.Path); err != nil {
		t.Fatalf("session start of %s: %v", id, err)
	}
	term.Heartbeat("not_busy", "Claude Code")
	if path := term.Options.InitialPromptFile; path != "" {
		prompt, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read the initial prompt of %s: %v", id, err)
		}
		agent.take(string(prompt))
	}
	synctest.Wait()
	return agent
}

func (a *bubbleClaude) take(prompt string) {
	a.transcript.Prompt(prompt)
	if err := a.cli.UpdateStateFromHookEvidence(a.id, protocol.StateWorking, "", "user_prompt_submit", prompt); err != nil {
		a.t.Errorf("%s reports its prompt taken: %v", a.id, err)
	}
}

func (a *bubbleClaude) reply(text string) {
	a.t.Helper()
	synctest.Wait()
	a.transcript.Answer(text)
	if err := a.cli.SendStop(a.id, a.transcript.Path, client.StopFacts{}); err != nil {
		a.t.Fatalf("%s stops: %v", a.id, err)
	}
	synctest.Wait()
}

func (a *bubbleClaude) prompts() []string {
	return a.term.Submitted()
}

func (a *bubbleClaude) promptsContaining(text string) int {
	return len(slices.DeleteFunc(a.prompts(), func(p string) bool { return !strings.Contains(p, text) }))
}

package fakeagent

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/google/uuid"
)

var copilotComposer = composer{prompt: "> "}

var copilotFlags = flagSpec{
	values:   map[string]bool{"--interactive": true, "-i": true, "--model": true},
	optional: map[string]bool{"--resume": true},
}

type copilot struct {
	cfg          config
	term         *terminal
	conversation string
	resumed      bool
	events       string
	lastEvent    any
	interaction  string
	turns        int
	prompt       string
}

func runCopilot(cfg config) int {
	return serve(cfg, copilotComposer, &copilot{cfg: cfg})
}

func (c *copilot) begin(term *terminal) error {
	c.term = term
	args := copilotFlags.parse(os.Args[1:])
	c.prompt = args.value("--interactive", "-i")
	if args.has("--resume") {
		if c.conversation = args.value("--resume"); c.conversation == "" {
			return errors.New("copilot --resume without a session id opens the resume picker, which the fake does not script")
		}
		c.resumed = true
	} else {
		c.conversation = uuid.NewString()
	}
	state := filepath.Join(c.cfg.ToolHome, ".copilot", "session-state", c.conversation)
	c.events = filepath.Join(state, "events.jsonl")
	if c.resumed {
		if _, err := os.Stat(c.events); err != nil {
			return fmt.Errorf("copilot --resume %s: %w", c.conversation, err)
		}
		return nil
	}
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(state, 0o700); err != nil {
		return err
	}
	workspace := "id: " + c.conversation + "\ncwd: " + cwd + "\n"
	if err := os.WriteFile(filepath.Join(state, "workspace.yaml"), []byte(workspace), 0o600); err != nil {
		return err
	}
	return c.record("session.start", map[string]any{
		"sessionId":      c.conversation,
		"version":        1,
		"producer":       "copilot-agent",
		"copilotVersion": "1.0.80",
		"startTime":      copilotNow(),
		"context":        map[string]any{"cwd": cwd},
	})
}

func (c *copilot) launch() launch {
	return launch{Harness: Copilot, ConversationID: c.conversation, Resumed: c.resumed}
}

func (c *copilot) initialPrompt() string { return c.prompt }

func (c *copilot) submit(prompt string) error {
	c.interaction = uuid.NewString()
	if err := c.record("user.message", map[string]any{"content": prompt, "interactionId": c.interaction}); err != nil {
		return err
	}
	return c.record("assistant.turn_start", map[string]any{"turnId": strconv.Itoa(c.turns), "interactionId": c.interaction})
}

func (c *copilot) reply(text string, afterStop bool) error {
	if afterStop {
		return errors.New("copilot has no Stop hook to reply after")
	}
	if err := c.record("assistant.message", map[string]any{
		"content":       text,
		"interactionId": c.interaction,
		"toolRequests":  []any{},
	}); err != nil {
		return err
	}
	c.term.print(text)
	turn := strconv.Itoa(c.turns)
	c.turns++
	return c.record("assistant.turn_end", map[string]any{"turnId": turn})
}

func (c *copilot) record(kind string, data map[string]any) error {
	id := uuid.NewString()
	if err := appendLines(c.events, map[string]any{
		"id":        id,
		"parentId":  c.lastEvent,
		"timestamp": copilotNow(),
		"type":      kind,
		"data":      data,
	}); err != nil {
		return err
	}
	c.lastEvent = id
	return nil
}

func copilotNow() string {
	return time.Now().UTC().Format("2006-01-02T15:04:05.000Z07:00")
}

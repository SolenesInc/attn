package fakeagent

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
)

const codexBusyGlyph = "⠋ "

var codexComposer = composer{prompt: "› "}

var codexFlags = flagSpec{
	values: map[string]bool{"-c": true, "-C": true, "--cd": true, "--add-dir": true, "--model": true, "-m": true},
}

type codex struct {
	cfg          config
	term         *terminal
	hooks        hookSet
	cwd          string
	conversation string
	resumed      bool
	transcript   string
	model        string
	prompt       string
	turnID       string
}

func runCodex(cfg config) int {
	return serve(cfg, codexComposer, &codex{cfg: cfg})
}

func (c *codex) begin(term *terminal) error {
	c.term = term
	args := codexFlags.parse(os.Args[1:])
	var err error
	if c.cwd = args.value("-C", "--cd"); c.cwd == "" {
		if c.cwd, err = os.Getwd(); err != nil {
			return err
		}
	}
	c.model = args.value("--model", "-m")
	c.prompt = strings.Join(args.afterDashes, " ")
	if c.hooks, err = codexHooks(args.values["-c"], c.cwd); err != nil {
		return err
	}
	source := "startup"
	if len(args.positionals) > 0 && args.positionals[0] == "resume" {
		if len(args.positionals) < 2 {
			return errors.New("codex resume without a session id opens the resume picker, which the fake does not script")
		}
		source, c.resumed, c.conversation = "resume", true, args.positionals[1]
		if c.transcript = c.findRollout(); c.transcript == "" {
			return fmt.Errorf("codex resume %s: no rollout under %s", c.conversation, c.sessionsDir())
		}
	} else if err := c.startRollout(); err != nil {
		return err
	}
	term.title(c.restingTitle())
	return c.hooks.run("SessionStart", source, c.hookInput("SessionStart", map[string]any{"source": source}))
}

func (c *codex) sessionsDir() string {
	return filepath.Join(c.cfg.CodexHome, "sessions")
}

func (c *codex) startRollout() error {
	id, err := uuid.NewV7()
	if err != nil {
		return err
	}
	c.conversation = id.String()
	started := time.Now().UTC()
	c.transcript = filepath.Join(c.sessionsDir(), started.Format("2006/01/02"),
		"rollout-"+started.Format("2006-01-02T15-04-05")+"-"+c.conversation+".jsonl")
	stamp := started.Format(time.RFC3339Nano)
	return appendLines(c.transcript, map[string]any{
		"timestamp": stamp,
		"type":      "session_meta",
		"payload": map[string]any{
			"id":         c.conversation,
			"timestamp":  stamp,
			"cwd":        c.cwd,
			"originator": "codex_cli_rs",
			"source":     "cli",
		},
	})
}

func (c *codex) findRollout() string {
	var found string
	_ = filepath.WalkDir(c.sessionsDir(), func(path string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.HasSuffix(d.Name(), "-"+c.conversation+".jsonl") {
			found = path
			return fs.SkipAll
		}
		return nil
	})
	return found
}

func (c *codex) restingTitle() string {
	return filepath.Base(c.cwd)
}

func (c *codex) launch() launch {
	return launch{
		Harness:        Codex,
		ConversationID: c.conversation,
		Resumed:        c.resumed,
	}
}

func (c *codex) initialPrompt() string { return c.prompt }

func (c *codex) hookInput(event string, extra map[string]any) map[string]any {
	input := map[string]any{
		"session_id":      c.conversation,
		"transcript_path": c.transcript,
		"cwd":             c.cwd,
		"hook_event_name": event,
		"model":           c.model,
		"permission_mode": "default",
	}
	if c.turnID != "" {
		input["turn_id"] = c.turnID
	}
	for key, value := range extra {
		input[key] = value
	}
	return input
}

func (c *codex) submit(prompt string) error {
	c.turnID = uuid.NewString()
	c.term.title(codexBusyGlyph + c.restingTitle())
	if err := c.hooks.run("UserPromptSubmit", "", c.hookInput("UserPromptSubmit", map[string]any{"prompt": prompt})); err != nil {
		return err
	}
	return appendLines(c.transcript, codexEvent("user_message", prompt))
}

func (c *codex) reply(text string, afterStop bool) error {
	if afterStop {
		return errors.New("the codex fake does not script a reply written after its Stop hook")
	}
	err := appendLines(c.transcript,
		map[string]any{
			"timestamp": now(),
			"type":      "response_item",
			"payload": map[string]any{
				"type":    "message",
				"role":    "assistant",
				"content": []map[string]any{{"type": "output_text", "text": text}},
			},
		},
		codexEvent("agent_message", text),
	)
	if err != nil {
		return err
	}
	c.term.print(text)
	c.term.title(c.restingTitle())
	return c.hooks.run("Stop", "", c.hookInput("Stop", map[string]any{
		"stop_hook_active":       false,
		"last_assistant_message": text,
	}))
}

func codexEvent(kind, message string) map[string]any {
	return map[string]any{
		"timestamp": now(),
		"type":      "event_msg",
		"payload":   map[string]any{"type": kind, "message": message},
	}
}

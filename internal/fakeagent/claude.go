package fakeagent

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/google/uuid"
)

const (
	claudeRestingTitle = "✳ Claude Code"
	claudeBusyTitle    = "✶ Claude Code"
	claudeDefaultModel = "claude-opus-5-5"
)

var claudeComposer = composer{prompt: "❯ ", footer: "  ? for shortcuts"}

var claudeFlags = flagSpec{
	values: map[string]bool{
		"--session-id": true, "--settings": true, "--append-system-prompt": true,
		"--model": true, "--effort": true, "--permission-mode": true,
	},
	variadic: map[string]bool{"--disallowed-tools": true, "--add-dir": true},
	optional: map[string]bool{"-r": true, "--resume": true},
}

type claude struct {
	cfg          config
	term         *terminal
	hooks        hookSet
	cwd          string
	conversation string
	resumed      bool
	transcript   string
	model        string
	permission   string
	prompt       string
	streaming    string
}

func runClaude(cfg config) int {
	return serve(cfg, claudeComposer, &claude{cfg: cfg})
}

func (c *claude) begin(term *terminal) error {
	c.term = term
	args := claudeFlags.parse(os.Args[1:])
	var err error
	if c.cwd, err = os.Getwd(); err != nil {
		return err
	}
	c.conversation = args.value("--session-id")
	if args.has("-r", "--resume") {
		c.conversation, c.resumed = args.value("-r", "--resume"), true
		if c.conversation == "" {
			return errors.New("claude -r without a session id opens the resume picker, which the fake does not script")
		}
	}
	if c.conversation == "" {
		return errors.New("claude launched without --session-id or -r <id>")
	}
	c.model = args.value("--model")
	if c.model == "" {
		c.model = claudeDefaultModel
	}
	c.permission = claudePermissionMode(args)
	c.prompt = strings.Join(args.afterDashes, " ")
	c.transcript = c.transcriptPath()
	if c.hooks, err = claudeHooks(args.value("--settings"), c.cwd); err != nil {
		return err
	}
	source := "startup"
	if c.resumed {
		source = "resume"
	}
	term.title(claudeRestingTitle)
	return c.hooks.run("SessionStart", source, c.hookInput("SessionStart", map[string]any{"source": source}))
}

func (c *claude) transcriptPath() string {
	return filepath.Join(c.cfg.ToolHome, ".claude", "projects", claudeProjectName(c.cwd), c.conversation+".jsonl")
}

func claudePermissionMode(args parsedArgs) string {
	switch {
	case args.has("--dangerously-skip-permissions"):
		return "bypassPermissions"
	case args.value("--permission-mode") != "":
		return args.value("--permission-mode")
	default:
		return "default"
	}
}

var claudeProjectNameUnsafe = regexp.MustCompile(`[^A-Za-z0-9]`)

func claudeProjectName(cwd string) string {
	return claudeProjectNameUnsafe.ReplaceAllString(cwd, "-")
}

func (c *claude) launch() launch {
	return launch{
		Harness:        Claude,
		ConversationID: c.conversation,
		Resumed:        c.resumed,
	}
}

func (c *claude) initialPrompt() string { return c.prompt }

func (c *claude) hookInput(event string, extra map[string]any) map[string]any {
	input := map[string]any{
		"session_id":      c.conversation,
		"transcript_path": c.transcript,
		"cwd":             c.cwd,
		"permission_mode": c.permission,
		"hook_event_name": event,
	}
	for key, value := range extra {
		input[key] = value
	}
	return input
}

func (c *claude) submit(prompt string) error {
	if strings.TrimSpace(prompt) == "/clear" {
		return c.clear()
	}
	c.term.title(claudeBusyTitle)
	if err := c.hooks.run("UserPromptSubmit", "", c.hookInput("UserPromptSubmit", map[string]any{"prompt": prompt})); err != nil {
		return err
	}
	return c.record("user", map[string]any{"role": "user", "content": prompt}, map[string]any{"permissionMode": c.permission})
}

func (c *claude) clear() error {
	if err := c.hooks.run("SessionEnd", "clear", c.hookInput("SessionEnd", map[string]any{"reason": "clear"})); err != nil {
		return err
	}
	c.conversation, c.resumed = uuid.NewString(), false
	c.transcript = c.transcriptPath()
	return c.hooks.run("SessionStart", "clear", c.hookInput("SessionStart", map[string]any{"source": "clear"}))
}

func (c *claude) reply(text string, afterStop bool) error {
	if afterStop {
		if err := c.stop(); err != nil {
			return err
		}
		return c.answer(text)
	}
	if err := c.answer(text); err != nil {
		return err
	}
	return c.stop()
}

func (c *claude) answer(text string) error {
	if err := c.stream(text); err != nil {
		return err
	}
	c.streaming = ""
	return nil
}

func (c *claude) stream(text string) error {
	if c.streaming == "" {
		c.streaming = claudeMessageID()
	}
	if err := appendLines(c.transcript, c.assistantLine(c.streaming, map[string]any{"type": "text", "text": text}, len(text), len(text))); err != nil {
		return err
	}
	c.term.print(text)
	return nil
}

func (c *claude) halt() error {
	c.streaming = ""
	c.term.title(claudeRestingTitle)
	return c.record("user", map[string]any{
		"role":    "user",
		"content": []map[string]any{{"type": "text", "text": "[Request interrupted by user]"}},
	}, map[string]any{"interruptedMessageId": claudeMessageID()})
}

func (c *claude) subagent(text string) error {
	line := c.assistantLine(claudeMessageID(), map[string]any{"type": "text", "text": text}, len(text), len(text))
	line["isSidechain"] = true
	return appendLines(filepath.Join(c.subagentDir(), "agent-"+claudeMessageID()+".jsonl"), line)
}

func (c *claude) deleteSubagentTranscripts() error {
	return os.RemoveAll(c.subagentDir())
}

func (c *claude) subagentDir() string {
	return filepath.Join(strings.TrimSuffix(c.transcript, ".jsonl"), "subagents")
}

func (c *claude) assistantLine(id string, content map[string]any, inputTokens, outputTokens int) map[string]any {
	return c.line("assistant", map[string]any{
		"id":      id,
		"role":    "assistant",
		"model":   c.model,
		"content": []map[string]any{content},
		"usage": map[string]any{
			"input_tokens":                inputTokens,
			"output_tokens":               outputTokens,
			"cache_creation_input_tokens": 0,
			"cache_read_input_tokens":     0,
		},
	})
}

func claudeMessageID() string {
	return "msg_" + strings.ReplaceAll(uuid.NewString(), "-", "")
}

func (c *claude) stop() error {
	c.term.title(claudeRestingTitle)
	return c.hooks.run("Stop", "", c.hookInput("Stop", map[string]any{"stop_hook_active": false}))
}

func (c *claude) record(kind string, message map[string]any, extra map[string]any) error {
	line := c.line(kind, message)
	for key, value := range extra {
		line[key] = value
	}
	return appendLines(c.transcript, line)
}

func (c *claude) line(kind string, message map[string]any) map[string]any {
	return map[string]any{
		"type":      kind,
		"uuid":      uuid.NewString(),
		"timestamp": now(),
		"sessionId": c.conversation,
		"cwd":       c.cwd,
		"message":   message,
	}
}

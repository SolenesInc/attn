package fakeagent

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
)

type ClaudeTranscript struct {
	Path           string
	ConversationID string
	t              testing.TB
	writer         *claude
}

// WriteClaudeTranscript writes a conversation's transcript as a Claude launched with
// --session-id or -r conversation would; an empty conversation is a new one.
func WriteClaudeTranscript(t testing.TB, cwd, conversation string) *ClaudeTranscript {
	t.Helper()
	if conversation == "" {
		conversation = uuid.NewString()
	}
	writer := &claude{
		cfg:          config{ToolHome: os.Getenv("ATTN_TOOL_HOME")},
		cwd:          cwd,
		conversation: conversation,
		model:        claudeDefaultModel,
		permission:   "default",
	}
	if writer.cfg.ToolHome == "" {
		t.Fatal("a Claude transcript needs the world's ATTN_TOOL_HOME")
	}
	writer.transcript = writer.transcriptPath()
	if err := os.MkdirAll(filepath.Dir(writer.transcript), 0o700); err != nil {
		t.Fatal(err)
	}
	return &ClaudeTranscript{Path: writer.transcript, ConversationID: writer.conversation, t: t, writer: writer}
}

func (tr *ClaudeTranscript) Prompt(text string) {
	tr.t.Helper()
	if err := tr.writer.record("user", map[string]any{"role": "user", "content": text}, map[string]any{"permissionMode": tr.writer.permission}); err != nil {
		tr.t.Fatal(err)
	}
	tr.stamp()
}

func (tr *ClaudeTranscript) Answer(text string) {
	tr.t.Helper()
	line := tr.writer.assistantLine(claudeMessageID(), map[string]any{"type": "text", "text": text}, len(text), len(text))
	if err := appendLines(tr.writer.transcript, line); err != nil {
		tr.t.Fatal(err)
	}
	tr.stamp()
}

func (tr *ClaudeTranscript) Halt() {
	tr.t.Helper()
	if err := tr.writer.record("user", map[string]any{
		"role":    "user",
		"content": []map[string]any{{"type": "text", "text": "[Request interrupted by user]"}},
	}, map[string]any{"interruptedMessageId": claudeMessageID()}); err != nil {
		tr.t.Fatal(err)
	}
	tr.stamp()
}

func (tr *ClaudeTranscript) stamp() {
	now := time.Now()
	if err := os.Chtimes(tr.Path, now, now); err != nil {
		tr.t.Fatal(err)
	}
}

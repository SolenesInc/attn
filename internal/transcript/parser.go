package transcript

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
	"time"
)

// bufio.Scanner's token size limit would truncate long transcript lines.
func readJSONLLines(r io.Reader, fn func(line []byte)) error {
	br := bufio.NewReader(r)
	for {
		line, err := br.ReadBytes('\n')
		if len(line) > 0 {
			line = bytes.TrimSpace(line)
			if len(line) > 0 {
				fn(line)
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
	}
}

type contentBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// Claude Code writes message.content as an array of content blocks, not a string.
type transcriptEntry struct {
	Type    string `json:"type"`
	UUID    string `json:"uuid"`
	Message struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	} `json:"message"`
}

func isUserEntry(line []byte) bool {
	var entry transcriptEntry
	if err := json.Unmarshal(line, &entry); err == nil {
		if entry.Type == "user" || entry.Message.Role == "user" {
			return true
		}
	}

	var codex codexEnvelope
	if err := json.Unmarshal(line, &codex); err == nil {
		switch codex.Type {
		case "event_msg":
			var payload codexEventMessage
			if err := json.Unmarshal(codex.Payload, &payload); err == nil {
				if payload.Type == "user_message" && payload.Message != "" {
					return true
				}
			}
		case "response_item":
			var payload codexResponseMessage
			if err := json.Unmarshal(codex.Payload, &payload); err == nil {
				if payload.Type == "message" && payload.Role == "user" {
					return true
				}
			}
		}
	}

	var copilot struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(line, &copilot); err == nil {
		if copilot.Type == "user.message" {
			return true
		}
	}
	return false
}

func extractLineTimestamp(line []byte) time.Time {
	var entry struct {
		Timestamp string `json:"timestamp"`
	}
	if err := json.Unmarshal(line, &entry); err != nil {
		return time.Time{}
	}
	return parseTranscriptTime(entry.Timestamp)
}

func extractLineUUID(line []byte) string {
	var entry struct {
		UUID string `json:"uuid"`
	}
	if err := json.Unmarshal(line, &entry); err != nil {
		return ""
	}
	return strings.TrimSpace(entry.UUID)
}

type AssistantTurn struct {
	Content   string
	Timestamp time.Time
	UUID      string
}

func ExtractLastAssistantMessage(path string, maxChars int) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()

	var lastAssistantContent string
	if err := readJSONLLines(file, func(line []byte) {
		if content := ExtractAssistantContent(line); content != "" {
			lastAssistantContent = content
		}
	}); err != nil {
		return "", err
	}

	if len(lastAssistantContent) > maxChars {
		lastAssistantContent = lastAssistantContent[len(lastAssistantContent)-maxChars:]
	}

	return lastAssistantContent, nil
}

func ExtractLastAssistantMessageAfterLastUser(path string, maxChars int) (string, error) {
	return ExtractLastAssistantMessageAfterLastUserSince(path, maxChars, time.Time{})
}

func ExtractLastAssistantMessageAfterLastUserSince(path string, maxChars int, minAssistantTimestamp time.Time) (string, error) {
	turn, err := ExtractLastAssistantTurnAfterLastUserSince(path, maxChars, minAssistantTimestamp)
	if err != nil {
		return "", err
	}
	return turn.Content, nil
}

func ExtractLastAssistantTurnAfterLastUserSince(path string, maxChars int, minAssistantTimestamp time.Time) (AssistantTurn, error) {
	file, err := os.Open(path)
	if err != nil {
		return AssistantTurn{}, err
	}
	defer file.Close()

	var (
		lastAssistantContent string
		lastAssistantSeq     int
		lastUserSeq          int
		lastAssistantTS      time.Time
		lastAssistantUUID    string
		seq                  int
	)

	if err := readJSONLLines(file, func(line []byte) {
		seq++
		if isUserEntry(line) {
			lastUserSeq = seq
			return
		}
		if content := ExtractAssistantContent(line); content != "" {
			lastAssistantContent = content
			lastAssistantSeq = seq
			lastAssistantTS = extractLineTimestamp(line)
			lastAssistantUUID = extractLineUUID(line)
		}
	}); err != nil {
		return AssistantTurn{}, err
	}

	if lastUserSeq > 0 && lastAssistantSeq <= lastUserSeq {
		return AssistantTurn{}, nil
	}
	if !minAssistantTimestamp.IsZero() && !lastAssistantTS.IsZero() && lastAssistantTS.Before(minAssistantTimestamp) {
		return AssistantTurn{}, nil
	}

	if len(lastAssistantContent) > maxChars {
		lastAssistantContent = lastAssistantContent[len(lastAssistantContent)-maxChars:]
	}
	return AssistantTurn{
		Content:   lastAssistantContent,
		Timestamp: lastAssistantTS,
		UUID:      lastAssistantUUID,
	}, nil
}

type codexEnvelope struct {
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload"`
}

type codexEventMessage struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

type codexResponseMessage struct {
	Type    string          `json:"type"`
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

type copilotEventEntry struct {
	Type string `json:"type"`
	Data struct {
		Content string `json:"content"`
	} `json:"data"`
}

type CopilotToolLifecycle struct {
	Kind       string
	ToolCallID string
	ToolName   string
}

func ExtractAssistantContent(line []byte) string {
	var entry transcriptEntry
	if err := json.Unmarshal(line, &entry); err == nil {
		isAssistant := entry.Type == "assistant" || entry.Message.Role == "assistant"
		if isAssistant {
			content := extractTextContent(entry.Message.Content)
			if content != "" {
				return content
			}
		}
	}

	var codex codexEnvelope
	if err := json.Unmarshal(line, &codex); err != nil {
		return ""
	}

	switch codex.Type {
	case "event_msg":
		var payload codexEventMessage
		if err := json.Unmarshal(codex.Payload, &payload); err != nil {
			return ""
		}
		if payload.Type == "agent_message" && payload.Message != "" {
			return payload.Message
		}
	case "response_item":
		var payload codexResponseMessage
		if err := json.Unmarshal(codex.Payload, &payload); err != nil {
			return ""
		}
		if payload.Type == "message" && payload.Role == "assistant" {
			content := extractTextContent(payload.Content)
			if content != "" {
				return content
			}
		}
	}

	var copilot copilotEventEntry
	if err := json.Unmarshal(line, &copilot); err == nil {
		if copilot.Type == "assistant.message" && copilot.Data.Content != "" {
			return copilot.Data.Content
		}
	}

	return ""
}

func ExtractCopilotToolLifecycle(line []byte) (CopilotToolLifecycle, bool) {
	var evt struct {
		Type string `json:"type"`
		Data struct {
			ToolCallID string `json:"toolCallId"`
			ToolName   string `json:"toolName"`
		} `json:"data"`
	}
	if err := json.Unmarshal(line, &evt); err != nil {
		return CopilotToolLifecycle{}, false
	}
	if evt.Data.ToolCallID == "" {
		return CopilotToolLifecycle{}, false
	}

	switch evt.Type {
	case "tool.execution_start":
		return CopilotToolLifecycle{
			Kind:       "start",
			ToolCallID: evt.Data.ToolCallID,
			ToolName:   evt.Data.ToolName,
		}, true
	case "tool.execution_complete":
		return CopilotToolLifecycle{
			Kind:       "complete",
			ToolCallID: evt.Data.ToolCallID,
		}, true
	default:
		return CopilotToolLifecycle{}, false
	}
}

// No agent reports a turn the user halted: measured on claude 2.1.220 (all 31 hook events),
// codex 0.146.0 and copilot 1.0.77, ESC writes the abort only to the transcript.
const (
	// Matched exactly: a user is free to type the text.
	claudeInterruptMarker           = "[Request interrupted by user]"
	claudeInterruptMarkerForToolUse = "[Request interrupted by user for tool use]"
	copilotUserAbortReason          = "user_initiated"
	// The 0.146.0 enum also carries `replaced`, `review_ended`, and `budget_limited`,
	// none of which are halts: after `replaced` the session works again a moment later.
	codexUserAbortReason = "interrupted"
)

type TurnAbort struct {
	Reason string

	// Only a user halt settles a session; the other abandonments are the harness's
	// business and some are followed by another turn.
	UserHalt bool

	At time.Time
}

// `interruptedMessageId` is believed on its own; the tool-use marker is honored solely in the
// exact shape claude emits it, so a user who types it cannot settle their own session.
func ClaudeTurnAborted(line []byte) (TurnAbort, bool) {
	var entry struct {
		Type                 string          `json:"type"`
		InterruptedMessageID string          `json:"interruptedMessageId"`
		Timestamp            string          `json:"timestamp"`
		PromptSource         string          `json:"promptSource"`
		PermissionMode       string          `json:"permissionMode"`
		Origin               json.RawMessage `json:"origin"`
		Message              struct {
			Content json.RawMessage `json:"content"`
		} `json:"message"`
	}
	if err := json.Unmarshal(line, &entry); err != nil || entry.Type != "user" {
		return TurnAbort{}, false
	}

	abort := TurnAbort{UserHalt: true, At: parseTranscriptTime(entry.Timestamp)}

	if strings.TrimSpace(entry.InterruptedMessageID) != "" {
		abort.Reason = claudeInterruptMarker
		if marker, ok := claudeInterruptMarkerBlock(entry.Message.Content); ok {
			abort.Reason = marker
		}
		return abort, true
	}

	submitted := strings.TrimSpace(entry.PromptSource) != "" ||
		strings.TrimSpace(entry.PermissionMode) != "" ||
		len(entry.Origin) > 0
	if submitted {
		return TurnAbort{}, false
	}
	marker, ok := claudeInterruptMarkerBlock(entry.Message.Content)
	if !ok {
		return TurnAbort{}, false
	}
	abort.Reason = marker
	return abort, true
}

// The array is required — a marker the user typed arrives as a plain string.
func claudeInterruptMarkerBlock(raw json.RawMessage) (string, bool) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] != '[' {
		return "", false
	}
	var blocks []contentBlock
	if err := json.Unmarshal(trimmed, &blocks); err != nil || len(blocks) != 1 {
		return "", false
	}
	if blocks[0].Type != "text" {
		return "", false
	}
	switch text := strings.TrimSpace(blocks[0].Text); text {
	case claudeInterruptMarker, claudeInterruptMarkerForToolUse:
		return text, true
	default:
		return "", false
	}
}

func CodexTurnAborted(line []byte) (TurnAbort, bool) {
	var envelope struct {
		Type      string          `json:"type"`
		Timestamp string          `json:"timestamp"`
		Payload   json.RawMessage `json:"payload"`
	}
	if err := json.Unmarshal(line, &envelope); err != nil || envelope.Type != "event_msg" {
		return TurnAbort{}, false
	}
	var payload struct {
		Type   string `json:"type"`
		Reason string `json:"reason"`
	}
	if err := json.Unmarshal(envelope.Payload, &payload); err != nil || payload.Type != "turn_aborted" {
		return TurnAbort{}, false
	}
	reason := strings.TrimSpace(payload.Reason)
	if reason == "" {
		reason = "aborted"
	}
	return TurnAbort{
		Reason:   reason,
		UserHalt: reason == codexUserAbortReason,
		At:       parseTranscriptTime(envelope.Timestamp),
	}, true
}

// Copilot writes a bare top-level `abort` event and no `assistant.turn_end`, so every abort
// must be seen, or the watcher's turn bracket stays open and pins the session working.
func CopilotTurnAborted(line []byte) (TurnAbort, bool) {
	var entry struct {
		Type      string `json:"type"`
		Timestamp string `json:"timestamp"`
		Data      struct {
			Reason string `json:"reason"`
		} `json:"data"`
	}
	if err := json.Unmarshal(line, &entry); err != nil || entry.Type != "abort" {
		return TurnAbort{}, false
	}
	reason := strings.TrimSpace(entry.Data.Reason)
	if reason == "" {
		reason = "aborted"
	}
	return TurnAbort{
		Reason:   reason,
		UserHalt: reason == copilotUserAbortReason,
		At:       parseTranscriptTime(entry.Timestamp),
	}, true
}

func parseTranscriptTime(raw string) time.Time {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}
	}
	ts, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		return time.Time{}
	}
	return ts
}

func extractTextContent(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}

	var strContent string
	if err := json.Unmarshal(raw, &strContent); err == nil && strContent != "" {
		return strContent
	}

	var blocks []contentBlock
	if err := json.Unmarshal(raw, &blocks); err == nil {
		var texts []string
		for _, block := range blocks {
			if (block.Type == "text" || block.Type == "output_text") && block.Text != "" {
				texts = append(texts, block.Text)
			}
		}
		return strings.Join(texts, "\n")
	}

	return ""
}

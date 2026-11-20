package transcript

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
)

type ServerEntry struct {
	ID    int64             `json:"id"`
	Kind  string            `json:"kind"`
	Model []json.RawMessage `json:"model"`
}

type serverMessage struct {
	Role       string          `json:"role"`
	Content    json.RawMessage `json:"content"`
	ToolCallID string          `json:"toolCallId"`
	ToolName   string          `json:"toolName"`
	IsError    bool            `json:"isError"`
	Timestamp  int64           `json:"timestamp"`
}

func (entry ServerEntry) messages() []serverMessage {
	messages := make([]serverMessage, 0, len(entry.Model))
	for _, raw := range entry.Model {
		var message serverMessage
		if json.Unmarshal(raw, &message) == nil {
			messages = append(messages, message)
		}
	}
	return messages
}

func (entry ServerEntry) Written() (time.Time, bool) {
	for _, message := range entry.messages() {
		if message.Timestamp > 0 {
			return time.UnixMilli(message.Timestamp), true
		}
	}
	return time.Time{}, false
}

func (entry ServerEntry) Text() string {
	var parts []string
	for _, message := range entry.messages() {
		if text := extractEventContentText(message.Content); text != "" {
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, "\n")
}

type ServerToolCall struct {
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

func (entry ServerEntry) ToolCalls() []ServerToolCall {
	var calls []ServerToolCall
	for _, message := range entry.messages() {
		var blocks []struct {
			Type string `json:"type"`
			ServerToolCall
		}
		_ = json.Unmarshal(message.Content, &blocks)
		for _, block := range blocks {
			if block.Type == "toolCall" && block.ID != "" {
				calls = append(calls, block.ServerToolCall)
			}
		}
	}
	return calls
}

type ServerToolResult struct {
	ToolCallID string
	Content    string
	Failed     bool
}

func (entry ServerEntry) ToolResults() []ServerToolResult {
	var results []ServerToolResult
	for _, message := range entry.messages() {
		if message.Role == "toolResult" {
			results = append(results, ServerToolResult{ToolCallID: message.ToolCallID, Content: extractEventContentText(message.Content), Failed: message.IsError})
		}
	}
	return results
}

func ServerEntryEvents(entry ServerEntry) []Event {
	var events []Event
	for _, message := range entry.messages() {
		timestamp := ""
		if message.Timestamp > 0 {
			timestamp = time.UnixMilli(message.Timestamp).UTC().Format(time.RFC3339Nano)
		}
		switch message.Role {
		case "user":
			events = append(events, parseMessageEvents(timestamp, EventKindUser, message.Content)...)
		case "assistant":
			events = append(events, parseMessageEvents(timestamp, EventKindAssistant, message.Content)...)
		case "toolResult":
			events = append(events, Event{Timestamp: timestamp, Kind: EventKindToolResult, ToolName: message.ToolName, ToolCallID: message.ToolCallID, Text: extractEventContentText(message.Content), IsError: message.IsError})
		}
	}
	events = withoutStateMarkers(events)
	for i := range events {
		events[i].Cursor = ServerCursor(entry.ID, i+1)
		redactEvent(&events[i])
	}
	return events
}

func ServerCursor(entry int64, events int) string {
	return fmt.Sprintf("%s:%d:%d", serverCursorVersion, entry, events)
}

const serverCursorVersion = "s1"

func ParseServerCursor(cursor string) (entry int64, events int, err error) {
	cursor = strings.TrimSpace(cursor)
	if cursor == "" {
		return 0, 0, nil
	}
	parts := strings.Split(cursor, ":")
	if len(parts) != 3 || parts[0] != serverCursorVersion {
		return 0, 0, ErrInvalidCursor
	}
	entry, entryErr := strconv.ParseInt(parts[1], 10, 64)
	events, eventsErr := strconv.Atoi(parts[2])
	if entryErr != nil || eventsErr != nil || entry < 0 || events < 0 {
		return 0, 0, ErrInvalidCursor
	}
	return entry, events, nil
}

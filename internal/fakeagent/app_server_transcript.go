package fakeagent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

const (
	methodToolCall           = "tool_call"
	methodToolRequested      = "tool_requested"
	methodToolReturned       = "tool_returned"
	methodSetUsage           = "set_usage"
	methodServerCompacting   = "server_compacting"
	methodServerCompacted    = "server_compacted"
	methodAppendMany         = "append_many"
	methodWatchCount         = "watch_count"
	methodCrash              = "crash"
	methodForget             = "forget"
	methodStartedElsewhere   = "started_elsewhere"
	methodServerSubagent     = "server_subagent"
	methodServerForkSubagent = "server_fork_subagent"
	methodServerWorker       = "server_worker"
	methodServerForkWorker   = "server_fork_worker"
	methodServerMail         = "server_mail"

	serverWindowMin = 50
	serverWindowMax = 2 * serverWindowMin
)

type serverEntry struct {
	ID             int64             `json:"id"`
	ConversationID int64             `json:"conversationId"`
	Kind           string            `json:"kind"`
	Model          []json.RawMessage `json:"model,omitempty"`
	Data           any               `json:"data,omitempty"`
}

type serverTranscript struct {
	Number  int64          `json:"number"`
	Entries []serverEntry  `json:"entries"`
	Docs    map[string]any `json:"docs"`
}

type serverWatcher struct {
	conn         *appServerConn
	id           uint64
	conversation string
	window       int
}

type ServerToolCall struct {
	Name   string `json:"name"`
	Input  any    `json:"input"`
	Output string `json:"output"`
}

type serverKitParams struct {
	ThreadID string          `json:"threadId"`
	Text     string          `json:"text"`
	Call     *ServerToolCall `json:"call,omitempty"`
	Key      string          `json:"key,omitempty"`
	Usage    any             `json:"usage,omitempty"`
	Count    int             `json:"count,omitempty"`
}

type serverState struct {
	Conversations map[string]*ServerConversation `json:"conversations"`
	Transcripts   map[string]*serverTranscript   `json:"transcripts"`
	NextEntry     int64                          `json:"nextEntry"`
}

func (s *appServer) statePath() string {
	return filepath.Join(os.Getenv(s.flavor.homeVariable), "fake-server-state.json")
}

func (s *appServer) loadState() {
	raw, err := os.ReadFile(s.statePath())
	if err != nil {
		return
	}
	var state serverState
	if json.Unmarshal(raw, &state) != nil {
		return
	}
	for id, conv := range state.Conversations {
		conv.Status, conv.requests = "idle", map[string]int{}
		s.convs[id] = conv
	}
	s.transcripts, s.nextEntry = state.Transcripts, state.NextEntry
}

func (s *appServer) saveLocked() {
	raw, err := json.Marshal(serverState{Conversations: s.convs, Transcripts: s.transcripts, NextEntry: s.nextEntry})
	if err == nil {
		_ = os.WriteFile(s.statePath(), raw, 0o600)
	}
}

func (s *appServer) transcriptLocked(conversation string) *serverTranscript {
	if s.transcripts == nil {
		s.transcripts = map[string]*serverTranscript{}
	}
	t := s.transcripts[conversation]
	if t == nil {
		s.nextEntry++
		t = &serverTranscript{Number: s.nextEntry, Docs: map[string]any{"pi.live": map[string]any{}, "pi.usage": map[string]any{"models": map[string]any{}, "tools": map[string]any{}}}}
		s.transcripts[conversation] = t
	}
	return t
}

func message(role string, fields map[string]any) json.RawMessage {
	fields["role"], fields["timestamp"] = role, time.Now().UnixMilli()
	raw, _ := json.Marshal(fields)
	return raw
}

func (s *appServer) appendLocked(conversation string, entries ...serverEntry) {
	t := s.transcriptLocked(conversation)
	for i := range entries {
		s.nextEntry++
		entries[i].ID, entries[i].ConversationID = s.nextEntry, t.Number
	}
	t.Entries = append(t.Entries, entries...)
	for _, w := range s.watchers {
		if w.conversation != conversation {
			continue
		}
		ops := []any{[]any{"p", []any{"entries"}, w.window, 0, entries}}
		w.window += len(entries)
		if w.window > serverWindowMax {
			drop := w.window - serverWindowMin
			ops = append(ops, []any{"p", []any{"entries"}, 0, drop, []any{}}, []any{"s", []any{"before"}, t.Entries[len(t.Entries)-w.window+drop-1].ID})
			w.window = serverWindowMin
		}
		w.send(map[string]any{"_tag": "change", "ops": ops})
	}
	s.saveLocked()
}

func (s *appServer) docOpLocked(conversation string, ops ...[]any) {
	t := s.transcriptLocked(conversation)
	for _, op := range ops {
		applyFakeDocOp(t.Docs, op)
	}
	wire := make([]any, 0, len(ops))
	for _, op := range ops {
		path := append([]any{"docs"}, op[1].([]any)...)
		wire = append(wire, append([]any{op[0], path}, op[2:]...))
	}
	for _, w := range s.watchers {
		if w.conversation == conversation {
			w.send(map[string]any{"_tag": "change", "ops": wire})
		}
	}
	s.saveLocked()
}

func applyFakeDocOp(docs map[string]any, op []any) {
	path := op[1].([]any)
	container := docs
	for _, key := range path[:len(path)-1] {
		next, _ := container[key.(string)].(map[string]any)
		if next == nil {
			next = map[string]any{}
			container[key.(string)] = next
		}
		container = next
	}
	last := path[len(path)-1].(string)
	if op[0] == "d" {
		delete(container, last)
		return
	}
	container[last] = op[2]
}

func (w *serverWatcher) send(item any) {
	w.conn.send(map[string]any{"jsonrpc": "2.0", "chunk": true, "id": w.id, "result": []any{item}})
}

func (s *appServer) watchLocked(c *appServerConn, id uint64, conversation string, from int64) string {
	if s.convs[conversation] == nil {
		return "NoSuchConversation"
	}
	t := s.transcriptLocked(conversation)
	start := len(t.Entries) - serverWindowMin
	if from > 0 {
		at := len(t.Entries)
		for i, entry := range t.Entries {
			if entry.ID >= from {
				at = i
				break
			}
		}
		if len(t.Entries)-at <= serverWindowMax {
			start = at
		}
	}
	start = max(start, 0)
	window := t.Entries[start:]
	view := map[string]any{"conversation": map[string]any{"id": t.Number}, "entries": window, "docs": t.Docs}
	if start > 0 {
		view["before"] = t.Entries[start-1].ID
	}
	w := &serverWatcher{conn: c, id: id, conversation: conversation, window: len(window)}
	s.watchers = append(s.watchers, w)
	w.send(map[string]any{"_tag": "view", "view": view})
	return ""
}

func (s *appServer) unwatchLocked(c *appServerConn, id *uint64) {
	kept := s.watchers[:0]
	for _, w := range s.watchers {
		if w.conn != c || (id != nil && w.id != *id) {
			kept = append(kept, w)
		}
	}
	s.watchers = kept
}

type entriesParams struct {
	Conversation string `json:"conversation"`
	From         int64  `json:"from"`
	To           int64  `json:"to"`
	Order        string `json:"order"`
	Limit        int    `json:"limit"`
	Cursor       *int   `json:"cursor"`
}

func (s *appServer) entriesLocked(raw json.RawMessage) (any, string) {
	var p entriesParams
	if json.Unmarshal(raw, &p) != nil || p.Limit < 1 {
		return nil, "InvalidParams"
	}
	if s.convs[p.Conversation] == nil {
		return nil, "NoSuchConversation"
	}
	var picked []serverEntry
	for _, entry := range s.transcriptLocked(p.Conversation).Entries {
		if (p.From == 0 || entry.ID >= p.From) && (p.To == 0 || entry.ID <= p.To) {
			picked = append(picked, entry)
		}
	}
	if p.Order != "ascending" {
		for i, j := 0, len(picked)-1; i < j; i, j = i+1, j-1 {
			picked[i], picked[j] = picked[j], picked[i]
		}
	}
	offset := 0
	if p.Cursor != nil {
		offset = min(*p.Cursor, len(picked))
	}
	page := map[string]any{"entries": picked[offset:min(offset+p.Limit, len(picked))]}
	if offset+p.Limit < len(picked) {
		page["next"] = offset + p.Limit
	}
	return page, ""
}

func (s *appServer) handleTranscriptKit(method string, raw json.RawMessage) (any, error, bool) {
	var p serverKitParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, err, true
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	switch method {
	case methodToolCall:
		s.toolCallLocked(p.ThreadID, *p.Call)
	case methodToolRequested:
		return s.toolRequestedLocked(p.ThreadID, *p.Call), nil, true
	case methodToolReturned:
		s.toolReturnedLocked(p.ThreadID, p.Key, *p.Call)
	case methodAppendMany:
		for range p.Count {
			s.toolCallLocked(p.ThreadID, *p.Call)
		}
	case methodSetUsage:
		s.docOpLocked(p.ThreadID, []any{"s", []any{"pi.usage", "models", p.Key}, p.Usage})
	case methodServerCompacting:
		s.docOpLocked(p.ThreadID, []any{"s", []any{"pi.live", "compactions"}, []any{map[string]any{"taskId": 1, "trigger": p.Text}}})
	case methodServerCompacted:
		s.docOpLocked(p.ThreadID, []any{"d", []any{"pi.live", "compactions"}})
		s.appendLocked(p.ThreadID, serverEntry{Kind: "pi.compaction", Data: map[string]any{"reason": p.Text}})
	case methodStartedElsewhere:
		s.convs[p.ThreadID] = &ServerConversation{ID: p.ThreadID, Cwd: p.Key, Status: "idle", requests: map[string]int{}}
		s.appendLocked(p.ThreadID,
			serverEntry{Kind: "sigma.user", Data: map[string]any{"author": "you"}, Model: []json.RawMessage{message("user", map[string]any{"content": p.Text})}},
			serverEntry{Kind: "pi.assistant", Model: []json.RawMessage{message("assistant", map[string]any{"content": []any{map[string]any{"type": "text", "text": "Started."}}})}},
		)
	case methodServerSubagent, methodServerForkSubagent, methodServerWorker, methodServerForkWorker:
		parent := s.convs[p.ThreadID]
		if parent == nil {
			return nil, os.ErrNotExist, true
		}
		isWorker := method == methodServerWorker || method == methodServerForkWorker
		root := parent.ID
		if parent.Lineage != nil && parent.Lineage.Root != "" {
			root = parent.Lineage.Root
		}
		if isWorker {
			root = p.Key
		}
		callID := s.nextCallID()
		child := &ServerConversation{
			ID:         p.Key,
			Cwd:        parent.Cwd,
			Target:     parent.Target,
			MCPServers: parent.MCPServers,
			Lineage:    &ServerLineage{Parent: parent.ID, Root: root, Call: callID, Title: p.Text},
			Status:     "idle",
			requests:   map[string]int{},
		}
		if isWorker {
			child.Lineage.Task = "/root/" + p.Text
		}
		var inherited []serverEntry
		if method == methodServerForkSubagent || method == methodServerForkWorker {
			parentEntries := s.transcriptLocked(parent.ID).Entries
			if len(parentEntries) > 0 {
				at := parentEntries[len(parentEntries)-1].ID
				child.Fork = &ServerFork{Of: parent.ID, At: at}
				inherited = append([]serverEntry(nil), parentEntries...)
			}
		}
		s.convs[p.Key] = child
		childTranscript := s.transcriptLocked(p.Key)
		if len(inherited) > 0 {
			childTranscript.Entries = inherited
		}
		s.appendLocked(p.Key, serverEntry{Kind: "sigma.started", Data: map[string]any{"author": ServerAuthor{ID: "attn", Name: "attn"}}})
		if !isWorker {
			s.appendLocked(parent.ID, serverEntry{Kind: "sigma.subagent", Data: map[string]any{"call": callID, "conversation": childTranscript.Number, "title": p.Text}})
		}
	case methodServerMail:
		conv := s.convs[p.ThreadID]
		if conv == nil {
			return nil, os.ErrNotExist, true
		}
		conv.submitted++
		task := "/root"
		if conv.Lineage != nil && conv.Lineage.Task != "" {
			task = conv.Lineage.Task
		}
		body := fmt.Sprintf("Message Type: NEW_TASK\nTask name: %s\nSender: %s\nPayload:\n%s", task, p.Key, p.Text)
		s.appendLocked(conv.ID, serverEntry{
			Kind:  "sigma.mail",
			Data:  map[string]any{"kind": "new_task", "task": task, "sender": p.Key},
			Model: []json.RawMessage{message("user", map[string]any{"content": body})},
		})
		if conv.Status == "idle" {
			s.statusLocked(conv, "working", nil)
		}
	case methodForget:
		delete(s.convs, p.ThreadID)
		delete(s.transcripts, p.ThreadID)
		s.saveLocked()
	case methodCrash:
		go os.Exit(0)
	case methodWatchCount:
		count := 0
		for _, w := range s.watchers {
			if w.conversation == p.ThreadID {
				count++
			}
		}
		return count, nil, true
	default:
		return nil, nil, false
	}
	return struct{}{}, nil, true
}

func (s *appServer) toolCallLocked(conversation string, call ServerToolCall) {
	id := s.nextCallID()
	s.appendLocked(conversation, toolRequestEntry(id, call), toolResultEntry(id, call))
}

func (s *appServer) toolRequestedLocked(conversation string, call ServerToolCall) string {
	id := s.nextCallID()
	s.appendLocked(conversation, toolRequestEntry(id, call))
	return id
}

func (s *appServer) toolReturnedLocked(conversation, id string, call ServerToolCall) {
	s.appendLocked(conversation, toolResultEntry(id, call))
}

func (s *appServer) nextCallID() string {
	s.nextCall++
	return "call-" + itoa(s.nextCall)
}

func toolRequestEntry(id string, call ServerToolCall) serverEntry {
	return serverEntry{Kind: "pi.assistant", Model: []json.RawMessage{message("assistant", map[string]any{"content": []any{map[string]any{"type": "toolCall", "id": id, "name": call.Name, "arguments": call.Input}}})}}
}

func toolResultEntry(id string, call ServerToolCall) serverEntry {
	return serverEntry{Kind: "pi.tool-result", Model: []json.RawMessage{message("toolResult", map[string]any{"toolCallId": id, "toolName": call.Name, "content": []any{map[string]any{"type": "text", "text": call.Output}}, "isError": false})}}
}

func itoa(n int) string {
	raw, _ := json.Marshal(n)
	return string(raw)
}

func (s *AppServer) ToolCall(conversation string, call ServerToolCall) {
	s.t.Helper()
	s.call(methodToolCall, serverKitParams{ThreadID: conversation, Call: &call}, nil)
}

func (s *AppServer) ToolRequested(conversation string, call ServerToolCall) string {
	s.t.Helper()
	var id string
	s.call(methodToolRequested, serverKitParams{ThreadID: conversation, Call: &call}, &id)
	return id
}

func (s *AppServer) ToolReturned(conversation, id string, call ServerToolCall) {
	s.t.Helper()
	s.call(methodToolReturned, serverKitParams{ThreadID: conversation, Key: id, Call: &call}, nil)
}

func (s *AppServer) ToolCalls(conversation string, count int, call ServerToolCall) {
	s.t.Helper()
	s.call(methodAppendMany, serverKitParams{ThreadID: conversation, Call: &call, Count: count}, nil)
}

func (s *AppServer) SetUsage(conversation, model string, usage any) {
	s.t.Helper()
	s.call(methodSetUsage, serverKitParams{ThreadID: conversation, Key: model, Usage: usage}, nil)
}

func (s *AppServer) Compacting(conversation, trigger string) {
	s.t.Helper()
	s.call(methodServerCompacting, serverKitParams{ThreadID: conversation, Text: trigger}, nil)
}

func (s *AppServer) Compacted(conversation, trigger string) {
	s.t.Helper()
	s.call(methodServerCompacted, serverKitParams{ThreadID: conversation, Text: trigger}, nil)
}

func (s *AppServer) Watchers(conversation string) int {
	s.t.Helper()
	var count int
	s.call(methodWatchCount, serverKitParams{ThreadID: conversation}, &count)
	return count
}

func (s *AppServer) Crash() {
	s.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), HangGuard)
	defer cancel()
	_ = s.fake.peer.call(ctx, methodCrash, serverKitParams{}, nil)
	s.AwaitStopped()
}

func (s *AppServer) Forget(conversation string) {
	s.t.Helper()
	s.call(methodForget, serverKitParams{ThreadID: conversation}, nil)
}

func (s *AppServer) StartedElsewhere(conversation, cwd, prompt string) {
	s.t.Helper()
	s.call(methodStartedElsewhere, serverKitParams{ThreadID: conversation, Key: cwd, Text: prompt}, nil)
}

func (s *AppServer) Subagent(parent, id, title string) {
	s.t.Helper()
	s.call(methodServerSubagent, serverKitParams{ThreadID: parent, Key: id, Text: title}, nil)
}

func (s *AppServer) ForkedSubagent(parent, id, title string) {
	s.t.Helper()
	s.call(methodServerForkSubagent, serverKitParams{ThreadID: parent, Key: id, Text: title}, nil)
}

func (s *AppServer) Worker(parent, id, task string) {
	s.t.Helper()
	s.call(methodServerWorker, serverKitParams{ThreadID: parent, Key: id, Text: task}, nil)
}

func (s *AppServer) ForkedWorker(parent, id, task string) {
	s.t.Helper()
	s.call(methodServerForkWorker, serverKitParams{ThreadID: parent, Key: id, Text: task}, nil)
}

func (s *AppServer) Mail(conversation, sender, text string) {
	s.t.Helper()
	s.call(methodServerMail, serverKitParams{ThreadID: conversation, Key: sender, Text: text}, nil)
}

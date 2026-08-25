package agent

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/transcript"
)

const (
	copilotToolStartGraceTime = 1200 * time.Millisecond
	claudeHookStaleThreshold  = 2 * time.Minute
)

type TranscriptWatcherBehaviorProvider interface {
	NewTranscriptWatcherBehavior() TranscriptWatcherBehavior
}

type TranscriptWatcherBehavior interface {
	Reset()

	HandleLine(line []byte, now time.Time, sessionState protocol.SessionState) WatcherLineResult

	HandleAssistantMessage(now time.Time)

	DeduplicateAssistantEvents() bool

	QuietSince(lastAssistantAt time.Time) time.Time

	Tick(now time.Time, sessionState protocol.SessionState) WatcherTickResult

	SkipClassification(sessionState protocol.SessionState, lastSeen string, now time.Time) (bool, string)
}

type WatcherLineResult struct {
	State string
	Log   string

	// AbortAt matters because the watcher re-reads history: an undated halt
	// cannot be told from a fresh one.
	Aborted     bool
	AbortDetail string
	AbortAt     time.Time

	// Retires the evidence bracket as well as this watcher's flag: for an agent
	// with no heartbeat nothing else retires one except the stuck timer.
	BracketClosed bool
}

type WatcherTickResult struct {
	State               string
	BlockClassification bool
	Log                 string
}

func newDefaultTranscriptWatcherBehavior() TranscriptWatcherBehavior {
	return &defaultTranscriptWatcherBehavior{}
}

type defaultTranscriptWatcherBehavior struct{}

func (b *defaultTranscriptWatcherBehavior) Reset() {}

func (b *defaultTranscriptWatcherBehavior) HandleLine(line []byte, now time.Time, sessionState protocol.SessionState) WatcherLineResult {
	return WatcherLineResult{}
}

func (b *defaultTranscriptWatcherBehavior) HandleAssistantMessage(now time.Time) {}

func (b *defaultTranscriptWatcherBehavior) DeduplicateAssistantEvents() bool { return true }

func (b *defaultTranscriptWatcherBehavior) QuietSince(lastAssistantAt time.Time) time.Time {
	return lastAssistantAt
}

func (b *defaultTranscriptWatcherBehavior) Tick(now time.Time, sessionState protocol.SessionState) WatcherTickResult {
	return WatcherTickResult{}
}

func (b *defaultTranscriptWatcherBehavior) SkipClassification(sessionState protocol.SessionState, lastSeen string, now time.Time) (bool, string) {
	return false, ""
}

type claudeTranscriptWatcherBehavior struct{}

func (b *claudeTranscriptWatcherBehavior) Reset() {}

func (b *claudeTranscriptWatcherBehavior) HandleLine(line []byte, now time.Time, sessionState protocol.SessionState) WatcherLineResult {
	if abort, ok := transcript.ClaudeTurnAborted(line); ok {
		return WatcherLineResult{
			Aborted:     true,
			AbortDetail: abort.Reason,
			AbortAt:     abort.At,
			Log:         "transcript watcher: claude turn aborted by user",
		}
	}
	return WatcherLineResult{}
}

func (b *claudeTranscriptWatcherBehavior) HandleAssistantMessage(now time.Time) {}

func (b *claudeTranscriptWatcherBehavior) DeduplicateAssistantEvents() bool { return false }

func (b *claudeTranscriptWatcherBehavior) QuietSince(lastAssistantAt time.Time) time.Time {
	return lastAssistantAt
}

func (b *claudeTranscriptWatcherBehavior) Tick(now time.Time, sessionState protocol.SessionState) WatcherTickResult {
	return WatcherTickResult{}
}

func (b *claudeTranscriptWatcherBehavior) SkipClassification(sessionState protocol.SessionState, lastSeen string, now time.Time) (bool, string) {
	// A scheduled session is parked on a /loop or cron; the transcript only shows the last
	// turn, which classifies as idle. Parks routinely outlast the hook-stale threshold.
	if sessionState == protocol.SessionStateScheduled {
		return true, "transcript watcher: skipping classification, session scheduled"
	}
	if sessionState != protocol.SessionStateWorking && sessionState != protocol.SessionStatePendingApproval {
		return false, ""
	}
	parsed := protocol.Timestamp(lastSeen).Time()
	if parsed.IsZero() {
		return false, ""
	}
	if now.Sub(parsed) < claudeHookStaleThreshold {
		return true, "transcript watcher: skipping classification, hooks active"
	}
	return false, ""
}

// Codex runs the watcher only for aborted turns, the one thing its hooks do not report.
// It never classifies — a second driver would race the Stop hook over the same turn.
type codexTranscriptWatcherBehavior struct{}

func (b *codexTranscriptWatcherBehavior) Reset() {}

func (b *codexTranscriptWatcherBehavior) HandleLine(line []byte, now time.Time, sessionState protocol.SessionState) WatcherLineResult {
	abort, ok := transcript.CodexTurnAborted(line)
	if !ok {
		return WatcherLineResult{}
	}
	if !abort.UserHalt {
		return WatcherLineResult{
			Log: fmt.Sprintf("transcript watcher: codex turn aborted without a user halt reason=%s", abort.Reason),
		}
	}
	return WatcherLineResult{
		Aborted:     true,
		AbortDetail: abort.Reason,
		AbortAt:     abort.At,
		Log:         fmt.Sprintf("transcript watcher: codex turn aborted reason=%s", abort.Reason),
	}
}

func (b *codexTranscriptWatcherBehavior) HandleAssistantMessage(now time.Time) {}

func (b *codexTranscriptWatcherBehavior) DeduplicateAssistantEvents() bool { return true }

func (b *codexTranscriptWatcherBehavior) QuietSince(lastAssistantAt time.Time) time.Time {
	return lastAssistantAt
}

func (b *codexTranscriptWatcherBehavior) Tick(now time.Time, sessionState protocol.SessionState) WatcherTickResult {
	return WatcherTickResult{}
}

func (b *codexTranscriptWatcherBehavior) SkipClassification(sessionState protocol.SessionState, lastSeen string, now time.Time) (bool, string) {
	return true, "transcript watcher: skipping classification, codex classification is hook-owned"
}

type copilotPendingTool struct {
	name      string
	startedAt time.Time
}

type copilotTranscriptWatcherBehavior struct {
	turnOpen              bool
	pendingTools          map[string]copilotPendingTool
	transcriptPendingLive bool
}

func (b *copilotTranscriptWatcherBehavior) Reset() {
	b.turnOpen = false
	b.pendingTools = make(map[string]copilotPendingTool)
	b.transcriptPendingLive = false
}

func (b *copilotTranscriptWatcherBehavior) HandleLine(line []byte, now time.Time, sessionState protocol.SessionState) WatcherLineResult {
	// An abort is the one turn ending copilot does not follow with `assistant.turn_end`
	// (measured on 1.0.77), so without this Tick pins the session working for its whole life.
	if abort, ok := transcript.CopilotTurnAborted(line); ok {
		b.turnOpen = false
		b.pendingTools = make(map[string]copilotPendingTool)
		b.transcriptPendingLive = false
		if !abort.UserHalt {
			return WatcherLineResult{
				BracketClosed: true,
				Log:           fmt.Sprintf("transcript watcher: copilot turn aborted without a user halt reason=%s", abort.Reason),
			}
		}
		return WatcherLineResult{
			Aborted:     true,
			AbortDetail: abort.Reason,
			AbortAt:     abort.At,
			Log:         fmt.Sprintf("transcript watcher: copilot turn aborted reason=%s", abort.Reason),
		}
	}

	switch extractTranscriptEventType(line) {
	case "assistant.turn_start":
		b.turnOpen = true
		return WatcherLineResult{Log: "transcript watcher: copilot turn start"}
	case "assistant.turn_end":
		b.turnOpen = false
		return WatcherLineResult{Log: "transcript watcher: copilot turn end"}
	}
	evt, ok := transcript.ExtractCopilotToolLifecycle(line)
	if !ok {
		return WatcherLineResult{}
	}
	switch evt.Kind {
	case "start":
		if evt.ToolCallID != "" {
			b.pendingTools[evt.ToolCallID] = copilotPendingTool{
				name:      evt.ToolName,
				startedAt: now,
			}
			return WatcherLineResult{
				Log: fmt.Sprintf("transcript watcher: tool start tool=%s call=%s", evt.ToolName, evt.ToolCallID),
			}
		}
	case "complete":
		if evt.ToolCallID != "" {
			delete(b.pendingTools, evt.ToolCallID)
			return WatcherLineResult{
				Log: fmt.Sprintf("transcript watcher: tool complete call=%s", evt.ToolCallID),
			}
		}
	}
	return WatcherLineResult{}
}

func (b *copilotTranscriptWatcherBehavior) HandleAssistantMessage(now time.Time) {
	b.turnOpen = true
}

func (b *copilotTranscriptWatcherBehavior) DeduplicateAssistantEvents() bool { return true }

func (b *copilotTranscriptWatcherBehavior) QuietSince(lastAssistantAt time.Time) time.Time {
	return lastAssistantAt
}

func (b *copilotTranscriptWatcherBehavior) Tick(now time.Time, sessionState protocol.SessionState) WatcherTickResult {
	result := WatcherTickResult{}

	pendingFromTranscript := hasCopilotTranscriptPendingApproval(b.pendingTools, now, b.turnOpen)
	if pendingFromTranscript {
		result.BlockClassification = true
		if shouldPromoteTranscriptPending(sessionState) {
			result.State = protocol.StatePendingApproval
			result.Log = "transcript watcher: promoting pending approval from transcript"
		}
		b.transcriptPendingLive = true
		return result
	}

	if b.transcriptPendingLive {
		b.transcriptPendingLive = false
		if sessionState == protocol.SessionStatePendingApproval {
			result.State = protocol.StateWorking
			result.Log = "transcript watcher: clearing transcript pending approval"
		}
	}

	if b.turnOpen {
		result.BlockClassification = true
		if result.State == "" &&
			sessionState != protocol.SessionStateWorking &&
			sessionState != protocol.SessionStatePendingApproval {
			result.State = protocol.StateWorking
			result.Log = "transcript watcher: keeping copilot working while turn open"
		}
	}
	return result
}

func (b *copilotTranscriptWatcherBehavior) SkipClassification(sessionState protocol.SessionState, lastSeen string, now time.Time) (bool, string) {
	return false, ""
}

func isCopilotApprovalTool(toolName string) bool {
	switch strings.ToLower(strings.TrimSpace(toolName)) {
	case "bash", "create":
		return true
	default:
		return false
	}
}

func hasCopilotTranscriptPendingApproval(pending map[string]copilotPendingTool, now time.Time, turnOpen bool) bool {
	if !turnOpen {
		return false
	}
	for _, tool := range pending {
		if !isCopilotApprovalTool(tool.name) {
			continue
		}
		if !tool.startedAt.IsZero() && now.Sub(tool.startedAt) >= copilotToolStartGraceTime {
			return true
		}
	}
	return false
}

func shouldPromoteTranscriptPending(sessionState protocol.SessionState) bool {
	switch sessionState {
	case protocol.SessionStateIdle,
		protocol.SessionStateWaitingInput,
		protocol.SessionStateUnknown,
		protocol.SessionStateLaunching:
		return true
	default:
		return false
	}
}

func extractTranscriptEventType(line []byte) string {
	var evt struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(line, &evt); err != nil {
		return ""
	}
	return evt.Type
}

package agent

import (
	"fmt"
	"time"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/transcript"
)

const claudeHookStaleThreshold = 2 * time.Minute

type TranscriptWatcherBehaviorProvider interface {
	NewTranscriptWatcherBehavior() TranscriptWatcherBehavior
}

type TranscriptWatcherBehavior interface {
	HandleLine(line []byte, now time.Time, sessionState protocol.SessionState) WatcherLineResult

	DeduplicateAssistantEvents() bool

	QuietSince(lastAssistantAt time.Time) time.Time

	SkipClassification(sessionState protocol.SessionState, lastSeen string, now time.Time) (bool, string)
}

type WatcherLineResult struct {
	Log string

	Aborted     bool
	AbortDetail string
	AbortAt     time.Time
}

func newDefaultTranscriptWatcherBehavior() TranscriptWatcherBehavior {
	return &defaultTranscriptWatcherBehavior{}
}

type defaultTranscriptWatcherBehavior struct{}

func (b *defaultTranscriptWatcherBehavior) HandleLine(line []byte, now time.Time, sessionState protocol.SessionState) WatcherLineResult {
	return WatcherLineResult{}
}

func (b *defaultTranscriptWatcherBehavior) DeduplicateAssistantEvents() bool { return true }

func (b *defaultTranscriptWatcherBehavior) QuietSince(lastAssistantAt time.Time) time.Time {
	return lastAssistantAt
}

func (b *defaultTranscriptWatcherBehavior) SkipClassification(sessionState protocol.SessionState, lastSeen string, now time.Time) (bool, string) {
	return false, ""
}

type claudeTranscriptWatcherBehavior struct{}

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

func (b *claudeTranscriptWatcherBehavior) DeduplicateAssistantEvents() bool { return false }

func (b *claudeTranscriptWatcherBehavior) QuietSince(lastAssistantAt time.Time) time.Time {
	return lastAssistantAt
}

func (b *claudeTranscriptWatcherBehavior) SkipClassification(sessionState protocol.SessionState, lastSeen string, now time.Time) (bool, string) {
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

type codexTranscriptWatcherBehavior struct{}

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

func (b *codexTranscriptWatcherBehavior) DeduplicateAssistantEvents() bool { return true }

func (b *codexTranscriptWatcherBehavior) QuietSince(lastAssistantAt time.Time) time.Time {
	return time.Time{}
}

func (b *codexTranscriptWatcherBehavior) SkipClassification(sessionState protocol.SessionState, lastSeen string, now time.Time) (bool, string) {
	return true, "transcript watcher: skipping classification, codex classification is hook-owned"
}

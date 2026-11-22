package harness

import (
	"errors"
	"time"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/transcript"
)

// Transcript is the optional part of a harness whose terminal writes a transcript file attn tails.
type Transcript interface {
	Format() transcript.Format
	// Watcher reads the live transcript's lines; nil when attn does not watch this harness's transcript.
	Watcher() TranscriptWatcher
	UsageSources(root string) transcript.UsageSourceResolver
	// LastAssistant is the stop classifier's input: the last assistant message after since, and its turn.
	LastAssistant(path string, max int, since time.Time, lastTurn string) (content, turnID string, err error)
}

// ErrNoNewAssistantTurn is LastAssistant's answer when the last turn is the one already classified.
var ErrNoNewAssistantTurn = errors.New("no new assistant turn")

// TranscriptWatcher is how a harness's live transcript lines read as turn evidence.
type TranscriptWatcher interface {
	HandleLine(line []byte) WatcherLine
	DeduplicateAssistantEvents() bool
	QuietSince(lastAssistantAt time.Time) time.Time
	SkipClassification(state protocol.SessionState, lastSeen string, now time.Time) (bool, string)
}

// WatcherLine is what one transcript line told the watcher.
type WatcherLine struct {
	Log         string
	Aborted     bool
	AbortDetail string
	AbortAt     time.Time
}

// ScreenReader is the optional part of a harness whose terminal screen shows states attn reads.
type ScreenReader interface {
	ShowsFolderTrustQuestion(screen string) bool
	ShowsComposerSuggestion(snapshot protocol.GetScreenSnapshotResultMessage) bool
}

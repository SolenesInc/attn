package claude

import (
	"encoding/base64"
	"regexp"
	"sync"
	"time"

	"github.com/victorarias/attn/internal/harness"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/transcript"
)

const claudeHookStaleThreshold = 2 * time.Minute

func (claudeHarness) Format() transcript.Format { return transcript.FormatClaude }

func (h claudeHarness) Watcher() harness.TranscriptWatcher {
	if !h.Facts().Signals.StopsReadable {
		return nil
	}
	if enabled, ok := harness.CapabilityEnv(claudeID, "TRANSCRIPT_WATCHER"); ok && !enabled {
		return nil
	}
	return &claudeTranscriptWatcherBehavior{}
}

func (h claudeHarness) UsageSources(root string) transcript.UsageSourceResolver {
	return transcript.NewClaudeUsageSourceResolver(root)
}

func (h claudeHarness) LastAssistant(path string, max int, since time.Time, lastTurn string) (string, string, error) {
	return extractLastAssistantForClassification(path, max, since, lastTurn)
}

func (h claudeHarness) ShowsFolderTrustQuestion(screen string) bool {
	return showsFolderTrustQuestion(screen)
}

func (claudeHarness) ShowsComposerSuggestion(snapshot protocol.GetScreenSnapshotResultMessage) bool {
	cells, err := base64.StdEncoding.DecodeString(protocol.Deref(snapshot.ScreenSnapshot))
	return err == nil && claudeSuggestionCells().Match(cells)
}

var claudeSuggestionCells = sync.OnceValue(func() *regexp.Regexp {
	const sgr = `\x1b\[[0-9;]*m`
	return regexp.MustCompile(`❯[ \x{a0}](?:` + sgr + `)*\x1b\[(?:[0-9;]*;)?7m[^\x1b](?:` + sgr + `)*\x1b\[(?:[0-9;]*;)?2m`)
})

type claudeTranscriptWatcherBehavior struct{}

func (b *claudeTranscriptWatcherBehavior) HandleLine(line []byte) harness.WatcherLine {
	if abort, ok := transcript.ClaudeTurnAborted(line); ok {
		return harness.WatcherLine{
			Aborted:     true,
			AbortDetail: abort.Reason,
			AbortAt:     abort.At,
			Log:         "transcript watcher: claude turn aborted by user",
		}
	}
	return harness.WatcherLine{}
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

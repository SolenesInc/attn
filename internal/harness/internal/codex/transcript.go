package codex

import (
	"fmt"
	"time"

	"github.com/victorarias/attn/internal/harness"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/transcript"
)

func (codexHarness) Format() transcript.Format { return transcript.FormatCodex }

func (h codexHarness) Watcher() harness.TranscriptWatcher {
	if !h.Facts().Signals.StopsReadable {
		return nil
	}
	if enabled, ok := harness.CapabilityEnv(codexID, "TRANSCRIPT_WATCHER"); ok && !enabled {
		return nil
	}
	return &codexTranscriptWatcherBehavior{shared: h.shared}
}

func (h codexHarness) UsageSources(root string) transcript.UsageSourceResolver {
	return transcript.NewCodexUsageSourceResolver(root)
}

func (codexHarness) LastAssistant(path string, max int, _ time.Time, _ string) (string, string, error) {
	content, err := transcript.ExtractLastAssistantMessage(path, max)
	return content, "", err
}

type codexTranscriptWatcherBehavior struct {
	shared *codexShared
}

func (b *codexTranscriptWatcherBehavior) HandleLine(line []byte) harness.WatcherLine {
	abort, ok := transcript.CodexTurnAborted(line)
	if !ok {
		return harness.WatcherLine{}
	}
	if b.shared.tookOwnInterrupt(abort.Turn) {
		return harness.WatcherLine{Log: fmt.Sprintf("transcript watcher: codex turn %s ended by attn's interrupt; the next turn carries on", abort.Turn)}
	}
	if !abort.UserHalt {
		return harness.WatcherLine{
			Log: fmt.Sprintf("transcript watcher: codex turn aborted without a user halt reason=%s", abort.Reason),
		}
	}
	return harness.WatcherLine{
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

package daemon

import (
	"fmt"
	"path/filepath"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/victorarias/attn/internal/protocol"
)

func TestIdleCodexWatchersDoNotReloadSessions(t *testing.T) {
	const watcherCount = 32

	d := newTraceDaemon(t)
	var lookups atomic.Int64
	d.transcriptWatcherSessionLookup = func(sessionID string) *protocol.Session {
		lookups.Add(1)
		return d.store.Get(sessionID)
	}

	synctest.Test(t, func(t *testing.T) {
		stopDaemonBackground(t, d)
		for i := range watcherCount {
			id := fmt.Sprintf("idle-codex-%02d", i)
			addCharacterizationSession(t, d, id, protocol.SessionAgentCodex, protocol.SessionStateIdle)
			path := filepath.Join(t.TempDir(), "rollout-"+id+".jsonl")
			writeLine(t, path, fmt.Sprintf(`{"timestamp":"2026-08-28T20:00:00Z","type":"session_meta","payload":{"id":%q,"cwd":%q}}`, id, t.TempDir()))
			writeLine(t, path, `{"type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"Already finished."}]}}`)
			d.startTranscriptWatcherAtPath(id, protocol.SessionAgentCodex, t.TempDir(), time.Now(), path)
		}

		advancePolls(2)
		if got := lookups.Load(); got != watcherCount {
			t.Fatalf("session lookups after startup = %d, want %d", got, watcherCount)
		}

		advancePolls(120)
		if got := lookups.Load(); got != watcherCount {
			t.Fatalf("session lookups after one idle minute = %d, want %d", got, watcherCount)
		}

		d.stopAllTranscriptWatchers()
		synctest.Wait()
	})
}

func TestTranscriptWatcherStateTracksDaemonCommits(t *testing.T) {
	d := newTraceDaemon(t)
	id := "watcher-state"
	addCharacterizationSession(t, d, id, protocol.SessionAgentCopilot, protocol.SessionStateIdle)

	watcher := newTranscriptWatcher(id, protocol.SessionAgentCopilot, t.TempDir(), time.Now(), nil)
	watcher.setState(protocol.SessionStateIdle)
	d.transcriptWatch[id] = watcher

	if !d.applyState(sessionStateChange{
		sessionID: id,
		state:     protocol.StateWorking,
		cause:     startupRecovery{},
	}) {
		t.Fatal("daemon state commit was rejected")
	}
	if got := watcher.state(); got != protocol.SessionStateWorking {
		t.Fatalf("watcher state = %q, want working after daemon commit", got)
	}
}

func TestIsTranscriptWatchedAgent(t *testing.T) {
	if !isTranscriptWatchedAgent(protocol.SessionAgentClaude) {
		t.Fatal("claude should be transcript-watched")
	}
	if !isTranscriptWatchedAgent(protocol.SessionAgentCodex) {
		t.Fatal("codex should be transcript-watched so a halted turn is seen")
	}
	if !isTranscriptWatchedAgent(protocol.SessionAgentCopilot) {
		t.Fatal("copilot should be transcript-watched")
	}
}

// Discovery walks the agent's whole session tree and repeats until it lands: a
// session that never gets a transcript would walk thousands of files twice a second.
func TestTranscriptDiscoveryBacksOff(t *testing.T) {
	if got := transcriptDiscoveryDelay(1); got != 0 {
		t.Fatalf("first attempts should retry on the next poll, got %s", got)
	}
	if got := transcriptDiscoveryDelay(transcriptDiscoveryFastAttempts - 1); got != 0 {
		t.Fatalf("a transcript still plausibly being created should be looked for eagerly, got %s", got)
	}
	if got := transcriptDiscoveryDelay(transcriptDiscoveryFastAttempts); got != transcriptDiscoverySlowInterval {
		t.Fatalf("delay = %s, want %s once the eager window is spent", got, transcriptDiscoverySlowInterval)
	}
	if got := transcriptDiscoveryDelay(transcriptDiscoverySlowAttempts); got != transcriptDiscoveryIdleInterval {
		t.Fatalf("delay = %s, want %s for a session that is never getting one", got, transcriptDiscoveryIdleInterval)
	}
	eager := time.Duration(transcriptDiscoveryFastAttempts) * transcriptPollInterval
	if eager < 5*time.Second || eager > 30*time.Second {
		t.Fatalf("eager discovery window is %s, which is outside the range any agent takes to start writing", eager)
	}
}

func TestIsTranscriptWatchedAgent_CapabilityOverride(t *testing.T) {
	t.Setenv("ATTN_AGENT_CLAUDE_TRANSCRIPT", "0")
	if isTranscriptWatchedAgent(protocol.SessionAgentClaude) {
		t.Fatal("claude transcript watching should be disabled by capability override")
	}
}

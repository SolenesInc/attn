package daemon

import (
	"fmt"
	"path/filepath"
	"sync"
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

func TestTranscriptWatcherInstallDoesNotMissConcurrentStateCommit(t *testing.T) {
	d := newTraceDaemon(t)
	id := "watcher-state-handoff"
	addCharacterizationSession(t, d, id, protocol.SessionAgentCodex, protocol.SessionStateIdle)

	synctest.Test(t, func(t *testing.T) {
		stopDaemonBackground(t, d)
		lookupCaptured := make(chan struct{})
		releaseLookup := make(chan struct{})
		var releaseOnce sync.Once
		release := func() { releaseOnce.Do(func() { close(releaseLookup) }) }
		d.transcriptWatcherSessionLookup = func(sessionID string) *protocol.Session {
			session := d.store.Get(sessionID)
			close(lookupCaptured)
			<-releaseLookup
			return session
		}

		startDone := make(chan struct{})
		go func() {
			d.startTranscriptWatcher(id, protocol.SessionAgentCodex, t.TempDir(), time.Now())
			close(startDone)
		}()
		<-lookupCaptured

		if d.watchersMu.TryLock() {
			d.watchersMu.Unlock()
			release()
			<-startDone
			d.stopTranscriptWatcher(id)
			t.Fatal("watcher installation did not serialize the state handoff")
		}
		if !d.store.UpdateState(id, protocol.StateWorking) {
			t.Fatal("durable state commit was rejected")
		}

		updateStarted := make(chan struct{})
		updateDone := make(chan struct{})
		go func() {
			close(updateStarted)
			d.updateTranscriptWatcherState(id, protocol.SessionStateWorking)
			close(updateDone)
		}()
		<-updateStarted

		release()
		<-startDone
		<-updateDone
		d.watchersMu.Lock()
		watcher := d.transcriptWatch[id]
		d.watchersMu.Unlock()
		if watcher == nil {
			t.Fatal("watcher was not installed")
		}
		if got := watcher.state(); got != protocol.SessionStateWorking {
			t.Fatalf("watcher state = %q, want concurrent durable state working", got)
		}
		d.stopTranscriptWatcher(id)
		synctest.Wait()
	})
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

func TestTranscriptDiscoveryStopsAfterOneCompatibilityLookup(t *testing.T) {
	d := newTraceDaemon(t)
	var lookups atomic.Int64
	d.transcriptResumeLookup = func(protocol.SessionAgent, string) string {
		lookups.Add(1)
		return ""
	}

	synctest.Test(t, func(t *testing.T) {
		stopDaemonBackground(t, d)
		id := "missing-transcript"
		addCharacterizationSession(t, d, id, protocol.SessionAgentCodex, protocol.SessionStateWorking)
		d.store.SetResumeSessionID(id, "native-missing")
		d.startTranscriptWatcher(id, protocol.SessionAgentCodex, t.TempDir(), time.Now())
		d.watchersMu.Lock()
		watcher := d.transcriptWatch[id]
		d.watchersMu.Unlock()

		advancePolls(1)
		requireDone(t, watcher.doneCh, "watcher kept retrying after its compatibility lookup")
		if got := lookups.Load(); got != 1 {
			t.Fatalf("compatibility lookups = %d, want 1", got)
		}
		advancePolls(120)
		if got := lookups.Load(); got != 1 {
			t.Fatalf("compatibility lookups after one idle minute = %d, want 1", got)
		}
	})
}

func TestTranscriptDiscoveryGivesAPersistedPathOnlyTheCreationGrace(t *testing.T) {
	d := newTraceDaemon(t)
	var lookups atomic.Int64
	d.transcriptResumeLookup = func(protocol.SessionAgent, string) string {
		lookups.Add(1)
		return ""
	}

	synctest.Test(t, func(t *testing.T) {
		stopDaemonBackground(t, d)
		id := "delayed-transcript"
		path := filepath.Join(t.TempDir(), "rollout-native-delayed.jsonl")
		addCharacterizationSession(t, d, id, protocol.SessionAgentCodex, protocol.SessionStateWorking)
		if changed, err := d.store.TransitionSessionConversation(id, "native-delayed", path); err != nil || !changed {
			t.Fatalf("seed binding: changed=%v err=%v", changed, err)
		}
		d.startTranscriptWatcherAtPath(id, protocol.SessionAgentCodex, t.TempDir(), time.Now(), path)
		d.watchersMu.Lock()
		watcher := d.transcriptWatch[id]
		d.watchersMu.Unlock()

		advancePolls(3)
		if got := lookups.Load(); got != 0 {
			t.Fatalf("compatibility lookup ran during creation grace: %d", got)
		}
		advancePolls(2)
		requireDone(t, watcher.doneCh, "watcher kept retrying after creation grace")
		if got := lookups.Load(); got != 1 {
			t.Fatalf("compatibility lookups after grace = %d, want 1", got)
		}
	})
}

func TestIsTranscriptWatchedAgent_CapabilityOverride(t *testing.T) {
	t.Setenv("ATTN_AGENT_CLAUDE_TRANSCRIPT", "0")
	if isTranscriptWatchedAgent(protocol.SessionAgentClaude) {
		t.Fatal("claude transcript watching should be disabled by capability override")
	}
}

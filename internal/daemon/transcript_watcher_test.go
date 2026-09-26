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

package daemon

import (
	"errors"
	"os"
	"strings"

	agentdriver "github.com/victorarias/attn/internal/agent"
	"github.com/victorarias/attn/internal/sessioncost"
	"github.com/victorarias/attn/internal/store"
	"github.com/victorarias/attn/internal/transcript"
)

type sessionUsageTracker struct {
	daemon    *Daemon
	sessionID string
	agent     string
	resolver  transcript.UsageSourceResolver
	rootPath  string
	sources   map[string]*trackedUsageSource
}

type trackedUsageSource struct {
	source   transcript.UsageSource
	follower *transcript.Follower
	info     os.FileInfo
}

func (d *Daemon) newSessionUsageTracker(w *transcriptWatcher, rootPath string) *sessionUsageTracker {
	driver := agentdriver.Get(string(w.agent))
	provider, ok := agentdriver.GetTranscriptUsageSourceProvider(driver)
	if !ok || !transcript.SupportsUsage(string(w.agent)) {
		return nil
	}
	resolver := provider.NewTranscriptUsageSourceResolver(rootPath)
	if resolver == nil {
		return nil
	}
	return newSessionUsageTrackerAt(d, w.sessionID, string(w.agent), rootPath, resolver)
}

func newSessionUsageTrackerAt(
	d *Daemon, sessionID, agent, rootPath string, resolver transcript.UsageSourceResolver,
) *sessionUsageTracker {
	return &sessionUsageTracker{
		daemon: d, sessionID: sessionID, agent: agent, resolver: resolver, rootPath: rootPath,
		sources: make(map[string]*trackedUsageSource),
	}
}

func (t *sessionUsageTracker) Reconcile() {
	sources, err := t.resolver.Discover()
	if err != nil {
		t.daemon.logf("transcript watcher: usage source discovery failed session=%s err=%v", t.sessionID, err)
		t.markIncomplete()
		return
	}
	state, err := t.daemon.store.SessionCost(t.sessionID)
	if err != nil {
		t.daemon.logf("transcript watcher: usage state read failed session=%s err=%v", t.sessionID, err)
		return
	}

	legacyBaseline := state.Initialized && len(state.Sources) == 0 && strings.TrimSpace(state.Cursor) != ""
	if !state.Initialized || legacyBaseline {
		cursors := make(map[string]string, len(sources))
		baselineFailed := false
		for _, source := range sources {
			if legacyBaseline && source.Root {
				cursors[source.ID] = state.Cursor
				continue
			}
			cursor, cursorErr := transcript.HeadCursor(source.Path)
			if cursorErr != nil {
				t.daemon.logf("transcript watcher: usage baseline failed session=%s path=%s err=%v", t.sessionID, source.Path, cursorErr)
				t.markIncomplete()
				baselineFailed = true
				continue
			}
			cursors[source.ID] = cursor
		}
		if baselineFailed {
			return
		}
		if err := t.daemon.store.InitializeSessionCostSources(t.sessionID, cursors); err != nil {
			t.daemon.logf("transcript watcher: usage baseline persist failed session=%s err=%v", t.sessionID, err)
			return
		}
		state, err = t.daemon.store.SessionCost(t.sessionID)
		if err != nil {
			return
		}
	}

	discovered := make(map[string]struct{}, len(sources))
	for _, source := range sources {
		discovered[source.ID] = struct{}{}
		sourceState, exists := state.Sources[source.ID]
		if !exists {
			cursor := ""
			if err := t.daemon.store.InitializeSessionCostSources(t.sessionID, map[string]string{source.ID: cursor}); err != nil {
				t.daemon.logf("transcript watcher: usage source persist failed session=%s path=%s err=%v", t.sessionID, source.Path, err)
				continue
			}
			sourceState = store.SessionCostSourceState{Cursor: cursor}
			if state.Sources == nil {
				state.Sources = make(map[string]store.SessionCostSourceState)
			}
			state.Sources[source.ID] = sourceState
		}
		tracked := t.sources[source.ID]
		if tracked == nil {
			tracked = &trackedUsageSource{source: source}
			tracked.follower, err = usageFollowerAt(source.Path, t.agent, sourceState.Cursor)
			if err != nil {
				if !isUsageCursorError(err) {
					t.daemon.logf("transcript watcher: usage follower init failed session=%s path=%s err=%v", t.sessionID, source.Path, err)
					t.markIncomplete()
					continue
				}
				if !t.resetAtHead(tracked) {
					continue
				}
			}
			t.sources[source.ID] = tracked
		}
		t.readIfMoved(tracked)
	}

	for id := range t.sources {
		if _, ok := discovered[id]; !ok {
			delete(t.sources, id)
			t.markIncomplete()
		}
	}
}

func usageFollowerAt(path, agent, cursor string) (*transcript.Follower, error) {
	if strings.TrimSpace(cursor) == "" {
		return transcript.NewFollower(path, agent, 0)
	}
	return transcript.NewFollowerAfterCursor(path, agent, cursor)
}

func (t *sessionUsageTracker) readIfMoved(tracked *trackedUsageSource) {
	info, err := os.Stat(tracked.source.Path)
	if err != nil {
		t.daemon.logf("transcript watcher: usage source unavailable session=%s path=%s err=%v", t.sessionID, tracked.source.Path, err)
		t.markIncomplete()
		return
	}
	if tracked.info != nil && info.Size() == tracked.info.Size() &&
		info.ModTime().Equal(tracked.info.ModTime()) && os.SameFile(info, tracked.info) {
		return
	}
	batch, err := tracked.follower.Read()
	if err != nil {
		if isUsageCursorError(err) {
			t.resetAtHead(tracked)
			return
		}
		t.daemon.logf("transcript watcher: usage source read failed session=%s path=%s err=%v", t.sessionID, tracked.source.Path, err)
		t.markIncomplete()
		return
	}
	tracked.info = info
	observations := make([]store.SessionCostObservation, 0, len(batch.Usage))
	for _, usage := range batch.Usage {
		model := strings.TrimSpace(usage.Model)
		if model == "" {
			model = "<unknown>"
		}
		observationID := usage.Key
		if !tracked.source.Root {
			observationID = "native:" + tracked.source.ID + ":" + usage.Key
		}
		observations = append(observations, store.SessionCostObservation{
			ObservationID: observationID,
			Model:         model,
			Purpose:       usage.Purpose,
			Usage: sessioncost.Usage{
				InputTokens:                  usage.InputTokens,
				OutputTokens:                 usage.OutputTokens,
				CacheReadInputTokens:         usage.CacheReadTokens,
				CacheWrite5mInputTokens:      usage.CacheWrite5mTokens,
				CacheWrite1hInputTokens:      usage.CacheWrite1hTokens,
				UnclassifiedCacheWriteTokens: usage.CacheWriteUnclassifiedTokens,
				ReportedCostUSD:              usage.ReportedCostUSD,
			},
		})
	}
	changed, err := t.daemon.store.ApplySessionCostSourceObservations(
		t.sessionID, tracked.source.ID, tracked.follower.Cursor(), observations,
	)
	if err != nil {
		t.daemon.logf("transcript watcher: usage source persist failed session=%s path=%s err=%v", t.sessionID, tracked.source.Path, err)
		t.markIncomplete()
		return
	}
	if changed {
		t.daemon.publishFact(FactSessionCostChanged, t.sessionID, nil)
	}
}

func (t *sessionUsageTracker) resetAtHead(tracked *trackedUsageSource) bool {
	t.markIncomplete()
	cursor, err := transcript.HeadCursor(tracked.source.Path)
	if err != nil {
		return false
	}
	if err := t.daemon.store.SetSessionCostSourceCursor(t.sessionID, tracked.source.ID, cursor); err != nil {
		return false
	}
	tracked.follower, err = usageFollowerAt(tracked.source.Path, t.agent, cursor)
	tracked.info = nil
	return err == nil
}

func (t *sessionUsageTracker) markIncomplete() {
	changed, err := t.daemon.store.MarkSessionCostMeasurementIncomplete(t.sessionID)
	if err != nil {
		t.daemon.logf("transcript watcher: usage incomplete persist failed session=%s err=%v", t.sessionID, err)
		return
	}
	if changed {
		t.daemon.publishFact(FactSessionCostChanged, t.sessionID, nil)
	}
}

func isUsageCursorError(err error) bool {
	return errors.Is(err, transcript.ErrCursorMismatch) ||
		errors.Is(err, transcript.ErrCursorPastEnd) ||
		errors.Is(err, transcript.ErrInvalidCursor)
}

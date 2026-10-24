package sessionmonitor

import (
	"errors"
	"os"
	"strings"

	agentdriver "github.com/victorarias/attn/internal/agent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/sessioncost"
	"github.com/victorarias/attn/internal/transcript"
)

type usageTracker struct {
	entry    *Entry
	agent    string
	resolver transcript.UsageSourceResolver
	rootPath string
	sources  map[string]*trackedUsageSource
}

type trackedUsageSource struct {
	source   transcript.UsageSource
	follower *transcript.Follower
	info     os.FileInfo
}

func newUsageTracker(w *transcriptWatcher, rootPath string) *usageTracker {
	driver := agentdriver.Get(string(w.agent))
	provider, ok := agentdriver.GetTranscriptUsageSourceProvider(driver)
	if !ok || !transcript.SupportsUsage(string(w.agent)) {
		return nil
	}
	resolver := provider.NewTranscriptUsageSourceResolver(rootPath)
	if resolver == nil {
		return nil
	}
	return newUsageTrackerAt(w.entry, string(w.agent), rootPath, resolver)
}

func newUsageTrackerAt(entry *Entry, agent string, rootPath string, resolver transcript.UsageSourceResolver) *usageTracker {
	return &usageTracker{entry: entry, agent: agent, resolver: resolver, rootPath: rootPath, sources: make(map[string]*trackedUsageSource)}
}

func (t *usageTracker) Reconcile() {
	if t.reconcile() {
		t.entry.publishCost()
	}
}

func (t *usageTracker) reconcile() (changed bool) {
	sources, err := t.resolver.Discover()
	if err != nil {
		t.entry.logf("transcript watcher: usage source discovery failed session=%s err=%v", t.entry.Label(), err)
		return t.markIncomplete()
	}
	state := t.entry.costCheckpoint()
	legacy := state.Sources[sessioncost.LegacySource]
	legacyBaseline := legacy != nil && strings.TrimSpace(legacy.Cursor) != ""
	if !state.Initialized || legacyBaseline {
		cursors := make(map[string]string, len(sources))
		baselineFailed := false
		for _, source := range sources {
			if legacyBaseline && source.Root {
				cursors[source.ID] = legacy.Cursor
				continue
			}
			cursor, cursorErr := transcript.HeadCursor(source.Path)
			if cursorErr != nil {
				t.entry.logf("transcript watcher: usage baseline failed session=%s path=%s err=%v", t.entry.Label(), source.Path, cursorErr)
				changed = t.markIncomplete() || changed
				baselineFailed = true
				continue
			}
			cursors[source.ID] = cursor
		}
		if baselineFailed {
			return changed
		}
		changed = t.entry.initializeCostSources(cursors, legacyBaseline)
		state = t.entry.costCheckpoint()
	}

	discovered := make(map[string]struct{}, len(sources))
	for _, source := range sources {
		discovered[source.ID] = struct{}{}
		sourceState, exists := state.Sources[source.ID]
		if !exists || !sourceState.Initialized {
			changed = t.entry.initializeCostSources(map[string]string{source.ID: ""}, false) || changed
			sourceState = t.entry.costCheckpoint().Sources[source.ID]
		}
		tracked := t.sources[source.ID]
		if tracked != nil && tracked.source.Path != source.Path {
			tracked = nil
		}
		if tracked == nil {
			tracked = &trackedUsageSource{source: source}
			tracked.follower, err = usageFollowerAt(source.Path, t.agent, sourceState.Cursor)
			if err != nil {
				if !isUsageCursorError(err) {
					t.entry.logf("transcript watcher: usage follower init failed session=%s path=%s err=%v", t.entry.Label(), source.Path, err)
					changed = t.markIncomplete() || changed
					continue
				}
				reset, ok := t.resetAtHead(tracked)
				changed = reset || changed
				if !ok {
					continue
				}
			}
			t.sources[source.ID] = tracked
		}
		changed = t.readIfMoved(tracked) || changed
	}

	for id := range t.sources {
		if _, ok := discovered[id]; !ok {
			delete(t.sources, id)
			changed = t.markIncomplete() || changed
		}
	}
	return changed
}

func usageFollowerAt(path, agent, cursor string) (*transcript.Follower, error) {
	if strings.TrimSpace(cursor) == "" {
		return transcript.NewFollower(path, agent, 0)
	}
	return transcript.NewFollowerAfterCursor(path, agent, cursor)
}

func (t *usageTracker) readIfMoved(tracked *trackedUsageSource) (changed bool) {
	info, err := os.Stat(tracked.source.Path)
	if err != nil {
		t.entry.logf("transcript watcher: usage source unavailable session=%s path=%s err=%v", t.entry.Label(), tracked.source.Path, err)
		return t.markIncomplete()
	}
	if tracked.info != nil && info.Size() == tracked.info.Size() &&
		info.ModTime().Equal(tracked.info.ModTime()) && os.SameFile(info, tracked.info) {
		return false
	}
	batch, err := tracked.follower.Read()
	if err != nil {
		if isUsageCursorError(err) {
			changed, _ = t.resetAtHead(tracked)
			return changed
		}
		t.entry.logf("transcript watcher: usage source read failed session=%s path=%s err=%v", t.entry.Label(), tracked.source.Path, err)
		return t.markIncomplete()
	}
	tracked.info = info
	return t.entry.applyCostObservations(tracked.source.ID, tracked.follower.Cursor(), usageObservations(tracked.source, batch.Usage))
}

func usageObservations(source transcript.UsageSource, usages []transcript.TokenUsage) []sessioncost.Observation {
	observations := make([]sessioncost.Observation, 0, len(usages))
	for _, usage := range usages {
		model := strings.TrimSpace(usage.Model)
		if model == "" {
			model = "<unknown>"
		}
		purpose := usage.Purpose
		if purpose == "" {
			purpose = source.Purpose
		}
		observationID := usage.Key
		if !source.Root {
			observationID = "native:" + source.ID + ":" + usage.Key
		}
		observations = append(observations, sessioncost.Observation{
			ObservationID: observationID,
			Model:         model,
			Purpose:       purpose,
			FastMode:      usage.FastMode,
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
	return observations
}

func (t *usageTracker) resetAtHead(tracked *trackedUsageSource) (changed, ok bool) {
	changed = t.markIncomplete()
	cursor, err := transcript.HeadCursor(tracked.source.Path)
	if err != nil {
		return changed, false
	}
	changed = t.entry.markCostSource(tracked.source.ID, true, false, &cursor) || changed
	tracked.follower, err = usageFollowerAt(tracked.source.Path, t.agent, cursor)
	tracked.info = nil
	return changed, err == nil
}

func (t *usageTracker) markIncomplete() bool {
	return t.entry.markCostSource(t.rootPath, true, false, nil)
}

func isUsageCursorError(err error) bool {
	return errors.Is(err, transcript.ErrCursorMismatch) ||
		errors.Is(err, transcript.ErrCursorPastEnd) ||
		errors.Is(err, transcript.ErrInvalidCursor)
}

func (e *Entry) FinishAndSettleUsage(agent protocol.SessionAgent, path string) func() {
	release, tracker := e.finishUsage()
	if tracker == nil && e != nil && path != "" {
		tracker = newUsageTracker(&transcriptWatcher{entry: e, agent: agent}, path)
	}
	if tracker != nil {
		tracker.Reconcile()
	}
	return release
}

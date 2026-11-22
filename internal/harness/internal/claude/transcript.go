package claude

import (
	"encoding/base64"
	"regexp"
	"sync"
	"time"

	agentdriver "github.com/victorarias/attn/internal/agent"
	"github.com/victorarias/attn/internal/harness"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/transcript"
)

func (h claudeHarness) Watcher() harness.TranscriptWatcher {
	watcher, _ := agentdriver.GetTranscriptWatcherBehavior(h.driver())
	return watcher
}

func (h claudeHarness) UsageSources(root string) transcript.UsageSourceResolver {
	return h.cli().NewTranscriptUsageSourceResolver(root)
}

func (h claudeHarness) LastAssistant(path string, max int, since time.Time, lastTurn string) (string, string, error) {
	return h.cli().ExtractLastAssistantForClassification(path, max, since, lastTurn)
}

func (h claudeHarness) ShowsFolderTrustQuestion(screen string) bool {
	return h.cli().ShowsFolderTrustQuestion(screen)
}

func (claudeHarness) ShowsComposerSuggestion(snapshot protocol.GetScreenSnapshotResultMessage) bool {
	cells, err := base64.StdEncoding.DecodeString(protocol.Deref(snapshot.ScreenSnapshot))
	return err == nil && claudeSuggestionCells().Match(cells)
}

var claudeSuggestionCells = sync.OnceValue(func() *regexp.Regexp {
	const sgr = `\x1b\[[0-9;]*m`
	return regexp.MustCompile(`❯[ \x{a0}](?:` + sgr + `)*\x1b\[(?:[0-9;]*;)?7m[^\x1b](?:` + sgr + `)*\x1b\[(?:[0-9;]*;)?2m`)
})

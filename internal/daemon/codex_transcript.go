package daemon

import (
	"time"

	agentdriver "github.com/victorarias/attn/internal/agent"
	"github.com/victorarias/attn/internal/harness"
	"github.com/victorarias/attn/internal/transcript"
)

func (h codexHarness) Watcher() harness.TranscriptWatcher {
	watcher, _ := agentdriver.GetTranscriptWatcherBehavior(h.driver())
	return watcher
}

func (h codexHarness) UsageSources(root string) transcript.UsageSourceResolver {
	return h.driver().(*agentdriver.Codex).NewTranscriptUsageSourceResolver(root)
}

func (codexHarness) LastAssistant(path string, max int, _ time.Time, _ string) (string, string, error) {
	content, err := transcript.ExtractLastAssistantMessage(path, max)
	return content, "", err
}

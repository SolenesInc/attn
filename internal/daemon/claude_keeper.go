package daemon

import (
	"io/fs"
	"strings"

	"github.com/google/uuid"

	agentdriver "github.com/victorarias/attn/internal/agent"
	"github.com/victorarias/attn/internal/harness"
)

func (h claudeHarness) ConversationFiles(ids []string) map[string][]string {
	return h.driver().(agentdriver.ConversationKeeper).ConversationFiles(ids)
}

func (claudeHarness) ClassifyFile(id, name string) harness.KeptFile {
	parsed, err := uuid.Parse(id)
	if err != nil || parsed.String() != id || !fs.ValidPath(name) {
		return harness.KeptFileNone
	}
	parts := strings.Split(name, "/")
	switch {
	case len(parts) == 4 && parts[0] == ".claude" && parts[1] == "projects" && parts[3] == id+".jsonl":
		return harness.KeptFileMain
	case len(parts) >= 3 && parts[0] == ".claude" && parts[1] == "file-history" && parts[2] == id,
		len(parts) >= 4 && parts[0] == ".claude" && parts[1] == "projects" && parts[3] == id:
		return harness.KeptFileAuxiliary
	}
	return harness.KeptFileNone
}

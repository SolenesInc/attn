package daemon_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
)

func TestTheAttnSkillIsInstalledForEachAvailableAgent(t *testing.T) {
	w := newWorld(t, fakeagent.Claude, fakeagent.Codex)
	settings := w.App().Initial.Settings
	toolHome := os.Getenv("ATTN_TOOL_HOME")
	for _, tc := range []struct{ available, dir string }{
		{"claude_available", ".claude"},
		{"codex_available", ".agents"},
	} {
		if got := settings[tc.available]; got != "true" {
			t.Errorf("the app is told %s = %v, want true", tc.available, got)
		}
		for _, file := range []string{"SKILL.md", filepath.Join("references", "delegation.md")} {
			if _, err := os.Stat(filepath.Join(toolHome, tc.dir, "skills", "attn", file)); err != nil {
				t.Errorf("the attn skill for %s is missing %s: %v", tc.dir, file, err)
			}
		}
	}
}

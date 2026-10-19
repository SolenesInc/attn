package daemon

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func readDoorbellScreen(t *testing.T, name string) string {
	t.Helper()
	text, err := os.ReadFile(filepath.Join("testdata", "doorbell", name+".txt"))
	if err != nil {
		t.Fatalf("read screen fixture %s: %v", name, err)
	}
	return string(text)
}

func TestScreenShowsSelector(t *testing.T) {
	selectorFooter := "Enter to select · Esc to cancel\n"
	for _, tc := range []struct {
		name    string
		screen  string
		blocked bool
	}{
		{name: "claude question selector", screen: readDoorbellScreen(t, "claude-question-selector"), blocked: true},
		{name: "claude resume selector", screen: readDoorbellScreen(t, "claude-resume-selector"), blocked: true},
		{name: "a selector footer under blank padding", screen: selectorFooter + strings.Repeat("\n", 9), blocked: true},
		{name: "claude composer while working", screen: readDoorbellScreen(t, "claude-composer-working")},
		{name: "claude composer at rest", screen: readDoorbellScreen(t, "claude-composer-idle")},
		{name: "a selector scrolled out of the footer", screen: selectorFooter + strings.Repeat("a line of ordinary output\n", doorbellScreenTailLines)},
		{name: "thinking that mentions a choice", screen: "∴ Let me check the AGENTS.md rules to confirm this is a docs-only PR, then start"},
		{name: "prose about picking", screen: "  I will pick the branch to rebase onto and then open the PR."},
		{name: "prose about choosing", screen: "  Waiting for you to choose which one to keep."},
		{name: "an empty screen", screen: ""},
	} {
		line, blocked := screenShowsSelector(tc.screen)
		if blocked != tc.blocked {
			t.Errorf("%s: blocked = %v on %q, want %v", tc.name, blocked, line, tc.blocked)
			continue
		}
		lower := strings.ToLower(line)
		if blocked && !strings.Contains(lower, "to select") && !strings.Contains(lower, "esc to cancel") {
			t.Errorf("%s: named %q as the selector line, which says neither", tc.name, line)
		}
	}
}

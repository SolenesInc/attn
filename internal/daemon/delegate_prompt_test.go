package daemon

import (
	"strings"
	"testing"
)

func TestDelegatedSeedPromptReferencesSeedWithoutCopyingTask(t *testing.T) {
	got := delegatedSeedPrompt("s-abc123")
	if got == "" || !strings.Contains(got, "attn seed show s-abc123") || strings.Contains(got, "Fix the launch guidance") {
		t.Fatal(got)
	}
}

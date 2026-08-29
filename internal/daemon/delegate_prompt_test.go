package daemon

import "testing"

func TestDelegatedBriefPromptCarriesOnlyTheSeedReportingContract(t *testing.T) {
	const want = `Fix the launch guidance.

---
Your work is seed ` + "`s-abc123`" + ` in the garden — the brief above is its body, and
you are its tender. Read the body and log with:

    attn seed show s-abc123

Report progress, what you learned, and decisions needed on the log:

    attn seed note s-abc123 -m "<what happened and what you learned>"

Harvest the seed when the outcome and required verification in its body are
complete:

    attn seed harvest s-abc123 -m "<what got done>"`

	if got := delegatedBriefPrompt("  Fix the launch guidance.  ", "s-abc123"); got != want {
		t.Fatalf("delegatedBriefPrompt = %q, want %q", got, want)
	}
}

func TestDelegatedBriefPromptWithoutSeedIsJustTheBrief(t *testing.T) {
	if got := delegatedBriefPrompt("  Work on the outpost.  ", ""); got != "Work on the outpost." {
		t.Fatalf("outpost brief = %q", got)
	}
}

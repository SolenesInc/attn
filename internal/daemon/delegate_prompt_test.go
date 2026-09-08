package daemon

import (
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/protocol"
)

func TestDelegatedBriefPromptCarriesSeedContextAndReporting(t *testing.T) {
	const want = `Fix the launch guidance.

---
Your work is seed ` + "`s-abc123`" + ` in the garden, and you are its tender.
Before working, read its current body and log:

    attn seed show s-abc123

Follow the body's references to the parent plan, sibling results or artifacts
you need for this assignment.

Report an update when it helps the delegator or a future tender understand the
current state, steer the work, or continue it later. This includes meaningful
progress, material findings, changes in direction or scope, blockers, and
decisions needed. Combine closely related developments into one note, and avoid
command-by-command or test-by-test narration. Add ` + "`--ring`" + ` only when the
delegator needs to respond now.

    attn seed note s-abc123 -m "<useful update>"

Before your final response, record the result on the seed. If the outcome, key
evidence or verification, artifact locations, and unresolved work fit in the
harvest reason, put them there. Otherwise, write one result note with the
necessary detail, then harvest with a concise summary. Attach or link long
findings as a durable artifact and name it in the result.

    attn seed harvest s-abc123 -m "<concise result for the delegator>"

Then give the user a useful final response in this session. State the outcome
and any next step; do not make them inspect the seed to learn what happened.`

	if got := delegatedBriefPrompt("  Fix the launch guidance.  ", "s-abc123"); got != want {
		t.Fatalf("delegatedBriefPrompt = %q, want %q", got, want)
	}
}

func TestDelegatedBriefPromptWithoutSeedIsJustTheBrief(t *testing.T) {
	if got := delegatedBriefPrompt("  Work on the outpost.  ", ""); got != "Work on the outpost." {
		t.Fatalf("outpost brief = %q", got)
	}
}

func TestDelegatedPullRequestReceiptPromptRecordsVerifiedLaunch(t *testing.T) {
	receipt := &protocol.DelegatePullRequestReceipt{
		URL: "https://github.com/owner/repo/pull/42", Number: 42, State: "open",
		BaseRepository: "github.com/owner/repo", HeadRepository: "github.com/fork/repo",
		HeadBranch: "feature", HeadSHA: "abc123", LocalBranch: "feature",
		WorktreePath: "/work/repo--feature", VerifiedHead: "abc123", Disposition: "preserved",
		BackupBranch: protocol.Ptr("feature--attn-backup-20260908T120000Z"),
		BackupHead:   protocol.Ptr("def456"),
	}
	got := delegatedPullRequestReceiptPrompt(receipt)
	for _, want := range []string{
		"Pull request checkout receipt",
		"https://github.com/owner/repo/pull/42 (#42, open)",
		"Resolved head: abc123",
		"Verified HEAD: abc123",
		"Preserved prior state: feature--attn-backup-20260908T120000Z @ def456",
		"Follow-up messages, resumes, and handovers do not synchronize it again.",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("receipt prompt missing %q:\n%s", want, got)
		}
	}
}

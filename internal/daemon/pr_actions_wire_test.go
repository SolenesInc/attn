package daemon_test

import (
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/github/mockserver"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestApprovingAndMergingFromTheDashboardAskGitHubForExactlyThat(t *testing.T) {
	gh := mockserver.New()
	t.Cleanup(gh.Close)
	gh.AddPR(mockserver.MockPR{Repo: "acme/shop", Number: 7, Title: "Add checkout", Role: "reviewer"})
	t.Setenv("ATTN_MOCK_GH_URL", gh.URL)
	t.Setenv("ATTN_MOCK_GH_TOKEN", "test-token")
	w := newWorld(t)
	app := w.App()
	refreshPRs(t, app)
	pr := prNumbered(t, queryPRs(t, w.Client(), ""), 7)
	act := func(cmd any) protocol.PRActionResultMessage {
		return testworld.Request(app, cmd, protocol.EventPRActionResult, func(r protocol.PRActionResultMessage) bool { return r.ID == pr.ID })
	}

	if approved := act(protocol.ApprovePRMessage{Cmd: protocol.CmdApprovePR, ID: pr.ID}); !approved.Success || !gh.HasApproveRequest("acme/shop", 7) {
		t.Errorf("approving answered %+v; GitHub saw an approval: %v", approved, gh.HasApproveRequest("acme/shop", 7))
	}
	if refused := act(protocol.MergePRMessage{Cmd: protocol.CmdMergePR, ID: pr.ID, Method: "yolo"}); refused.Success || !strings.Contains(protocol.Deref(refused.Error), "yolo") {
		t.Errorf("merging with method yolo answered %+v, want a refusal naming it", refused)
	}
	if merged := act(protocol.MergePRMessage{Cmd: protocol.CmdMergePR, ID: pr.ID, Method: "squash"}); !merged.Success || !gh.HasMergeRequest("acme/shop", 7, "squash") {
		t.Errorf("squash merging answered %+v; GitHub saw a squash merge: %v", merged, gh.HasMergeRequest("acme/shop", 7, "squash"))
	}
}

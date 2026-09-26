package daemon_test

import (
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
)

func TestAMalformedDelegationIsRefusedAndLeavesNoOperation(t *testing.T) {
	w := newWorld(t)
	cli := w.Client()
	if saved := savePreferences(w.App(), buildRole()); !saved.Success {
		t.Fatalf("saving the build role: %s", protocol.Deref(saved.Error))
	}
	cwd := w.Path("api")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, row := range []struct {
		name    string
		edit    func(*protocol.DelegateMessage)
		refusal string
	}{
		{"a blank brief", func(m *protocol.DelegateMessage) { m.Assignment.Brief = protocol.Ptr(" ") }, "new assignment requires a non-empty brief"},
		{"a new assignment naming a seed", func(m *protocol.DelegateMessage) { m.Assignment.SeedID = protocol.Ptr("s-one") }, "new assignment requires"},
		{"a new assignment with a handover", func(m *protocol.DelegateMessage) { m.Assignment.Handover = &protocol.DelegateHandover{} }, "new assignment requires"},
		{"a seed assignment without a seed", func(m *protocol.DelegateMessage) {
			m.Assignment = protocol.DelegateAssignment{Kind: protocol.DelegateAssignmentKindSeed}
		}, "seed assignment requires seed_id"},
		{"a choice without a role", func(m *protocol.DelegateMessage) { m.Choice = protocol.Ptr("deep") }, "choice requires role"},
		{"a role and the fallback", func(m *protocol.DelegateMessage) { m.Role = protocol.Ptr("build"); m.Fallback = protocol.Ptr(true) }, "role and fallback cannot be combined"},
		{"review evidence without a handover", func(m *protocol.DelegateMessage) { m.Review = &protocol.SeedReviewActionContext{} }, "review evidence is valid only for a seed handover"},
		{"a reuse with a path", func(m *protocol.DelegateMessage) {
			m.Checkout = &protocol.DelegateCheckout{Kind: protocol.DelegateCheckoutKindReuse, Branch: "main", Path: protocol.Ptr("/tmp/w")}
		}, "reuse checkout accepts branch only"},
		{"a new worktree without a base", func(m *protocol.DelegateMessage) {
			m.Checkout = &protocol.DelegateCheckout{Kind: protocol.DelegateCheckoutKindNewWorktree, Branch: "feature"}
		}, "new worktree requires an explicit from ref"},
		{"an existing branch with a base", func(m *protocol.DelegateMessage) {
			m.Checkout = &protocol.DelegateCheckout{Kind: protocol.DelegateCheckoutKindExistingBranchWorktree, Branch: "feature", From: protocol.Ptr("main")}
		}, "existing branch worktree does not accept from"},
		{"a role nobody configured", func(m *protocol.DelegateMessage) { m.Role = protocol.Ptr("missing") }, `role "missing" is unavailable`},
		{"a request id in the operation namespace", func(m *protocol.DelegateMessage) { m.RequestID = "op-mine" }, "request_id uses reserved operation prefix op-"},
	} {
		request := brief(cwd, "Do the work")
		request.RequestID = "malformed-" + strings.ReplaceAll(row.name, " ", "-")
		request.Agent = protocol.Ptr("codex")
		row.edit(&request)
		if operation, err := cli.StartDelegation(request); err == nil || !strings.Contains(err.Error(), row.refusal) {
			t.Errorf("%s: delegating = %+v, %v; want the refusal %q", row.name, operation, err, row.refusal)
		}
		if operation, err := cli.DelegationStatus(request.RequestID); err == nil {
			t.Errorf("%s: the refused request left operation %+v", row.name, operation)
		}
	}
	if sessions, err := cli.Query(""); err != nil || len(sessions) != 0 {
		t.Errorf("refused delegations left sessions %+v, %v", sessions, err)
	}
}

func TestRetryingADelegationConvergesOnOneOperation(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	cli := w.Client()
	cwd := w.Path("api")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	request := brief(cwd, "Do the work once")
	request.RequestID = "stable-request"
	request.Agent = protocol.Ptr("codex")

	operations := make([]*protocol.DelegationOperation, 4)
	failures := make([]error, len(operations))
	var retries sync.WaitGroup
	for i := range operations {
		retries.Go(func() { operations[i], failures[i] = w.Client().StartDelegation(request) })
	}
	retries.Wait()
	for i, operation := range operations {
		if failures[i] != nil || operation.OperationID != operations[0].OperationID || operation.SessionID != operations[0].SessionID {
			t.Fatalf("concurrent retries diverged: %+v, %v; first %+v", operation, failures[i], operations[0])
		}
	}
	result, err := cli.Delegate(request)
	if err != nil || result.SessionID != operations[0].SessionID {
		t.Fatalf("a sequential retry = %+v, %v; want the accepted operation's session %s", result, err, operations[0].SessionID)
	}
	w.Launched(result.SessionID)
	if shown, err := cli.SeedShow("", result.SeedID); err != nil || shown.Seed.TenderSession != result.SessionID {
		t.Errorf("the converged delegation's seed is %+v, %v; want it tended by %s", shown, err, result.SessionID)
	}

	failing := brief(cwd, "Fail once")
	failing.RequestID = "failing-request"
	failing.Agent = protocol.Ptr("missing-agent")
	if _, err := cli.Delegate(failing); err == nil || !strings.Contains(err.Error(), `agent "missing-agent" is not available`) {
		t.Fatalf("delegating to an agent that does not exist = %v", err)
	}
	failed, err := cli.DelegationStatus("failing-request")
	if err != nil {
		t.Fatal(err)
	}
	if retried, err := cli.StartDelegation(failing); err != nil || retried.OperationID != failed.OperationID || retried.State != protocol.DelegationOperationStateFailed {
		t.Errorf("retrying the failed delegation = %+v, %v; want failed operation %s back", retried, err, failed.OperationID)
	}

	if sessions, err := cli.Query(""); err != nil || len(sessions) != 1 || sessions[0].ID != result.SessionID {
		t.Errorf("sessions after the retries are %+v, %v; want only delegate %s", sessions, err, result.SessionID)
	}
}

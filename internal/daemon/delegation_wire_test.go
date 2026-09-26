package daemon_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func brief(cwd, text string) protocol.DelegateMessage {
	return protocol.DelegateMessage{
		Cmd:        protocol.CmdDelegate,
		Cwd:        cwd,
		Assignment: protocol.DelegateAssignment{Kind: protocol.DelegateAssignmentKindNew, Brief: protocol.Ptr(text)},
	}
}

func TestADelegationRetryReturnsTheAcceptedOperationAfterTheRolesChange(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	app, cli := w.App(), w.Client()
	roles := buildRole()
	roles.Roles[0].Choices[0].Selection = protocol.DelegationSelection{Harness: "codex"}
	saved := savePreferences(app, roles)
	if !saved.Success {
		t.Fatalf("saving the build role: %s", protocol.Deref(saved.Error))
	}
	cwd := w.Path("api")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	request := brief(cwd, "rename the checkout module")
	request.RequestID = "rename-request"
	request.Role = protocol.Ptr("build")

	accepted, err := cli.StartDelegation(request)
	if err != nil {
		t.Fatalf("delegating with the build role: %v", err)
	}
	w.Launched(accepted.SessionID)
	off := *saved.Preferences
	off.Enabled = false
	if turnedOff := savePreferences(app, off); !turnedOff.Success {
		t.Fatalf("turning the roles off: %s", protocol.Deref(turnedOff.Error))
	}

	if retried, err := cli.StartDelegation(request); err != nil || retried.OperationID != accepted.OperationID {
		t.Fatalf("retrying the accepted request = %+v, %v; want operation %s", retried, err, accepted.OperationID)
	}
	fresh := request
	fresh.RequestID = "another-request"
	if _, err := cli.StartDelegation(fresh); err == nil || !strings.Contains(err.Error(), "no delegation roles available") {
		t.Errorf("a new launch with the build role after the roles were turned off = %v; want the roles-unavailable refusal", err)
	}
	changed := request
	changed.Assignment.Brief = protocol.Ptr("rename the payments module")
	if _, err := cli.StartDelegation(changed); err == nil || !strings.Contains(err.Error(), "different inputs") {
		t.Errorf("a retry that changed the request under the same request id = %v; want the request-id conflict", err)
	}
}

func gitRepo(t *testing.T, dir string, branches ...string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "web"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "web", "index.html"), []byte("shop\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	commands := [][]string{
		{"init", "-q", "-b", "main"},
		{"add", "web"},
		{"-c", "user.name=attn", "-c", "user.email=attn@example.invalid", "commit", "-q", "-m", "start"},
	}
	for _, branch := range branches {
		commands = append(commands, []string{"branch", branch})
	}
	for _, args := range commands {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, out)
		}
	}
}

func TestADelegationThatCannotCheckOutItsBranchFailsAsACheckoutConflict(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	app, cli := w.App(), w.Client()
	repo := w.Path("shop")
	gitRepo(t, repo, "feature/existing")

	for name, checkout := range map[string]protocol.DelegateCheckout{
		"a new branch that already exists":  {Kind: protocol.DelegateCheckoutKindNewWorktree, Branch: "feature/existing", From: protocol.Ptr("main")},
		"an existing branch that is absent": {Kind: protocol.DelegateCheckoutKindExistingBranchWorktree, Branch: "feature/missing"},
		"a reuse on the wrong branch":       {Kind: protocol.DelegateCheckoutKindReuse, Branch: "feature/other"},
	} {
		request := brief(repo, "add a discount field")
		request.RequestID = strings.ReplaceAll(name, " ", "-")
		request.Agent = protocol.Ptr("codex")
		request.Checkout = &checkout
		result := testworld.Request(app, request, protocol.EventDelegateResult,
			func(m protocol.DelegateResultMessage) bool { return protocol.Deref(m.RequestID) == request.RequestID })
		if result.Success {
			t.Errorf("%s: the delegation succeeded", name)
			continue
		}
		operation, err := cli.DelegationStatus(request.RequestID)
		if err != nil || operation.Failure == nil || operation.Failure.Code != "checkout_conflict" {
			t.Errorf("%s: status = %+v, %v; want a checkout_conflict failure", name, operation, err)
		}
	}
}

func delegateSeed(t *testing.T, app *testworld.Peer, requestID, seedID, cwd string, checkout protocol.DelegateCheckout) protocol.DelegateResultMessage {
	t.Helper()
	request := protocol.DelegateMessage{
		Cmd: protocol.CmdDelegate, RequestID: requestID, Cwd: cwd, Agent: protocol.Ptr("codex"), Checkout: &checkout,
		Assignment: protocol.DelegateAssignment{Kind: protocol.DelegateAssignmentKindSeed, SeedID: protocol.Ptr(seedID)},
	}
	return testworld.Request(app, request, protocol.EventDelegateResult,
		func(m protocol.DelegateResultMessage) bool { return protocol.Deref(m.RequestID) == requestID })
}

func plantSeed(t *testing.T, w *world, title string) protocol.Seed {
	t.Helper()
	planted, err := w.Client().SeedPlant("", title, "Checkout needs a field for discount codes.", "", "", "")
	if err != nil {
		t.Fatalf("plant %q: %v", title, err)
	}
	return planted.Seed
}

func TestACompletedSeedDelegationReportsItsSeedAndTheWorktreeRoot(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	app := w.App()
	repo := w.Path("shop")
	gitRepo(t, repo)
	seed := plantSeed(t, w, "Add a discount field")

	result := delegateSeed(t, app, "discount", seed.ID, filepath.Join(repo, "web"),
		protocol.DelegateCheckout{Kind: protocol.DelegateCheckoutKindNewWorktree, Branch: "feature/discount", From: protocol.Ptr("main")})
	if !result.Success {
		t.Fatalf("delegating the seed: %s", protocol.Deref(result.Error))
	}
	operation, err := w.Client().DelegationStatus("discount")
	if err != nil {
		t.Fatal(err)
	}
	root := protocol.Deref(operation.WorktreePath)
	if root == "" || filepath.Join(root, "web") != result.Result.Directory {
		t.Errorf("the operation reports worktree %q for a session working in %q; want the worktree root", root, result.Result.Directory)
	}
	if protocol.Deref(operation.SeedID) != seed.ID || operation.TicketID != nil {
		t.Errorf("the operation names seed %q and ticket %q; want only seed %s", protocol.Deref(operation.SeedID), protocol.Deref(operation.TicketID), seed.ID)
	}
}

func TestAFailedSeedDelegationIsLeftAsItWasAcrossARestart(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	repo := w.Path("shop")
	gitRepo(t, repo, "feature/existing")
	seed := plantSeed(t, w, "Add a discount field")
	if result := delegateSeed(t, w.App(), "discount", seed.ID, repo,
		protocol.DelegateCheckout{Kind: protocol.DelegateCheckoutKindNewWorktree, Branch: "feature/existing", From: protocol.Ptr("main")}); result.Success {
		t.Fatal("delegating onto an existing branch succeeded")
	}
	before, err := w.Client().DelegationStatus("discount")
	if err != nil {
		t.Fatal(err)
	}

	w.restart()
	after, err := w.Client().DelegationStatus("discount")
	if err != nil || !reflect.DeepEqual(after, before) {
		t.Fatalf("after the restart the operation is %+v (%v), was %+v", after, err, before)
	}
}

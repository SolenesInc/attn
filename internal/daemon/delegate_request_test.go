package daemon

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	attngit "github.com/victorarias/attn/internal/git"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

func validNewDelegateRequest(cwd string) protocol.DelegateMessage {
	return protocol.DelegateMessage{
		Cmd: protocol.CmdDelegate, RequestID: "request", Agent: protocol.Ptr("codex"), Cwd: cwd,
		Assignment: protocol.DelegateAssignment{Kind: protocol.DelegateAssignmentKindNew, Brief: protocol.Ptr("Do the work")},
	}
}

func TestValidateDelegateRequestShapeRejectsInvalidDirectAPIInputs(t *testing.T) {
	for _, test := range []struct {
		name string
		edit func(*protocol.DelegateMessage)
		want string
	}{
		{"empty brief", func(m *protocol.DelegateMessage) { m.Assignment.Brief = protocol.Ptr(" ") }, "new assignment requires"},
		{"new with seed", func(m *protocol.DelegateMessage) { m.Assignment.SeedID = protocol.Ptr("s-one") }, "new assignment requires"},
		{"seed without id", func(m *protocol.DelegateMessage) {
			m.Assignment = protocol.DelegateAssignment{Kind: protocol.DelegateAssignmentKindSeed}
		}, "seed assignment requires"},
		{"handover on new", func(m *protocol.DelegateMessage) { m.Assignment.Handover = &protocol.DelegateHandover{} }, "new assignment requires"},
		{"choice without role", func(m *protocol.DelegateMessage) { m.Choice = protocol.Ptr("deep") }, "choice requires role"},
		{"role and fallback", func(m *protocol.DelegateMessage) { m.Role = protocol.Ptr("builder"); m.Fallback = protocol.Ptr(true) }, "cannot be combined"},
		{"review without handover", func(m *protocol.DelegateMessage) { m.Review = &protocol.SeedReviewActionContext{} }, "only for a seed handover"},
		{"reuse with path", func(m *protocol.DelegateMessage) {
			m.Checkout = &protocol.DelegateCheckout{Kind: protocol.DelegateCheckoutKindReuse, Branch: "main", Path: protocol.Ptr("/tmp/w")}
		}, "accepts branch only"},
		{"new worktree without base", func(m *protocol.DelegateMessage) {
			m.Checkout = &protocol.DelegateCheckout{Kind: protocol.DelegateCheckoutKindNewWorktree, Branch: "feature"}
		}, "explicit from ref"},
		{"existing branch with base", func(m *protocol.DelegateMessage) {
			m.Checkout = &protocol.DelegateCheckout{Kind: protocol.DelegateCheckoutKindExistingBranchWorktree, Branch: "feature", From: protocol.Ptr("main")}
		}, "does not accept from"},
	} {
		t.Run(test.name, func(t *testing.T) {
			msg := validNewDelegateRequest(t.TempDir())
			test.edit(&msg)
			if err := validateDelegateRequestShape(&msg); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error=%v, want %q", err, test.want)
			}
		})
	}
}

func TestDelegationRequestIDRejectsDifferentNormalizedInput(t *testing.T) {
	d := newDelegationDaemon(t)
	backend := &fakeSpawnBackend{}
	_, sourceID, _ := setupDelegationSource(t, d, backend)
	consumeDelegatedPrompt(t, backend)
	first := explicitOperationMessage(d, "same-key", sourceID, "First brief", "first")
	if _, err := d.startDelegation(&first); err != nil {
		t.Fatal(err)
	}
	second := explicitOperationMessage(d, "same-key", sourceID, "Different brief", "first")
	if _, err := d.startDelegation(&second); !errors.Is(err, store.ErrDelegationRequestConflict) {
		t.Fatalf("error=%v, want request conflict", err)
	}
}

func TestResolveDelegateRuntimeUsesExactRequestedBaseCommit(t *testing.T) {
	d := newDelegationDaemon(t)
	repo := filepath.Join(t.TempDir(), "repo")
	if err := os.Mkdir(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	runGitDaemon(t, repo, "init")
	runGitDaemon(t, repo, "commit", "--allow-empty", "-m", "base")
	base := strings.TrimSpace(string(mustGitOutput(t, repo, "rev-parse", "HEAD")))
	runGitDaemon(t, repo, "branch", "delegation-base")
	runGitDaemon(t, repo, "commit", "--allow-empty", "-m", "later")

	msg := validNewDelegateRequest(repo)
	msg.Checkout = &protocol.DelegateCheckout{
		Kind: protocol.DelegateCheckoutKindNewWorktree, Branch: "feature/exact-base", From: protocol.Ptr("delegation-base"),
	}
	runtime, err := d.resolveDelegateRuntime(&msg, "s-reserved", "", "", "session-reserved", "", false)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(protocol.Deref(runtime.Worktree.StartingFrom)); got != base {
		t.Fatalf("resolved base = %q, want exact commit %q", got, base)
	}
}

func TestResolveDelegateRuntimeRejectsUnavailableBaseWithoutFallback(t *testing.T) {
	d := newDelegationDaemon(t)
	repo := filepath.Join(t.TempDir(), "repo")
	if err := os.Mkdir(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	runGitDaemon(t, repo, "init")
	runGitDaemon(t, repo, "commit", "--allow-empty", "-m", "base")
	msg := validNewDelegateRequest(repo)
	msg.Checkout = &protocol.DelegateCheckout{
		Kind: protocol.DelegateCheckoutKindNewWorktree, Branch: "feature/missing-base", From: protocol.Ptr("origin/not-fetched"),
	}
	_, err := d.resolveDelegateRuntime(&msg, "s-reserved", "", "", "session-reserved", "", false)
	if err == nil || !strings.Contains(err.Error(), "unavailable") {
		t.Fatalf("error=%v, want unavailable base error", err)
	}
}

func TestResolveDelegateRuntimeRequiresLocalExistingBranch(t *testing.T) {
	d := newDelegationDaemon(t)
	repo := filepath.Join(t.TempDir(), "repo")
	if err := os.Mkdir(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	runGitDaemon(t, repo, "init")
	runGitDaemon(t, repo, "commit", "--allow-empty", "-m", "base")
	runGitDaemon(t, repo, "update-ref", "refs/remotes/origin/remote-only", "HEAD")
	msg := validNewDelegateRequest(repo)
	msg.Checkout = &protocol.DelegateCheckout{
		Kind: protocol.DelegateCheckoutKindExistingBranchWorktree, Branch: "remote-only",
	}
	_, err := d.resolveDelegateRuntime(&msg, "s-reserved", "", "", "session-reserved", "", false)
	if err == nil || !strings.Contains(err.Error(), "local branch") {
		t.Fatalf("error=%v, want local branch error", err)
	}
}

func TestResolveDelegateRuntimeVerifiesReusedBranch(t *testing.T) {
	d := newDelegationDaemon(t)
	repo := filepath.Join(t.TempDir(), "repo")
	if err := os.Mkdir(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	runGitDaemon(t, repo, "init")
	runGitDaemon(t, repo, "commit", "--allow-empty", "-m", "base")
	current := strings.TrimSpace(string(mustGitOutput(t, repo, "branch", "--show-current")))
	msg := validNewDelegateRequest(repo)
	msg.Checkout = &protocol.DelegateCheckout{Kind: protocol.DelegateCheckoutKindReuse, Branch: current + "-other"}
	_, err := d.resolveDelegateRuntime(&msg, "s-reserved", "", "", "session-reserved", "", false)
	if err == nil || !strings.Contains(err.Error(), "branch mismatch") {
		t.Fatalf("error=%v, want branch mismatch", err)
	}
}

func TestResolveDelegateRuntimeRejectsDetachedReuse(t *testing.T) {
	d := newDelegationDaemon(t)
	repo := filepath.Join(t.TempDir(), "repo")
	if err := os.Mkdir(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	runGitDaemon(t, repo, "init")
	runGitDaemon(t, repo, "commit", "--allow-empty", "-m", "base")
	head := strings.TrimSpace(string(mustGitOutput(t, repo, "rev-parse", "--short", "HEAD")))
	runGitDaemon(t, repo, "checkout", "--detach")

	msg := validNewDelegateRequest(repo)
	msg.Checkout = &protocol.DelegateCheckout{Kind: protocol.DelegateCheckoutKindReuse, Branch: head}
	_, err := d.resolveDelegateRuntime(&msg, "s-reserved", "", "", "session-reserved", "", false)
	if err == nil || !strings.Contains(err.Error(), "cannot reuse detached") {
		t.Fatalf("error=%v, want detached checkout error", err)
	}
}

func mustGitOutput(t *testing.T, repo string, args ...string) []byte {
	t.Helper()
	out, err := attngit.Output(attngit.OpMetadata, repo, args...)
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return out
}

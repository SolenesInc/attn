package daemon

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	attngit "github.com/victorarias/attn/internal/git"
	"github.com/victorarias/attn/internal/protocol"
)

func validNewDelegateRequest(cwd string) protocol.DelegateMessage {
	return protocol.DelegateMessage{
		Cmd: protocol.CmdDelegate, RequestID: "request", Agent: protocol.Ptr("codex"), Cwd: cwd,
		Assignment: protocol.DelegateAssignment{Kind: protocol.DelegateAssignmentKindNew, Brief: protocol.Ptr("Do the work")},
	}
}

func TestAcceptedDelegationBasePinsTheRequestedRef(t *testing.T) {
	repo := filepath.Join(t.TempDir(), "repo")
	if err := os.Mkdir(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	runGitDaemon(t, repo, "init")
	runGitDaemon(t, repo, "commit", "--allow-empty", "-m", "base")
	want := strings.TrimSpace(string(mustGitOutput(t, repo, "rev-parse", "HEAD")))
	msg := validNewDelegateRequest(repo)
	msg.Checkout = &protocol.DelegateCheckout{
		Kind: protocol.DelegateCheckoutKindNewWorktree, Branch: "feature/pinned-base", From: protocol.Ptr("HEAD"),
	}

	d := &Daemon{gitExec: testGitExecutor(t, productionGitExecutorConfig)}
	got, err := d.resolveAcceptedDelegationBase(&msg)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("accepted base = %q, want %q", got, want)
	}
	runGitDaemon(t, repo, "commit", "--allow-empty", "-m", "later")
	if got == strings.TrimSpace(string(mustGitOutput(t, repo, "rev-parse", "HEAD"))) {
		t.Fatal("accepted base followed the mutable ref")
	}
}

func mustGitOutput(t *testing.T, repo string, args ...string) []byte {
	t.Helper()
	out, err := attngit.NewClient().Output(context.Background(), attngit.OpMetadata, repo, args...)
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return out
}

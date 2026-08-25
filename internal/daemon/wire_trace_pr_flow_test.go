package daemon

import (
	"net"
	"path/filepath"
	"testing"

	"github.com/victorarias/attn/internal/protocol"
)

// The producer golden cannot prove that muting a PR still causes a prs_updated: these
// handlers only reached clients via a whole-list re-push, so a dropped publish is green.
func TestWireTracePRFlowGolden(t *testing.T) {
	dir := t.TempDir()
	d := NewForTesting(filepath.Join(dir, "test.sock"))
	trace := wireRecorder(d)

	client := newWorkspaceProtocolTestClient()
	prID := "github.com/owner/repo#1"
	injected := protocol.PR{
		ID: prID, Repo: "owner/repo", Host: "github.com", Number: 1,
		Title: "Add the thing", Author: "octocat", State: protocol.PRStateWaiting,
		URL: "https://github.com/owner/repo/pull/1",
	}

	runDaemonSocketCommand(t, func(conn net.Conn) {
		d.handleInjectTestPR(conn, &protocol.InjectTestPRMessage{PR: injected})
	})
	runDaemonSocketCommand(t, func(conn net.Conn) {
		d.handleInjectTestPR(conn, &protocol.InjectTestPRMessage{PR: injected})
	})
	d.handleMutePRWS(&protocol.MutePRMessage{ID: prID})
	d.handleMutePRWS(&protocol.MutePRMessage{ID: prID})
	d.handlePRVisitedWS(&protocol.PRVisitedMessage{ID: prID})
	d.handleMuteRepoWS(&protocol.MuteRepoMessage{Repo: "owner/repo"})
	d.handleMuteAuthorWS(&protocol.MuteAuthorMessage{Author: "octocat"})
	_ = client

	assertWireGolden(t, "pr_flow", renderWireTrace(trace, map[string]string{
		dir: "<tmp>",
	}))
}

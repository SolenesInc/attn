package daemon

import (
	"sync"
	"sync/atomic"
	"testing"

	"github.com/victorarias/attn/internal/protocol"
)

func closeMetadataFixture(t *testing.T) (*Daemon, *wsClient, string, string, string) {
	t.Helper()
	d := newGardenDaemon(t)
	d.ptyBackend = &fakeSpawnBackend{}
	client := newWorkspaceProtocolTestClient()
	workspaceID, sessionID, paneID := "workspace-close-metadata", "session-close-metadata", "pane-close-metadata"
	cwd := t.TempDir()
	d.handleRegisterWorkspace(client, &protocol.RegisterWorkspaceMessage{
		Cmd: protocol.CmdRegisterWorkspace, ID: workspaceID, Title: "Close metadata", Directory: cwd,
	})
	d.handleWorkspaceLayoutAddSessionPane(client, &protocol.WorkspaceLayoutAddSessionPaneMessage{
		Cmd: protocol.CmdWorkspaceLayoutAddSessionPane, WorkspaceID: workspaceID,
		PaneID: protocol.Ptr(paneID), SessionID: sessionID,
	})
	expectWorkspaceLayoutActionResult(t, client, protocol.CmdWorkspaceLayoutAddSessionPane, workspaceID, paneID, true)
	d.store.Add(&protocol.Session{
		ID: sessionID, Agent: protocol.SessionAgentCodex, Directory: cwd, WorkspaceID: workspaceID,
		Branch: protocol.Ptr("feature/current"), MainRepo: protocol.Ptr("/projects/repo"),
	})
	d.associateSessionWithWorkspace(sessionID, workspaceID)
	d.store.SetResumeSessionID(sessionID, "native-current")
	return d, client, workspaceID, sessionID, paneID
}

func TestClosePaneWinsOverInFlightExecutionCapture(t *testing.T) {
	for _, beforeRemoval := range []bool{true, false} {
		name := "after record removal"
		if beforeRemoval {
			name = "before record removal"
		}
		t.Run(name, func(t *testing.T) {
			d, client, workspaceID, sessionID, paneID := closeMetadataFixture(t)
			d.store.SetResumeSessionID(sessionID, "native-old")
			read, release := make(chan struct{}), make(chan struct{})
			var paused atomic.Bool
			d.gardenDispatchBeforeWrite = func(id string) {
				if id == sessionID && paused.CompareAndSwap(false, true) {
					close(read)
					<-release
				}
			}
			done := make(chan error, 1)
			go func() {
				_, err := d.captureGardenSessionExecution(d.store.Get(sessionID))
				done <- err
			}()
			var releaseOnce sync.Once
			finishCapture := func() {
				releaseOnce.Do(func() {
					close(release)
					if err := <-done; err != nil {
						t.Errorf("late capture: %v", err)
					}
				})
			}
			defer finishCapture()
			<-read
			if beforeRemoval {
				var writes atomic.Int32
				d.gardenDispatchAfterWrite = func(id string) {
					if id == sessionID && writes.Add(1) == 2 {
						finishCapture()
					}
				}
			}
			d.store.SetResumeSessionID(sessionID, "native-current")
			d.store.UpdateBranch(sessionID, "feature/newer", true, "/projects/repo", "/projects/repo")
			d.handleWorkspaceLayoutClosePane(client, &protocol.WorkspaceLayoutClosePaneMessage{
				Cmd: protocol.CmdWorkspaceLayoutClosePane, WorkspaceID: workspaceID, PaneID: paneID,
			})
			expectWorkspaceLayoutActionResult(t, client, protocol.CmdWorkspaceLayoutClosePane, workspaceID, paneID, true)
			d.waitForSessionTeardown(sessionID)
			finishCapture()
			execution, ok := d.gardenDispatch(sessionID)
			if !ok || execution.Resume != "native-current" || execution.Branch != "feature/newer" {
				t.Errorf("late capture replaced close information: %+v", execution)
			}
		})
	}
}

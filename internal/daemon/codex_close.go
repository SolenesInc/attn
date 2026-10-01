package daemon

import (
	"context"
	"errors"
	"fmt"
	"github.com/google/uuid"
	agentdriver "github.com/victorarias/attn/internal/agent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/pty"
	"github.com/victorarias/attn/internal/ptybackend"
	"github.com/victorarias/attn/internal/store"
	"github.com/victorarias/attn/internal/workspacelayout"
	"syscall"
)

func (r *codexRuntime) closeView(runtimeID string, closed store.SessionClose) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.closeViewLocked(runtimeID, closed)
}

func (r *codexRuntime) closeViewLocked(runtimeID string, closed store.SessionClose) error {
	v, ok := r.views[runtimeID]
	if !ok {
		return fmt.Errorf("codex view %s is not open", runtimeID)
	}
	if v.Resolution == "resolved" {
		last := true
		for id, other := range r.views {
			if id != runtimeID && other.Resolution == "resolved" && other.SessionID == v.SessionID {
				last = false
				break
			}
		}
		if last {
			if err := r.d.sessionCloseError(v.SessionID); err != nil {
				return err
			}
			for otherID, other := range r.views {
				if otherID != runtimeID && other.Resolution == "unresolved" {
					return fmt.Errorf("cannot archive Codex owner %s while view %s has unresolved identity; close or resolve that view first", v.SessionID, otherID)
				}
			}
			if err := r.closeOwnerLocked(v.SessionID, closed); err != nil {
				return err
			}
		}
	}
	if err := r.removeViewLocked(runtimeID); err != nil {
		return err
	}
	r.cleanupReservation(v.LaunchOwnerID)
	return nil
}

func (r *codexRuntime) removeViewLocked(runtimeID string) error {
	if server := r.servers[runtimeID]; server != nil {
		_ = server.Close()
		delete(r.servers, runtimeID)
	}
	if err := r.d.ptyBackend.Kill(context.Background(), runtimeID, syscall.SIGTERM); err != nil && !errors.Is(err, pty.ErrSessionNotFound) {
		return err
	}
	if err := r.d.ptyBackend.Remove(context.Background(), runtimeID); err != nil && !errors.Is(err, pty.ErrSessionNotFound) {
		return err
	}
	if err := r.d.store.RemoveCodexView(runtimeID); err != nil {
		return err
	}
	delete(r.views, runtimeID)
	delete(r.initialConsumed, runtimeID)
	return nil
}

// CloseOwner is the single final-close boundary: native archive, await the
// transcript's available-record reconciliation, then finalize the ledger.
func (r *codexRuntime) closeOwnerLocked(id string, closed store.SessionClose) error {
	owner, err := r.d.store.CodexOwner(id)
	if err != nil {
		return err
	}
	if owner == nil {
		return fmt.Errorf("missing Codex owner %s", id)
	}
	if owner.NativeRootID == "" {
		for runtimeID, view := range r.views {
			if view.LaunchOwnerID == id {
				if err := r.removeViewLocked(runtimeID); err != nil {
					return err
				}
				r.removeLayoutView(runtimeID)
			}
		}
		r.cleanupReservation(id)
		return nil
	}
	launch, err := r.ownerContext(owner)
	if err != nil {
		return err
	}
	if err := r.ensureServer(r.d.life.Context(), launch); err != nil {
		return err
	}
	if _, err := r.control.Call(r.d.life.Context(), "thread/archive", map[string]any{"threadId": owner.NativeRootID}); err != nil {
		return err
	}
	if err := r.d.store.SetCodexArchived(id, true); err != nil {
		return err
	}
	r.d.stopCodexTranscriptWatcherAndWait(id)
	session := r.d.store.Get(id)
	r.d.commitSessionUnregister(id, closed)
	r.d.dissociateSessionFromWorkspace(id)
	if session != nil {
		r.d.publishSessionUnregistered(session)
	}
	return nil
}

func (d *Daemon) stopCodexTranscriptWatcherAndWait(id string) {
	d.watchersMu.Lock()
	watcher := d.transcriptWatch[id]
	delete(d.transcriptWatch, id)
	d.watchersMu.Unlock()
	if watcher != nil {
		close(watcher.stopCh)
		<-watcher.doneCh
	}
}

func (r *codexRuntime) closeAddressedOwner(id string, closed store.SessionClose) error {
	if err := r.d.sessionCloseError(id); err != nil {
		return err
	}
	r.mu.Lock()
	var runtimeID string
	for key, v := range r.views {
		if v.Resolution == "resolved" && v.SessionID == id {
			if runtimeID != "" {
				r.mu.Unlock()
				return fmt.Errorf("codex owner %s has several views; close a specific pane", id)
			}
			runtimeID = key
		}
	}
	if runtimeID == "" {
		defer r.mu.Unlock()
		for key, view := range r.views {
			if view.Resolution == "unresolved" {
				return fmt.Errorf("cannot archive Codex owner %s while view %s has unresolved identity", id, key)
			}
		}
		return r.closeOwnerLocked(id, closed)
	}
	defer r.mu.Unlock()
	if err := r.closeViewLocked(runtimeID, closed); err != nil {
		return err
	}
	r.removeLayoutView(runtimeID)
	return nil
}

func (r *codexRuntime) removeLayoutView(runtimeID string) {
	for _, workspaceID := range r.d.store.WorkspaceLayoutIDs() {
		snapshot := r.d.store.GetWorkspaceLayout(workspaceID)
		if snapshot == nil {
			continue
		}
		next := snapshot.Panes[:0]
		changed := false
		for _, pane := range snapshot.Panes {
			if pane.RuntimeID == runtimeID {
				snapshot.Layout, _ = workspacelayout.Remove(snapshot.Layout, pane.PaneID)
				changed = true
			} else {
				next = append(next, pane)
			}
		}
		if !changed {
			continue
		}
		snapshot.Panes = next
		if workspacelayout.LayoutEmpty(snapshot.Layout) {
			r.d.store.RemoveWorkspaceLayout(workspaceID)
			r.d.unregisterWorkspaceIfEmpty(workspaceID)
		} else {
			if err := r.d.store.SaveWorkspaceLayout(*snapshot); err != nil {
				r.d.logf("Codex remove view layout: %v", err)
			}
			r.d.broadcastWorkspaceLayoutUpdated(workspaceID)
		}
	}
}

func (r *codexRuntime) attachOwner(id string) (*sessionReopenOutcome, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	owner, err := r.d.store.CodexOwner(id)
	if err != nil {
		return nil, err
	}
	if owner == nil || owner.NativeRootID == "" {
		return nil, fmt.Errorf("codex owner %s has no native history to attach", id)
	}
	launch, err := r.ownerContext(owner)
	if err != nil {
		return nil, err
	}
	if err := r.ensureServer(r.d.life.Context(), launch); err != nil {
		return nil, err
	}
	if owner.Archived {
		if _, err := r.control.Call(r.d.life.Context(), "thread/unarchive", map[string]any{"threadId": owner.NativeRootID}); err != nil {
			return nil, err
		}
		if err := r.d.store.SetCodexArchived(id, false); err != nil {
			return nil, err
		}
	}
	if _, _, err := r.d.store.ReopenSession(id); err != nil {
		return nil, err
	}
	session := r.d.store.Get(id)
	if session == nil {
		return nil, fmt.Errorf("codex owner %s is absent from ledger", id)
	}
	workspaceID := launch.WorkspaceID
	if r.d.store.GetWorkspace(workspaceID) == nil {
		workspaceID = reopenWorkspaceID(id)
		r.d.handleRegisterWorkspace(nil, &protocol.RegisterWorkspaceMessage{Cmd: protocol.CmdRegisterWorkspace, ID: workspaceID, Directory: session.Directory, Title: session.Label})
	}
	r.d.associateSessionWithWorkspace(id, workspaceID)
	snapshot, err := r.d.currentOrEmptyWorkspaceLayout(workspaceID)
	if err != nil {
		return nil, err
	}
	runtimeID := uuid.NewString()
	paneID := newWorkspaceLayoutEntityID("pane")
	v := store.CodexView{RuntimeID: runtimeID, ServerID: owner.ServerID, LaunchOwnerID: id, Generation: uuid.NewString(), Resolution: "unresolved"}
	err = r.addViewLocked(v)
	if err != nil {
		return nil, err
	}
	attached := false
	defer func() {
		if !attached {
			if err := r.removeViewLocked(runtimeID); err != nil {
				r.d.logf("rollback Codex attachment: %v", err)
			}
			r.removeLayoutView(runtimeID)
		}
	}()
	pane := workspacelayout.Pane{PaneID: paneID, RuntimeID: runtimeID, SessionID: id, Kind: workspacelayout.PaneKindAgent, Title: session.Label, Status: workspacelayout.PaneStatusReady, CodexResolution: "unresolved"}
	if workspacelayout.LayoutEmpty(snapshot.Layout) {
		snapshot.Layout = workspacelayout.DefaultLayout(paneID)
	} else {
		var changed bool
		snapshot.Layout, changed = workspacelayout.Split(snapshot.Layout, firstWorkspaceLayoutPaneID(*snapshot), paneID, newWorkspaceLayoutEntityID("split"), workspacelayout.DirectionVertical, workspacelayout.DefaultSplitRatio)
		if !changed {
			return nil, errors.New("cannot attach Codex pane")
		}
	}
	snapshot.Panes = append(snapshot.Panes, pane)
	snapshot.ActivePaneID = paneID
	if err := r.d.store.SaveWorkspaceLayout(*snapshot); err != nil {
		return nil, err
	}
	executable := agentdriver.MustGet("codex").ResolveExecutable(launch.Executable)
	cmd := agentdriver.MustGet("codex").BuildCommand(agentdriver.SpawnOpts{Executable: executable, CWD: session.Directory, CodexRemote: "unix://" + r.socket(runtimeID), ResumeSessionID: owner.NativeRootID})
	opts := ptybackend.SpawnOptions{ID: runtimeID, CWD: session.Directory, Agent: "codex", Cols: 80, Rows: 24, LifecycleID: v.Generation, ExternalCommand: cmd.Args, LoginShellEnv: r.d.cachedLoginShellEnv(), DaemonEnv: r.d.spawnRoutingEnv()}
	if err := r.d.ptyBackend.Spawn(r.d.life.Context(), opts); err != nil {
		return nil, err
	}
	attached = true
	r.d.broadcastWorkspaceLayoutUpdated(workspaceID)
	r.d.publishFact(FactSessionReregistered, id, nil)
	return &sessionReopenOutcome{SessionID: id, WorkspaceID: workspaceID, PaneID: paneID, Directory: session.Directory, Action: protocol.SessionReopenActionReopen}, nil
}

func (r *codexRuntime) reopenOwnerLocked(id string) error {
	owner, err := r.d.store.CodexOwner(id)
	if err != nil || owner == nil || !owner.Archived {
		return err
	}
	if err := r.d.store.SetCodexArchived(id, false); err != nil {
		return err
	}
	if _, _, err := r.d.store.ReopenSession(id); err != nil {
		return err
	}
	if session := r.d.store.Get(id); session != nil {
		r.d.associateSessionWithWorkspace(id, session.WorkspaceID)
	}
	r.d.publishFact(FactSessionReregistered, id, nil)
	return nil
}

func (r *codexRuntime) closeAllOwnerViews(id string, closed store.SessionClose) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for runtimeID, view := range r.views {
		if view.Resolution == "unresolved" {
			return fmt.Errorf("cannot archive Codex owner %s while view %s has unresolved identity", id, runtimeID)
		}
	}
	if err := r.closeOwnerLocked(id, closed); err != nil {
		return err
	}
	for runtimeID, view := range r.views {
		if view.Resolution == "resolved" && view.SessionID == id {
			if err := r.removeViewLocked(runtimeID); err != nil {
				return err
			}
			r.removeLayoutView(runtimeID)
		}
	}
	return nil
}

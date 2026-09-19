package daemon

import (
	"context"
	"errors"
	"net"
	"os"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/victorarias/attn/internal/bus"
	"github.com/victorarias/attn/internal/git"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

func (d *Daemon) doListWorktrees(mainRepo string) []protocol.Worktree {
	var result []protocol.Worktree
	_ = d.worktreeMaintenance.RunForeground(context.Background(), "list worktrees", func(context.Context) error {
		result = d.doListWorktreesForeground(mainRepo)
		return nil
	})
	return result
}

func (d *Daemon) doListWorktreesForeground(mainRepo string) []protocol.Worktree {
	storedWorktrees := d.store.ListWorktreesByRepo(mainRepo)

	gitWorktrees, err := git.ListWorktrees(mainRepo)
	if err != nil {
		protoWorktrees := make([]protocol.Worktree, len(storedWorktrees))
		for i, wt := range storedWorktrees {
			protoWorktrees[i] = protocol.Worktree{
				Path:      wt.Path,
				Branch:    wt.Branch,
				MainRepo:  wt.MainRepo,
				CreatedAt: protocol.Ptr(wt.CreatedAt.Format(time.RFC3339)),
			}
		}
		return protoWorktrees
	}

	gitWorktreePaths := make(map[string]bool)
	for _, gwt := range gitWorktrees {
		gitWorktreePaths[gwt.Path] = true
	}

	var validWorktrees []*store.Worktree
	for _, wt := range storedWorktrees {
		if gitWorktreePaths[wt.Path] {
			validWorktrees = append(validWorktrees, wt)
		} else {
			d.store.RemoveWorktree(wt.Path)
		}
	}

	for _, gwt := range gitWorktrees {
		if gwt.Path == mainRepo {
			continue
		}
		found := false
		for _, wt := range validWorktrees {
			if wt.Path == gwt.Path {
				found = true
				break
			}
		}
		if !found {
			newWt := &store.Worktree{
				Path:      gwt.Path,
				Branch:    gwt.Branch,
				MainRepo:  mainRepo,
				CreatedAt: time.Now(),
			}
			d.store.AddWorktree(newWt)
			validWorktrees = append(validWorktrees, newWt)
		}
	}

	protoWorktrees := make([]protocol.Worktree, len(validWorktrees))
	for i, wt := range validWorktrees {
		protoWorktrees[i] = protocol.Worktree{
			Path:      wt.Path,
			Branch:    wt.Branch,
			MainRepo:  wt.MainRepo,
			CreatedAt: protocol.Ptr(wt.CreatedAt.Format(time.RFC3339)),
		}
	}
	return protoWorktrees
}

func (d *Daemon) doCreateWorktree(msg *protocol.CreateWorktreeMessage) (string, error) {
	var path string
	err := d.worktreeMaintenance.RunForeground(context.Background(), "create worktree", func(context.Context) error {
		var err error
		path, err = d.doCreateWorktreeForeground(msg)
		return err
	})
	return path, err
}

func (d *Daemon) doCreateWorktreeForeground(msg *protocol.CreateWorktreeMessage) (string, error) {
	mainRepo := git.ResolveMainRepoPath(msg.MainRepo)

	requestedPath := protocol.Deref(msg.Path)
	path := requestedPath
	if path == "" {
		path = git.GenerateWorktreePath(mainRepo, msg.Branch)
	}
	path = git.CanonicalizePath(path)

	startingFrom := protocol.Deref(msg.StartingFrom)
	if err := d.dispatchWorktreeBeforeCreateHooks(mainRepo, msg.Branch, startingFrom, requestedPath); err != nil {
		return "", err
	}
	providerPath, providerBranch, handled, err := d.dispatchWorktreeCreateProvider(mainRepo, msg.Branch, startingFrom, requestedPath)
	if err != nil {
		return "", err
	}
	if handled {
		d.registerCreatedWorktree(mainRepo, providerPath, providerBranch)
		if err := d.dispatchWorktreeAfterCreateHooks(mainRepo, providerPath, providerBranch); err != nil {
			return providerPath, err
		}
		return providerPath, nil
	}

	if remote, branch, ok := strings.Cut(startingFrom, "/"); ok {
		if remotes, rerr := git.ListRemotes(mainRepo); rerr == nil && slices.Contains(remotes, remote) {
			if ferr := git.FetchRemoteBranch(mainRepo, remote, branch); ferr != nil {
				d.logf("Warning: could not fetch %s before creating worktree: %v", startingFrom, ferr)
			}
		}
	}
	if startingFrom != "" && !git.RefExists(mainRepo, startingFrom) {
		d.logf("Worktree start ref %q not resolvable in %s; falling back to current HEAD", startingFrom, mainRepo)
		startingFrom = ""
	}
	var createErr error
	if startingFrom != "" {
		createErr = git.CreateWorktreeFromPoint(mainRepo, msg.Branch, path, startingFrom)
	} else {
		createErr = git.CreateWorktree(mainRepo, msg.Branch, path)
	}
	if createErr != nil {
		return "", createErr
	}

	d.registerCreatedWorktree(mainRepo, path, msg.Branch)
	if err := d.dispatchWorktreeAfterCreateHooks(mainRepo, path, msg.Branch); err != nil {
		return path, err
	}
	return path, nil
}

func (d *Daemon) registerCreatedWorktree(mainRepo, path, branch string) {
	wt := &store.Worktree{
		Path:      path,
		Branch:    branch,
		MainRepo:  mainRepo,
		CreatedAt: time.Now(),
	}
	d.store.AddWorktree(wt)

	d.publishFact(FactWorktreeCreated, wt.Path, protocol.Worktree{
		Path:      wt.Path,
		Branch:    wt.Branch,
		MainRepo:  wt.MainRepo,
		CreatedAt: protocol.Ptr(wt.CreatedAt.Format(time.RFC3339)),
	})
}

func (d *Daemon) discoverWorktree(path string) *store.Worktree {
	mainRepo := git.GetMainRepoFromWorktree(path)
	if mainRepo == "" {
		return nil
	}

	gitWorktrees, err := git.ListWorktrees(mainRepo)
	if err != nil {
		return nil
	}

	for _, gwt := range gitWorktrees {
		if gwt.Path == path {
			wt := &store.Worktree{
				Path:      gwt.Path,
				Branch:    gwt.Branch,
				MainRepo:  mainRepo,
				CreatedAt: time.Now(),
			}
			d.store.AddWorktree(wt)
			d.logf("Discovered worktree not in registry: %s (branch: %s, main: %s)", path, gwt.Branch, mainRepo)
			return wt
		}
	}

	return nil
}

type deleteWorktreeOptions struct {
	Force         bool
	RemovalAction string
	RemovalReason string
}

type deleteWorktreeFailureKind string

const (
	deleteWorktreeFailureDirtyWorktree deleteWorktreeFailureKind = "dirty_worktree"
	deleteWorktreeFailureProviderError deleteWorktreeFailureKind = "provider_error"
	deleteWorktreeFailureNotFound      deleteWorktreeFailureKind = "not_found"
	deleteWorktreeFailureGitError      deleteWorktreeFailureKind = "git_error"
)

type deleteWorktreeError struct {
	err       error
	kind      deleteWorktreeFailureKind
	forceable bool
}

func (e *deleteWorktreeError) Error() string {
	return e.err.Error()
}

func (e *deleteWorktreeError) Unwrap() error {
	return e.err
}

func (d *Daemon) doDeleteWorktree(path string, endpointID *string, opts deleteWorktreeOptions) (err error) {
	return d.worktreeMaintenance.RunForeground(context.Background(), "delete worktree", func(context.Context) error {
		return d.doDeleteWorktreeForeground(path, endpointID, opts)
	})
}

func (d *Daemon) doDeleteWorktreeForeground(path string, endpointID *string, opts deleteWorktreeOptions) (err error) {
	finishOperation := d.beginGitOperation(protocol.GitOperationKindDeleteWorktree, path, endpointID)
	defer func() {
		finishOperation(err)
	}()

	wt := d.store.GetWorktree(path)
	if wt == nil {
		wt = d.discoverWorktree(path)
		if wt == nil {
			if _, err := os.Stat(path); os.IsNotExist(err) {
				d.logf("Worktree %s doesn't exist and not in registry, treating as already deleted", path)
				d.publishFact(FactWorktreeDeleted, path, nil)
				d.cleanupDeletedWorktreeSessions(path)
				return nil
			}
			return &deleteWorktreeError{
				err:  &worktreeNotFoundError{path: path},
				kind: deleteWorktreeFailureNotFound,
			}
		}
	}

	d.captureGardenExecutionsInDirectory(path)

	seeds := d.seedsForWorktree(wt)

	branch := wt.Branch
	mainRepo := wt.MainRepo

	handled, err := d.dispatchWorktreeDeleteProvider(mainRepo, path, branch, opts.Force)
	if err != nil {
		if d.worktreeDeletionHappened(mainRepo, path) {
			d.finalizeDeletedWorktree(path, mainRepo, branch)
			d.recordWorktreeRemoval(wt, seeds, opts, time.Now())
			return nil
		}
		return d.classifyDeleteWorktreeProviderError(path, opts.Force, err)
	}
	if !handled {
		if err := git.DeleteWorktree(mainRepo, path, opts.Force); err != nil {
			return d.classifyDeleteWorktreeGitError(path, opts.Force, err)
		}
	} else if !d.worktreeDeletionHappened(mainRepo, path) {
		return &deleteWorktreeError{
			err:  errors.New("worktree delete provider reported success but the worktree still exists"),
			kind: deleteWorktreeFailureProviderError,
		}
	}

	d.finalizeDeletedWorktree(path, mainRepo, branch)
	d.recordWorktreeRemoval(wt, seeds, opts, time.Now())
	return nil
}

func (d *Daemon) worktreeDeletionHappened(mainRepo, path string) bool {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return true
	}
	states, err := git.ListWorktreeStates(mainRepo)
	if err != nil {
		return false
	}
	for _, state := range states {
		if state.Path == path {
			return false
		}
	}
	return true
}

func (d *Daemon) finalizeDeletedWorktree(path, mainRepo, branch string) {
	d.cleanupDeletedWorktreeSessions(path)
	d.store.RemoveWorktree(path)

	if branch != "" {
		if d.gardenKeepsBranch(mainRepo, branch) {
			d.logf("Preserved branch %s because an open seed can continue from it", branch)
		} else if err := git.DeleteBranch(mainRepo, branch, true); err != nil {
			d.logf("Warning: worktree deleted but failed to delete branch %s: %v", branch, err)
		} else {
			d.logf("Deleted branch %s along with worktree", branch)
		}
	}

	d.publishFact(FactWorktreeDeleted, path, nil)
}

func (d *Daemon) cleanupDeletedWorktreeSessions(path string) {
	for _, session := range d.store.List("") {
		if !pathAtOrBelow(session.Directory, path) {
			continue
		}
		d.terminateSession(session.ID, syscall.SIGTERM)
		d.closeSession(session.ID, store.SessionClose{By: store.SessionClosedByUser, Reason: "worktree deleted"})
		d.publishSessionUnregistered(session)
		d.dissociateSessionFromWorkspace(session.ID)
		d.removeWorkspaceLayoutPaneForSession(session.ID)
	}
}

func (d *Daemon) classifyDeleteWorktreeGitError(path string, force bool, err error) error {
	kind := deleteWorktreeFailureGitError
	forceable := false
	if !force && d.worktreeHasLocalChanges(path) {
		kind = deleteWorktreeFailureDirtyWorktree
		forceable = true
	}
	return &deleteWorktreeError{
		err:       err,
		kind:      kind,
		forceable: forceable,
	}
}

func (d *Daemon) classifyDeleteWorktreeProviderError(path string, force bool, err error) error {
	kind := deleteWorktreeFailureProviderError
	forceable := false
	if !force && isDirtyWorktreeDeleteError(err) && d.worktreeHasLocalChanges(path) {
		kind = deleteWorktreeFailureDirtyWorktree
		forceable = true
	}
	return &deleteWorktreeError{
		err:       err,
		kind:      kind,
		forceable: forceable,
	}
}

func isDirtyWorktreeDeleteError(err error) bool {
	message := err.Error()
	return strings.Contains(message, "contains modified or untracked files") &&
		strings.Contains(message, "use --force to delete it")
}

func (d *Daemon) worktreeHasLocalChanges(path string) bool {
	status, err := getGitStatusWithOptions(path, gitStatusOptions{
		mode: gitStatusModeFull,
	})
	if err != nil || status == nil || status.Error != nil {
		return false
	}
	return len(status.Staged) > 0 || len(status.Unstaged) > 0 || len(status.Untracked) > 0
}

type worktreeNotFoundError struct {
	path string
}

func (e *worktreeNotFoundError) Error() string {
	return "worktree not found in registry: " + e.path
}

func (d *Daemon) handleListWorktrees(conn net.Conn, msg *protocol.ListWorktreesMessage) {
	protoWorktrees := d.doListWorktrees(msg.MainRepo)
	d.publishFact(FactWorktreeListReconciled, msg.MainRepo, protoWorktrees)
	d.sendOK(conn)
}

func (d *Daemon) handleCreateWorktree(conn net.Conn, msg *protocol.CreateWorktreeMessage) {
	_, err := d.doCreateWorktree(msg)
	if err != nil {
		d.sendError(conn, err.Error())
		return
	}
	d.sendOK(conn)
}

func (d *Daemon) handleDeleteWorktree(conn net.Conn, msg *protocol.DeleteWorktreeMessage) {
	if err := d.doDeleteWorktree(msg.Path, msg.EndpointID, deleteWorktreeOptions{
		Force: protocol.Deref(msg.Force),
	}); err != nil {
		d.sendError(conn, err.Error())
		return
	}
	d.sendOK(conn)
}

func (d *Daemon) handleListWorktreesWS(client *wsClient, msg *protocol.ListWorktreesMessage) {
	protoWorktrees := d.doListWorktrees(msg.MainRepo)
	d.sendToClient(client, &protocol.WebSocketEvent{
		Event:     protocol.EventWorktreesUpdated,
		Worktrees: protoWorktrees,
	})
}

func (d *Daemon) handleCreateWorktreeWS(client *wsClient, msg *protocol.CreateWorktreeMessage) {
	go func() {
		path, err := d.doCreateWorktree(msg)
		result := protocol.CreateWorktreeResultMessage{
			Event:      protocol.EventCreateWorktreeResult,
			Path:       protocol.Ptr(path),
			EndpointID: msg.EndpointID,
			Success:    err == nil,
		}
		if err != nil {
			result.Error = protocol.Ptr(err.Error())
			d.logf("Create worktree failed for %s: %v", msg.Branch, err)
		} else {
			d.logf("Create worktree succeeded: %s at %s", msg.Branch, path)
		}
		d.sendToClient(client, result)
	}()
}

func (d *Daemon) handleDeleteWorktreeWS(client *wsClient, msg *protocol.DeleteWorktreeMessage) {
	go func() {
		defer d.publishFact(FactWorktreeSessionsRemoved, msg.Path, nil)

		err := d.doDeleteWorktree(msg.Path, msg.EndpointID, deleteWorktreeOptions{
			Force: protocol.Deref(msg.Force),
		})
		result := protocol.DeleteWorktreeResultMessage{
			Event:      protocol.EventDeleteWorktreeResult,
			Path:       msg.Path,
			EndpointID: msg.EndpointID,
			Success:    err == nil,
		}
		if err != nil {
			result.Error = protocol.Ptr(err.Error())
			var deleteErr *deleteWorktreeError
			if errors.As(err, &deleteErr) {
				if deleteErr.kind != "" {
					result.ReasonKind = string(deleteErr.kind)
				}
				result.Forceable = protocol.Ptr(deleteErr.forceable)
			}
			d.logf("Delete worktree failed for %s: %v", msg.Path, err)
		} else {
			d.logf("Delete worktree succeeded: %s", msg.Path)
		}
		d.sendToClient(client, result)
	}()
}

func (d *Daemon) projectWorktreeCreated(ev bus.Event) {
	worktree, ok := decodeFact[protocol.Worktree](d, ev)
	if !ok {
		return
	}
	d.wsHub.Broadcast(&protocol.WebSocketEvent{
		Event:     protocol.EventWorktreeCreated,
		Worktrees: []protocol.Worktree{worktree},
	})
}

func (d *Daemon) projectWorktreeDeleted(ev bus.Event) {
	d.wsHub.Broadcast(&protocol.WebSocketEvent{
		Event:     protocol.EventWorktreeDeleted,
		Worktrees: []protocol.Worktree{{Path: ev.Subject}},
	})
}

func (d *Daemon) projectWorktreesUpdated(ev bus.Event) {
	worktrees, ok := decodeFact[[]protocol.Worktree](d, ev)
	if !ok {
		return
	}
	d.wsHub.Broadcast(&protocol.WebSocketEvent{
		Event:     protocol.EventWorktreesUpdated,
		Worktrees: worktrees,
	})
}

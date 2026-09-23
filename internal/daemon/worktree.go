package daemon

import (
	"context"
	"errors"
	"fmt"
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
	_ = d.worktreeMaintenance.ProtectFromAutomaticCleanup(context.Background(), func(protection foregroundCleanupProtection) error {
		gitWorktrees, err := gitValue(protection.Context(), d.gitExecution(), gitTask{Kind: gitTaskWorktreeObserve, Lane: gitInteractive}, func(ctx context.Context, client *git.Client) ([]git.WorktreeEntry, error) {
			return client.ObserveLiveWorktrees(ctx, mainRepo)
		})
		if err != nil {
			result = storedWorktreesAsProtocol(d.store.ListWorktreesByRepo(mainRepo))
			return nil
		}
		result = d.reconcileListedWorktrees(protection, mainRepo, gitWorktrees)
		return nil
	})
	return result
}

func storedWorktreesAsProtocol(storedWorktrees []*store.Worktree) []protocol.Worktree {
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

func (d *Daemon) reconcileListedWorktrees(_ foregroundCleanupProtection, mainRepo string, liveWorktrees []git.WorktreeEntry) []protocol.Worktree {
	liveWorktreePaths := make(map[string]bool, len(liveWorktrees))
	for _, gwt := range liveWorktrees {
		liveWorktreePaths[gwt.Path] = true
	}

	var validWorktrees []*store.Worktree
	for _, wt := range d.store.ListWorktreesByRepo(mainRepo) {
		if liveWorktreePaths[wt.Path] {
			validWorktrees = append(validWorktrees, wt)
		} else {
			d.store.RemoveWorktree(wt.Path)
		}
	}

	for _, gwt := range liveWorktrees {
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

	return storedWorktreesAsProtocol(validWorktrees)
}

func (d *Daemon) doCreateWorktree(msg *protocol.CreateWorktreeMessage) (string, error) {
	var createdPath string
	err := d.worktreeMaintenance.ProtectFromAutomaticCleanup(context.Background(), func(protection foregroundCleanupProtection) error {
		var createErr error
		createdPath, createErr = d.doCreateWorktreeProtected(protection, msg)
		return createErr
	})
	return createdPath, err
}

func (d *Daemon) doCreateWorktreeProtected(protection foregroundCleanupProtection, msg *protocol.CreateWorktreeMessage) (string, error) {
	mainRepo, err := d.resolveMainRepo(protection.Context(), gitTask{Kind: gitTaskWorktreeMutation, Lane: gitInteractive}, msg.MainRepo)
	if err != nil {
		return "", err
	}

	requestedPath := protocol.Deref(msg.Path)
	path := requestedPath
	if path == "" {
		path = git.GenerateWorktreePath(mainRepo, msg.Branch)
	}
	path = git.CanonicalizePath(path)

	startingFrom := protocol.Deref(msg.StartingFrom)
	createdBranch := msg.Branch
	if hookErr := d.dispatchWorktreeBeforeCreateHooks(mainRepo, msg.Branch, startingFrom, requestedPath); hookErr != nil {
		return "", hookErr
	}
	providerPath, providerBranch, handled, providerErr := d.dispatchWorktreeCreateProvider(mainRepo, msg.Branch, startingFrom, requestedPath)
	if providerErr != nil {
		return "", providerErr
	}
	createdPath := providerPath
	if handled {
		createdBranch = providerBranch
	} else {
		mutationErr := d.gitExecution().Run(protection.Context(), gitTask{Kind: gitTaskWorktreeMutation, Lane: gitInteractive}, func(ctx context.Context, client *git.Client) error {
			if remote, branch, ok := strings.Cut(startingFrom, "/"); ok {
				if remotes, remotesErr := client.ListRemotes(ctx, mainRepo); remotesErr == nil && slices.Contains(remotes, remote) {
					if fetchErr := client.FetchRemoteBranch(ctx, mainRepo, remote, branch); fetchErr != nil {
						d.logf("Warning: could not fetch %s before creating worktree: %v", startingFrom, fetchErr)
					}
				}
			}
			if startingFrom != "" {
				exists, existsErr := client.RefExists(ctx, mainRepo, startingFrom)
				if existsErr != nil {
					return existsErr
				}
				if !exists {
					d.logf("Worktree start ref %q not resolvable in %s; falling back to current HEAD", startingFrom, mainRepo)
					startingFrom = ""
				}
			}
			if startingFrom != "" {
				return client.CreateWorktreeFromPoint(ctx, mainRepo, msg.Branch, path, startingFrom)
			}
			return client.CreateWorktree(ctx, mainRepo, msg.Branch, path)
		})
		if mutationErr != nil {
			return "", mutationErr
		}
		createdPath = path
	}
	d.registerCreatedWorktree(protection, mainRepo, createdPath, createdBranch)
	return createdPath, d.dispatchWorktreeAfterCreateHooks(mainRepo, createdPath, createdBranch)
}

func (d *Daemon) registerCreatedWorktree(_ foregroundCleanupProtection, mainRepo, path, branch string) {
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

func (d *Daemon) discoverWorktree(protection foregroundCleanupProtection, path string) *store.Worktree {
	type discovery struct {
		mainRepo string
		entries  []git.WorktreeEntry
	}
	result, err := gitValue(protection.Context(), d.gitExecution(), gitTask{Kind: gitTaskWorktreeObserve, Lane: gitInteractive}, func(ctx context.Context, client *git.Client) (discovery, error) {
		root, rootErr := client.GetRepoRoot(ctx, path)
		if rootErr != nil {
			return discovery{}, rootErr
		}
		mainRepo := client.ResolveMainRepoPath(ctx, root)
		entries, listErr := client.ObserveLiveWorktrees(ctx, mainRepo)
		return discovery{mainRepo: mainRepo, entries: entries}, listErr
	})
	if err != nil || result.mainRepo == "" {
		return nil
	}

	for _, gwt := range result.entries {
		if gwt.Path == path {
			wt := &store.Worktree{
				Path:      gwt.Path,
				Branch:    gwt.Branch,
				MainRepo:  result.mainRepo,
				CreatedAt: time.Now(),
			}
			d.store.AddWorktree(wt)
			d.logf("Discovered worktree not in registry: %s (branch: %s, main: %s)", path, gwt.Branch, result.mainRepo)
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
	err = d.worktreeMaintenance.ProtectFromAutomaticCleanup(context.Background(), func(protection foregroundCleanupProtection) error {
		return d.doDeleteWorktreeProtected(protection, path, endpointID, opts)
	})
	if err == nil {
		d.reconcileCrewRestarts()
	}
	return err
}

func (d *Daemon) doDeleteWorktreeProtected(protection foregroundCleanupProtection, path string, endpointID *string, opts deleteWorktreeOptions) (err error) {
	finishOperation := d.beginGitOperation(protocol.GitOperationKindDeleteWorktree, path, endpointID)
	defer func() {
		finishOperation(err)
	}()

	wt := d.store.GetWorktree(path)
	if wt == nil {
		wt = d.discoverWorktree(protection, path)
		if wt == nil {
			if _, statErr := os.Stat(path); os.IsNotExist(statErr) {
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

	seeds, failure := d.removeWorktreeCheckout(protection, wt, opts.Force)
	if failure != nil {
		if failure.fromProvider {
			return d.classifyDeleteWorktreeProviderError(path, opts.Force, failure.err)
		}
		return d.classifyDeleteWorktreeGitError(path, opts.Force, failure.err)
	}
	d.recordWorktreeRemoval(wt, seeds, opts, time.Now())
	return nil
}

type removalFailure struct {
	err          error
	fromProvider bool
}

func (d *Daemon) removeWorktreeCheckout(protection worktreeCleanupProtection, wt *store.Worktree, force bool) ([]string, *removalFailure) {
	d.captureGardenExecutionsInDirectory(wt.Path)
	seeds := d.seedsForWorktree(wt)
	handled, providerErr := d.dispatchWorktreeDeleteProvider(wt.MainRepo, wt.Path, wt.Branch, force)
	switch {
	case !handled && providerErr == nil:
		deleteErr := d.gitExecution().Run(protection.Context(), gitTask{Kind: gitTaskWorktreeMutation, Lane: gitInteractive}, func(ctx context.Context, client *git.Client) error {
			if err := client.DeleteWorktree(ctx, wt.MainRepo, wt.Path, force); err != nil {
				return err
			}
			d.deleteWorktreeBranch(ctx, client, wt.MainRepo, wt.Branch)
			return nil
		})
		if deleteErr != nil {
			return seeds, &removalFailure{err: deleteErr}
		}
	default:
		if !d.worktreeDeletionHappened(protection.Context(), wt.MainRepo, wt.Path) {
			if providerErr == nil {
				providerErr = errors.New("worktree delete provider reported success but the worktree still exists")
			}
			return seeds, &removalFailure{err: providerErr, fromProvider: true}
		}
		if err := d.settleProviderRemovedWorktree(protection.Context(), wt.MainRepo, wt.Branch); err != nil {
			return seeds, &removalFailure{err: err}
		}
	}
	d.finalizeDeletedWorktree(protection, wt.Path)
	return seeds, nil
}

func (d *Daemon) worktreeDeletionHappened(ctx context.Context, mainRepo, path string) bool {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return true
	}
	states, err := gitValue(ctx, d.gitExecution(), gitTask{Kind: gitTaskWorktreeObserve, Lane: gitInteractive}, func(runCtx context.Context, client *git.Client) ([]git.WorktreeState, error) {
		return client.ListWorktreeStates(runCtx, mainRepo)
	})
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

func (d *Daemon) settleProviderRemovedWorktree(ctx context.Context, mainRepo, branch string) error {
	deleteBranch := d.worktreeBranchIsDeletable(mainRepo, branch)
	return d.gitExecution().Run(ctx, gitTask{Kind: gitTaskWorktreeMutation, Lane: gitInteractive}, func(runCtx context.Context, client *git.Client) error {
		if err := client.PruneWorktrees(runCtx, mainRepo); err != nil {
			return fmt.Errorf("provider removed a worktree of %s but pruning its registration failed: %w", mainRepo, err)
		}
		if deleteBranch {
			d.deleteDeletableWorktreeBranch(runCtx, client, mainRepo, branch)
		}
		return nil
	})
}

func (d *Daemon) deleteWorktreeBranch(ctx context.Context, client *git.Client, mainRepo, branch string) {
	if d.worktreeBranchIsDeletable(mainRepo, branch) {
		d.deleteDeletableWorktreeBranch(ctx, client, mainRepo, branch)
	}
}

func (d *Daemon) worktreeBranchIsDeletable(mainRepo, branch string) bool {
	if branch == "" {
		return false
	}
	if d.gardenKeepsBranch(mainRepo, branch) {
		d.logf("Preserved branch %s because an open seed can continue from it", branch)
		return false
	}
	return true
}

func (d *Daemon) deleteDeletableWorktreeBranch(ctx context.Context, client *git.Client, mainRepo, branch string) {
	if err := client.DeleteBranch(ctx, mainRepo, branch, true); err != nil {
		d.logf("Warning: worktree deleted but failed to delete branch %s: %v", branch, err)
		return
	}
	d.logf("Deleted branch %s along with worktree", branch)
}

func (d *Daemon) finalizeDeletedWorktree(_ worktreeCleanupProtection, path string) {
	d.cleanupDeletedWorktreeSessions(path)
	d.store.RemoveWorktree(path)
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
	status, _, err := d.statusReader().Status(context.Background(), path, gitStatusModeFull)
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

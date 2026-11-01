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
	"github.com/victorarias/attn/internal/who"
)

func (d *Daemon) doListWorktrees(mainRepo string) []protocol.Worktree {
	gitWorktrees, err := gitValue(context.Background(), d.gitExecution(), gitTask{Kind: gitTaskWorktreeObserve, Lane: gitInteractive}, func(ctx context.Context, client *git.Client) ([]git.WorktreeEntry, error) {
		return client.ObserveLiveWorktrees(ctx, mainRepo)
	})
	if err != nil {
		return storedWorktreesAsProtocol(d.store.ListWorktreesByRepo(mainRepo))
	}
	return d.listedWorktreesAsProtocol(mainRepo, gitWorktrees)
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

func (d *Daemon) listedWorktreesAsProtocol(mainRepo string, liveWorktrees []git.WorktreeEntry) []protocol.Worktree {
	storedByPath := make(map[string]*store.Worktree)
	for _, wt := range d.store.ListWorktreesByRepo(mainRepo) {
		storedByPath[wt.Path] = wt
	}
	result := make([]protocol.Worktree, 0, len(liveWorktrees))
	for _, gwt := range liveWorktrees {
		if gwt.Path == mainRepo {
			continue
		}
		wt := protocol.Worktree{Path: gwt.Path, Branch: gwt.Branch, MainRepo: mainRepo}
		if stored := storedByPath[gwt.Path]; stored != nil {
			wt.CreatedAt = protocol.Ptr(stored.CreatedAt.Format(time.RFC3339))
		}
		result = append(result, wt)
	}
	return result
}

func (d *Daemon) doCreateWorktree(msg *protocol.CreateWorktreeMessage) (string, error) {
	result, err := Call[protocol.WorktreeCreation](context.Background(), d.router(), *msg)
	if err == nil && result.Error != nil {
		err = errors.New(*result.Error)
	}
	return result.Path, err
}

func (d *Daemon) doCreateWorktreeProtected(protection foregroundCleanupProtection, msg *protocol.CreateWorktreeMessage) (string, error) {
	mainRepo, err := d.resolveMainRepo(protection.Context(), gitTask{Kind: gitTaskWorktreeMutation, Lane: gitInteractive}, msg.MainRepo)
	if err != nil {
		return "", err
	}
	d.store.MonitorWorktreeRepository(mainRepo)

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
			return client.CreateWorktreeFromPoint(ctx, mainRepo, msg.Branch, path, startingFrom)
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

func (d *Daemon) readWorktreeFromGit(ctx context.Context, path string) *store.Worktree {
	type discovery struct {
		mainRepo string
		entries  []git.WorktreeEntry
	}
	result, err := gitValue(ctx, d.gitExecution(), gitTask{Kind: gitTaskWorktreeObserve, Lane: gitInteractive}, func(ctx context.Context, client *git.Client) (discovery, error) {
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
			d.logf("Found worktree in Git: %s (branch: %s, main: %s)", path, gwt.Branch, result.mainRepo)
			return &store.Worktree{
				Path:     gwt.Path,
				Branch:   gwt.Branch,
				MainRepo: result.mainRepo,
			}
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

func (d *Daemon) doDeleteWorktree(path string, opts deleteWorktreeOptions) error {
	return d.worktreeMaintenance.ProtectFromAutomaticCleanup(context.Background(), func(protection foregroundCleanupProtection) error {
		return d.doDeleteWorktreeProtected(protection, path, opts)
	})
}

func (d *Daemon) doDeleteWorktreeProtected(protection foregroundCleanupProtection, path string, opts deleteWorktreeOptions) (err error) {
	finishOperation := d.beginGitOperation(protocol.GitOperationKindDeleteWorktree, path)
	defer func() {
		finishOperation(err)
	}()

	wt := d.store.GetWorktree(path)
	if wt == nil {
		wt = d.readWorktreeFromGit(protection.Context(), path)
		if wt == nil {
			if _, statErr := os.Stat(path); os.IsNotExist(statErr) {
				d.logf("Worktree %s doesn't exist and not in registry, treating as already deleted", path)
				d.publishFact(FactWorktreeDeleted, path, nil)
				if _, err := Call[struct{}](protection.Context(), d.router(), protocol.WorktreeRemovedMessage{Cmd: protocol.CmdWorktreeRemoved, Path: path}); err != nil {
					return &deleteWorktreeError{err: fmt.Errorf("worktree %s is gone, but closing its sessions failed: %w", path, err), kind: deleteWorktreeFailureGitError}
				}
				return nil
			}
			return &deleteWorktreeError{
				err:  &worktreeNotFoundError{path: path},
				kind: deleteWorktreeFailureNotFound,
			}
		}
	}

	if failure := d.removeWorktreeCheckout(protection, wt, opts); failure != nil {
		switch {
		case failure.unclassified:
			return &deleteWorktreeError{err: failure.err, kind: deleteWorktreeFailureGitError}
		case failure.fromProvider:
			return d.classifyDeleteWorktreeProviderError(path, opts.Force, failure.err)
		}
		return d.classifyDeleteWorktreeGitError(path, opts.Force, failure.err)
	}
	d.recordWorktreeRemoval(wt, opts, time.Now())
	return nil
}

type removalFailure struct {
	err          error
	fromProvider bool
	unclassified bool
}

func (d *Daemon) removeWorktreeCheckout(protection worktreeCleanupProtection, wt *store.Worktree, opts deleteWorktreeOptions) *removalFailure {
	plan, err := Call[protocol.WorktreeRemovalPlan](protection.Context(), d.router(), protocol.WorktreeRemovalPrepareMessage{
		Cmd: protocol.CmdWorktreeRemovalPrepare, MainRepo: wt.MainRepo, Path: wt.Path, Branch: wt.Branch,
	})
	if err != nil {
		return &removalFailure{err: fmt.Errorf("worktree %s was left in place: %w", wt.Path, err), unclassified: true}
	}
	handled, providerErr := d.dispatchWorktreeDeleteProvider(wt.MainRepo, wt.Path, wt.Branch, opts.Force)
	switch {
	case !handled && providerErr == nil:
		deleteErr := d.gitExecution().Run(protection.Context(), gitTask{Kind: gitTaskWorktreeMutation, Lane: gitInteractive}, func(ctx context.Context, client *git.Client) error {
			if err := client.DeleteWorktree(ctx, wt.MainRepo, wt.Path, opts.Force); err != nil {
				return err
			}
			d.deleteWorktreeBranch(ctx, client, wt.MainRepo, wt.Branch, plan.KeepBranch)
			return nil
		})
		if deleteErr != nil {
			return &removalFailure{err: deleteErr}
		}
	default:
		if !d.worktreeDeletionHappened(protection.Context(), wt.MainRepo, wt.Path) {
			if providerErr == nil {
				providerErr = errors.New("worktree delete provider reported success but the worktree still exists")
			}
			return &removalFailure{err: providerErr, fromProvider: true}
		}
		if err := d.settleProviderRemovedWorktree(protection.Context(), wt.MainRepo, wt.Branch, plan.KeepBranch); err != nil {
			return &removalFailure{err: err}
		}
	}
	return d.finalizeDeletedWorktree(protection, wt, plan.Seeds, opts)
}

func (d *Daemon) prepareWorktreeRemovalHere(ctx context.Context, msg protocol.WorktreeRemovalPrepareMessage) (protocol.WorktreeRemovalPlan, error) {
	d.captureGardenExecutionsInDirectory(removingMachine(ctx), msg.Path)
	seeds, err := d.seedsForWorktree(&store.Worktree{Path: msg.Path, MainRepo: msg.MainRepo, Branch: msg.Branch})
	if err != nil {
		return protocol.WorktreeRemovalPlan{}, err
	}
	return protocol.WorktreeRemovalPlan{
		Seeds:      seeds,
		KeepBranch: d.gardenKeepsBranch(msg.MainRepo, msg.Branch),
	}, nil
}

func removingMachine(ctx context.Context) string {
	if origin, remote := OriginOf(ctx); remote {
		return origin.Host
	}
	return ""
}

func (d *Daemon) worktreeRemovedHere(ctx context.Context, msg protocol.WorktreeRemovedMessage) (struct{}, error) {
	d.closeWorktreeSessions(removingMachine(ctx), msg.Path)
	for _, seedID := range msg.Seeds {
		if _, err := d.appendSeedNote(seedID, protocol.Deref(msg.Note), who.Attn(), "", nil, false, ""); err != nil {
			d.logf("worktree removal: noting %s on seed %s: %v", msg.Path, seedID, err)
		}
	}
	return struct{}{}, nil
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

func (d *Daemon) settleProviderRemovedWorktree(ctx context.Context, mainRepo, branch string, keepBranch bool) error {
	deleteBranch := d.worktreeBranchIsDeletable(branch, keepBranch)
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

func (d *Daemon) deleteWorktreeBranch(ctx context.Context, client *git.Client, mainRepo, branch string, keepBranch bool) {
	if d.worktreeBranchIsDeletable(branch, keepBranch) {
		d.deleteDeletableWorktreeBranch(ctx, client, mainRepo, branch)
	}
}

func (d *Daemon) worktreeBranchIsDeletable(branch string, keepBranch bool) bool {
	if branch == "" {
		return false
	}
	if keepBranch {
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

func (d *Daemon) finalizeDeletedWorktree(protection worktreeCleanupProtection, wt *store.Worktree, seeds []string, opts deleteWorktreeOptions) *removalFailure {
	_, err := Call[struct{}](protection.Context(), d.router(), protocol.WorktreeRemovedMessage{
		Cmd: protocol.CmdWorktreeRemoved, Path: wt.Path, Seeds: seeds, Note: protocol.Ptr(worktreeRemovalNote(wt, opts)),
	})
	d.store.RemoveWorktree(wt.Path)
	d.publishFact(FactWorktreeDeleted, wt.Path, nil)
	if err != nil {
		return &removalFailure{err: fmt.Errorf("worktree %s was removed, but closing its sessions and noting its seeds failed: %w", wt.Path, err), unclassified: true}
	}
	return nil
}

func (d *Daemon) closeWorktreeSessions(machine, path string) {
	for _, session := range d.store.List("") {
		if d.sessionHost(session.ID) != machine || !pathAtOrBelow(session.Directory, path) {
			continue
		}
		d.terminateSession(session.ID, syscall.SIGTERM)
		if err := d.closeSession(session.ID, store.SessionClose{By: who.User(), Reason: "worktree deleted"}); err != nil {
			d.logf("close deleted worktree session %s: %v", session.ID, err)
			continue
		}
		d.publishSessionUnregistered(session)
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
	dirtyFiles, err := gitValue(context.Background(), d.gitExecution(), gitTask{Kind: gitTaskWorktreeObserve, Lane: gitInteractive}, func(ctx context.Context, client *git.Client) (int, error) {
		return client.WorktreeDirtyCount(ctx, path)
	})
	return err == nil && dirtyFiles > 0
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
	if result := d.deleteWorktree(msg); !result.Success {
		d.sendError(conn, protocol.Deref(result.Error))
		return
	}
	d.sendOK(conn)
}

func (d *Daemon) deleteWorktree(msg *protocol.DeleteWorktreeMessage) protocol.DeleteWorktreeResultMessage {
	result, err := Call[protocol.DeleteWorktreeResultMessage](context.Background(), d.router(), *msg)
	if err != nil {
		result = protocol.DeleteWorktreeResultMessage{Event: protocol.EventDeleteWorktreeResult, Path: msg.Path, Error: protocol.Ptr(err.Error())}
	}
	result.RequestID = msg.RequestID
	return result
}

func (d *Daemon) handleListWorktreesWS(client *wsClient, msg *protocol.ListWorktreesMessage) {
	protoWorktrees := d.doListWorktrees(msg.MainRepo)
	d.sendToClient(client, &protocol.WebSocketEvent{
		Event:     protocol.EventWorktreesUpdated,
		Worktrees: protoWorktrees,
	})
}

func (d *Daemon) handleCreateWorktreeWS(client *wsClient, msg *protocol.CreateWorktreeMessage) {
	d.life.Go("handleCreateWorktreeWS", func() {
		path, err := d.doCreateWorktree(msg)
		result := protocol.CreateWorktreeResultMessage{
			Event:     protocol.EventCreateWorktreeResult,
			RequestID: msg.RequestID,
			Path:      protocol.Ptr(path),
			Success:   err == nil,
		}
		if err != nil {
			result.Error = protocol.Ptr(err.Error())
			d.logf("Create worktree failed for %s: %v", msg.Branch, err)
		} else {
			d.logf("Create worktree succeeded: %s at %s", msg.Branch, path)
		}
		d.sendToClient(client, result)
	})
}

func (d *Daemon) handleDeleteWorktreeWS(client *wsClient, msg *protocol.DeleteWorktreeMessage) {
	d.life.Go("handleDeleteWorktreeWS", func() {
		defer d.publishFact(FactWorktreeSessionsRemoved, msg.Path, nil)

		result := d.deleteWorktree(msg)
		if result.Success {
			d.logf("Delete worktree succeeded: %s", msg.Path)
		} else {
			d.logf("Delete worktree failed for %s: %s", msg.Path, protocol.Deref(result.Error))
		}
		d.sendToClient(client, result)
	})
}

func (d *Daemon) deleteWorktreeHere(_ context.Context, msg protocol.DeleteWorktreeMessage) (protocol.DeleteWorktreeResultMessage, error) {
	result := protocol.DeleteWorktreeResultMessage{Event: protocol.EventDeleteWorktreeResult, Path: msg.Path, Success: true}
	err := d.doDeleteWorktree(msg.Path, deleteWorktreeOptions{Force: protocol.Deref(msg.Force)})
	if err == nil {
		return result, nil
	}
	result.Success = false
	result.Error = protocol.Ptr(err.Error())
	var deleteErr *deleteWorktreeError
	if errors.As(err, &deleteErr) {
		if deleteErr.kind != "" {
			result.ReasonKind = string(deleteErr.kind)
		}
		result.Forceable = protocol.Ptr(deleteErr.forceable)
	}
	return result, nil
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

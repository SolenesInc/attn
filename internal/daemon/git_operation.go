package daemon

import (
	"context"
	"errors"
	"io/fs"
	"os/exec"
	"time"

	"github.com/victorarias/attn/internal/bus"

	"github.com/google/uuid"
	"github.com/victorarias/attn/internal/protocol"
)

func (d *Daemon) beginGitOperation(kind protocol.GitOperationKind, path string) func(error) {
	startedAt := time.Now()
	operation := protocol.GitOperation{
		ID:        uuid.NewString(),
		Kind:      kind,
		Status:    protocol.GitOperationStatusRunning,
		Path:      protocol.Ptr(path),
		StartedAt: startedAt.Format(time.RFC3339),
	}

	d.publishFact(FactGitOperationStarted, operation.ID, operation)

	return func(err error) {
		finishedAt := time.Now()
		operation.Status = protocol.GitOperationStatusSucceeded
		if err != nil {
			failure, exitCode := gitFailureOf(err)
			operation.Status = protocol.GitOperationStatusFailed
			operation.Error = protocol.Ptr(err.Error())
			operation.Failure = protocol.Ptr(failure)
			operation.ExitCode = exitCode
		}
		operation.FinishedAt = protocol.Ptr(finishedAt.Format(time.RFC3339))
		operation.DurationMs = protocol.Ptr(int(finishedAt.Sub(startedAt).Milliseconds()))

		d.publishFact(FactGitOperationFinished, operation.ID, operation)
	}
}

func gitFailureOf(err error) (protocol.GitFailure, *int) {
	var saturated *ErrGitQueueSaturated
	var path *fs.PathError
	var deleteErr *deleteWorktreeError
	var exited *exec.ExitError
	switch {
	case errors.Is(err, context.Canceled), errors.Is(err, errAutomaticWorktreeCleanupPreempted), errors.Is(err, ErrGitExecutorClosed):
		return protocol.GitFailureCancelled, nil
	case errors.As(err, &saturated):
		return protocol.GitFailureQueueFull, nil
	case errors.As(err, &path) && path.Op == "chdir" && errors.Is(path, fs.ErrNotExist):
		return protocol.GitFailureMissingDirectory, nil
	case errors.Is(err, exec.ErrNotFound):
		return protocol.GitFailureGitUnavailable, nil
	case errors.Is(err, context.DeadlineExceeded):
		return protocol.GitFailureTimeout, nil
	case errors.Is(err, errIntegrationRefUnresolved):
		return protocol.GitFailureIntegrationRefUnresolved, nil
	case errors.Is(err, errWorktreeStashCounts):
		return protocol.GitFailureStashUnreadable, nil
	}
	if errors.As(err, &deleteErr) {
		switch deleteErr.kind {
		case deleteWorktreeFailureDirtyWorktree:
			return protocol.GitFailureDirtyWorktree, nil
		case deleteWorktreeFailureNotFound:
			return protocol.GitFailureWorktreeNotFound, nil
		case deleteWorktreeFailureProviderError:
			return protocol.GitFailureProviderError, nil
		}
	}
	if errors.As(err, &exited) {
		return protocol.GitFailureExited, protocol.Ptr(exited.ExitCode())
	}
	return protocol.GitFailureUnclassified, nil
}

func (d *Daemon) projectGitOperation(ev bus.Event) {
	operation, ok := decodeFact[protocol.GitOperation](d, ev)
	if !ok {
		return
	}
	event := protocol.EventGitOperationStarted
	if ev.Name == FactGitOperationFinished {
		event = protocol.EventGitOperationFinished
	}
	d.wsHub.Broadcast(&protocol.WebSocketEvent{
		Event:     event,
		Operation: &operation,
	})
}

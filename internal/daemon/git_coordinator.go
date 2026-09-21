package daemon

import (
	"context"
	"os"
	"path/filepath"
	"time"

	attngit "github.com/victorarias/attn/internal/git"
	"github.com/victorarias/attn/internal/protocol"
)

type gitStatusCacheKey struct {
	directory string
	mode      gitStatusMode
}

type gitStatusResult struct {
	status   *protocol.GitStatusUpdateMessage
	duration time.Duration
}

type gitStatusReader struct {
	executor gitExecutor
	calls    *sharedCalls[gitStatusCacheKey, gitStatusResult]
}

type fileDiffCacheKey struct {
	directory string
	path      string
	baseRef   string
	headRef   string
	staged    bool
}

type fileDiffContent struct {
	original string
	modified string
}

type fileDiffReader struct {
	executor gitExecutor
	calls    *sharedCalls[fileDiffCacheKey, fileDiffContent]
}

var (
	getGitStatusForDaemon = getGitStatusForSubscription
	readFileDiffForDaemon = readFileDiffCoordinated
)

func newGitStatusReader(executor gitExecutor) *gitStatusReader {
	return &gitStatusReader{executor: executor, calls: newSharedCalls[gitStatusCacheKey, gitStatusResult](context.Background())}
}

func newFileDiffReader(executor gitExecutor) *fileDiffReader {
	return &fileDiffReader{executor: executor, calls: newSharedCalls[fileDiffCacheKey, fileDiffContent](context.Background())}
}

func (d *Daemon) statusReader() *gitStatusReader {
	d.gitReaderMu.Lock()
	defer d.gitReaderMu.Unlock()
	if d.gitStatus == nil {
		d.gitStatus = newGitStatusReader(d.gitExecution())
	}
	return d.gitStatus
}

func (d *Daemon) diffReader() *fileDiffReader {
	d.gitReaderMu.Lock()
	defer d.gitReaderMu.Unlock()
	if d.fileDiff == nil {
		d.fileDiff = newFileDiffReader(d.gitExecution())
	}
	return d.fileDiff
}

func (r *gitStatusReader) Status(ctx context.Context, directory string, mode gitStatusMode) (*protocol.GitStatusUpdateMessage, time.Duration, error) {
	key := gitStatusCacheKey{directory: directory, mode: mode}
	result, err := r.calls.Do(ctx, key, func(callCtx context.Context) (gitStatusResult, error) {
		started := time.Now()
		status, runErr := getGitStatusForDaemon(callCtx, r.executor, directory, mode)
		return gitStatusResult{status: status, duration: time.Since(started)}, runErr
	})
	return cloneGitStatusUpdate(result.status), result.duration, err
}

func (r *fileDiffReader) FileDiff(ctx context.Context, directory, path, baseRef, headRef string, staged bool) (fileDiffContent, error) {
	key := fileDiffCacheKey{directory: directory, path: path, baseRef: baseRef, headRef: headRef, staged: staged}
	return r.calls.Do(ctx, key, func(callCtx context.Context) (fileDiffContent, error) {
		return readFileDiffForDaemon(callCtx, r.executor, key)
	})
}

func readFileDiff(directory, path, baseRef, headRef string, staged bool) (fileDiffContent, error) {
	executor, err := newDirectGitExecutor()
	if err != nil {
		return fileDiffContent{}, err
	}
	defer executor.Close(nil)
	return readFileDiffCoordinated(context.Background(), executor, fileDiffCacheKey{
		directory: directory,
		path:      path,
		baseRef:   baseRef,
		headRef:   headRef,
		staged:    staged,
	})
}

func readFileDiffCoordinated(ctx context.Context, executor gitExecutor, key fileDiffCacheKey) (fileDiffContent, error) {
	content, err := gitValue(ctx, executor, gitTask{Kind: gitTaskFileDiff, Lane: gitInteractive}, func(runCtx context.Context, client *attngit.Client) (fileDiffContent, error) {
		content := fileDiffContent{}
		origOutput, origErr := client.Output(runCtx, attngit.OpDiff, key.directory, "show", key.baseRef+":"+key.path)
		if origErr == nil {
			content.original = string(origOutput)
		}
		if key.headRef != "" {
			headOutput, headErr := client.Output(runCtx, attngit.OpDiff, key.directory, "show", key.headRef+":"+key.path)
			if headErr == nil {
				content.modified = string(headOutput)
			}
			return content, nil
		}
		if key.staged {
			stagedOutput, stagedErr := client.Output(runCtx, attngit.OpDiff, key.directory, "show", ":"+key.path)
			if stagedErr != nil {
				return fileDiffContent{}, stagedErr
			}
			content.modified = string(stagedOutput)
		}
		return content, nil
	})
	if err != nil || key.headRef != "" || key.staged {
		return content, err
	}
	modified, err := os.ReadFile(filepath.Join(key.directory, key.path))
	if err != nil {
		if os.IsNotExist(err) {
			return content, nil
		}
		return fileDiffContent{}, err
	}
	content.modified = string(modified)
	return content, nil
}

func testDirectGitExecutorConfig() gitExecutorConfig {
	return gitExecutorConfig{
		MaxActive:            2,
		MaxDeferredActive:    1,
		InteractiveBurst:     1,
		MaxQueuedInteractive: 8,
		MaxQueuedDeferred:    8,
	}
}

func cloneGitStatusUpdate(status *protocol.GitStatusUpdateMessage) *protocol.GitStatusUpdateMessage {
	if status == nil {
		return nil
	}
	cloned := *status
	cloned.Staged = cloneGitFileChanges(status.Staged)
	cloned.Unstaged = cloneGitFileChanges(status.Unstaged)
	cloned.Untracked = cloneGitFileChanges(status.Untracked)
	return &cloned
}

func cloneGitFileChanges(files []protocol.GitFileChange) []protocol.GitFileChange {
	if files == nil {
		return nil
	}
	cloned := make([]protocol.GitFileChange, len(files))
	copy(cloned, files)
	return cloned
}

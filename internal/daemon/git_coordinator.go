package daemon

import (
	"context"
	"os"
	"path/filepath"

	attngit "github.com/victorarias/attn/internal/git"
)

type fileDiffCacheKey struct {
	directory string
	path      string
	baseRef   string
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

var readFileDiffForDaemon = readFileDiffCoordinated

func newFileDiffReader(executor gitExecutor, life *lifetime) *fileDiffReader {
	return &fileDiffReader{executor: executor, calls: newSharedCalls[fileDiffCacheKey, fileDiffContent](life, "fileDiff")}
}

func (d *Daemon) diffReader() *fileDiffReader {
	d.gitReaderMu.Lock()
	defer d.gitReaderMu.Unlock()
	if d.fileDiff == nil {
		d.fileDiff = newFileDiffReader(d.gitExecution(), &d.life)
	}
	return d.fileDiff
}

func (r *fileDiffReader) FileDiff(ctx context.Context, directory, path, baseRef string, staged bool) (fileDiffContent, error) {
	key := fileDiffCacheKey{directory: directory, path: path, baseRef: baseRef, staged: staged}
	return r.calls.Do(ctx, key, func(callCtx context.Context) (fileDiffContent, error) {
		return readFileDiffForDaemon(callCtx, r.executor, key)
	})
}

func readFileDiffCoordinated(ctx context.Context, executor gitExecutor, key fileDiffCacheKey) (fileDiffContent, error) {
	content, err := gitValue(ctx, executor, gitTask{Kind: gitTaskFileDiff, Lane: gitInteractive}, func(runCtx context.Context, client *attngit.Client) (fileDiffContent, error) {
		content := fileDiffContent{}
		origOutput, origErr := client.Output(runCtx, attngit.OpDiff, key.directory, "show", key.baseRef+":"+key.path)
		if origErr == nil {
			content.original = string(origOutput)
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
	if err != nil || key.staged {
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

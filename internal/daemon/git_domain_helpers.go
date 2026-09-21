package daemon

import (
	"context"

	attngit "github.com/victorarias/attn/internal/git"
)

func (d *Daemon) readBranchInfo(ctx context.Context, kind gitTaskKind, lane gitLane, dir string) (*attngit.BranchInfo, error) {
	return gitValue(ctx, d.gitExecution(), gitTask{Kind: kind, Lane: lane}, func(runCtx context.Context, client *attngit.Client) (*attngit.BranchInfo, error) {
		return client.GetBranchInfo(runCtx, dir)
	})
}

func (d *Daemon) readRepoRoot(ctx context.Context, kind gitTaskKind, lane gitLane, dir string) (string, error) {
	return gitValue(ctx, d.gitExecution(), gitTask{Kind: kind, Lane: lane}, func(runCtx context.Context, client *attngit.Client) (string, error) {
		return client.GetRepoRoot(runCtx, dir)
	})
}

func (d *Daemon) resolveMainRepo(ctx context.Context, kind gitTaskKind, lane gitLane, path string) (string, error) {
	return gitValue(ctx, d.gitExecution(), gitTask{Kind: kind, Lane: lane}, func(runCtx context.Context, client *attngit.Client) (string, error) {
		_, err := client.GetRepoRoot(runCtx, path)
		if err != nil {
			return "", err
		}
		return client.ResolveMainRepoPath(runCtx, path), nil
	})
}

func (d *Daemon) gitOutput(ctx context.Context, task gitTask, op attngit.Operation, dir string, args ...string) ([]byte, error) {
	return gitValue(ctx, d.gitExecution(), task, func(runCtx context.Context, client *attngit.Client) ([]byte, error) {
		return client.Output(runCtx, op, dir, args...)
	})
}

func (d *Daemon) refExists(ctx context.Context, kind gitTaskKind, lane gitLane, repo, ref string) (bool, error) {
	return gitValue(ctx, d.gitExecution(), gitTask{Kind: kind, Lane: lane}, func(runCtx context.Context, client *attngit.Client) (bool, error) {
		return client.RefExists(runCtx, repo, ref)
	})
}

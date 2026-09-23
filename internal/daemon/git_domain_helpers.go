package daemon

import (
	"context"

	attngit "github.com/victorarias/attn/internal/git"
)

func (d *Daemon) readBranchInfo(ctx context.Context, task gitTask, dir string) (*attngit.BranchInfo, error) {
	return gitValue(ctx, d.gitExecution(), task, func(runCtx context.Context, client *attngit.Client) (*attngit.BranchInfo, error) {
		return client.GetBranchInfo(runCtx, dir)
	})
}

func (d *Daemon) readRepoRoot(ctx context.Context, task gitTask, dir string) (string, error) {
	return gitValue(ctx, d.gitExecution(), task, func(runCtx context.Context, client *attngit.Client) (string, error) {
		return client.GetRepoRoot(runCtx, dir)
	})
}

func (d *Daemon) readCheckoutRepo(ctx context.Context, task gitTask, dir string) (checkoutRoot, mainRepo string, err error) {
	err = d.gitExecution().Run(ctx, task, func(runCtx context.Context, client *attngit.Client) error {
		root, rootErr := client.GetRepoRoot(runCtx, dir)
		if rootErr != nil {
			return rootErr
		}
		checkoutRoot = root
		mainRepo = client.ResolveMainRepoPath(runCtx, root)
		return nil
	})
	if err != nil {
		return "", "", err
	}
	return checkoutRoot, mainRepo, nil
}

func (d *Daemon) resolveMainRepo(ctx context.Context, task gitTask, path string) (string, error) {
	return gitValue(ctx, d.gitExecution(), task, func(runCtx context.Context, client *attngit.Client) (string, error) {
		resolved, err := client.ResolveRepoDir(runCtx, path)
		if err != nil {
			return "", err
		}
		root, err := client.GetRepoRoot(runCtx, resolved)
		if err != nil {
			return "", err
		}
		return client.ResolveMainRepoPath(runCtx, root), nil
	})
}

func (d *Daemon) gitOutput(ctx context.Context, task gitTask, op attngit.Operation, dir string, args ...string) ([]byte, error) {
	return gitValue(ctx, d.gitExecution(), task, func(runCtx context.Context, client *attngit.Client) ([]byte, error) {
		return client.Output(runCtx, op, dir, args...)
	})
}

func (d *Daemon) refExists(ctx context.Context, task gitTask, repo, ref string) (bool, error) {
	return gitValue(ctx, d.gitExecution(), task, func(runCtx context.Context, client *attngit.Client) (bool, error) {
		return client.RefExists(runCtx, repo, ref)
	})
}

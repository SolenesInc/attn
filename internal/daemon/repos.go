package daemon

import (
	"context"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"

	attngit "github.com/victorarias/attn/internal/git"
	"github.com/victorarias/attn/internal/setting"
)

type repositoryStores struct {
	mu    sync.Mutex
	locks map[string]*sync.Mutex
}

func (d *Daemon) lockRepositoryStore(identity string) (unlock func()) {
	d.repositoryStores.mu.Lock()
	if d.repositoryStores.locks == nil {
		d.repositoryStores.locks = make(map[string]*sync.Mutex)
	}
	lock := d.repositoryStores.locks[identity]
	if lock == nil {
		lock = &sync.Mutex{}
		d.repositoryStores.locks[identity] = lock
	}
	d.repositoryStores.mu.Unlock()
	lock.Lock()
	return lock.Unlock
}

func (d *Daemon) repositoryStorePath(identity string) string {
	return filepath.Join(d.dataRoot, "repos", attngit.RepositoryCacheKey(identity))
}

func preStoreAutomationCache(root, identity string) string {
	return filepath.Join(root, "automation", "repos", attngit.RepositoryCacheKey(identity), "repo")
}

func (d *Daemon) worktreeBelongsTo(ctx context.Context, worktree, mainRepo string) bool {
	belongs, _ := gitValue(ctx, d.gitExecution(), gitTask{Kind: gitTaskAutomation, Lane: gitDeferred}, func(runCtx context.Context, client *attngit.Client) (bool, error) {
		if _, err := os.Stat(worktree); err != nil {
			return false, nil
		}
		return attngit.CanonicalizePath(client.ResolveMainRepoPath(runCtx, worktree)) == attngit.CanonicalizePath(mainRepo), nil
	})
	return belongs
}

func (d *Daemon) gitHTTPSAuthorization(host string) string {
	if d.ghRegistry == nil {
		return ""
	}
	if client, ok := d.ghRegistry.Get(host); ok {
		return client.GitHTTPSAuthorizationHeader()
	}
	return ""
}

func (d *Daemon) ensureRepositoryStore(ctx context.Context, identity, authorization string) (string, error) {
	return gitValue(ctx, d.gitExecution(), gitTask{Kind: gitTaskAutomation, Lane: gitDeferred}, func(runCtx context.Context, client *attngit.Client) (string, error) {
		transport := d.repositoryTransport(runCtx, client, identity)
		mainRepo, _, err := client.EnsureManagedClone(runCtx, transport, d.repositoryStorePath(identity), identity, authorization)
		return mainRepo, err
	})
}

func (d *Daemon) repositoryTransport(ctx context.Context, client *attngit.Client, identity string) string {
	if clone := d.personalClone(identity); clone != "" {
		if origin, err := client.LocalCloneOrigin(ctx, clone, identity); err == nil {
			return origin
		}
	}
	return "https://" + identity + ".git"
}

func (d *Daemon) personalClone(identity string) string {
	projects := strings.TrimSpace(d.setting(setting.ProjectsDirectory))
	if projects == "" {
		return ""
	}
	return filepath.Join(attngit.ExpandPath(projects), path.Base(identity))
}

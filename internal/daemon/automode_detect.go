package daemon

import (
	"context"
	"strings"

	"github.com/victorarias/attn/internal/automode"
	attngit "github.com/victorarias/attn/internal/git"
)

func (d *Daemon) detectAutoModeEnvironment(cwd string) map[string][]string {
	type detection struct {
		slots      map[string][]string
		identities []string
	}
	detectedResult, err := gitValue(context.Background(), d.gitExecution(), gitTask{Kind: gitTaskAutoMode, Lane: gitInteractive, Effect: gitRead, Scope: cwd}, func(ctx context.Context, client *attngit.Client) (detection, error) {
		slots, identities := automode.DetectFromRepoWithGit(ctx, client, cwd)
		return detection{slots: slots, identities: identities}, nil
	})
	if err != nil {
		return nil
	}
	detected, identities := detectedResult.slots, detectedResult.identities
	if detected == nil {
		return nil
	}
	if len(identities) > 0 {
		if visibility, ok := d.repoVisibility(identities[0]); ok {
			detected["repo_visibility"] = []string{visibility}
		}
	}
	return detected
}

func (d *Daemon) repoVisibility(identity string) (string, bool) {
	d.repoVisibilityMu.Lock()
	defer d.repoVisibilityMu.Unlock()
	if visibility, known := d.repoVisibilityKnown[identity]; known {
		return visibility, true
	}
	if !d.repoVisibilityPending[identity] {
		if d.repoVisibilityPending == nil {
			d.repoVisibilityPending = map[string]bool{}
		}
		d.repoVisibilityPending[identity] = true
		go d.lookUpRepoVisibility(identity)
	}
	return "", false
}

func (d *Daemon) lookUpRepoVisibility(identity string) {
	defer func() {
		d.repoVisibilityMu.Lock()
		delete(d.repoVisibilityPending, identity)
		d.repoVisibilityMu.Unlock()
	}()

	host, ownerRepo, ok := splitRepoIdentity(identity)
	if !ok || d.ghRegistry == nil {
		return
	}
	client, ok := d.ghRegistry.Get(host)
	if !ok || client == nil {
		return
	}
	visibility, err := client.RepoVisibility(ownerRepo)
	if err != nil {
		d.logf("[automode] repo visibility for %s unknown: %v", identity, err)
		return
	}
	d.repoVisibilityMu.Lock()
	if d.repoVisibilityKnown == nil {
		d.repoVisibilityKnown = map[string]string{}
	}
	d.repoVisibilityKnown[identity] = visibility
	d.repoVisibilityMu.Unlock()
}

func splitRepoIdentity(identity string) (host, ownerRepo string, ok bool) {
	parts := strings.Split(identity, "/")
	if len(parts) != 3 {
		return "", "", false
	}
	for _, part := range parts {
		if part == "" {
			return "", "", false
		}
	}
	return parts[0], parts[1] + "/" + parts[2], true
}

func (d *Daemon) autoModeConfigForSession(
	cfg automode.Config, cwd string,
) (automode.Config, automode.RepositoryRules, error) {
	cfg.Environment = cfg.Environment.WithDetected(d.detectAutoModeEnvironment(cwd))
	return d.autoModeConfigWithRepositoryRules(cfg, cwd)
}

func (d *Daemon) autoModeConfigWithRepositoryRules(
	cfg automode.Config, cwd string,
) (automode.Config, automode.RepositoryRules, error) {
	repository, err := gitValue(context.Background(), d.gitExecution(), gitTask{Kind: gitTaskAutoMode, Lane: gitInteractive, Effect: gitRead, Scope: cwd}, func(ctx context.Context, client *attngit.Client) (automode.RepositoryRules, error) {
		return automode.LoadRepositoryRulesWithGit(ctx, client, cwd)
	})
	if err != nil {
		return automode.Config{}, automode.RepositoryRules{}, err
	}
	return automode.MergeRepositoryRules(cfg, repository), repository, nil
}

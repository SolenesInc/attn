package daemon

import (
	"strings"

	"github.com/victorarias/attn/internal/automode"
)

// detectAutoModeEnvironment answers the detected slots for a session in cwd. Git
// answers now; visibility comes from an earlier lookup, unset meaning private.
func (d *Daemon) detectAutoModeEnvironment(cwd string) map[string][]string {
	detected, identities := automode.DetectFromRepo(cwd)
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

// repoVisibility reads what a previous lookup learned, and starts one when
// nothing has. It runs off-path: a launch must not wait on GitHub.
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

// splitRepoIdentity cuts "host/owner/name" into its host and its "owner/name".
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

// autoModeConfigForSession is the promoted config as the session in cwd reads
// it: the user's slots, with the detected ones filling what they left empty.
func (d *Daemon) autoModeConfigForSession(cfg automode.Config, cwd string) automode.Config {
	cfg.Environment = cfg.Environment.WithDetected(d.detectAutoModeEnvironment(cwd))
	return cfg
}

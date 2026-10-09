package daemon

import (
	"github.com/victorarias/attn/internal/config"
	"github.com/victorarias/attn/internal/notebook"
	"os"
)

func (d *Daemon) defaultNotebookRoot(name string) string {
	base := config.HarnessNotebookRoot()
	if base == "" {
		base = notebook.DefaultRoot("~", config.Instance())
	}
	return notebook.ProfileDefaultRoot(base, name, d.notebookRootTaken)
}

func (d *Daemon) notebookRootTaken(raw string) bool {
	root, err := normalizeNotebookRoot(raw)
	if err != nil {
		return true
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		return true
	}
	profiles, err := d.store.ListProfiles(false)
	if err != nil {
		return true
	}
	for _, p := range profiles {
		other, err := normalizeNotebookRoot(d.store.ProfileSetting(p.ID, "notebook.root"))
		if err == nil && root == other {
			return true
		}
	}
	return false
}

func (d *Daemon) pruneNotebookRoots() {
	live, err := d.store.ListProfiles(false)
	if err != nil {
		return
	}
	roots := make(map[string]bool)
	for _, p := range live {
		root, err := d.notebookRoot(p.ID)
		if err == nil {
			roots[root] = true
		}
	}
	d.notebookMu.Lock()
	for root := range d.notebookStores {
		if !roots[root] {
			delete(d.notebookStores, root)
		}
	}
	d.notebookMu.Unlock()
	d.rootWatchMu.Lock()
	var close []*notebook.Watcher
	for root, entry := range d.rootWatches {
		entry.notebook = roots[root]
		if !entry.notebook && len(entry.clients) == 0 {
			close = append(close, entry.watcher)
			delete(d.rootWatches, root)
		}
	}
	d.rootWatchMu.Unlock()
	for _, watcher := range close {
		_ = watcher.Close()
	}
}

func (d *Daemon) seedNotebookRoot(seedID string) (string, error) {
	seed, _, err := d.readSeed(seedID)
	if err != nil {
		return "", err
	}
	profileID, err := d.seedBirthProfile(seed)
	if err != nil {
		return "", err
	}
	return d.notebookRoot(profileID)
}

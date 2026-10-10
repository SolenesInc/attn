package daemon

import (
	"fmt"
	"github.com/victorarias/attn/internal/config"
	"github.com/victorarias/attn/internal/notebook"
	"os"
)

func (d *Daemon) defaultNotebookRoot(name string) (string, error) {
	base := config.HarnessNotebookRoot()
	if base == "" {
		base = notebook.DefaultRoot("~", config.Instance())
	}
	return notebook.ProfileDefaultRoot(base, name, d.notebookRootTaken)
}

func (d *Daemon) notebookRootTaken(raw string) (bool, error) {
	root, err := normalizeNotebookRoot(raw)
	if err != nil {
		return false, err
	}
	if _, err := os.Lstat(root); err == nil {
		return true, nil
	} else if !os.IsNotExist(err) {
		return false, fmt.Errorf("inspect Notebook root %q: %w", root, err)
	}
	profiles, err := d.store.ListProfiles(false)
	if err != nil {
		return false, err
	}
	for _, p := range profiles {
		other, err := normalizeNotebookRoot(d.profileSetting(p.ID, settingNotebookRoot))
		if err == nil && root == other {
			return true, nil
		}
	}
	return false, nil
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

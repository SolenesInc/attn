package daemon

import (
	"fmt"
	"github.com/victorarias/attn/internal/config"
	"github.com/victorarias/attn/internal/notebook"
	"os"
)

func (d *Daemon) defaultNotebookRoot(name, currentRoot string) (string, error) {
	base := config.HarnessNotebookRoot()
	if base == "" {
		base = notebook.DefaultRoot("~", config.Instance())
	}
	current, _ := normalizeNotebookRoot(currentRoot)
	return notebook.ProfileDefaultRoot(base, name, func(raw string) (bool, error) {
		root, err := normalizeNotebookRoot(raw)
		if err != nil {
			return false, err
		}
		if currentRoot != "" && root == current {
			return false, nil
		}
		return d.notebookRootTaken(raw)
	})
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
		other, err := normalizeNotebookRoot(d.profileSetting(p.ID, SettingNotebookRoot))
		if err == nil && root == other {
			return true, nil
		}
	}
	return false, nil
}

func (d *Daemon) validatePairedNotebookRoots(profileID, name, root string) error {
	if !d.enrollmentStatus().IsHome() {
		return nil
	}
	rows, err := d.store.ListOutposts()
	if err != nil || len(rows) == 0 {
		return err
	}
	return d.validateNotebookSyncRoots(profileID, name, root)
}

func (d *Daemon) validateNotebookSyncRoots(profileID, name, proposed string) error {
	profiles, err := d.store.ListProfiles(false)
	if err != nil {
		return err
	}
	type notebookRoot struct{ name, path string }
	roots := []notebookRoot{}
	if profileID == "" && name != "" {
		root, err := normalizeNotebookRoot(proposed)
		if err != nil {
			return err
		}
		root, err = config.CanonicalRuntimePath(root)
		if err != nil {
			return err
		}
		roots = append(roots, notebookRoot{name, root})
	}
	for _, profile := range profiles {
		root, err := d.notebookRoot(profile.ID)
		if profile.ID == profileID {
			root, err = normalizeNotebookRoot(proposed)
		}
		if err != nil {
			return err
		}
		root, err = config.CanonicalRuntimePath(root)
		if err != nil {
			return err
		}
		roots = append(roots, notebookRoot{profile.Name, root})
	}
	for i, root := range roots {
		for _, other := range roots[i+1:] {
			if root.path != other.path && (pathWithin(root.path, other.path) || pathWithin(other.path, root.path)) {
				return fmt.Errorf("notebook sync cannot pair profile %q (%s) with profile %q (%s): their Notebook roots are nested; use separate or identical roots", root.name, root.path, other.name, other.path)
			}
		}
	}
	return nil
}

func (d *Daemon) pruneNotebookRoots() {
	live, err := d.notebookProfiles()
	if err != nil {
		return
	}
	roots := make(map[string]bool)
	for id := range live {
		root, err := d.notebookRoot(id)
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
	profileID, err := d.seedBirthProfileID(seedID)
	if err != nil {
		return "", err
	}
	return d.notebookRoot(profileID)
}

func (d *Daemon) seedBirthProfileID(seedID string) (string, error) {
	seed, _, err := d.readSeed(seedID)
	if err != nil {
		return "", err
	}
	return d.seedBirthProfile(seed)
}

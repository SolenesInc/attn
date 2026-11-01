package daemon

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/victorarias/attn/internal/bridge"
	"github.com/victorarias/attn/internal/notebook"
	"github.com/victorarias/attn/internal/protocol"
)

type notebookFolder struct {
	ID       string
	Key      string
	Name     string
	HomePath string
	Profiles []string
}

type profileNotebook struct {
	profileID, profileName, path string
}

func (d *Daemon) profileNotebooks(resolve func(profileID string) (string, error)) ([]profileNotebook, error) {
	live, err := d.store.ListProfiles(false)
	if err != nil {
		return nil, err
	}
	notebooks := make([]profileNotebook, 0, len(live))
	for _, profile := range live {
		path, err := resolve(profile.ID)
		if err != nil {
			return nil, fmt.Errorf("the Notebook folder of profile %s: %w", profile.Name, err)
		}
		notebooks = append(notebooks, profileNotebook{profileID: profile.ID, profileName: profile.Name, path: path})
	}
	slices.SortFunc(notebooks, func(a, b profileNotebook) int { return strings.Compare(a.profileName, b.profileName) })
	return notebooks, nil
}

func (d *Daemon) notebookFolders() ([]notebookFolder, error) {
	notebooks, err := d.profileNotebooks(d.notebookRoot)
	if err != nil {
		return nil, err
	}
	return notebookFoldersOf(d.daemonInstanceID, notebooks), nil
}

func notebookFolderID(home, key string) string { return "notebook-" + home + "-" + key }

func notebookFoldersOf(home string, notebooks []profileNotebook) []notebookFolder {
	byPath := map[string]*notebookFolder{}
	for _, nb := range notebooks {
		folder := byPath[nb.path]
		if folder == nil {
			key := notebook.FolderKey(nb.path)
			folder = &notebookFolder{ID: notebookFolderID(home, key), Key: key, Name: notebook.CopyName(nb.path), HomePath: nb.path}
			byPath[nb.path] = folder
		}
		folder.Profiles = append(folder.Profiles, nb.profileID)
	}
	folders := make([]notebookFolder, 0, len(byPath))
	for _, folder := range byPath {
		slices.Sort(folder.Profiles)
		folders = append(folders, *folder)
	}
	slices.SortFunc(folders, func(a, b notebookFolder) int { return strings.Compare(a.ID, b.ID) })
	return folders
}

func refuseNestedNotebooks(notebooks []profileNotebook) error {
	for _, inner := range notebooks {
		for _, outer := range notebooks {
			if rel, err := filepath.Rel(outer.path, inner.path); err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				return fmt.Errorf("profile %s's Notebook folder %s is inside profile %s's Notebook folder %s; outposts sync each Notebook folder on its own, so one cannot hold another while outposts are paired", inner.profileName, inner.path, outer.profileName, outer.path)
			}
		}
	}
	return nil
}

func (d *Daemon) refuseNestedNotebooksAtHome() error {
	notebooks, err := d.profileNotebooks(d.notebookRoot)
	if err != nil {
		return err
	}
	return refuseNestedNotebooks(notebooks)
}

func (d *Daemon) refuseNestedNotebookSetting(msg protocol.SetSettingMessage) error {
	if msg.Key != string(SettingNotebookRoot) && msg.Key != string(SettingNotebookProfileRoot) || !d.enrollmentStatus().IsHome() {
		return nil
	}
	rows, err := d.store.ListOutposts()
	if err != nil || len(rows) == 0 {
		return err
	}
	shared, target := d.machineSetting(SettingNotebookRoot), ""
	if msg.Key == string(SettingNotebookRoot) {
		shared = msg.Value
	} else {
		profile, err := d.settingsProfile(msg.SourceSessionID, msg.ProfileID)
		if err != nil {
			return err
		}
		target = profile.ID
	}
	notebooks, err := d.profileNotebooks(func(profileID string) (string, error) {
		own := d.profileNotebookRoot(profileID)
		if profileID == target {
			own = msg.Value
		}
		return homeNotebookRoot(own, shared)
	})
	if err != nil {
		return err
	}
	return refuseNestedNotebooks(notebooks)
}

func notebookFoldersWire(folders []notebookFolder) []protocol.NotebookSyncFolder {
	wire := make([]protocol.NotebookSyncFolder, len(folders))
	for i, folder := range folders {
		wire[i] = protocol.NotebookSyncFolder{ID: folder.ID, Key: folder.Key, Name: folder.Name, Profiles: slices.Clone(folder.Profiles)}
	}
	return wire
}

func (d *Daemon) notebookFoldersChanged() {
	d.pruneRootWatchers()
	if d.enrollmentStatus().IsHome() {
		d.configureNotebookSync()
	}
}

func (d *Daemon) notebookFoldersChanges() <-chan struct{} {
	r := &d.notebookSync
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.foldersChanged == nil {
		r.foldersChanged = make(chan struct{})
	}
	return r.foldersChanged
}

func (d *Daemon) setHomeNotebookFoldersLocked(folders []notebookFolder) {
	r := &d.notebookSync
	if slices.EqualFunc(r.folders, folders, func(a, b notebookFolder) bool {
		return a.ID == b.ID && a.HomePath == b.HomePath && slices.Equal(a.Profiles, b.Profiles)
	}) {
		return
	}
	r.folders = folders
	if r.foldersChanged != nil {
		close(r.foldersChanged)
		r.foldersChanged = nil
	}
}

func (d *Daemon) pushNotebookFolders(conn *bridge.Conn) {
	var sent []protocol.NotebookSyncFolder
	pushed := false
	for conn.Context().Err() == nil {
		changed := d.notebookFoldersChanges()
		d.notebookSync.mu.Lock()
		folders := notebookFoldersWire(d.notebookSync.folders)
		d.notebookSync.mu.Unlock()
		if !pushed || !slices.EqualFunc(sent, folders, func(a, b protocol.NotebookSyncFolder) bool {
			return a.ID == b.ID && slices.Equal(a.Profiles, b.Profiles)
		}) {
			err := conn.Call(conn.Context(), bridgeCall, protocol.NotebookFoldersMessage{Cmd: protocol.CmdNotebookFolders, Folders: folders}, nil)
			if err == nil {
				sent, pushed = folders, true
			} else if conn.Context().Err() == nil {
				d.logf("Notebook folders for %s: %v", conn.Peer().Name, err)
			}
		}
		select {
		case <-changed:
		case <-conn.Done():
			return
		case <-d.life.Done():
			return
		}
	}
}

func (d *Daemon) receiveNotebookFolders(_ context.Context, msg protocol.NotebookFoldersMessage) (struct{}, error) {
	status := d.enrollmentStatus()
	if status.IsHome() {
		return struct{}{}, errors.New("only the home sends the Notebook folder list")
	}
	base, err := outpostNotebookBase()
	if err != nil {
		return struct{}{}, err
	}
	r := &d.notebookSync
	r.configure.Lock()
	err = d.storeOutpostNotebookFoldersLocked(status.HomeDaemonID, base, msg.Folders)
	r.configure.Unlock()
	if err != nil {
		return struct{}{}, err
	}
	d.configureNotebookSync()
	return struct{}{}, nil
}

func (d *Daemon) storeOutpostNotebookFoldersLocked(home, base string, folders []protocol.NotebookSyncFolder) error {
	r := &d.notebookSync
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := d.loadNotebookSyncDiskLocked(home); err != nil {
		return err
	}
	disk := r.disk.clone()
	listed := map[string]bool{}
	for _, folder := range folders {
		if folder.ID != notebookFolderID(home, folder.Key) || len(folder.Key) != 8 || strings.Trim(folder.Key, "0123456789abcdef") != "" || folder.Name == "" || notebook.CopyName(folder.Name) != folder.Name {
			return fmt.Errorf("the home sent a malformed Notebook folder %q", folder.ID)
		}
		listed[folder.ID] = true
		entry, known := disk.Folders[folder.ID]
		if !known {
			root := notebook.OutpostCopyRoot(base, folder.Name, folder.Key)
			var err error
			if entry, err = newOutpostNotebookCopy(root); err != nil {
				return err
			}
		}
		entry.Profiles = slices.Clone(folder.Profiles)
		disk.Folders[folder.ID] = entry
	}
	for id, entry := range disk.Folders {
		if !listed[id] {
			entry.Profiles = nil
			disk.Folders[id] = entry
		}
	}
	return d.saveNotebookSyncDiskLocked(disk)
}

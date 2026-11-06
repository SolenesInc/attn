package daemon

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/bridge"
	"github.com/victorarias/attn/internal/notebook"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/stepclock"
)

type notebookFolder struct {
	ID       string
	Key      string
	Name     string
	Epoch    string
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

type notebookNesting struct {
	inner, outer profileNotebook
}

func (n notebookNesting) String() string {
	return n.inner.String() + " is inside " + n.outer.String()
}

func (nb profileNotebook) String() string {
	if nb.profileID == "" {
		return "the default Notebook folder " + nb.path
	}
	return fmt.Sprintf("profile %s's Notebook folder %s", nb.profileName, nb.path)
}

func notebookNestings(notebooks []profileNotebook) []notebookNesting {
	var nestings []notebookNesting
	for _, inner := range notebooks {
		for _, outer := range notebooks {
			if rel, err := filepath.Rel(outer.path, inner.path); err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				nestings = append(nestings, notebookNesting{inner: inner, outer: outer})
			}
		}
	}
	return nestings
}

func (d *Daemon) refuseNestedNotebookSetting(msg protocol.SetSettingMessage) error {
	if msg.Key != string(SettingNotebookRoot) || !d.enrollmentStatus().IsHome() {
		return nil
	}
	target, err := d.settingsProfile(msg.SourceSessionID, msg.ProfileID)
	if err != nil {
		return err
	}
	notebooks, err := d.profileNotebooks(func(profileID string) (string, error) {
		if profileID == target.ID {
			return homeNotebookRoot(msg.Value)
		}
		root, err := d.notebookRoot(profileID)
		if err != nil {
			return "", nil
		}
		return root, nil
	})
	if err != nil {
		return err
	}
	notebooks = slices.DeleteFunc(notebooks, func(nb profileNotebook) bool { return nb.path == "" })
	defaultRoot, err := defaultNotebookRoot()
	if err != nil {
		return err
	}
	notebooks = append(notebooks, profileNotebook{path: defaultRoot})
	for _, n := range notebookNestings(notebooks) {
		if n.inner.profileID == target.ID || n.outer.profileID == target.ID {
			return fmt.Errorf("%s; a Notebook folder cannot sit inside or around another, or the default folder, where new profiles start", n)
		}
	}
	return nil
}

func notebookFoldersWire(folders []notebookFolder) []protocol.NotebookSyncFolder {
	wire := make([]protocol.NotebookSyncFolder, len(folders))
	for i, folder := range folders {
		wire[i] = protocol.NotebookSyncFolder{ID: folder.ID, Key: folder.Key, Name: folder.Name, Epoch: folder.Epoch, Profiles: slices.Clone(folder.Profiles)}
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
	if r.folders != nil && slices.EqualFunc(r.folders, folders, func(a, b notebookFolder) bool {
		return a.ID == b.ID && a.Epoch == b.Epoch && a.HomePath == b.HomePath && slices.Equal(a.Profiles, b.Profiles)
	}) {
		return
	}
	r.folders = folders
	if r.foldersChanged != nil {
		close(r.foldersChanged)
		r.foldersChanged = nil
	}
}

const NotebookFoldersRetryTimer stepclock.Name = "notebook-folders-retry"

func (d *Daemon) pushNotebookFolders(conn *bridge.Conn, openStream func()) {
	var sent []protocol.NotebookSyncFolder
	pushed, failed := false, false
	backoff := bridge.ReconnectInterval
	for conn.Context().Err() == nil {
		changed := d.notebookFoldersChanges()
		d.notebookSync.mu.Lock()
		listed := d.notebookSync.folders != nil
		folders := notebookFoldersWire(d.notebookSync.folders)
		d.notebookSync.mu.Unlock()
		var retry stepclock.Timer
		if listed && (!pushed || failed || !slices.EqualFunc(sent, folders, func(a, b protocol.NotebookSyncFolder) bool {
			return a.ID == b.ID && a.Epoch == b.Epoch && slices.Equal(a.Profiles, b.Profiles)
		})) {
			err := conn.Call(conn.Context(), bridgeCall, protocol.NotebookFoldersMessage{Cmd: protocol.CmdNotebookFolders, Folders: folders}, nil)
			switch {
			case err == nil:
				d.setNotebookFoldersRejection(conn, "")
				if !pushed {
					openStream()
				}
				sent, pushed, failed, backoff = folders, true, false, bridge.ReconnectInterval
			case conn.Context().Err() == nil:
				d.logf("Notebook folders for %s: %v", conn.Peer().Name, err)
				if rejected := (*bridge.CallRejected)(nil); errors.As(err, &rejected) {
					d.setNotebookFoldersRejection(conn, rejected.Reason)
				}
				failed = true
				retry = d.clock.NewTimer(NotebookFoldersRetryTimer, backoff)
				backoff = min(2*backoff, bridge.RefusedRetryInterval)
			}
		}
		if !d.awaitNotebookBridgeWake(conn, changed, retry) {
			return
		}
	}
}

func (d *Daemon) awaitNotebookBridgeWake(conn *bridge.Conn, changed <-chan struct{}, retry stepclock.Timer) bool {
	var retried <-chan time.Time
	if retry != nil {
		retried = retry.C()
		defer retry.Stop()
	}
	select {
	case <-changed:
		return true
	case <-retried:
		return true
	case <-conn.Done():
		return false
	case <-d.life.Done():
		return false
	}
}

func (d *Daemon) setNotebookFoldersRejection(conn *bridge.Conn, reason string) {
	r := &d.notebookSync
	peer := conn.Peer().DaemonID
	r.mu.Lock()
	if conn.Context().Err() != nil || r.rejections[peer] == reason {
		r.mu.Unlock()
		return
	}
	if reason == "" {
		delete(r.rejections, peer)
	} else {
		if r.rejections == nil {
			r.rejections = map[string]string{}
		}
		r.rejections[peer] = reason
	}
	d.notebookSyncChangedLocked()
	r.mu.Unlock()
	d.refreshNotebookRows()
}

func (d *Daemon) receiveNotebookFolders(_ context.Context, msg protocol.NotebookFoldersMessage) (struct{}, error) {
	status := d.enrollmentStatus()
	if status.IsHome() {
		return struct{}{}, errors.New("only the home sends the Notebook folder list")
	}
	base, err := defaultNotebookRoot()
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
		if !known || entry.Epoch != folder.Epoch {
			entry = notebookSyncFolderDisk{Root: notebook.OutpostCopyRoot(base, folder.Name, folder.Key), Epoch: folder.Epoch, Completed: map[string]bool{}}
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

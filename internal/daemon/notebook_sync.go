package daemon

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/victorarias/attn/internal/bridge"
	"github.com/victorarias/attn/internal/enrollment"
	"github.com/victorarias/attn/internal/notebook"
	"github.com/victorarias/attn/internal/pausepoint"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
	"github.com/victorarias/syncthing/lib/attnsync"
	syncconfig "github.com/victorarias/syncthing/lib/config"
	"github.com/victorarias/syncthing/lib/events"
	syncprotocol "github.com/victorarias/syncthing/lib/protocol"
	"golang.org/x/crypto/ssh"
	"golang.org/x/text/unicode/norm"
)

const notebookChannel = "notebook"
const notebookTrashDays = 30
const notebookProgressInterval = 500 * time.Millisecond

type notebookSyncDisk struct {
	Home    string
	Folders map[string]notebookSyncFolderDisk
}

type notebookSyncFolderDisk struct {
	Root         string
	Epoch        string
	PreviousRoot string
	Initialized  bool
	Completed    map[string]bool
	Profiles     []string
}

func (disk notebookSyncDisk) clone() notebookSyncDisk {
	copied := notebookSyncDisk{Home: disk.Home, Folders: make(map[string]notebookSyncFolderDisk, len(disk.Folders))}
	for id, entry := range disk.Folders {
		entry.Completed = maps.Clone(entry.Completed)
		entry.Profiles = slices.Clone(entry.Profiles)
		copied.Folders[id] = entry
	}
	return copied
}

type notebookSyncReport struct {
	Folders map[string]notebookFolderSnapshot
	Error   string
}

type notebookFolderSnapshot struct {
	Root          string
	PreviousRoot  string
	ScanComplete  bool
	LocalSequence int64
	PeerSequence  int64
	NeedItems     int
	NeedDeletes   int
	NeedBytes     int64
	Error         string
}

type notebookSyncRun struct {
	streams   map[string]notebookSyncStream
	engine    *attnsync.Engine
	home      string
	folders   map[string]string
	parked    map[string]string
	failed    map[string]string
	peers     []string
	cancel    context.CancelFunc
	done      chan struct{}
	ctx       context.Context
	snapshots map[string]map[string]notebookFolderSnapshot
	remote    map[string]notebookSyncReport
	conflicts map[string]map[string]struct{}
	homeState protocol.OutpostNotebook
	progress  *time.Timer
}

type notebookSyncStream struct {
	conn  *bridge.Conn
	error string
}

type notebookSyncRuntime struct {
	configure      sync.Mutex
	mu             sync.Mutex
	disk           notebookSyncDisk
	loaded         bool
	folders        []notebookFolder
	foldersChanged chan struct{}
	rejections     map[string]notebookSyncStream
	run            *notebookSyncRun
	error          string
	changed        chan struct{}
	settingState   string
	settingError   string
}

func notebookDevice(id string) syncprotocol.DeviceID {
	return syncprotocol.NewDeviceID([]byte("attn-daemon:" + id))
}

func (d *Daemon) notebookSyncDir() string { return filepath.Join(d.dataRoot, "notebook-sync") }

func (d *Daemon) notebookSyncPeers() (string, []string, error) {
	status := d.enrollmentStatus()
	if !status.IsHome() {
		if status.HomeDaemonID != "" {
			return status.HomeDaemonID, []string{status.HomeDaemonID}, nil
		}
		return status.HomeDaemonID, nil, nil
	}
	if !d.outpostsEnabled() {
		return status.HomeDaemonID, nil, nil
	}
	rows, err := d.store.ListOutposts()
	if err != nil {
		return "", nil, err
	}
	var peers []string
	for _, row := range rows {
		if row.DaemonID != "" {
			peers = append(peers, row.DaemonID)
		}
	}
	slices.Sort(peers)
	return status.HomeDaemonID, peers, nil
}

func (d *Daemon) configureNotebookSync() {
	r := &d.notebookSync
	r.configure.Lock()
	defer r.configure.Unlock()
	run, err := d.configureNotebookSyncLocked()
	r.mu.Lock()
	if err != nil {
		r.error = err.Error()
	} else {
		r.error = ""
	}
	d.notebookSyncChangedLocked()
	r.mu.Unlock()
	if err != nil {
		d.logf("Notebook sync: %v", err)
	}
	if run != nil {
		select {
		case <-run.engine.Ready():
			d.refreshNotebookConflicts(run)
			d.refreshNotebookSync(run)
		default:
		}
	}
	d.refreshNotebookRows()
	d.publishNotebookSyncSettings()
}

func (d *Daemon) configureNotebookSyncLocked() (*notebookSyncRun, error) {
	r := &d.notebookSync
	home, peers, err := d.notebookSyncPeers()
	isHome := err == nil && home == d.daemonInstanceID
	var listed []notebookFolder
	if isHome {
		listed, err = d.notebookFolders()
	}
	r.mu.Lock()
	old := r.run
	r.mu.Unlock()
	if old != nil && (len(peers) == 0 || err != nil || old.home != home || old.ctx.Err() != nil) {
		old.cancel()
		<-old.done
		r.mu.Lock()
		if r.run == old {
			r.run = nil
		}
		r.mu.Unlock()
		old = nil
	}
	if err != nil || len(peers) == 0 || d.life.Ended() {
		return nil, err
	}
	r.mu.Lock()
	err = d.loadNotebookSyncDiskLocked(home)
	if err == nil && isHome {
		if err = d.listHomeNotebookFoldersLocked(listed); err == nil {
			d.setHomeNotebookFoldersLocked(d.stampNotebookFolderEpochsLocked(listed))
		}
	}
	r.mu.Unlock()
	if err != nil {
		return nil, err
	}
	prepared, unprepared, leaving := d.notebookFolderStates()
	nested, parked := map[string]string{}, map[string]string{}
	for _, folder := range listed {
		if folder.Nested == "" {
			continue
		}
		nested[folder.ID] = folder.Nested
		if root, indexed := prepared[folder.ID]; indexed {
			parked[folder.ID] = root
		}
		delete(prepared, folder.ID)
		delete(unprepared, folder.ID)
	}
	unindexed := maps.Clone(leaving)
	for id, entry := range unprepared {
		unindexed[id] = entry.Root
	}
	if old == nil {
		run, err := d.startNotebookSync(home, peers, prepared, parked, unindexed)
		if err != nil {
			return nil, err
		}
		old = run
	} else if len(unindexed) != 0 {
		if err := old.engine.Configure(d.notebookEngineConfig(peers, prepared, parked)); err != nil {
			return nil, err
		}
		r.mu.Lock()
		old.peers, old.folders, old.parked = slices.Clone(peers), prepared, parked
		r.mu.Unlock()
	}
	ready, failed := d.prepareNotebookFolders(prepared, unprepared)
	maps.Copy(failed, nested)
	if !slices.Equal(old.peers, peers) || !maps.Equal(old.folders, ready) || !maps.Equal(old.parked, parked) {
		if err := old.engine.Configure(d.notebookEngineConfig(peers, ready, parked)); err != nil {
			return nil, err
		}
	}
	r.mu.Lock()
	old.peers, old.folders, old.parked, old.failed = slices.Clone(peers), ready, parked, failed
	for id := range old.snapshots {
		if _, kept := ready[id]; !kept {
			delete(old.snapshots, id)
			delete(old.conflicts, id)
		}
	}
	err = d.dropLeavingNotebookFoldersLocked(leaving)
	r.mu.Unlock()
	for _, root := range ready {
		d.ensureNotebookWatcher(root)
	}
	return old, err
}

func (d *Daemon) loadNotebookSyncDiskLocked(home string) error {
	r := &d.notebookSync
	if r.loaded && r.disk.Home == home {
		return nil
	}
	disk := notebookSyncDisk{}
	data, err := os.ReadFile(filepath.Join(d.notebookSyncDir(), "state.json"))
	if err == nil {
		if err := json.Unmarshal(data, &disk); err != nil {
			return err
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	if err == nil && (disk.Home != home || disk.Folders == nil) {
		if err := os.RemoveAll(d.notebookSyncDir()); err != nil {
			return err
		}
	}
	if disk.Home != home || disk.Folders == nil {
		disk = notebookSyncDisk{Home: home, Folders: map[string]notebookSyncFolderDisk{}}
	}
	r.disk, r.loaded = disk, true
	return nil
}

func (d *Daemon) loadedNotebookSyncDiskLocked(home string) notebookSyncDisk {
	r := &d.notebookSync
	if !r.loaded || r.disk.Home != home {
		return notebookSyncDisk{}
	}
	return r.disk
}

func (d *Daemon) forgetNotebookSyncDiskLocked() {
	r := &d.notebookSync
	r.disk, r.loaded = notebookSyncDisk{}, false
}

func (d *Daemon) listHomeNotebookFoldersLocked(listed []notebookFolder) error {
	r := &d.notebookSync
	disk := r.disk.clone()
	inList := map[string]bool{}
	for _, folder := range listed {
		inList[folder.ID] = true
		entry, known := disk.Folders[folder.ID]
		if !known {
			entry = notebookSyncFolderDisk{Root: folder.HomePath, Epoch: strconv.FormatInt(time.Now().UnixNano(), 10), Completed: map[string]bool{}}
		}
		entry.Profiles = slices.Clone(folder.Profiles)
		disk.Folders[folder.ID] = entry
	}
	for id, entry := range disk.Folders {
		if !inList[id] {
			entry.Profiles = nil
			disk.Folders[id] = entry
		}
	}
	if notebookSyncDiskEqual(disk, r.disk) {
		return nil
	}
	return d.saveNotebookSyncDiskLocked(disk)
}

func (d *Daemon) stampNotebookFolderEpochsLocked(listed []notebookFolder) []notebookFolder {
	stamped := slices.Clone(listed)
	for i := range stamped {
		stamped[i].Epoch = d.notebookSync.disk.Folders[stamped[i].ID].Epoch
	}
	return stamped
}

func notebookSyncDiskEqual(a, b notebookSyncDisk) bool {
	return a.Home == b.Home && maps.EqualFunc(a.Folders, b.Folders, func(x, y notebookSyncFolderDisk) bool {
		return x.Root == y.Root && x.Epoch == y.Epoch && x.PreviousRoot == y.PreviousRoot && x.Initialized == y.Initialized && maps.Equal(x.Completed, y.Completed) && slices.Equal(x.Profiles, y.Profiles)
	})
}

func (d *Daemon) notebookFolderStates() (prepared map[string]string, unprepared map[string]notebookSyncFolderDisk, leaving map[string]string) {
	r := &d.notebookSync
	r.mu.Lock()
	disk := r.disk.clone()
	r.mu.Unlock()
	prepared, unprepared, leaving = map[string]string{}, map[string]notebookSyncFolderDisk{}, map[string]string{}
	for id, entry := range disk.Folders {
		switch {
		case len(entry.Profiles) == 0:
			leaving[id] = entry.Root
		case entry.Initialized:
			prepared[id] = entry.Root
		default:
			unprepared[id] = entry
		}
	}
	return prepared, unprepared, leaving
}

func (d *Daemon) prepareNotebookFolders(prepared map[string]string, unprepared map[string]notebookSyncFolderDisk) (ready, failed map[string]string) {
	ready, failed = maps.Clone(prepared), map[string]string{}
	for id, entry := range unprepared {
		if err := d.prepareNotebookFolder(id, entry); err != nil {
			failed[id] = err.Error()
			d.logf("Notebook sync: prepare %s: %v", entry.Root, err)
			continue
		}
		ready[id] = entry.Root
	}
	return ready, failed
}

func (d *Daemon) prepareNotebookFolder(id string, entry notebookSyncFolderDisk) error {
	if entry.Initialized {
		return nil
	}
	if entry.PreviousRoot == "" && d.enrollmentStatus().IsOutpost() {
		entries, err := os.ReadDir(entry.Root)
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		if len(entries) != 0 {
			entry.PreviousRoot = entry.Root + ".before-sync-" + time.Now().UTC().Format("20060102T150405.000000000Z")
			if err := d.updateNotebookFolderDisk(id, func(current *notebookSyncFolderDisk) { current.PreviousRoot = entry.PreviousRoot }); err != nil {
				return err
			}
		}
	}
	if entry.PreviousRoot != "" {
		if _, err := os.Lstat(entry.PreviousRoot); os.IsNotExist(err) {
			if err := os.Rename(entry.Root, entry.PreviousRoot); err != nil {
				return err
			}
		} else if err != nil {
			return err
		}
	}
	marker := filepath.Join(entry.Root, ".attn", "notebook-sync")
	if err := os.MkdirAll(filepath.Dir(marker), 0700); err != nil {
		return err
	}
	if err := os.WriteFile(marker, nil, 0600); err != nil {
		return err
	}
	return d.updateNotebookFolderDisk(id, func(current *notebookSyncFolderDisk) { current.Initialized = true })
}

func (d *Daemon) updateNotebookFolderDisk(id string, update func(*notebookSyncFolderDisk)) error {
	r := &d.notebookSync
	r.mu.Lock()
	defer r.mu.Unlock()
	disk := r.disk.clone()
	current, ok := disk.Folders[id]
	if !ok {
		return fmt.Errorf("the Notebook folder %s left while it was being prepared", id)
	}
	update(&current)
	disk.Folders[id] = current
	return d.saveNotebookSyncDiskLocked(disk)
}

func (d *Daemon) dropLeavingNotebookFoldersLocked(leaving map[string]string) error {
	if len(leaving) == 0 {
		return nil
	}
	r := &d.notebookSync
	disk := r.disk.clone()
	for id := range leaving {
		if entry, ok := disk.Folders[id]; ok && len(entry.Profiles) == 0 {
			delete(disk.Folders, id)
		}
	}
	return d.saveNotebookSyncDiskLocked(disk)
}

func (d *Daemon) notebookEngineConfig(peers []string, folders, paused map[string]string) syncconfig.Configuration {
	id := notebookDevice(d.daemonInstanceID)
	cfg := attnsync.Configuration(id)
	var devices []syncconfig.FolderDeviceConfiguration
	devices = append(devices, syncconfig.FolderDeviceConfiguration{DeviceID: id})
	for _, peer := range peers {
		device := cfg.Defaults.Device.Copy()
		device.DeviceID = notebookDevice(peer)
		cfg.Devices = append(cfg.Devices, device)
		devices = append(devices, syncconfig.FolderDeviceConfiguration{DeviceID: device.DeviceID})
	}
	add := func(folderID, root string, isPaused bool) {
		folder := cfg.Defaults.Folder.Copy()
		folder.ID, folder.Path, folder.Type, folder.Paused = folderID, root, syncconfig.FolderTypeSendReceive, isPaused
		folder.RescanIntervalS = 3600
		folder.FSWatcherEnabled = d.notebookSyncWatch
		folder.Versioning.Type = "trashcan"
		folder.Versioning.FSPath = filepath.Join(d.dataRoot, "notebook-trash")
		folder.Versioning.Params = map[string]string{"cleanoutDays": strconv.Itoa(notebookTrashDays)}
		folder.Devices = slices.Clone(devices)
		cfg.Folders = append(cfg.Folders, folder)
	}
	for _, folderID := range slices.Sorted(maps.Keys(folders)) {
		add(folderID, folders[folderID], false)
	}
	for _, folderID := range slices.Sorted(maps.Keys(paused)) {
		add(folderID, paused[folderID], true)
	}
	return cfg
}

func (d *Daemon) saveNotebookSyncDiskLocked(disk notebookSyncDisk) error {
	dir := d.notebookSyncDir()
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	data, err := json.Marshal(disk)
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(dir, ".state-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	_, writeErr := file.Write(data)
	err = errors.Join(writeErr, file.Sync(), file.Close())
	if err != nil {
		return err
	}
	if err := os.Rename(file.Name(), filepath.Join(dir, "state.json")); err != nil {
		return err
	}
	if err := syncDirectory(dir); err != nil {
		return err
	}
	d.notebookSync.disk, d.notebookSync.loaded = disk, true
	return nil
}

func (d *Daemon) startNotebookSync(home string, peers []string, ready, parked, unindexed map[string]string) (*notebookSyncRun, error) {
	logger := slog.New(slog.NewTextHandler(notebookSyncLog{d}, &slog.HandlerOptions{Level: slog.LevelWarn}))
	paused := maps.Clone(parked)
	maps.Copy(paused, unindexed)
	engine, err := attnsync.New(filepath.Join(d.notebookSyncDir(), "index"), d.notebookEngineConfig(peers, ready, paused), notebookDevice(d.daemonInstanceID), logger)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(d.life.Context())
	run := &notebookSyncRun{engine: engine, home: home, folders: ready, parked: parked, failed: map[string]string{}, peers: slices.Clone(peers), cancel: cancel, ctx: ctx, done: make(chan struct{}), snapshots: map[string]map[string]notebookFolderSnapshot{}, remote: map[string]notebookSyncReport{}, conflicts: map[string]map[string]struct{}{}, streams: map[string]notebookSyncStream{}}
	d.notebookSync.mu.Lock()
	d.notebookSync.run = run
	d.notebookSync.mu.Unlock()
	if !d.life.Go("notebookSync", func() {
		defer close(run.done)
		serving := make(chan error, 1)
		runJoined(func() {
			err := engine.Serve(ctx)
			d.notebookSync.mu.Lock()
			if d.notebookSync.run == run && ctx.Err() == nil {
				d.notebookSync.error = fmt.Sprintf("sync engine stopped: %v", err)
				d.notebookSyncChangedLocked()
			}
			d.notebookSync.mu.Unlock()
			serving <- err
			cancel()
		}, func() {
			select {
			case <-engine.Ready():
			case <-ctx.Done():
				return
			}
			sub, err := engine.SubscribeContext(ctx, events.StateChanged|events.LocalIndexUpdated|events.RemoteIndexUpdated|events.FolderErrors|events.RemoteDownloadProgress|events.ClusterConfigReceived|events.DeviceConnected|events.DeviceDisconnected)
			if err != nil {
				return
			}
			defer sub.Unsubscribe()
			d.refreshNotebookConflicts(run)
			d.refreshNotebookSync(run)
			refreshReady := make(chan struct{}, 1)
			var refresh *time.Timer
			defer func() {
				if refresh != nil {
					refresh.Stop()
				}
			}()
			queueRefresh := func() {
				if refresh == nil {
					refresh = d.life.AfterFunc("notebookRefresh", notebookProgressInterval, func() {
						refreshReady <- struct{}{}
					})
				}
			}
			for {
				select {
				case event, ok := <-sub.C():
					if !ok {
						return
					}
					if event.Type == events.LocalIndexUpdated {
						data, _ := event.Data.(map[string]any)
						folder, _ := data["folder"].(string)
						paths, _ := data["filenames"].([]string)
						d.updateNotebookConflicts(run, folder, paths)
						d.refreshNotebookRows()
					}
					queueRefresh()
				case <-sub.Overflow():
					d.logf("Notebook sync event overflow; refreshing conflicts and health")
					d.refreshNotebookConflicts(run)
					d.refreshNotebookRows()
					queueRefresh()
				case <-refreshReady:
					refresh = nil
					d.refreshNotebookSync(run)
				case <-ctx.Done():
					return
				}
			}
		})
		<-serving
		d.notebookSync.mu.Lock()
		if run.progress != nil {
			run.progress.Stop()
			run.progress = nil
		}
		if d.notebookSync.run == run {
			d.notebookSync.run = nil
			d.notebookSyncChangedLocked()
		}
		d.notebookSync.mu.Unlock()
		d.refreshNotebookRows()
	}) {
		cancel()
		_ = engine.Serve(ctx)
		close(run.done)
		return nil, errDaemonStopping
	}
	if len(unindexed) != 0 {
		select {
		case <-engine.Ready():
		case <-run.done:
			return nil, errors.New("sync engine stopped before it started")
		}
		if err := engine.Configure(d.notebookEngineConfig(peers, ready, parked)); err != nil {
			cancel()
			<-run.done
			return nil, err
		}
	}
	return run, nil
}

type notebookSyncLog struct{ daemon *Daemon }

func (w notebookSyncLog) Write(data []byte) (int, error) {
	w.daemon.logf("Notebook sync: %s", strings.TrimSpace(string(data)))
	return len(data), nil
}

func (d *Daemon) notebookSyncChangedLocked() {
	if d.notebookSync.changed != nil {
		close(d.notebookSync.changed)
		d.notebookSync.changed = nil
	}
}

func (d *Daemon) notebookSyncChanges() <-chan struct{} {
	d.notebookSync.mu.Lock()
	defer d.notebookSync.mu.Unlock()
	if d.notebookSync.changed == nil {
		d.notebookSync.changed = make(chan struct{})
	}
	return d.notebookSync.changed
}

func (d *Daemon) refreshNotebookSync(run *notebookSyncRun) {
	r := &d.notebookSync
	r.mu.Lock()
	if r.run != run {
		r.mu.Unlock()
		return
	}
	peers := slices.Clone(run.peers)
	folders := maps.Clone(run.folders)
	disk := r.disk.clone()
	r.mu.Unlock()
	snapshots := make(map[string]map[string]notebookFolderSnapshot, len(folders))
	for id := range folders {
		snapshots[id] = make(map[string]notebookFolderSnapshot, len(peers))
		for _, peer := range peers {
			current, err := run.engine.Snapshot(id, notebookDevice(peer))
			snapshot := notebookFolderSnapshot{Root: disk.Folders[id].Root, PreviousRoot: disk.Folders[id].PreviousRoot,
				ScanComplete: current.ScanComplete, LocalSequence: current.LocalSequence, PeerSequence: current.PeerSequence,
				NeedItems: current.NeedItems, NeedDeletes: current.NeedDeletes, NeedBytes: current.NeedBytes}
			if err != nil {
				snapshot.Error = err.Error()
			} else if current.Error != nil {
				snapshot.Error = current.Error.Error()
			}
			for _, failure := range current.Errors {
				if snapshot.Error != "" {
					snapshot.Error += "; "
				}
				snapshot.Error += failure.Path + ": " + failure.Err
			}
			snapshots[id][peer] = snapshot
		}
	}
	r.mu.Lock()
	if r.run != run {
		r.mu.Unlock()
		return
	}
	if !maps.EqualFunc(run.snapshots, snapshots, maps.Equal) {
		run.snapshots = snapshots
		d.notebookSyncChangedLocked()
	}
	r.mu.Unlock()
	d.refreshNotebookRows()
	d.publishNotebookSyncSettings()
}

func (d *Daemon) notebookConflictCopies(folder, root string) (map[string]struct{}, error) {
	copies := map[string]struct{}{}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if rel == ".attn" {
				return filepath.SkipDir
			}
			return nil
		}
		if !entry.Type().IsRegular() || !attnsync.IsConflict(path) {
			return nil
		}
		rel = norm.NFC.String(filepath.ToSlash(rel))
		copies[rel] = struct{}{}
		return d.notifyNotebookConflict(folder, root, rel)
	})
	return copies, err
}

func (d *Daemon) notifyNotebookConflict(folder, root, rel string) error {
	path := filepath.Join(root, filepath.FromSlash(rel))
	sum := sha256.Sum256([]byte(root + "\x00" + rel))
	body, source := fmt.Sprintf("Both versions were kept. Compare %s and %s, merge what you need, then remove the conflict copy.", notebookConflictOriginal(path), path), rel
	if shared, err := d.notebookFolderSharedByEveryProfile(folder); err != nil {
		return err
	} else if !shared {
		body, source = "Both versions were kept in a Notebook folder. Open your Notebook, compare the file with its .sync-conflict- copy, merge what you need, then remove the copy.", folder
	}
	notification, inserted, err := d.store.EnsureNotification(store.NotificationRecord{
		ID: "notebook-conflict-" + hex.EncodeToString(sum[:]), Kind: "notebook_sync_conflict", Severity: store.NotificationWarning,
		Title: "Notebook conflict copy", Body: body,
		SourceKind: "notebook", SourceID: source,
	}, time.Now())
	if err == nil && inserted {
		d.publishFact(FactNotificationCreated, notification.ID, nil)
	}
	return err
}

func (d *Daemon) updateNotebookConflicts(run *notebookSyncRun, folder string, paths []string) {
	if run.home != d.daemonInstanceID {
		return
	}
	r := &d.notebookSync
	r.mu.Lock()
	root, ok := run.folders[folder]
	r.mu.Unlock()
	if !ok {
		return
	}
	for _, rel := range paths {
		if !attnsync.IsConflict(rel) {
			continue
		}
		rel = norm.NFC.String(filepath.ToSlash(rel))
		info, err := os.Lstat(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil && !os.IsNotExist(err) {
			d.logf("Notebook conflict %s: %v", rel, err)
			continue
		}
		present := err == nil && info.Mode().IsRegular()
		r.mu.Lock()
		_, known := run.conflicts[folder][rel]
		r.mu.Unlock()
		if present == known {
			continue
		}
		if present {
			if err := d.notifyNotebookConflict(folder, root, rel); err != nil {
				d.logf("Notebook conflict %s: %v", rel, err)
				continue
			}
		}
		r.mu.Lock()
		if r.run == run {
			if present {
				if run.conflicts[folder] == nil {
					run.conflicts[folder] = map[string]struct{}{}
				}
				run.conflicts[folder][rel] = struct{}{}
			} else {
				delete(run.conflicts[folder], rel)
			}
			d.notebookSyncChangedLocked()
		}
		r.mu.Unlock()
	}
}

func (d *Daemon) notebookPeerState(peer, name string) protocol.OutpostNotebook {
	r := &d.notebookSync
	r.mu.Lock()
	defer r.mu.Unlock()
	return d.notebookPeerStateLocked(peer, name)
}

func (d *Daemon) notebookPeerStateLocked(peer, name string) protocol.OutpostNotebook {
	r := &d.notebookSync
	info := protocol.OutpostNotebook{State: protocol.OutpostNotebookStateFirstSync}
	if r.error != "" {
		info.State, info.Error = protocol.OutpostNotebookStateError, protocol.Ptr(r.error)
		return info
	}
	if reason := r.rejections[peer].error; reason != "" {
		info.State, info.Error = protocol.OutpostNotebookStateError, protocol.Ptr(name+" rejected the Notebook folder list: "+reason)
		return info
	}
	run := r.run
	if run == nil {
		return info
	}
	if reason := run.streams[peer].error; reason != "" {
		info.State, info.Error = protocol.OutpostNotebookStateError, protocol.Ptr("Notebook stream: "+reason)
		return info
	}
	if reason := run.remote[peer].Error; reason != "" {
		info.State, info.Error = protocol.OutpostNotebookStateError, protocol.Ptr(name+": "+reason)
		return info
	}
	ids := slices.Sorted(maps.Keys(run.folders))
	ids = append(ids, slices.Sorted(maps.Keys(run.failed))...)
	if len(ids) == 0 {
		return info
	}
	rank := map[protocol.OutpostNotebookState]int{protocol.OutpostNotebookStateInSync: 0, protocol.OutpostNotebookStateSyncing: 1, protocol.OutpostNotebookStateFirstSync: 2, protocol.OutpostNotebookStateError: 3}
	info.State = protocol.OutpostNotebookStateInSync
	needBytes, needItems, measured, conflicts := 0, 0, true, 0
	var errs, previous []string
	for _, id := range ids {
		folder, _ := d.notebookFolderStateLocked(run, id, peer, name)
		if rank[folder.State] > rank[info.State] {
			info.State = folder.State
		}
		if folder.Error != nil {
			errs = append(errs, r.disk.Folders[id].Root+": "+*folder.Error)
		}
		if _, leftOut := run.failed[id]; leftOut {
			continue
		}
		if folder.NeedBytes == nil || folder.NeedItems == nil {
			measured = false
		} else {
			needBytes, needItems = needBytes+*folder.NeedBytes, needItems+*folder.NeedItems
		}
		conflicts += protocol.Deref(folder.Conflicts)
		if folder.PreviousRoot != nil {
			previous = append(previous, *folder.PreviousRoot)
		}
	}
	info.Conflicts = protocol.Ptr(conflicts)
	if measured {
		info.NeedBytes, info.NeedItems = protocol.Ptr(needBytes), protocol.Ptr(needItems)
	}
	if len(errs) != 0 {
		info.Error = protocol.Ptr(strings.Join(errs, "; "))
	}
	if len(previous) != 0 {
		info.PreviousRoot = protocol.Ptr(strings.Join(previous, ", "))
	}
	return info
}

func (d *Daemon) notebookFolderStateLocked(run *notebookSyncRun, id, peer, name string) (protocol.OutpostNotebook, bool) {
	info := protocol.OutpostNotebook{State: protocol.OutpostNotebookStateFirstSync, Conflicts: protocol.Ptr(len(run.conflicts[id]))}
	complete := d.notebookSync.disk.Folders[id].Completed[peer]
	remote := run.remote[peer].Folders[id]
	if remote.PreviousRoot != "" {
		info.PreviousRoot = protocol.Ptr(remote.PreviousRoot)
	}
	if reason, failed := run.failed[id]; failed {
		info.State, info.Error = protocol.OutpostNotebookStateError, protocol.Ptr("home: "+reason)
		return info, complete
	}
	local := run.snapshots[id][peer]
	needed := local.NeedItems + local.NeedDeletes + remote.NeedItems + remote.NeedDeletes
	indicesExchanged := local.ScanComplete && remote.ScanComplete && remote.PeerSequence >= local.LocalSequence && local.PeerSequence >= remote.LocalSequence
	if indicesExchanged {
		info.NeedBytes, info.NeedItems = protocol.Ptr(int(local.NeedBytes+remote.NeedBytes)), protocol.Ptr(needed)
	}
	switch {
	case local.Error != "":
		info.State, info.Error = protocol.OutpostNotebookStateError, protocol.Ptr("home: "+local.Error)
	case remote.Error != "":
		info.State, info.Error = protocol.OutpostNotebookStateError, protocol.Ptr(name+": "+remote.Error)
	case indicesExchanged && needed == 0:
		info.State = protocol.OutpostNotebookStateInSync
	case complete:
		info.State = protocol.OutpostNotebookStateSyncing
	}
	return info, complete
}

func (d *Daemon) recordNotebookCompletionLocked(peer, name string) error {
	r := &d.notebookSync
	run := r.run
	if run == nil || r.error != "" || r.rejections[peer].error != "" || run.streams[peer].error != "" || run.remote[peer].Error != "" {
		return nil
	}
	var done []string
	for id := range run.folders {
		if state, complete := d.notebookFolderStateLocked(run, id, peer, name); state.State == protocol.OutpostNotebookStateInSync && !complete {
			done = append(done, id)
		}
	}
	if len(done) == 0 {
		return nil
	}
	disk := r.disk.clone()
	for _, id := range done {
		entry := disk.Folders[id]
		if entry.Completed == nil {
			entry.Completed = map[string]bool{}
		}
		entry.Completed[peer] = true
		disk.Folders[id] = entry
	}
	return d.saveNotebookSyncDiskLocked(disk)
}

func (d *Daemon) refreshNotebookRows() {
	if !d.enrollmentStatus().IsHome() {
		return
	}
	rows, err := d.store.ListOutposts()
	if err != nil {
		return
	}
	for _, row := range rows {
		if row.DaemonID == "" {
			continue
		}
		d.notebookSync.mu.Lock()
		saveErr := d.recordNotebookCompletionLocked(row.DaemonID, row.Name)
		info := d.notebookPeerStateLocked(row.DaemonID, row.Name)
		d.notebookSync.mu.Unlock()
		if saveErr != nil {
			info.State, info.Error = protocol.OutpostNotebookStateError, protocol.Ptr("save first sync completion: "+saveErr.Error())
		}
		d.outposts.mu.Lock()
		run := d.outposts.dialers[row.ID]
		changed := run != nil && (run.notebook == nil || run.notebook.State != info.State || protocol.Deref(run.notebook.Error) != protocol.Deref(info.Error) || protocol.Deref(run.notebook.Conflicts) != protocol.Deref(info.Conflicts) || protocol.Deref(run.notebook.PreviousRoot) != protocol.Deref(info.PreviousRoot))
		progress := run != nil && run.notebook != nil && (protocol.Deref(run.notebook.NeedBytes) != protocol.Deref(info.NeedBytes) || protocol.Deref(run.notebook.NeedItems) != protocol.Deref(info.NeedItems))
		if run != nil {
			run.notebook = &info
		}
		d.outposts.mu.Unlock()
		if changed {
			d.cancelNotebookProgress()
			d.publishFact(FactOutpostChanged, row.ID, nil)
		} else if progress {
			d.queueNotebookProgress()
		}
	}
}

func (d *Daemon) cancelNotebookProgress() {
	r := &d.notebookSync
	r.mu.Lock()
	defer r.mu.Unlock()
	if run := r.run; run != nil && run.progress != nil {
		run.progress.Stop()
		run.progress = nil
	}
}

func (d *Daemon) queueNotebookProgress() {
	r := &d.notebookSync
	r.mu.Lock()
	defer r.mu.Unlock()
	run := r.run
	if run == nil || run.ctx.Err() != nil || run.progress != nil {
		return
	}
	var timer *time.Timer
	timer = d.life.AfterFunc("notebookProgress", notebookProgressInterval, func() {
		r.mu.Lock()
		current := r.run == run && run.ctx.Err() == nil && run.progress == timer
		if current {
			run.progress = nil
		}
		r.mu.Unlock()
		if current {
			d.projectOutpostsUpdated()
		}
	})
	run.progress = timer
}

func (d *Daemon) receiveNotebookSyncReport(ctx context.Context, msg protocol.NotebookSyncReportMessage) (protocol.OutpostNotebook, error) {
	origin, remote := OriginOf(ctx)
	if !remote || !d.isOutpostConn(origin.Host, origin.bridge) {
		return protocol.OutpostNotebook{}, errors.New("notebook report requires the current outpost bridge")
	}
	var report notebookSyncReport
	if err := json.Unmarshal([]byte(msg.Report), &report); err != nil {
		return protocol.OutpostNotebook{}, err
	}
	row, err := d.resolveOutpost(origin.Host)
	if err != nil {
		return protocol.OutpostNotebook{}, err
	}
	d.notebookSync.mu.Lock()
	if run := d.notebookSync.run; run != nil {
		run.remote[row.DaemonID] = report
		d.notebookSyncChangedLocked()
	}
	d.notebookSync.mu.Unlock()
	d.refreshNotebookRows()
	return d.notebookPeerState(row.DaemonID, row.Name), nil
}

func (d *Daemon) outpostNotebookReportLocked(home string) notebookSyncReport {
	r := &d.notebookSync
	report := notebookSyncReport{Error: r.error, Folders: map[string]notebookFolderSnapshot{}}
	run := r.run
	if run == nil {
		return report
	}
	for id, entry := range r.disk.Folders {
		_, ready := run.folders[id]
		reason, failed := run.failed[id]
		if !ready && !failed {
			continue
		}
		snapshot := run.snapshots[id][home]
		snapshot.Root, snapshot.PreviousRoot = entry.Root, entry.PreviousRoot
		if failed {
			snapshot.Error = reason
		}
		report.Folders[id] = snapshot
	}
	if reason := run.streams[home].error; reason != "" {
		report.Error = reason
	}
	return report
}

func (d *Daemon) sendNotebookSyncReports(conn *bridge.Conn) {
	for conn.Context().Err() == nil {
		changed := d.notebookSyncChanges()
		d.notebookSync.mu.Lock()
		report := d.outpostNotebookReportLocked(conn.Peer().DaemonID)
		d.notebookSync.mu.Unlock()
		encoded, err := json.Marshal(report)
		if err == nil {
			var state protocol.OutpostNotebook
			err = conn.Call(conn.Context(), bridgeCall, protocol.NotebookSyncReportMessage{Cmd: protocol.CmdNotebookSyncReport, Report: string(encoded)}, &state)
			pausepoint.Done(pausepoint.NotebookReportSent, err)
			if err == nil {
				d.notebookSync.mu.Lock()
				if run := d.notebookSync.run; run != nil {
					run.homeState = state
				}
				d.notebookSync.mu.Unlock()
				d.publishNotebookSyncSettings()
			}
		}
		if err != nil {
			if conn.Context().Err() == nil {
				d.logf("Notebook report: %v", err)
			}
			return
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

func (d *Daemon) notebookSyncEngine() *notebookSyncRun {
	d.notebookSync.mu.Lock()
	defer d.notebookSync.mu.Unlock()
	return d.notebookSync.run
}

func (d *Daemon) serveNotebookSync(conn *bridge.Conn, incoming ssh.NewChannel) {
	run := d.notebookSyncEngine()
	if run == nil {
		_ = incoming.Reject(ssh.ResourceShortage, "Notebook sync engine is not running")
		return
	}
	channel, requests, err := incoming.Accept()
	if err != nil {
		return
	}
	ctx, cancel := context.WithCancel(run.ctx)
	defer cancel()
	stop := context.AfterFunc(conn.Context(), cancel)
	defer stop()
	d.notebookSync.mu.Lock()
	run.streams[conn.Peer().DaemonID] = notebookSyncStream{conn: conn}
	d.notebookSync.mu.Unlock()
	stream := conn.Bulk(ctx, channel, requests)
	if err := run.engine.ServeConn(ctx, stream, notebookDevice(conn.Peer().DaemonID)); err != nil && ctx.Err() == nil {
		d.notebookStreamError(run, conn, err)
	}
}

func (d *Daemon) openNotebookSync(conn *bridge.Conn) {
	pausepoint.At(pausepoint.NotebookStreamOpen)
	run := d.notebookSyncEngine()
	if run == nil {
		return
	}
	ctx, cancel := context.WithCancel(run.ctx)
	defer cancel()
	stop := context.AfterFunc(conn.Context(), cancel)
	defer stop()
	d.notebookSync.mu.Lock()
	delete(run.remote, conn.Peer().DaemonID)
	run.streams[conn.Peer().DaemonID] = notebookSyncStream{conn: conn}
	d.notebookSync.mu.Unlock()
	channel, requests, err := conn.Open(ctx, notebookChannel, nil)
	if err == nil {
		err = run.engine.ServeConn(ctx, conn.Bulk(ctx, channel, requests), notebookDevice(conn.Peer().DaemonID))
	}
	if err != nil && ctx.Err() == nil {
		d.notebookStreamError(run, conn, err)
	}
}

func (d *Daemon) notebookSyncSettings() (string, string) {
	r := &d.notebookSync
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.error != "" {
		return "error", r.error
	}
	run := r.run
	if run == nil {
		return "off", ""
	}
	if run.home == d.daemonInstanceID {
		result := protocol.OutpostNotebookStateInSync
		for _, peer := range run.peers {
			state := d.notebookPeerStateLocked(peer, peer)
			if state.State == protocol.OutpostNotebookStateError {
				return "error", protocol.Deref(state.Error)
			}
			if state.State == protocol.OutpostNotebookStateFirstSync {
				result = state.State
			} else if state.State == protocol.OutpostNotebookStateSyncing && result != protocol.OutpostNotebookStateFirstSync {
				result = state.State
			}
		}
		return string(result), ""
	}
	if reason := run.streams[run.home].error; reason != "" {
		return "error", reason
	}
	if failed := slices.Sorted(maps.Keys(run.failed)); len(failed) != 0 {
		return "error", r.disk.Folders[failed[0]].Root + ": " + run.failed[failed[0]]
	}
	for _, id := range slices.Sorted(maps.Keys(run.folders)) {
		if reason := run.snapshots[id][run.home].Error; reason != "" {
			return "error", run.folders[id] + ": " + reason
		}
	}
	if run.homeState.State == "" {
		return "first_sync", ""
	}
	return string(run.homeState.State), protocol.Deref(run.homeState.Error)
}

func (d *Daemon) NotebookReady(host, profileID string) error {
	row, err := d.resolveOutpost(host)
	if err != nil {
		return err
	}
	root, err := d.notebookRoot(profileID)
	if err != nil {
		return err
	}
	id := notebookFolderID(d.daemonInstanceID, notebook.FolderKey(root))
	r := &d.notebookSync
	r.mu.Lock()
	defer r.mu.Unlock()
	if run := r.run; run != nil {
		if reason, leftOut := run.failed[id]; leftOut {
			return fmt.Errorf("the Notebook folder %s does not sync with %s: %s", root, row.Name, reason)
		}
	}
	if d.loadedNotebookSyncDiskLocked(d.daemonInstanceID).Folders[id].Completed[row.DaemonID] {
		return nil
	}
	info := protocol.OutpostNotebook{State: protocol.OutpostNotebookStateFirstSync}
	if run := r.run; run != nil {
		info, _ = d.notebookFolderStateLocked(run, id, row.DaemonID, row.Name)
	}
	if r.error != "" {
		info.Error = protocol.Ptr(r.error)
	}
	remaining := notebookRemainingWork(info)
	if info.Error != nil {
		remaining += "; " + *info.Error
	}
	return fmt.Errorf("%s's first sync of the Notebook folder %s has not completed: %s", row.Name, root, remaining)
}

func notebookRemainingWork(info protocol.OutpostNotebook) string {
	if info.NeedBytes == nil || info.NeedItems == nil {
		return "remaining work has not been reported yet"
	}
	return fmt.Sprintf("%d bytes and %d items left", *info.NeedBytes, *info.NeedItems)
}

func (d *Daemon) refreshNotebookConflicts(run *notebookSyncRun) {
	if run.home != d.daemonInstanceID {
		return
	}
	r := &d.notebookSync
	r.mu.Lock()
	folders := maps.Clone(run.folders)
	r.mu.Unlock()
	found := map[string]map[string]struct{}{}
	for id, root := range folders {
		copies, err := d.notebookConflictCopies(id, root)
		if err != nil {
			d.logf("Notebook conflicts in %s: %v", root, err)
			return
		}
		found[id] = copies
	}
	r.mu.Lock()
	if r.run == run {
		changed := !maps.EqualFunc(run.conflicts, found, func(a, b map[string]struct{}) bool { return len(a) == len(b) })
		run.conflicts = found
		if changed {
			d.notebookSyncChangedLocked()
		}
	}
	r.mu.Unlock()
}

func (d *Daemon) notebookFilesChanged() {
	d.life.Do("notebookFilesChanged", func() {
		d.notebookSync.mu.Lock()
		d.notebookSyncChangedLocked()
		d.notebookSync.mu.Unlock()
	})
}

func (d *Daemon) publishNotebookSyncSettings() {
	state, reason := d.notebookSyncSettings()
	r := &d.notebookSync
	r.mu.Lock()
	stateChanged, errorChanged := r.settingState != state, r.settingError != reason
	r.settingState, r.settingError = state, reason
	r.mu.Unlock()
	d.coalesceSnapshots(func() {
		if stateChanged {
			d.publishSettingsFact(FactSettingChanged, string(settingNotebookSyncState))
		}
		if errorChanged {
			d.publishSettingsFact(FactSettingChanged, string(settingNotebookSyncLastError))
		}
	})
}

func (d *Daemon) dropNotebookSyncFromOtherHome(home string) error {
	r := &d.notebookSync
	r.configure.Lock()
	defer r.configure.Unlock()
	if run := d.notebookSyncEngine(); run != nil && run.home != home {
		run.cancel()
		<-run.done
		r.mu.Lock()
		if r.run == run {
			r.run = nil
		}
		d.notebookSyncChangedLocked()
		r.mu.Unlock()
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return d.loadNotebookSyncDiskLocked(home)
}

func (d *Daemon) forgetNotebookSyncPeer(peer string) {
	r := &d.notebookSync
	r.configure.Lock()
	defer r.configure.Unlock()
	r.mu.Lock()
	defer r.mu.Unlock()
	run := r.run
	if run != nil {
		delete(run.remote, peer)
		delete(run.streams, peer)
		for _, snapshots := range run.snapshots {
			delete(snapshots, peer)
		}
		if r.loaded {
			disk := r.disk.clone()
			for id, entry := range disk.Folders {
				delete(entry.Completed, peer)
				disk.Folders[id] = entry
			}
			if err := d.saveNotebookSyncDiskLocked(disk); err != nil {
				d.logf("forget Notebook peer: %v", err)
			}
		}
		return
	}
	rows, err := d.store.ListOutposts()
	if err == nil && len(rows) == 0 {
		if err := os.RemoveAll(d.notebookSyncDir()); err != nil {
			d.logf("remove Notebook sync index: %v", err)
		}
		d.forgetNotebookSyncDiskLocked()
	}
}

const notebookArtifactWaitTripwire = 8 * time.Hour

func (d *Daemon) waitForNotebookArtifact(ctx context.Context, origin Origin, receipt *seedArtifactTransferReceipt) error {
	root, err := d.seedNotebookRoot(receipt.SeedID)
	if err != nil {
		return err
	}
	path := filepath.Join(root, "seeds", receipt.SeedID, receipt.Filename)
	timer := time.NewTimer(notebookArtifactWaitTripwire)
	defer timer.Stop()
	for {
		changed := d.notebookSyncChanges()
		if receipt.Operation == "detach" {
			if _, err := os.Lstat(path); os.IsNotExist(err) {
				return nil
			}
		} else if hash, err := hashRegularFile(path); err == nil && hash == receipt.Hash {
			return nil
		}
		row, err := d.resolveOutpost(origin.Host)
		if err != nil {
			return err
		}
		state := d.notebookPeerState(row.DaemonID, row.Name)
		pending := fmt.Sprintf("%s completed on %s; the home has not received %s (%d bytes): %s", receipt.Operation, origin.Name, receipt.Filename, receipt.Size, notebookRemainingWork(state))
		if state.Error != nil {
			return fmt.Errorf("%s; %s; retry the same command after sync recovers", pending, *state.Error)
		}
		select {
		case <-changed:
		case <-origin.bridge.Done():
			return fmt.Errorf("%s; lost %s; retry the same command after reconnecting", pending, origin.Name)
		case <-ctx.Done():
			return fmt.Errorf("%s; %w", pending, ctx.Err())
		case <-timer.C:
			return fmt.Errorf("%s; wait limit=%s exceeded; retry the same command", pending, notebookArtifactWaitTripwire)
		}
	}
}

func (d *Daemon) seedNotebookFolder(seedID string) (string, string, error) {
	root, err := d.seedNotebookRoot(seedID)
	if err != nil {
		return "", "", err
	}
	return notebookFolderID(d.daemonInstanceID, notebook.FolderKey(root)), root, nil
}

func (d *Daemon) outpostNotebookRoot(status enrollment.Status, profileID string) (string, error) {
	r := &d.notebookSync
	r.mu.Lock()
	defer r.mu.Unlock()
	disk := d.loadedNotebookSyncDiskLocked(status.HomeDaemonID)
	for _, id := range slices.Sorted(maps.Keys(disk.Folders)) {
		if entry := disk.Folders[id]; slices.Contains(entry.Profiles, profileID) {
			return entry.Root, nil
		}
	}
	home := status.HomeName
	if home == "" {
		home = status.HomeDaemonID
	}
	return "", fmt.Errorf("%s has not sent the Notebook folder for profile %s yet", home, profileID)
}

func (d *Daemon) outpostNotebookCopy(folderID string) (string, error) {
	status := d.enrollmentStatus()
	if status.IsHome() {
		return "", errors.New("the home keeps no outpost copy of a Notebook folder")
	}
	r := &d.notebookSync
	r.mu.Lock()
	defer r.mu.Unlock()
	entry, ok := d.loadedNotebookSyncDiskLocked(status.HomeDaemonID).Folders[folderID]
	if !ok || len(entry.Profiles) == 0 {
		return "", fmt.Errorf("this outpost does not sync Notebook folder %s", folderID)
	}
	if !entry.Initialized {
		return "", fmt.Errorf("this outpost has not prepared its copy %s yet", entry.Root)
	}
	return entry.Root, nil
}

func (d *Daemon) callerSeedArtifactDir(ctx context.Context, seed string) (string, error) {
	folder, root, err := d.seedNotebookFolder(seed)
	if err != nil {
		return "", err
	}
	if origin, remote := OriginOf(ctx); remote {
		row, err := d.resolveOutpost(origin.Host)
		if err != nil {
			return "", err
		}
		r := &d.notebookSync
		r.mu.Lock()
		copied := ""
		if run := r.run; run != nil {
			copied = run.remote[row.DaemonID].Folders[folder].Root
		}
		r.mu.Unlock()
		if copied == "" {
			return "", fmt.Errorf("%s has not reported its copy of %s yet", origin.Name, root)
		}
		root = copied
	}
	return filepath.Join(root, "seeds", seed), nil
}

func (d *Daemon) notebookFolderSharedByEveryProfile(folder string) (bool, error) {
	d.notebookSync.mu.Lock()
	users := slices.Clone(d.notebookSync.disk.Folders[folder].Profiles)
	d.notebookSync.mu.Unlock()
	live, err := d.store.ListProfiles(false)
	if err != nil {
		return false, err
	}
	for _, profile := range live {
		if !slices.Contains(users, profile.ID) {
			return false, nil
		}
	}
	return true, nil
}

func notebookConflictOriginal(path string) string {
	original, suffix, _ := strings.Cut(path, ".sync-conflict-")
	if _, ext, found := strings.Cut(suffix, "."); found {
		original += "." + ext
	}
	return original
}

func (d *Daemon) notebookStreamError(run *notebookSyncRun, conn *bridge.Conn, err error) {
	pausepoint.At(pausepoint.NotebookStreamError)
	defer pausepoint.At(pausepoint.NotebookStreamErrorHandled)
	r := &d.notebookSync
	r.mu.Lock()
	peer := conn.Peer().DaemonID
	if r.run != run || run.streams[peer].conn != conn {
		r.mu.Unlock()
		return
	}
	run.streams[peer] = notebookSyncStream{conn: conn, error: err.Error()}
	d.notebookSyncChangedLocked()
	r.mu.Unlock()
	d.logf("Notebook stream: %v", err)
	d.refreshNotebookRows()
	d.publishNotebookSyncSettings()
}

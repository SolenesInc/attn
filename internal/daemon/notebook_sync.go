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
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/victorarias/attn/internal/bridge"
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
	Initialized  bool
	Home         string
	Root         string
	PreviousRoot string
	Completed    map[string]bool
}

type notebookSyncSnapshot struct {
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
	folder    string
	disk      notebookSyncDisk
	peers     []string
	cancel    context.CancelFunc
	done      chan struct{}
	ctx       context.Context
	snapshots map[string]notebookSyncSnapshot
	remote    map[string]notebookSyncSnapshot
	conflicts map[string]struct{}
	homeState protocol.OutpostNotebook
	progress  *time.Timer
}

type notebookSyncStream struct {
	conn  *bridge.Conn
	error string
}

type notebookSyncRuntime struct {
	configure    sync.Mutex
	mu           sync.Mutex
	run          *notebookSyncRun
	error        string
	changed      chan struct{}
	settingState string
	settingError string
}

func notebookDevice(id string) syncprotocol.DeviceID {
	return syncprotocol.NewDeviceID([]byte("attn-daemon:" + id))
}

func (d *Daemon) notebookSyncDir() string { return filepath.Join(d.dataRoot, "notebook-sync") }

func (d *Daemon) notebookSyncPeers() (string, []string, error) {
	status := d.enrollmentStatus()
	if !status.IsHome() {
		if d.machineSetting(settingOutpostListen) != "" && status.HomeDaemonID != "" {
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
	home, peers, err := d.notebookSyncPeers()
	root, rootErr := d.notebookRoot()
	err = errors.Join(err, rootErr)
	r.mu.Lock()
	old := r.run
	r.mu.Unlock()
	if old != nil && (len(peers) == 0 || err != nil || old.disk.Home != home || old.disk.Root != root || old.ctx.Err() != nil) {
		old.cancel()
		<-old.done
		r.mu.Lock()
		if r.run == old {
			r.run = nil
		}
		r.mu.Unlock()
		old = nil
	}
	if err == nil && len(peers) != 0 && !d.life.Ended() {
		if old != nil {
			if !slices.Equal(old.peers, peers) {
				err = old.engine.Configure(d.notebookEngineConfig(home, root, peers))
				if err == nil {
					r.mu.Lock()
					old.peers = slices.Clone(peers)
					r.mu.Unlock()
				}
			}
		} else {
			err = d.startNotebookSync(home, root, peers)
		}
	}
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
	d.refreshNotebookRows()
	d.publishNotebookSyncSettings()
}

func (d *Daemon) notebookEngineConfig(home, root string, peers []string) syncconfig.Configuration {
	id := notebookDevice(d.daemonInstanceID)
	cfg := attnsync.Configuration(id)
	folder := cfg.Defaults.Folder.Copy()
	folder.ID, folder.Path, folder.Type = "notebook-"+home, root, syncconfig.FolderTypeSendReceive
	folder.RescanIntervalS = 3600
	folder.FSWatcherEnabled = d.notebookSyncWatch
	folder.Versioning.Type = "trashcan"
	folder.Versioning.FSPath = filepath.Join(d.dataRoot, "notebook-trash")
	folder.Versioning.Params = map[string]string{"cleanoutDays": strconv.Itoa(notebookTrashDays)}
	folder.Devices = []syncconfig.FolderDeviceConfiguration{{DeviceID: id}}
	for _, peer := range peers {
		device := cfg.Defaults.Device.Copy()
		device.DeviceID = notebookDevice(peer)
		cfg.Devices = append(cfg.Devices, device)
		folder.Devices = append(folder.Devices, syncconfig.FolderDeviceConfiguration{DeviceID: device.DeviceID})
	}
	cfg.Folders = []syncconfig.FolderConfiguration{folder}
	return cfg
}

func (d *Daemon) prepareNotebookSync(home, root string) (notebookSyncDisk, error) {
	var disk notebookSyncDisk
	data, err := os.ReadFile(filepath.Join(d.notebookSyncDir(), "state.json"))
	if err == nil {
		if err := json.Unmarshal(data, &disk); err != nil {
			return disk, err
		}
	} else if !os.IsNotExist(err) {
		return disk, err
	}
	if disk.Home != home || disk.Root != root {
		if err := os.RemoveAll(d.notebookSyncDir()); err != nil {
			return disk, err
		}
		disk = notebookSyncDisk{Home: home, Root: root, Completed: map[string]bool{}}
		if home != d.daemonInstanceID {
			entries, err := os.ReadDir(root)
			if err != nil && !os.IsNotExist(err) {
				return disk, err
			}
			if len(entries) != 0 {
				disk.PreviousRoot = root + ".before-sync-" + time.Now().UTC().Format("20060102T150405.000000000Z")
			}
		}
		if err := d.saveNotebookSyncDisk(disk); err != nil {
			return disk, err
		}
	}
	if disk.Initialized {
		return disk, nil
	}
	if disk.PreviousRoot != "" {
		if _, err := os.Lstat(disk.PreviousRoot); os.IsNotExist(err) {
			if err := os.Rename(root, disk.PreviousRoot); err != nil {
				return disk, err
			}
		} else if err != nil {
			return disk, err
		}
	}
	marker := filepath.Join(root, ".attn", "notebook-sync")
	if err := os.MkdirAll(filepath.Dir(marker), 0700); err != nil {
		return disk, err
	}
	if err := os.WriteFile(marker, nil, 0600); err != nil {
		return disk, err
	}
	disk.Initialized = true
	return disk, d.saveNotebookSyncDisk(disk)
}

func (d *Daemon) saveNotebookSyncDisk(disk notebookSyncDisk) error {
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
	return syncDirectory(dir)
}

func (d *Daemon) startNotebookSync(home, root string, peers []string) error {
	disk, err := d.prepareNotebookSync(home, root)
	if err != nil {
		return err
	}
	logger := slog.New(slog.NewTextHandler(notebookSyncLog{d}, &slog.HandlerOptions{Level: slog.LevelWarn}))
	engine, err := attnsync.New(filepath.Join(d.notebookSyncDir(), "index"), d.notebookEngineConfig(home, root, peers), notebookDevice(d.daemonInstanceID), logger)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(d.life.Context())
	run := &notebookSyncRun{engine: engine, folder: "notebook-" + home, disk: disk, peers: slices.Clone(peers), cancel: cancel, ctx: ctx, done: make(chan struct{}), snapshots: map[string]notebookSyncSnapshot{}, remote: map[string]notebookSyncSnapshot{}, conflicts: map[string]struct{}{}, streams: map[string]notebookSyncStream{}}
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
						if data["folder"] == run.folder {
							paths, _ := data["filenames"].([]string)
							d.updateNotebookConflicts(run, paths)
							d.refreshNotebookRows()
						}
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
		return errDaemonStopping
	}
	d.ensureNotebookWatcher(root)
	return nil
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
	d.notebookSync.mu.Lock()
	if d.notebookSync.run != run {
		d.notebookSync.mu.Unlock()
		return
	}
	peers := slices.Clone(run.peers)
	d.notebookSync.mu.Unlock()
	snapshots := make(map[string]notebookSyncSnapshot, len(peers))
	for _, peer := range peers {
		current, err := run.engine.Snapshot(run.folder, notebookDevice(peer))
		snapshot := notebookSyncSnapshot{Root: run.disk.Root, PreviousRoot: run.disk.PreviousRoot,
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
		snapshots[peer] = snapshot
	}
	d.notebookSync.mu.Lock()
	if d.notebookSync.run != run {
		d.notebookSync.mu.Unlock()
		return
	}
	changed := false
	for peer, snapshot := range snapshots {
		if run.snapshots[peer] != snapshot {
			run.snapshots[peer] = snapshot
			changed = true
		}
	}
	if changed {
		d.notebookSyncChangedLocked()
	}
	d.notebookSync.mu.Unlock()
	d.refreshNotebookRows()
	d.publishNotebookSyncSettings()
}

func (d *Daemon) notebookConflictCopies(root string) (map[string]struct{}, error) {
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
		return d.notifyNotebookConflict(root, rel)
	})
	return copies, err
}

func (d *Daemon) notifyNotebookConflict(root, rel string) error {
	path := filepath.Join(root, filepath.FromSlash(rel))
	sum := sha256.Sum256([]byte(root + "\x00" + rel))
	notification, inserted, err := d.store.EnsureNotification(store.NotificationRecord{
		ID: "notebook-conflict-" + hex.EncodeToString(sum[:]), Kind: "notebook_sync_conflict", Severity: store.NotificationWarning,
		Title: "Notebook conflict copy", Body: fmt.Sprintf("Both versions were kept. Compare %s and %s, merge what you need, then remove the conflict copy.", notebookConflictOriginal(path), path),
		SourceKind: "notebook", SourceID: rel,
	}, time.Now())
	if err == nil && inserted {
		d.publishFact(FactNotificationCreated, notification.ID, nil)
	}
	return err
}

func (d *Daemon) updateNotebookConflicts(run *notebookSyncRun, paths []string) {
	if run.disk.Home != d.daemonInstanceID {
		return
	}
	for _, rel := range paths {
		if !attnsync.IsConflict(rel) {
			continue
		}
		rel = norm.NFC.String(filepath.ToSlash(rel))
		info, err := os.Lstat(filepath.Join(run.disk.Root, filepath.FromSlash(rel)))
		if err != nil && !os.IsNotExist(err) {
			d.logf("Notebook conflict %s: %v", rel, err)
			continue
		}
		present := err == nil && info.Mode().IsRegular()
		d.notebookSync.mu.Lock()
		_, known := run.conflicts[rel]
		d.notebookSync.mu.Unlock()
		if present == known {
			continue
		}
		if present {
			if err := d.notifyNotebookConflict(run.disk.Root, rel); err != nil {
				d.logf("Notebook conflict %s: %v", rel, err)
				continue
			}
		}
		d.notebookSync.mu.Lock()
		if d.notebookSync.run == run {
			if present {
				run.conflicts[rel] = struct{}{}
			} else {
				delete(run.conflicts, rel)
			}
			d.notebookSyncChangedLocked()
		}
		d.notebookSync.mu.Unlock()
	}
}

func (d *Daemon) notebookPeerState(peer, name string) (protocol.OutpostNotebook, bool) {
	r := &d.notebookSync
	r.mu.Lock()
	defer r.mu.Unlock()
	return d.notebookPeerStateLocked(peer, name)
}

func (d *Daemon) notebookPeerStateLocked(peer, name string) (protocol.OutpostNotebook, bool) {
	r := &d.notebookSync
	info := protocol.OutpostNotebook{State: protocol.OutpostNotebookStateFirstSync}
	if r.error != "" {
		info.State, info.Error = protocol.OutpostNotebookStateError, protocol.Ptr(r.error)
		return info, false
	}
	run := r.run
	if run == nil {
		return info, false
	}
	local := run.snapshots[peer]
	remote := run.remote[peer]
	complete := run.disk.Completed[peer]
	needed := local.NeedItems + local.NeedDeletes + remote.NeedItems + remote.NeedDeletes
	indicesExchanged := local.ScanComplete && remote.ScanComplete && remote.PeerSequence >= local.LocalSequence && local.PeerSequence >= remote.LocalSequence
	info.Conflicts = protocol.Ptr(len(run.conflicts))
	if indicesExchanged {
		info.NeedBytes, info.NeedItems = protocol.Ptr(int(local.NeedBytes+remote.NeedBytes)), protocol.Ptr(needed)
	}
	if remote.PreviousRoot != "" {
		info.PreviousRoot = protocol.Ptr(remote.PreviousRoot)
	}
	switch {
	case run.streams[peer].error != "":
		info.State, info.Error = protocol.OutpostNotebookStateError, protocol.Ptr("Notebook stream: "+run.streams[peer].error)
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
		info, complete := d.notebookPeerStateLocked(row.DaemonID, row.Name)
		if info.State == protocol.OutpostNotebookStateInSync && !complete {
			run := d.notebookSync.run
			run.disk.Completed[row.DaemonID] = true
			if err := d.saveNotebookSyncDisk(run.disk); err != nil {
				delete(run.disk.Completed, row.DaemonID)
				info.State, info.Error = protocol.OutpostNotebookStateError, protocol.Ptr("save first sync completion: "+err.Error())
			}
		}
		d.notebookSync.mu.Unlock()
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
	var report notebookSyncSnapshot
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
	state, _ := d.notebookPeerState(row.DaemonID, row.Name)
	return state, nil
}

func (d *Daemon) sendNotebookSyncReports(conn *bridge.Conn) {
	for conn.Context().Err() == nil {
		changed := d.notebookSyncChanges()
		d.notebookSync.mu.Lock()
		report := notebookSyncSnapshot{Error: d.notebookSync.error}
		if run := d.notebookSync.run; run != nil {
			report = run.snapshots[conn.Peer().DaemonID]
			report.Root, report.PreviousRoot = run.disk.Root, run.disk.PreviousRoot
			if reason := run.streams[conn.Peer().DaemonID].error; reason != "" {
				report.Error = reason
			}
		}
		d.notebookSync.mu.Unlock()
		encoded, err := json.Marshal(report)
		if err == nil {
			var state protocol.OutpostNotebook
			state, err = Call[protocol.OutpostNotebook](conn.Context(), d.router(), protocol.NotebookSyncReportMessage{Cmd: protocol.CmdNotebookSyncReport, Report: string(encoded)})
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

func (d *Daemon) notebookRootChangeAllowed() error {
	home, peers, err := d.notebookSyncPeers()
	if err != nil || len(peers) == 0 {
		return err
	}
	names := []string{}
	if home != d.daemonInstanceID {
		name := d.enrollmentStatus().HomeName
		if name == "" {
			name = home
		}
		names = append(names, name)
	} else {
		rows, err := d.store.ListOutposts()
		if err != nil {
			return err
		}
		for _, row := range rows {
			if row.DaemonID != "" {
				names = append(names, row.Name)
			}
		}
	}
	return fmt.Errorf("notebook.root cannot change while syncing with %s; disconnect those peers first", strings.Join(names, ", "))
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
	if run.disk.Home == d.daemonInstanceID {
		result := protocol.OutpostNotebookStateInSync
		for _, peer := range run.peers {
			state, _ := d.notebookPeerStateLocked(peer, peer)
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
	snapshot := run.snapshots[run.disk.Home]
	if reason := run.streams[run.disk.Home].error; reason != "" {
		return "error", reason
	}
	if snapshot.Error != "" {
		return "error", snapshot.Error
	}
	if run.homeState.State == "" {
		return "first_sync", ""
	}
	return string(run.homeState.State), protocol.Deref(run.homeState.Error)
}

func (d *Daemon) NotebookReady(host string) error {
	row, err := d.resolveOutpost(host)
	if err != nil {
		return err
	}
	root, err := d.notebookRoot()
	if err != nil {
		return err
	}
	data, err := os.ReadFile(filepath.Join(d.notebookSyncDir(), "state.json"))
	if err == nil {
		var disk notebookSyncDisk
		if err := json.Unmarshal(data, &disk); err != nil {
			return err
		}
		if disk.Home == d.daemonInstanceID && disk.Root == root && disk.Completed[row.DaemonID] {
			return nil
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	info, _ := d.notebookPeerState(row.DaemonID, row.Name)
	remaining := notebookRemainingWork(info)
	if info.Error != nil {
		remaining += "; " + *info.Error
	}
	return fmt.Errorf("%s's Notebook first sync has not completed: %s", row.Name, remaining)
}

func notebookRemainingWork(info protocol.OutpostNotebook) string {
	if info.NeedBytes == nil || info.NeedItems == nil {
		return "remaining work has not been reported yet"
	}
	return fmt.Sprintf("%d bytes and %d items left", *info.NeedBytes, *info.NeedItems)
}

func (d *Daemon) refreshNotebookConflicts(run *notebookSyncRun) {
	if run.disk.Home != d.daemonInstanceID {
		return
	}
	copies, err := d.notebookConflictCopies(run.disk.Root)
	if err != nil {
		d.logf("Notebook conflicts: %v", err)
		return
	}
	d.notebookSync.mu.Lock()
	if d.notebookSync.run == run {
		changed := len(run.conflicts) != len(copies)
		run.conflicts = copies
		if changed {
			d.notebookSyncChangedLocked()
		}
	}
	d.notebookSync.mu.Unlock()
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
	data, err := os.ReadFile(filepath.Join(d.notebookSyncDir(), "state.json"))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var disk notebookSyncDisk
	if err := json.Unmarshal(data, &disk); err != nil {
		return err
	}
	if disk.Home == home {
		return nil
	}
	if run := d.notebookSyncEngine(); run != nil {
		run.cancel()
		<-run.done
		r.mu.Lock()
		r.run = nil
		d.notebookSyncChangedLocked()
		r.mu.Unlock()
	}
	return os.RemoveAll(d.notebookSyncDir())
}

func (d *Daemon) forgetNotebookSyncPeer(peer string) {
	r := &d.notebookSync
	r.configure.Lock()
	defer r.configure.Unlock()
	r.mu.Lock()
	run := r.run
	if run != nil {
		delete(run.disk.Completed, peer)
		delete(run.remote, peer)
		delete(run.snapshots, peer)
		delete(run.streams, peer)
		if err := d.saveNotebookSyncDisk(run.disk); err != nil {
			d.logf("forget Notebook peer: %v", err)
		}
	}
	r.mu.Unlock()
	if run == nil {
		rows, err := d.store.ListOutposts()
		if err == nil && len(rows) == 0 {
			if err := os.RemoveAll(d.notebookSyncDir()); err != nil {
				d.logf("remove Notebook sync index: %v", err)
			}
		}
	}
}

const notebookArtifactWaitTripwire = 8 * time.Hour

func (d *Daemon) waitForNotebookArtifact(ctx context.Context, origin Origin, receipt *seedArtifactTransferReceipt) error {
	root, err := d.notebookRoot()
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
		state, _ := d.notebookPeerState(row.DaemonID, row.Name)
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

func (d *Daemon) callerSeedArtifactDir(ctx context.Context, seed string) (string, error) {
	if origin, remote := OriginOf(ctx); remote {
		d.outposts.mu.Lock()
		root := ""
		if run := d.outposts.dialers[origin.Host]; run != nil {
			root = run.notebookRoot
		}
		d.outposts.mu.Unlock()
		if root == "" {
			return "", fmt.Errorf("%s's Notebook folder is not known yet; wait for bridge recovery", origin.Name)
		}
		return filepath.Join(root, "seeds", seed), nil
	}
	root, err := d.notebookRoot()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "seeds", seed), nil
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

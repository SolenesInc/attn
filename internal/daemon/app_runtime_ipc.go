package daemon

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"strings"

	"github.com/victorarias/attn/internal/apps"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
	"github.com/victorarias/attn/internal/supervise"
)

// `runtime` is addressed here as itself, not as an app, which is why internal/apps
// refuses it as an app name.

const appLogDefaultLines = 200

// The runtime appends to this log for as long as the daemon lives, so a request
// for all of it has no ceiling.
const appLogMaxLines = 10000

func (d *Daemon) handleAppLogs(conn net.Conn, msg *protocol.AppLogsMessage) {
	name := strings.TrimSpace(msg.Name)
	whole := name == appRuntimeChildName
	if !whole {
		if err := apps.ValidateName(name); err != nil {
			d.sendError(conn, err.Error())
			return
		}
		if d.store == nil {
			d.sendError(conn, "no database")
			return
		}
		if _, ok, err := d.store.GetApp(name); err != nil {
			d.sendError(conn, fmt.Sprintf("reading app %q: %v", name, err))
			return
		} else if !ok {
			d.sendError(conn, d.unknownAppError("logs", name))
			return
		}
	}

	limit := int(protocol.Deref(msg.Lines))
	if limit <= 0 {
		limit = appLogDefaultLines
	}
	if limit > appLogMaxLines {
		d.sendError(conn, fmt.Sprintf(
			"app logs %s: asked for %d lines, and the most this returns in one answer is %d. Read %s directly for more.",
			name, limit, appLogMaxLines, AppRuntimeLogPath(d.socketPath)))
		return
	}

	path := AppRuntimeLogPath(d.socketPath)
	lines, truncated, err := readAppLog(path, name, whole, limit)
	if err != nil {
		d.sendError(conn, fmt.Sprintf("app logs %s: reading %s: %v", name, path, err))
		return
	}
	d.sendDocResponse(conn, protocol.Response{
		Ok: true,
		AppLogsResult: &protocol.AppLogsResult{
			Name: name, Path: path, Lines: lines, Truncated: truncated,
		},
	})
}

func readAppLog(path, app string, whole bool, limit int) ([]string, bool, error) {
	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return []string{}, false, nil
		}
		return nil, false, err
	}
	defer file.Close()

	tag := appRuntimeAppTag(app)
	kept := make([]string, 0, limit)
	truncated := false
	scanner := bufio.NewScanner(file)
	// A handler can print a stack trace or a JSON body; the default 64KB would end
	// the scan on it rather than truncating the line.
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if !whole {
			if !strings.HasPrefix(line, tag) {
				continue
			}
			line = strings.TrimPrefix(line, tag)
		}
		if len(kept) == limit {
			kept = kept[1:]
			truncated = true
		}
		kept = append(kept, line)
	}
	if err := scanner.Err(); err != nil {
		return nil, false, err
	}
	return kept, truncated, nil
}

func (d *Daemon) handleAppRuntimeStatus(conn net.Conn, _ *protocol.AppRuntimeStatusMessage) {
	result := protocol.AppRuntimeStatusResult{LogPath: AppRuntimeLogPath(d.socketPath)}
	if host, err := resolveAppRuntimeHost(); err != nil {
		result.HostError = protocol.Ptr(err.Error())
	} else {
		result.HostPath = protocol.Ptr(host)
	}
	if d.store != nil {
		rows, err := d.store.ListApps()
		if err != nil {
			d.sendError(conn, fmt.Sprintf("listing apps: %v", err))
			return
		}
		result.Apps = len(rows)
		for _, row := range rows {
			consumer, ok, err := d.store.GetBusConsumer(apps.ConsumerName(row.Name))
			if err == nil && ok && consumer.Enabled {
				result.AppsEnabled++
			}
		}
	}
	if snapshot, ok := d.appRuntimeSnapshot(); ok {
		info := d.appRuntimeInfo(snapshot)
		result.Runtime = &info
	}
	d.sendDocResponse(conn, protocol.Response{Ok: true, AppRuntimeStatusResult: &result})
}

func (d *Daemon) handleAppRuntimeRestart(conn net.Conn, _ *protocol.AppRuntimeRestartMessage) {
	was := string(supervise.PhaseStopped)
	if snapshot, ok := d.appRuntimeSnapshot(); ok {
		was = string(snapshot.Phase)
	}
	d.ensureAppRuntimeSupervisor().Stop(appRuntimeChildName)
	if err := d.ensureAppRuntime(); err != nil {
		d.sendError(conn, fmt.Sprintf("app runtime restart: %v", err))
		return
	}
	snapshot, _ := d.appRuntimeSnapshot()
	info := d.appRuntimeInfo(snapshot)
	d.sendDocResponse(conn, protocol.Response{
		Ok: true,
		AppRuntimeRestartResult: &protocol.AppRuntimeRestartResult{
			Was: was, Runtime: info,
		},
	})
}

func (d *Daemon) appRuntimeInfo(snapshot supervise.Snapshot) protocol.AppRuntimeInfo {
	info := protocol.AppRuntimeInfo{
		Phase:          string(snapshot.Phase),
		Desired:        string(snapshot.Desired),
		Running:        snapshot.Running,
		Connected:      snapshot.Connected,
		Generation:     int(snapshot.Generation),
		RestartAttempt: int(snapshot.RestartAttempt),
	}
	if !snapshot.StartedAt.IsZero() {
		info.StartedAt = protocol.Ptr(stampForWire(snapshot.StartedAt))
	}
	if !snapshot.ConnectedAt.IsZero() {
		info.ConnectedAt = protocol.Ptr(stampForWire(snapshot.ConnectedAt))
	}
	if !snapshot.NextRestartAt.IsZero() {
		info.NextRestartAt = protocol.Ptr(stampForWire(snapshot.NextRestartAt))
	}
	if !snapshot.ParkedAt.IsZero() {
		info.ParkedAt = protocol.Ptr(stampForWire(snapshot.ParkedAt))
	}
	if snapshot.LastExit != nil {
		info.LastExit = protocol.Ptr(snapshot.LastExit.String())
	}
	if runtime := d.appRuntimeConnected(); runtime != nil {
		info.Pid = protocol.Ptr(runtime.pid)
	}
	return info
}

// Deliveries are dropped rather than queued when the buffer fills, so a slow reader cannot
// slow the delivery loop; a missed burst reads back with `attn app status`.
type appWatcher struct {
	app    string
	events chan protocol.AppInvocationInfo
}

func (d *Daemon) addAppWatcher(watcher *appWatcher) {
	d.appWatcherMu.Lock()
	defer d.appWatcherMu.Unlock()
	if d.appWatchers == nil {
		d.appWatchers = make(map[*appWatcher]struct{})
	}
	d.appWatchers[watcher] = struct{}{}
}

func (d *Daemon) removeAppWatcher(watcher *appWatcher) {
	d.appWatcherMu.Lock()
	defer d.appWatcherMu.Unlock()
	delete(d.appWatchers, watcher)
}

// Called from the delivery path, so it must never block.
func (d *Daemon) notifyAppWatchers(info protocol.AppInvocationInfo, app string) {
	d.appWatcherMu.Lock()
	watchers := make([]*appWatcher, 0, len(d.appWatchers))
	for watcher := range d.appWatchers {
		if watcher.app == app {
			watchers = append(watchers, watcher)
		}
	}
	d.appWatcherMu.Unlock()
	for _, watcher := range watchers {
		select {
		case watcher.events <- info:
		default:
		}
	}
}

func (d *Daemon) handleAppWatch(conn net.Conn, msg *protocol.AppWatchMessage) {
	name := strings.TrimSpace(msg.Name)
	if err := apps.ValidateName(name); err != nil {
		d.sendError(conn, err.Error())
		return
	}
	watcher := &appWatcher{app: name, events: make(chan protocol.AppInvocationInfo, 64)}
	d.addAppWatcher(watcher)
	defer d.removeAppWatcher(watcher)

	if err := json.NewEncoder(conn).Encode(protocol.Response{Ok: true}); err != nil {
		return
	}

	// The caller sends nothing, so any read that returns means the socket closed.
	gone := make(chan struct{})
	go func() {
		defer close(gone)
		_, _ = conn.Read(make([]byte, 1))
	}()

	encoder := json.NewEncoder(conn)
	for {
		select {
		case <-d.done:
			return
		case <-gone:
			return
		case info := <-watcher.events:
			result := info
			if err := encoder.Encode(protocol.Response{
				Ok:             true,
				AppWatchResult: &protocol.AppWatchResult{Invocation: result},
			}); err != nil {
				return
			}
		}
	}
}

func appInvocationForWire(id int64, inv store.AppInvocation) protocol.AppInvocationInfo {
	info := protocol.AppInvocationInfo{
		ID:        int(id),
		VersionID: int(inv.VersionID),
		Kind:      inv.Kind,
		Handler:   inv.Handler,
		Status:    inv.Status,
		StartedAt: stampForWire(inv.StartedAt),
	}
	if info.Kind == "" {
		info.Kind = store.AppInvocationKindSubscription
	}
	// Fact identity belongs to a subscription: other kinds borrow the event columns,
	// and a reader could not tell a real seq from a placeholder.
	if info.Kind == store.AppInvocationKindSubscription {
		info.EventSeq = protocol.Ptr(int(inv.EventSeq))
		info.EventName = protocol.Ptr(inv.EventName)
		info.EventSubject = protocol.Ptr(inv.EventSubject)
	} else if inv.EventName != "" {
		info.EventName = protocol.Ptr(inv.EventName)
		if inv.EventSubject != "" {
			info.EventSubject = protocol.Ptr(inv.EventSubject)
		}
	}
	if inv.Error != "" {
		info.Error = protocol.Ptr(inv.Error)
	}
	if inv.Status != store.AppInvocationStatusRunning {
		info.DurationMs = protocol.Ptr(int(inv.Duration.Milliseconds()))
	}
	if !inv.FinishedAt.IsZero() {
		info.FinishedAt = protocol.Ptr(stampForWire(inv.FinishedAt))
	}
	if inv.ThroughRequestID > 0 {
		info.ThroughRequestID = protocol.Ptr(int(inv.ThroughRequestID))
	}
	if inv.ReconcileReason != "" {
		var reason appReconcileReason
		if err := json.Unmarshal([]byte(inv.ReconcileReason), &reason); err == nil {
			info.Reconcile = appReconcileReasonForWire(reason)
		}
	}
	return info
}

func appReconcileReasonForWire(reason appReconcileReason) *protocol.AppReconcileReasonInfo {
	info := &protocol.AppReconcileReasonInfo{
		Causes:           append([]string{}, reason.Causes...),
		Version:          int(reason.Version),
		ThroughSeq:       int(reason.ThroughSeq),
		PreviousVersions: []int{},
	}
	for _, version := range reason.PreviousVersions {
		info.PreviousVersions = append(info.PreviousVersions, int(version))
	}
	if reason.Gap != nil {
		info.Gap = &protocol.AppReconcileGapInfo{
			Cursor:   int(reason.Gap.Cursor),
			Earliest: int(reason.Gap.Earliest),
			Missed:   int(reason.Gap.Missed),
		}
	}
	return info
}

func (d *Daemon) appStallForWire(stall appStall) protocol.AppStallInfo {
	info := protocol.AppStallInfo{
		Kind:       stall.kind,
		Since:      stampForWire(stall.since),
		Attempts:   stall.attempts,
		LastError:  stall.lastError,
		DisablesAt: stampForWire(stall.since.Add(d.appAutoDisableWindow())),
	}
	if info.Kind == "" {
		info.Kind = appStallKindSubscription
	}
	if info.Kind == appStallKindReconcile {
		info.ThroughRequestID = protocol.Ptr(int(stall.reconcileRequestID))
		return info
	}
	info.EventSeq = protocol.Ptr(int(stall.seq))
	info.EventName = protocol.Ptr(stall.eventName)
	return info
}

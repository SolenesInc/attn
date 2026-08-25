package daemon

import (
	"fmt"

	"github.com/victorarias/attn/internal/fsdoc"
	"github.com/victorarias/attn/internal/notebook"
	"github.com/victorarias/attn/internal/protocol"
)

// Each watcher is a goroutine plus an OS watch handle (kqueue fd), so this is a resource
// bound, not a UI limit.
const maxFsWatchers = 16

// Never created for the notebook root — that watcher is always-on via ensureNotebookWatcher.
type fsRootWatch struct {
	watcher *notebook.Watcher
	refs    map[*wsClient]int
}

// The root resolves through resolveFsRoot, the single chokepoint gating an explicit root
// on the authenticated app client, so there is no separate check here.
func (d *Daemon) handleFsWatch(client *wsClient, requestID, rawRoot string) {
	root, err := d.resolveFsRoot(client, rawRoot)
	if err == nil && !d.isNotebookRoot(root) {
		err = d.addFsWatchRef(client, root)
	}
	msg := protocol.FsWatchResultMessage{
		Event:     protocol.EventFsWatchResult,
		RequestID: requestID,
		Success:   err == nil,
	}
	if err == nil {
		msg.Root = protocol.Ptr(root)
	} else {
		msg.Error = protocol.Ptr(err.Error())
	}
	d.sendToClient(client, msg)
}

func (d *Daemon) handleFsUnwatch(client *wsClient, requestID, rawRoot string) {
	root, err := d.resolveFsRoot(client, rawRoot)
	if err == nil && !d.isNotebookRoot(root) {
		d.dropFsWatchRef(client, root)
	}
	msg := protocol.FsUnwatchResultMessage{
		Event:     protocol.EventFsUnwatchResult,
		RequestID: requestID,
		Success:   err == nil,
	}
	if err == nil {
		msg.Root = protocol.Ptr(root)
	} else {
		msg.Error = protocol.Ptr(err.Error())
	}
	d.sendToClient(client, msg)
}

func (d *Daemon) addFsWatchRef(client *wsClient, root string) error {
	d.fsWatchMu.Lock()
	defer d.fsWatchMu.Unlock()
	if d.fsWatchers == nil {
		d.fsWatchers = make(map[string]*fsRootWatch)
	}
	entry, ok := d.fsWatchers[root]
	if !ok {
		if len(d.fsWatchers) >= maxFsWatchers {
			return fmt.Errorf("too many watched roots")
		}
		w, err := notebook.NewWatcherWithCleaner(root, notebook.DefaultWatchDebounce, fsdoc.CleanPath, func(paths []string) {
			d.broadcastFsChanged(root, originExternal, paths...)
		})
		if err != nil {
			return err
		}
		entry = &fsRootWatch{watcher: w, refs: make(map[*wsClient]int)}
		d.fsWatchers[root] = entry
	}
	entry.refs[client]++
	return nil
}

func (d *Daemon) dropFsWatchRef(client *wsClient, root string) {
	d.fsWatchMu.Lock()
	entry, ok := d.fsWatchers[root]
	if !ok {
		d.fsWatchMu.Unlock()
		return
	}
	if entry.refs[client] > 1 {
		entry.refs[client]--
		d.fsWatchMu.Unlock()
		return
	}
	delete(entry.refs, client)
	var toClose *notebook.Watcher
	if len(entry.refs) == 0 {
		delete(d.fsWatchers, root)
		toClose = entry.watcher
	}
	d.fsWatchMu.Unlock()
	// Close outside fsWatchMu: it joins the watcher's loop goroutine and can block briefly.
	_ = toClose.Close()
}

func (d *Daemon) dropFsWatchClient(client *wsClient) {
	d.fsWatchMu.Lock()
	var toClose []*notebook.Watcher
	for root, entry := range d.fsWatchers {
		if _, held := entry.refs[client]; !held {
			continue
		}
		delete(entry.refs, client)
		if len(entry.refs) == 0 {
			delete(d.fsWatchers, root)
			toClose = append(toClose, entry.watcher)
		}
	}
	d.fsWatchMu.Unlock()
	for _, w := range toClose {
		_ = w.Close()
	}
}

func (d *Daemon) stopFsWatchers() {
	d.fsWatchMu.Lock()
	watchers := d.fsWatchers
	d.fsWatchers = nil
	d.fsWatchMu.Unlock()
	for _, entry := range watchers {
		_ = entry.watcher.Close()
	}
}

func (d *Daemon) fsWatcherFor(root string) *notebook.Watcher {
	d.fsWatchMu.Lock()
	defer d.fsWatchMu.Unlock()
	entry, ok := d.fsWatchers[root]
	if !ok {
		return nil
	}
	return entry.watcher
}

// The audience restriction that keeps a generic editor root's absolute path from leaking
// to a client that never subscribed to it.
func (d *Daemon) sendFsChangedToWatchers(root string, msg protocol.FsChangedMessage) {
	d.fsWatchMu.Lock()
	entry, ok := d.fsWatchers[root]
	var clients []*wsClient
	if ok {
		clients = make([]*wsClient, 0, len(entry.refs))
		for c := range entry.refs {
			clients = append(clients, c)
		}
	}
	d.fsWatchMu.Unlock()
	for _, c := range clients {
		d.sendToClient(c, msg)
	}
}

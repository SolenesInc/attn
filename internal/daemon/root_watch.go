package daemon

import (
	"fmt"
	"os"

	"github.com/victorarias/attn/internal/fsdoc"
	"github.com/victorarias/attn/internal/notebook"
	"github.com/victorarias/attn/internal/protocol"
)

const maxFsWatchers = 16

type rootWatch struct {
	watcher  *notebook.Watcher
	notebook bool
	clients  map[*wsClient]int
}

func (d *Daemon) handleFsWatch(client *wsClient, requestID string, rawRoot resolvedFsRoot) {
	root := string(rawRoot)
	var err error
	if err == nil {
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

func (d *Daemon) handleFsUnwatch(client *wsClient, requestID string, rawRoot resolvedFsRoot) {
	root := string(rawRoot)
	var err error
	if err == nil {
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
	inNotebook := d.isNotebookRoot(root)
	d.rootWatchMu.Lock()
	defer d.rootWatchMu.Unlock()
	if d.rootWatches == nil {
		d.rootWatches = make(map[string]*rootWatch)
	}
	entry, ok := d.rootWatches[root]
	if !ok {
		count := 0
		for _, entry := range d.rootWatches {
			if len(entry.clients) > 0 && !entry.notebook {
				count++
			}
		}
		if !inNotebook && count >= maxFsWatchers {
			return fmt.Errorf("too many watched roots (maxFsWatchers=%d, asked for %d)", maxFsWatchers, count+1)
		}
		w, err := notebook.NewWatcherWithCleaner(root, notebook.DefaultWatchDebounce, fsdoc.CleanPath, func(paths []string) {
			d.rootChanged(root, paths)
		})
		if err != nil {
			return err
		}
		entry = &rootWatch{watcher: w, notebook: inNotebook, clients: make(map[*wsClient]int)}
		d.rootWatches[root] = entry
	}
	entry.clients[client]++
	return nil
}

func (d *Daemon) dropFsWatchRef(client *wsClient, root string) {
	d.rootWatchMu.Lock()
	entry, ok := d.rootWatches[root]
	if !ok {
		d.rootWatchMu.Unlock()
		return
	}
	if entry.clients[client] > 1 {
		entry.clients[client]--
		d.rootWatchMu.Unlock()
		return
	}
	delete(entry.clients, client)
	var toClose *notebook.Watcher
	if len(entry.clients) == 0 && !entry.notebook {
		delete(d.rootWatches, root)
		toClose = entry.watcher
	}
	d.rootWatchMu.Unlock()
	_ = toClose.Close()
}

func (d *Daemon) dropFsWatchClient(client *wsClient) {
	d.rootWatchMu.Lock()
	var toClose []*notebook.Watcher
	for root, entry := range d.rootWatches {
		if _, held := entry.clients[client]; !held {
			continue
		}
		delete(entry.clients, client)
		if len(entry.clients) == 0 && !entry.notebook {
			delete(d.rootWatches, root)
			toClose = append(toClose, entry.watcher)
		}
	}
	d.rootWatchMu.Unlock()
	for _, w := range toClose {
		_ = w.Close()
	}
}

func (d *Daemon) stopFsWatchers() {
	d.rootWatchMu.Lock()
	watchers := d.rootWatches
	d.rootWatches = nil
	d.rootWatchMu.Unlock()
	for _, entry := range watchers {
		_ = entry.watcher.Close()
	}
}

func (d *Daemon) rootWatcherFor(root string) *notebook.Watcher {
	d.rootWatchMu.Lock()
	defer d.rootWatchMu.Unlock()
	entry, ok := d.rootWatches[root]
	if !ok {
		return nil
	}
	return entry.watcher
}

func (d *Daemon) sendFsChangedToWatchers(root string, msg protocol.FsChangedMessage) {
	d.rootWatchMu.Lock()
	entry, ok := d.rootWatches[root]
	var clients []*wsClient
	if ok {
		clients = make([]*wsClient, 0, len(entry.clients))
		for c := range entry.clients {
			clients = append(clients, c)
		}
	}
	d.rootWatchMu.Unlock()
	selected := make(map[*wsClient]bool)
	for _, c := range clients {
		selected[c] = true
	}
	d.wsHub.ForEachClient(func(c *wsClient) {
		notebookRoot, err := d.notebookRoot(c.selectedProfile())
		if selected[c] || err == nil && notebookRoot == root {
			d.sendToClient(c, msg)
		}
	})
}

func (d *Daemon) ensureNotebookWatcher(root string) {
	select {
	case <-d.life.Done():
		return
	default:
	}
	if info, err := os.Stat(root); err != nil || !info.IsDir() {
		return
	}
	d.rootWatchMu.Lock()
	defer d.rootWatchMu.Unlock()
	if d.rootWatches == nil {
		d.rootWatches = make(map[string]*rootWatch)
	}
	if entry := d.rootWatches[root]; entry != nil {
		entry.notebook = true
		return
	}
	w, err := notebook.NewWatcherWithCleaner(root, notebook.DefaultWatchDebounce, fsdoc.CleanPath, func(paths []string) { d.rootChanged(root, paths) })
	if err != nil {
		d.logf("notebook watcher: failed to watch %s: %v", root, err)
		return
	}
	d.rootWatches[root] = &rootWatch{watcher: w, notebook: true, clients: make(map[*wsClient]int)}
}

func (d *Daemon) rootChanged(root string, paths []string) {
	d.broadcastFsChanged(root, originExternal, paths...)
	if !d.isNotebookRoot(root) {
		return
	}
	var mdPaths []string
	artifactSeeds := map[string]struct{}{}
	for _, p := range paths {
		if _, err := notebook.CleanPath(p); err == nil {
			mdPaths = append(mdPaths, p)
		}
		if seedID, ok := seedArtifactSeedFromNotebookPath(p); ok {
			artifactSeeds[seedID] = struct{}{}
		}
	}
	if len(mdPaths) > 0 {
		d.broadcastNotebookChanged(originExternal, mdPaths...)
	}
	if len(artifactSeeds) > 0 {
		d.coalesceSnapshots(func() {
			for seedID := range artifactSeeds {
				seedRoot, err := d.seedNotebookRoot(seedID)
				if err != nil {
					d.logf("Garden artifact observation for %s: %v", seedID, err)
					continue
				}
				if seedRoot != root {
					continue
				}
				if err := d.recordObservedSeedArtifacts(seedID); err != nil {
					d.logf("Garden artifact observation for %s: %v", seedID, err)
				}
			}
		})
	}
}

func (d *Daemon) noteSelfWrite(root string, writes ...notebook.SelfWrite) {
	watcher := d.rootWatcherFor(root)
	if watcher == nil && d.isNotebookRoot(root) {
		d.ensureNotebookWatcher(root)
		watcher = d.rootWatcherFor(root)
	}
	watcher.NoteSelfWrite(writes...)
}

func (d *Daemon) stopNotebookWatcher() { d.stopFsWatchers() }

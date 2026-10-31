package daemon

import (
	"fmt"
	"os"

	"github.com/victorarias/attn/internal/fsdoc"
	"github.com/victorarias/attn/internal/notebook"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/stepclock"
)

const maxFsWatchers = 16

func (d *Daemon) clientNotebookRoot(client *wsClient, root string) bool {
	own, err := d.notebookRoot(d.profileForClient(client))
	return err == nil && own == root
}

func (d *Daemon) handleFsWatch(client *wsClient, requestID, rawRoot string) {
	root, err := d.resolveFsRoot(client, rawRoot)
	if err == nil && !d.clientNotebookRoot(client, root) {
		err = d.addFsWatchRef(client, root)
		if err == nil {
			if err = d.ensureRootWatcher(root); err != nil {
				d.dropFsWatchRef(client, root)
			}
		}
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
	if err == nil && !d.clientNotebookRoot(client, root) {
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
	if d.fsWatchRefs == nil {
		d.fsWatchRefs = make(map[string]map[*wsClient]int)
	}
	refs, ok := d.fsWatchRefs[root]
	if !ok {
		if len(d.fsWatchRefs) >= maxFsWatchers {
			return fmt.Errorf("too many watched roots")
		}
		refs = make(map[*wsClient]int)
		d.fsWatchRefs[root] = refs
	}
	refs[client]++
	return nil
}

func (d *Daemon) dropFsWatchRef(client *wsClient, root string) {
	d.fsWatchMu.Lock()
	refs, ok := d.fsWatchRefs[root]
	if ok {
		if refs[client] > 1 {
			refs[client]--
		} else {
			delete(refs, client)
		}
		if len(refs) == 0 {
			delete(d.fsWatchRefs, root)
		}
	}
	released := ok && len(refs) == 0
	d.fsWatchMu.Unlock()
	if released {
		d.pruneRootWatchers()
	}
}

func (d *Daemon) dropFsWatchClient(client *wsClient) {
	d.fsWatchMu.Lock()
	released := false
	for root, refs := range d.fsWatchRefs {
		if _, held := refs[client]; !held {
			continue
		}
		delete(refs, client)
		if len(refs) == 0 {
			delete(d.fsWatchRefs, root)
			released = true
		}
	}
	d.fsWatchMu.Unlock()
	if released {
		d.pruneRootWatchers()
	}
}

func (d *Daemon) sendFsChangedToWatchers(root string, msg protocol.FsChangedMessage) {
	d.fsWatchMu.Lock()
	refs := d.fsWatchRefs[root]
	clients := make([]*wsClient, 0, len(refs))
	for c := range refs {
		clients = append(clients, c)
	}
	d.fsWatchMu.Unlock()
	for _, c := range clients {
		d.sendToClient(c, msg)
	}
}

func openRootWatch(clock *stepclock.Clock, root string, changed func([]string)) (*notebook.Watcher, error) {
	return notebook.NewWatcher(clock, root, fsdoc.CleanPath, changed)
}

func (d *Daemon) ensureNotebookWatcher(root string) {
	if info, err := os.Stat(root); err != nil || !info.IsDir() {
		return
	}
	if err := d.ensureRootWatcher(root); err != nil {
		d.logf("notebook watcher: failed to watch %s: %v", root, err)
	}
}

func (d *Daemon) ensureRootWatcher(root string) error {
	select {
	case <-d.life.Done():
		return nil
	default:
	}
	d.rootWatcherMu.Lock()
	defer d.rootWatcherMu.Unlock()
	if d.rootWatchers[root] != nil {
		return nil
	}
	w, err := d.rootWatch(d.clock, root, func(paths []string) { d.rootChanged(root, paths) })
	if err != nil {
		return err
	}
	if d.rootWatchers == nil {
		d.rootWatchers = make(map[string]*notebook.Watcher)
	}
	d.rootWatchers[root] = w
	return nil
}

func (d *Daemon) mutateRoot(root string, mutate func() []notebook.SelfWrite) {
	d.rootWatcherMu.Lock()
	w := d.rootWatchers[root]
	d.rootWatcherMu.Unlock()
	w.Mutate(mutate)
}

func (d *Daemon) pruneRootWatchers() {
	kept := d.notebookRoots()
	d.rootWatcherMu.Lock()
	d.fsWatchMu.Lock()
	for root := range d.fsWatchRefs {
		kept[root] = struct{}{}
	}
	d.fsWatchMu.Unlock()
	var stale []*notebook.Watcher
	for root, w := range d.rootWatchers {
		if _, used := kept[root]; !used {
			delete(d.rootWatchers, root)
			stale = append(stale, w)
		}
	}
	d.rootWatcherMu.Unlock()
	for _, w := range stale {
		_ = w.Close()
	}
}

func (d *Daemon) stopRootWatchers() {
	d.rootWatcherMu.Lock()
	watchers := d.rootWatchers
	d.rootWatchers = nil
	d.rootWatcherMu.Unlock()
	for _, w := range watchers {
		_ = w.Close()
	}
}

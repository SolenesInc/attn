//go:build !darwin

package notebook

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/fsnotify/fsnotify"
)

type notifyBackend struct {
	watcher *fsnotify.Watcher
	changes chan treeEvent
	done    chan struct{}
	stopped chan struct{}
}

func newTreeBackend(root string) (treeBackend, error) {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	b := &notifyBackend{watcher: watcher, changes: make(chan treeEvent), done: make(chan struct{}), stopped: make(chan struct{})}
	if err := b.addTree(root); err != nil {
		_ = watcher.Close()
		return nil, err
	}
	go b.loop()
	return b, nil
}

func (b *notifyBackend) events() <-chan treeEvent { return b.changes }

func (b *notifyBackend) Close() error {
	close(b.done)
	err := b.watcher.Close()
	<-b.stopped
	return err
}

func (b *notifyBackend) addTree(root string) error {
	return filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if entry.IsDir() {
			if path != root && strings.HasPrefix(entry.Name(), ".") {
				return fs.SkipDir
			}
			_ = b.watcher.Add(path)
		}
		return nil
	})
}

func (b *notifyBackend) loop() {
	defer close(b.stopped)
	defer close(b.changes)
	for {
		select {
		case <-b.done:
			return
		case ev, ok := <-b.watcher.Events:
			if !ok {
				return
			}
			change := treeEvent{Path: ev.Name}
			if ev.Op&fsnotify.Create != 0 {
				if info, err := os.Stat(ev.Name); err == nil && info.IsDir() {
					if strings.HasPrefix(filepath.Base(ev.Name), ".") {
						continue
					}
					_ = b.addTree(ev.Name)
					change.IsDir = true
				}
			}
			select {
			case b.changes <- change:
			case <-b.done:
				return
			}
		case _, ok := <-b.watcher.Errors:
			if !ok {
				return
			}
		}
	}
}

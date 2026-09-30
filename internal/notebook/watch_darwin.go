//go:build darwin

package notebook

import (
	"path/filepath"
	"sync"
	"time"

	"github.com/fsnotify/fsevents"
)

type eventsBackend struct {
	root       string
	canonical  string
	stream     *fsevents.EventStream
	changes    chan treeEvent
	done       chan struct{}
	streamDone chan struct{}
	stopped    chan struct{}
	closeOnce  sync.Once
}

func newTreeBackend(root string) (treeBackend, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	canonical, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return nil, err
	}
	b := &eventsBackend{
		root: root, canonical: canonical,
		changes: make(chan treeEvent), done: make(chan struct{}),
		streamDone: make(chan struct{}), stopped: make(chan struct{}),
		stream: &fsevents.EventStream{
			Paths:   []string{canonical},
			Flags:   fsevents.FileEvents | fsevents.NoDefer | fsevents.WatchRoot,
			Latency: 50 * time.Millisecond,
			Events:  make(chan []fsevents.Event, 1),
		},
	}
	if err := b.stream.Start(); err != nil {
		return nil, err
	}
	go b.loop()
	return b, nil
}

func (b *eventsBackend) events() <-chan treeEvent { return b.changes }

func (b *eventsBackend) Close() error {
	b.closeOnce.Do(func() {
		close(b.done)
		b.stream.Stop()
		close(b.streamDone)
	})
	<-b.stopped
	return nil
}

func (b *eventsBackend) loop() {
	defer close(b.stopped)
	defer close(b.changes)
	for {
		select {
		case <-b.streamDone:
			// Stop prevents new callbacks; leave room for the serial queue's last send.
			// The library owns Events, so it must stay open.
			for {
				select {
				case <-b.stream.Events:
				default:
					return
				}
			}
		case batch := <-b.stream.Events:
			for _, ev := range batch {
				if ev.Flags&fsevents.RootChanged != 0 {
					go b.Close()
					break
				}
				if ev.Flags&fsevents.HistoryDone != 0 {
					continue
				}
				rel, err := filepath.Rel(b.canonical, ev.Path)
				if err != nil {
					continue
				}
				change := treeEvent{
					Path:   filepath.Join(b.root, rel),
					IsDir:  ev.Flags&fsevents.ItemIsDir != 0,
					Rescan: ev.Flags&(fsevents.MustScanSubDirs|fsevents.UserDropped|fsevents.KernelDropped) != 0,
				}
				if change.IsDir && !change.Rescan && ev.Flags&(fsevents.ItemCreated|fsevents.ItemRenamed|fsevents.ItemRemoved) == 0 {
					continue
				}
				select {
				case b.changes <- change:
				case <-b.done:
				}
			}
		}
	}
}

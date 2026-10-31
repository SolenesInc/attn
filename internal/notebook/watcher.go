package notebook

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/victorarias/attn/internal/stepclock"
)

const WatchDebounce = 400 * time.Millisecond

const selfWriteTTL = 3 * time.Second

type treeEvent struct {
	Path   string
	IsDir  bool
	Rescan bool
}

type treeBackend interface {
	events() <-chan treeEvent
	Close() error
}

type Watcher struct {
	root      string
	clock     *stepclock.Clock
	cleanPath func(string) (string, error)
	onChange  func(paths []string)

	backend treeBackend

	mu         sync.Mutex
	selfWrites map[string]selfWriteRecord
	closeOnce  sync.Once
	loopDone   chan struct{}
}

type selfWriteRecord struct {
	expiry time.Time
	hash   string
}

type SelfWrite struct {
	Rel  string
	Hash string
}

func NewWatcher(clock *stepclock.Clock, root string, cleanPath func(string) (string, error), onChange func(paths []string)) (*Watcher, error) {
	clean := filepath.Clean(root)
	if info, err := os.Stat(clean); err != nil {
		return nil, fmt.Errorf("notebook watcher: %w", err)
	} else if !info.IsDir() {
		return nil, fmt.Errorf("notebook watcher: %s is not a directory", clean)
	}
	backend, err := newTreeBackend(clean)
	if err != nil {
		return nil, err
	}
	w := &Watcher{
		root:       clean,
		clock:      clock,
		cleanPath:  cleanPath,
		onChange:   onChange,
		backend:    backend,
		selfWrites: make(map[string]selfWriteRecord),
		loopDone:   make(chan struct{}),
	}
	go w.loop()
	return w, nil
}

func (w *Watcher) Mutate(mutate func() []SelfWrite) {
	if w == nil {
		mutate()
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	writes := mutate()
	exp := w.clock.Now().Add(selfWriteTTL)
	for _, sw := range writes {
		clean, err := w.cleanPath(sw.Rel)
		if err != nil {
			continue
		}
		w.selfWrites[clean] = selfWriteRecord{expiry: exp, hash: sw.Hash}
	}
}

func (w *Watcher) Close() error {
	if w == nil {
		return nil
	}
	var err error
	w.closeOnce.Do(func() {
		err = w.backend.Close()
	})
	<-w.loopDone
	return err
}

func (w *Watcher) loop() {
	defer close(w.loopDone)
	pending := make(map[string]struct{})
	var debounce stepclock.Timer
	var debounced <-chan time.Time
	defer func() {
		if debounce != nil {
			debounce.Stop()
		}
	}()
	for {
		select {
		case ev, ok := <-w.backend.events():
			if !ok {
				return
			}
			w.handleEvent(ev, pending)
			if len(pending) > 0 && debounced == nil {
				debounce = w.clock.NewTimer(WatchDebounce)
				debounced = debounce.C()
			}
		case <-debounced:
			debounced = nil
			w.flush(pending)
		}
	}
}

func (w *Watcher) handleEvent(ev treeEvent, pending map[string]struct{}) {
	if !w.visible(ev.Path) {
		return
	}
	if ev.IsDir || ev.Rescan {
		files, _ := w.scanTree(ev.Path)
		for _, rel := range files {
			pending[rel] = struct{}{}
		}
		// A rescan still invalidates the root after its last file disappears.
		if ev.Rescan && filepath.Clean(ev.Path) == w.root {
			pending["."] = struct{}{}
		}
		if rel, ok := w.trackable(ev.Path); ok {
			pending[rel] = struct{}{}
		}
		return
	}
	if rel, ok := w.trackable(ev.Path); ok {
		pending[rel] = struct{}{}
	}
}

func (w *Watcher) flush(pending map[string]struct{}) {
	if len(pending) == 0 {
		return
	}
	rels := make([]string, 0, len(pending))
	for rel := range pending {
		rels = append(rels, rel)
		delete(pending, rel)
	}
	rels = w.dropSelfWrites(rels)
	if len(rels) == 0 {
		return
	}
	sort.Strings(rels)
	w.onChange(rels)
}

func (w *Watcher) dropSelfWrites(rels []string) []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	now := w.clock.Now()
	for k, rec := range w.selfWrites {
		if now.After(rec.expiry) {
			delete(w.selfWrites, k)
		}
	}
	out := make([]string, 0, len(rels))
	for _, rel := range rels {
		if rec, ok := w.selfWrites[rel]; ok && w.diskHash(rel) == rec.hash {
			continue
		}
		out = append(out, rel)
	}
	return out
}

func (w *Watcher) diskHash(rel string) string {
	content, err := os.ReadFile(filepath.Join(w.root, filepath.FromSlash(rel)))
	if err != nil {
		return ""
	}
	return Hash(content)
}

func (w *Watcher) scanTree(dir string) ([]string, error) {
	var files []string
	err := filepath.WalkDir(dir, func(p string, dirent fs.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if dirent.IsDir() {
			if p != w.root && strings.HasPrefix(dirent.Name(), ".") {
				return fs.SkipDir
			}
			return nil
		}
		if rel, ok := w.trackable(p); ok {
			files = append(files, rel)
		}
		return nil
	})
	return files, err
}

func (w *Watcher) visible(absPath string) bool {
	rel, err := filepath.Rel(w.root, absPath)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return false
	}
	if rel == "." {
		return true
	}
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		if strings.HasPrefix(part, ".") {
			return false
		}
	}
	return true
}

func (w *Watcher) trackable(absPath string) (string, bool) {
	if !w.visible(absPath) {
		return "", false
	}
	rel, err := filepath.Rel(w.root, absPath)
	if err != nil {
		return "", false
	}
	clean, err := w.cleanPath(filepath.ToSlash(rel))
	if err != nil {
		return "", false
	}
	return clean, true
}

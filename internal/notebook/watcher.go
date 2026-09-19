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

	"github.com/fsnotify/fsnotify"
)

const DefaultWatchDebounce = 400 * time.Millisecond

const selfWriteTTL = 3 * time.Second

type Watcher struct {
	root      string
	debounce  time.Duration
	cleanPath func(string) (string, error)
	onChange  func(paths []string)

	fsw *fsnotify.Watcher

	mu         sync.Mutex
	selfWrites map[string]selfWriteRecord
	closeOnce  sync.Once
	loopDone   chan struct{}
	now        func() time.Time
}

type selfWriteRecord struct {
	expiry time.Time
	hash   string
}

type SelfWrite struct {
	Rel  string
	Hash string
}

func NewWatcher(root string, debounce time.Duration, onChange func(paths []string)) (*Watcher, error) {
	return NewWatcherWithCleaner(root, debounce, CleanPath, onChange)
}

func NewWatcherWithCleaner(root string, debounce time.Duration, cleanPath func(string) (string, error), onChange func(paths []string)) (*Watcher, error) {
	clean := filepath.Clean(root)
	if info, err := os.Stat(clean); err != nil {
		return nil, fmt.Errorf("notebook watcher: %w", err)
	} else if !info.IsDir() {
		return nil, fmt.Errorf("notebook watcher: %s is not a directory", clean)
	}
	fsw, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	w := &Watcher{
		root:       clean,
		debounce:   debounce,
		cleanPath:  cleanPath,
		onChange:   onChange,
		fsw:        fsw,
		selfWrites: make(map[string]selfWriteRecord),
		loopDone:   make(chan struct{}),
		now:        time.Now,
	}
	if _, err := w.addTree(w.root); err != nil {
		_ = fsw.Close()
		return nil, err
	}
	go w.loop()
	return w, nil
}

func (w *Watcher) NoteSelfWrite(writes ...SelfWrite) {
	if w == nil || len(writes) == 0 {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	exp := w.now().Add(selfWriteTTL)
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
		err = w.fsw.Close()
	})
	<-w.loopDone
	return err
}

func (w *Watcher) loop() {
	defer close(w.loopDone)
	pending := make(map[string]struct{})
	var timerC <-chan time.Time
	for {
		select {
		case ev, ok := <-w.fsw.Events:
			if !ok {
				return
			}
			w.handleEvent(ev, pending)
			if len(pending) > 0 && timerC == nil {
				timerC = time.After(w.debounce)
			}
		case <-timerC:
			timerC = nil
			w.flush(pending)
		case _, ok := <-w.fsw.Errors:
			if !ok {
				return
			}
		}
	}
}

func (w *Watcher) handleEvent(ev fsnotify.Event, pending map[string]struct{}) {
	if ev.Op&fsnotify.Create != 0 {
		if info, err := os.Stat(ev.Name); err == nil && info.IsDir() {
			if base := filepath.Base(ev.Name); base == "." || strings.HasPrefix(base, ".") {
				return
			}
			files, _ := w.addTree(ev.Name)
			for _, rel := range files {
				pending[rel] = struct{}{}
			}
			return
		}
	}
	if rel, ok := w.trackable(ev.Name); ok {
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
	now := w.now()
	for k, rec := range w.selfWrites {
		if now.After(rec.expiry) {
			delete(w.selfWrites, k)
		}
	}
	out := make([]string, 0, len(rels))
	type recheck struct{ rel, hash string }
	var pending []recheck
	for _, rel := range rels {
		if rec, ok := w.selfWrites[rel]; ok && !now.After(rec.expiry) {
			delete(w.selfWrites, rel)
			if rec.hash == "" {
				continue
			}
			pending = append(pending, recheck{rel: rel, hash: rec.hash})
			continue
		}
		out = append(out, rel)
	}
	w.mu.Unlock()
	for _, rc := range pending {
		if w.diskHash(rc.rel) != rc.hash {
			out = append(out, rc.rel)
		}
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

func (w *Watcher) addTree(dir string) ([]string, error) {
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
			_ = w.fsw.Add(p)
			return nil
		}
		if rel, ok := w.trackable(p); ok {
			files = append(files, rel)
		}
		return nil
	})
	return files, err
}

func (w *Watcher) trackable(absPath string) (string, bool) {
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

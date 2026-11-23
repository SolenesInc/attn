package main

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/fsnotify/fsnotify"
)

func (e *editor) newWatcher() (*fsnotify.Watcher, error) {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	paths := []string{e.root.Name()}
	_ = fs.WalkDir(e.root.FS(), "content", func(name string, entry fs.DirEntry, err error) error {
		if err == nil && entry.IsDir() {
			paths = append(paths, filepath.Join(e.root.Name(), name))
		}
		return err
	})
	for _, dir := range []string{"scenarios", "situations"} {
		if _, err := e.root.Stat(dir); err == nil {
			paths = append(paths, filepath.Join(e.root.Name(), dir))
		}
	}
	for _, path := range paths {
		if err := watcher.Add(path); err != nil {
			watcher.Close()
			return nil, err
		}
	}
	return watcher, nil
}
func watchEvent(watcher *fsnotify.Watcher, event fsnotify.Event) (bool, error) {
	if event.Op&fsnotify.Create != 0 {
		if info, err := os.Stat(event.Name); err == nil && info.IsDir() {
			err = filepath.WalkDir(event.Name, func(path string, entry fs.DirEntry, err error) error {
				if err != nil {
					return err
				}
				if entry.IsDir() {
					return watcher.Add(path)
				}
				return nil
			})
			return true, err
		}
	}
	return relevantEvent(event), nil
}
func relevantEvent(event fsnotify.Event) bool {
	name := filepath.Base(event.Name)
	return !strings.HasPrefix(name, ".") && (strings.HasSuffix(name, ".json") || strings.HasSuffix(name, ".md") || strings.HasSuffix(name, ".go")) && event.Op&(fsnotify.Write|fsnotify.Create|fsnotify.Remove|fsnotify.Rename) != 0
}
func (e *editor) events(w http.ResponseWriter, r *http.Request) {
	watcher, err := e.newWatcher()
	if err != nil {
		problem(w, 500, err)
		return
	}
	defer watcher.Close()
	flusher, ok := w.(http.Flusher)
	if !ok {
		problem(w, 500, fmt.Errorf("streaming unavailable"))
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	fmt.Fprint(w, "event: ready\ndata: {}\n\n")
	flusher.Flush()
	for {
		select {
		case <-r.Context().Done():
			return
		case err, ok := <-watcher.Errors:
			if !ok {
				return
			}
			data, _ := json.Marshal(map[string]string{"error": err.Error()})
			fmt.Fprintf(w, "event: error\ndata: %s\n\n", data)
			flusher.Flush()
			return
		case event, ok := <-watcher.Events:
			if !ok {
				return
			}
			relevant, err := watchEvent(watcher, event)
			if err != nil {
				return
			}
			if !relevant {
				continue
			}
			name, err := filepath.Rel(e.root.Name(), event.Name)
			if err != nil {
				continue
			}
			data, _ := json.Marshal(map[string]string{"path": filepath.ToSlash(name)})
			if _, err := fmt.Fprintf(w, "data: %s\n\n", data); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

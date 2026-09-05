package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fsnotify/fsnotify"
)

func (e *editor) newWatcher() (*fsnotify.Watcher, error) {
	state, err := e.stateRoot()
	if err != nil {
		return nil, err
	}
	state.Close()
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	paths := []string{filepath.Join(e.repo, ".prompt-editor/drafts"), filepath.Join(e.repo, ".prompt-editor/reviews"), e.root.Name()}
	_ = fs.WalkDir(e.root.FS(), "content", func(name string, entry fs.DirEntry, err error) error {
		if err == nil && entry.IsDir() {
			paths = append(paths, filepath.Join(e.root.Name(), name))
		}
		return err
	})
	if _, err := e.root.Stat("scenarios"); err == nil {
		paths = append(paths, filepath.Join(e.root.Name(), "scenarios"))
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
			data, _ := json.Marshal(map[string]string{"path": event.Name})
			if _, err := fmt.Fprintf(w, "data: %s\n\n", data); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}
func (e *editor) watch(ctx context.Context, q operationRequest, after int64, timeout time.Duration) (any, error) {
	if q.DraftID == "" && q.ReviewID == "" {
		return nil, fmt.Errorf("watch needs --draft or --review")
	}
	watcher, err := e.newWatcher()
	if err != nil {
		return nil, err
	}
	defer watcher.Close()
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	read := func() (any, int64, error) {
		var result any
		var revision int64
		_, err := e.withState(func(root *os.Root) (any, error) {
			if q.ReviewID != "" {
				r, err := readReview(root, q.ReviewID)
				if err != nil {
					return nil, err
				}
				result = r
				revision = int64(len(r.Feedback))
			} else {
				d, err := readDraft(root, q.DraftID)
				if err != nil {
					return nil, err
				}
				result = d
				revision = d.Revision
			}
			return nil, nil
		})
		return result, revision, err
	}
	for {
		result, revision, err := read()
		if err != nil {
			return nil, err
		}
		if revision > after {
			return result, nil
		}
		select {
		case <-ctx.Done():
			return map[string]any{"changed": false, "revision": revision}, nil
		case err, ok := <-watcher.Errors:
			if !ok {
				return nil, fmt.Errorf("watch closed")
			}
			return nil, err
		case event, ok := <-watcher.Events:
			if !ok {
				return nil, fmt.Errorf("watch closed")
			}
			if _, err := watchEvent(watcher, event); err != nil {
				return nil, err
			}
		}
	}
}

package main

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/victorarias/attn/internal/prompts"
)

var validName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,79}$`)

type operationError struct {
	Code    string `json:"code"`
	Message string `json:"error"`
	Path    string `json:"path,omitempty"`
	Current any    `json:"current,omitempty"`
}

func (e *operationError) Error() string { return e.Message }
func conflict(message string, current any) error {
	return &operationError{Code: "revision_conflict", Message: message, Current: current}
}

func decodeJSON(data []byte, value any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return fmt.Errorf("expected one JSON value")
	}
	return nil
}

type draftFile struct {
	Text         string `json:"text"`
	BaseRevision string `json:"base_revision"`
	Revision     string `json:"revision"`
	Author       string `json:"author"`
}

type focus struct {
	Event      string         `json:"event"`
	Path       string         `json:"path,omitempty"`
	Scenario   string         `json:"scenario,omitempty"`
	Values     prompts.Values `json:"values"`
	BaseCommit string         `json:"base_commit,omitempty"`
}

type sharedDraft struct {
	Version      int                  `json:"version"`
	ID           string               `json:"id"`
	Title        string               `json:"title"`
	Revision     int64                `json:"revision"`
	Manifest     json.RawMessage      `json:"manifest"`
	Files        map[string]draftFile `json:"files"`
	Focus        focus                `json:"focus"`
	Archived     bool                 `json:"archived"`
	Author       string               `json:"author"`
	UpdatedAt    string               `json:"updated_at"`
	LatestReview string               `json:"latest_review,omitempty"`
}

type feedback struct {
	ID             string `json:"id"`
	Author         string `json:"author"`
	Message        string `json:"message"`
	Target         string `json:"target"`
	Selection      string `json:"selection,omitempty"`
	Path           string `json:"path,omitempty"`
	SourceRevision string `json:"source_revision,omitempty"`
	CreatedAt      string `json:"created_at"`
}

type review struct {
	Version       int             `json:"version"`
	ID            string          `json:"id"`
	Title         string          `json:"title"`
	DraftID       string          `json:"draft_id"`
	DraftRevision int64           `json:"draft_revision"`
	Author        string          `json:"author"`
	CreatedAt     string          `json:"created_at"`
	Focus         focus           `json:"focus"`
	Snapshot      catalogSnapshot `json:"snapshot"`
	Feedback      []feedback      `json:"feedback"`
}

func now() string                { return time.Now().UTC().Format(time.RFC3339Nano) }
func newID(prefix string) string { return prefix + strings.ToLower(rand.Text()[:12]) }
func stateName(kind, id string) (string, error) {
	if !validName.MatchString(id) {
		return "", fmt.Errorf("invalid %s id", kind)
	}
	return kind + "/" + id + ".json", nil
}

func atomicWrite(root *os.Root, name string, data []byte, mode fs.FileMode) error {
	temp := filepath.ToSlash(filepath.Join(filepath.Dir(name), ".write-"+rand.Text()))
	file, err := root.OpenFile(temp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	defer root.Remove(temp)
	_, err = file.Write(data)
	if err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return root.Rename(temp, name)
}

func writeJSON(root *os.Root, name string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return atomicWrite(root, name, append(data, '\n'), 0600)
}

func readJSON(root *os.Root, name string, value any) error {
	data, err := root.ReadFile(name)
	if err != nil {
		return err
	}
	return decodeJSON(data, value)
}

func (e *editor) stateRoot() (*os.Root, error) {
	repo, err := os.OpenRoot(e.repo)
	if err != nil {
		return nil, err
	}
	defer repo.Close()
	if err := repo.MkdirAll(".prompt-editor/drafts", 0700); err != nil {
		return nil, err
	}
	if err := repo.MkdirAll(".prompt-editor/reviews", 0700); err != nil {
		return nil, err
	}
	return repo.OpenRoot(".prompt-editor")
}

func (e *editor) withState(fn func(*os.Root) (any, error)) (any, error) {
	root, err := e.stateRoot()
	if err != nil {
		return nil, err
	}
	defer root.Close()
	lock, err := root.OpenFile("lock", os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		return nil, err
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	if err := e.recoverApply(root); err != nil {
		return nil, err
	}
	return fn(root)
}

func readDraft(root *os.Root, id string) (sharedDraft, error) {
	name, err := stateName("drafts", id)
	if err != nil {
		return sharedDraft{}, err
	}
	var draft sharedDraft
	if err := readJSON(root, name, &draft); err != nil {
		return draft, err
	}
	if draft.Version != 1 || draft.ID != id {
		return draft, fmt.Errorf("invalid draft %s", id)
	}
	return draft, nil
}
func saveDraft(root *os.Root, draft *sharedDraft, author string) error {
	draft.Revision++
	draft.Author = author
	draft.UpdatedAt = now()
	return writeJSON(root, "drafts/"+draft.ID+".json", draft)
}
func readReview(root *os.Root, id string) (review, error) {
	name, err := stateName("reviews", id)
	if err != nil {
		return review{}, err
	}
	var r review
	if err := readJSON(root, name, &r); err != nil {
		return r, err
	}
	if r.Version != 1 || r.ID != id {
		return r, fmt.Errorf("invalid review %s", id)
	}
	return r, nil
}

func draftTexts(d sharedDraft) map[string]string {
	result := map[string]string{}
	for path, file := range d.Files {
		result[path] = file.Text
	}
	return result
}
func (e *editor) draftSnapshot(d sharedDraft) (catalogSnapshot, error) {
	snapshot, err := e.snapshot()
	if err != nil {
		return snapshot, err
	}
	if !sameDefinitions(d.Manifest, snapshot.Manifest) {
		return snapshot, conflict("composition changed; sync this draft before continuing", nil)
	}
	snapshot.EditedSources = []string{}
	for name, file := range d.Files {
		if _, ok := snapshot.Sources[name]; !ok {
			return snapshot, fmt.Errorf("unregistered source: %s", name)
		}
		snapshot.Sources[name] = source{file.Text, file.Revision}
		snapshot.EditedSources = append(snapshot.EditedSources, name)
	}
	sort.Strings(snapshot.EditedSources)
	return snapshot, nil
}

type fileWrite struct {
	Before string      `json:"before"`
	After  string      `json:"after"`
	Mode   fs.FileMode `json:"mode"`
}

type applyTransaction struct {
	Files map[string]fileWrite `json:"files"`
	Draft sharedDraft          `json:"draft"`
}

// A journal makes interrupted multi-file applies recoverable before another client writes.
func (e *editor) recoverApply(root *os.Root) error {
	var tx applyTransaction
	err := readJSON(root, "apply.json", &tx)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	complete := true
	for name, file := range tx.Files {
		data, err := e.root.ReadFile(name)
		if err != nil {
			return err
		}
		if string(data) != file.After {
			complete = false
		}
		if string(data) != file.Before && string(data) != file.After {
			return conflict("an interrupted apply conflicts with an external edit; preserve that edit and restore one of the recorded file versions before retrying", name)
		}
	}
	if complete {
		if err := writeJSON(root, "drafts/"+tx.Draft.ID+".json", tx.Draft); err != nil {
			return err
		}
	} else {
		for name, file := range tx.Files {
			if err := atomicWrite(e.root, name, []byte(file.Before), file.Mode); err != nil {
				return err
			}
		}
	}
	return root.Remove("apply.json")
}

func (e *editor) applyDraft(root *os.Root, d sharedDraft, expected int64, author string) (any, error) {
	if d.Revision != expected {
		return nil, conflict("draft changed; inspect it before applying", d)
	}
	snapshot, err := e.snapshot()
	if err != nil {
		return nil, err
	}
	if err := e.freshness(snapshot); err != nil {
		return nil, err
	}
	if !sameDefinitions(d.Manifest, snapshot.Manifest) {
		return nil, conflict("composition changed; sync and inspect this draft before applying", nil)
	}
	tx := applyTransaction{Files: map[string]fileWrite{}}
	for name, file := range d.Files {
		current, ok := snapshot.Sources[name]
		if !ok {
			return nil, fmt.Errorf("unregistered source: %s", name)
		}
		if current.Revision != file.BaseRevision {
			return nil, conflict("file changed on disk; sync this draft before applying", name)
		}
		if err := e.regularSource(name); err != nil {
			return nil, err
		}
		info, err := e.root.Stat(name)
		if err != nil {
			return nil, err
		}
		tx.Files[name] = fileWrite{current.Text, file.Text, info.Mode().Perm()}
	}
	if _, err := snapshot.load(draftTexts(d)); err != nil {
		return nil, err
	}
	d.Files = map[string]draftFile{}
	d.Revision++
	d.Author = author
	d.UpdatedAt = now()
	tx.Draft = d
	if err := writeJSON(root, "apply.json", tx); err != nil {
		return nil, err
	}
	for name, file := range tx.Files {
		if err := atomicWrite(e.root, name, []byte(file.After), file.Mode); err != nil {
			return nil, fmt.Errorf("apply interrupted; retry to recover: %w", err)
		}
	}
	if err := e.recoverApply(root); err != nil {
		return nil, err
	}
	return d, nil
}

func listState(root *os.Root, kind string) (any, error) {
	names, err := fs.Glob(root.FS(), kind+"/*.json")
	if err != nil {
		return nil, err
	}
	items := []map[string]any{}
	for _, name := range names {
		var item map[string]any
		if err := readJSON(root, name, &item); err != nil {
			return nil, err
		}
		for _, field := range []string{"manifest", "snapshot", "files", "feedback"} {
			delete(item, field)
		}
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool { return fmt.Sprint(items[i]["id"]) < fmt.Sprint(items[j]["id"]) })
	return items, nil
}

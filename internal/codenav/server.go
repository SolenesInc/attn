package codenav

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/victorarias/attn/internal/procreap"
)

// Server states, as the bundle's navigation and `attn nav status` show them.
const (
	StateStarting = "starting"
	StateReady    = "ready"
	StateMissing  = "missing"
	StateFailed   = "failed"
)

// stderrTailBytes keeps the end of a server's stderr for the reason it failed.
const stderrTailBytes = 4 << 10

// Key names one language server: a language in a workspace folder.
type Key struct {
	Folder   string
	Language string
}

func (k Key) String() string { return k.Language + " in " + k.Folder }

// Location is one place a server answered with.
type Location struct {
	Path      string
	Line      int // 1-based
	Character int // UTF-16 code units, 0-based
	EndLine   int
	EndChar   int
}

type server struct {
	key      Key
	def      Definition
	program  string
	worktree func(ctx context.Context, folder, since string) (Worktree, error)
	now      func() time.Time
	cmd      *exec.Cmd
	conn     *conn
	reaper   string
	exited   chan struct{}
	ready    chan struct{}
	stderr   *tail

	mu       sync.Mutex
	state    string
	detail   string
	reloaded time.Time
	progress map[string]string
	opened   map[string]openFile
	changed  func()

	diskMu sync.Mutex
	head   string
	dirty  map[string]stamp
}

type openFile struct {
	version int
	sum     [sha256.Size]byte
}

// Worktree is what a workspace folder holds against its commit: the files that
// differ from it, and the files that differ between an earlier commit and it.
type Worktree struct {
	Head  string
	Dirty []string
	Moved []string
}

type stamp struct {
	exists  bool
	size    int64
	modTime int64
}

func stampOf(path string) stamp {
	info, err := os.Stat(path)
	if err != nil {
		return stamp{}
	}
	return stamp{exists: true, size: info.Size(), modTime: info.ModTime().UnixNano()}
}

type tail struct {
	mu  sync.Mutex
	buf []byte
}

func (t *tail) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, p...)
	if len(t.buf) > stderrTailBytes {
		t.buf = t.buf[len(t.buf)-stderrTailBytes:]
	}
	return len(p), nil
}

func (t *tail) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return strings.TrimSpace(string(t.buf))
}

func fileURI(path string) string {
	return (&url.URL{Scheme: "file", Path: filepath.ToSlash(path)}).String()
}

func uriPath(uri string) string {
	parsed, err := url.Parse(uri)
	if err != nil || parsed.Scheme != "file" {
		return ""
	}
	return filepath.FromSlash(parsed.Path)
}

// startServer launches the program and begins the LSP handshake; the answer
// comes later, through the state.
func startServer(key Key, def Definition, program, reapDir string, worktree func(context.Context, string, string) (Worktree, error), now func() time.Time, changed func()) (*server, error) {
	cmd := exec.Command(program, def.Command[1:]...)
	cmd.Dir = key.Folder
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	s := &server{
		key: key, def: def, program: program, cmd: cmd, worktree: worktree, now: now,
		exited: make(chan struct{}), ready: make(chan struct{}),
		stderr: &tail{}, state: StateStarting, progress: map[string]string{}, opened: map[string]openFile{}, changed: changed,
	}
	cmd.Stderr = s.stderr
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start %s: %w", program, err)
	}
	s.reaper = filepath.Join(reapDir, fmt.Sprintf("%d.json", cmd.Process.Pid))
	_ = procreap.WriteEntry(s.reaper, procreap.NewEntry(key.String(), cmd.Process.Pid, cmd.Process.Pid, append([]string{program}, def.Command[1:]...)))
	s.conn = &conn{w: stdin}
	go func() {
		err := s.conn.serve(stdout, s.answer, s.heard)
		waitErr := cmd.Wait()
		_ = procreap.RemoveEntry(s.reaper)
		reason := s.stderr.String()
		if reason == "" {
			reason = fmt.Sprintf("exited: %v", firstError(waitErr, err))
		}
		s.set(StateFailed, reason)
		close(s.exited)
	}()
	go s.initialize()
	return s, nil
}

func firstError(errs ...error) error {
	for _, err := range errs {
		if err != nil {
			return err
		}
	}
	return errors.New("no reason given")
}

func (s *server) initialize() {
	folder := s.key.Folder
	s.diskMu.Lock()
	_, err := s.diskChanges(context.Background())
	s.diskMu.Unlock()
	if err != nil {
		s.abandon(fmt.Sprintf("read what changed in %s: %v", folder, err))
		return
	}
	params := map[string]any{
		"processId": os.Getpid(),
		"rootUri":   fileURI(folder),
		"workspaceFolders": []map[string]string{
			{"uri": fileURI(folder), "name": filepath.Base(folder)},
		},
		"capabilities": map[string]any{
			"window": map[string]any{"workDoneProgress": true},
			"textDocument": map[string]any{
				"definition":      map[string]any{},
				"references":      map[string]any{},
				"hover":           map[string]any{"contentFormat": []string{"plaintext", "markdown"}},
				"synchronization": map[string]any{"didSave": false},
			},
			"workspace": map[string]any{"configuration": true, "workspaceFolders": true},
		},
		"initializationOptions": s.def.Settings,
	}
	if err := s.conn.call(context.Background(), "initialize", params, nil); err != nil {
		s.abandon(fmt.Sprintf("initialize: %v", err))
		return
	}
	if err := s.conn.notify("initialized", map[string]any{}); err != nil {
		s.abandon(fmt.Sprintf("initialized: %v", err))
		return
	}
	close(s.ready)
	s.mu.Lock()
	s.settleLocked()
	s.mu.Unlock()
	s.changed()
}

func (s *server) answer(method string, params json.RawMessage) any {
	switch method {
	case "workspace/configuration":
		var request struct {
			Items []json.RawMessage `json:"items"`
		}
		_ = json.Unmarshal(params, &request)
		settings := make([]json.RawMessage, len(request.Items))
		for i := range settings {
			settings[i] = s.def.Settings
			if settings[i] == nil {
				settings[i] = json.RawMessage("null")
			}
		}
		return settings
	case "workspace/workspaceFolders":
		return []map[string]string{{"uri": fileURI(s.key.Folder), "name": filepath.Base(s.key.Folder)}}
	}
	return nil
}

func (s *server) heard(method string, params json.RawMessage) {
	if method != "$/progress" {
		return
	}
	var progress struct {
		Token json.RawMessage `json:"token"`
		Value struct {
			Kind  string `json:"kind"`
			Title string `json:"title"`
		} `json:"value"`
	}
	if json.Unmarshal(params, &progress) != nil {
		return
	}
	token := string(progress.Token)
	s.mu.Lock()
	before := s.state + s.detail
	switch progress.Value.Kind {
	case "begin":
		s.progress[token] = progress.Value.Title
	case "end":
		delete(s.progress, token)
	}
	s.settleLocked()
	moved := before != s.state+s.detail
	s.mu.Unlock()
	if moved {
		s.changed()
	}
}

// settleLocked derives the state from the handshake and the work the server
// reports: it is starting while it loads, and ready once nothing is in progress.
func (s *server) settleLocked() {
	if s.state == StateFailed {
		return
	}
	select {
	case <-s.ready:
	default:
		return
	}
	if len(s.progress) == 0 {
		s.state, s.detail = StateReady, ""
		return
	}
	titles := make([]string, 0, len(s.progress))
	for _, title := range s.progress {
		titles = append(titles, title)
	}
	sort.Strings(titles)
	if s.state == StateReady {
		s.reloaded = s.now()
	}
	s.state, s.detail = StateStarting, strings.Join(titles, "; ")
}

func (s *server) set(state, detail string) {
	s.mu.Lock()
	if s.state == StateFailed {
		s.mu.Unlock()
		return
	}
	s.state, s.detail = state, detail
	s.mu.Unlock()
	s.changed()
}

func (s *server) abandon(reason string) {
	s.set(StateFailed, reason)
	_ = syscall.Kill(-s.cmd.Process.Pid, syscall.SIGKILL)
}

func (s *server) status() (string, string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state, s.detail
}

func (s *server) reloadedAt() time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.reloaded
}

func (s *server) stop() {
	select {
	case <-s.ready:
		ctx, cancel := context.WithTimeout(context.Background(), shutdownGrace)
		_ = s.conn.call(ctx, "shutdown", nil, nil)
		cancel()
		_ = s.conn.notify("exit", nil)
	default:
	}
	select {
	case <-s.exited:
		return
	case <-afterGrace():
	}
	_ = syscall.Kill(-s.cmd.Process.Pid, syscall.SIGKILL)
	<-s.exited
}

// awaitReady waits for the handshake: a request made while the server loads
// is held until it can answer (F8).
func (s *server) awaitReady(ctx context.Context) error {
	select {
	case <-s.ready:
		return nil
	case <-s.exited:
		_, detail := s.status()
		return fmt.Errorf("%s failed: %s", s.program, detail)
	case <-ctx.Done():
		return ctx.Err()
	}
}

// open tells the server the file's text as it is on disk now.
func (s *server) open(path string) error {
	text, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(text)
	s.mu.Lock()
	known, wasOpen := s.opened[path]
	if wasOpen && known.sum == sum {
		s.mu.Unlock()
		return nil
	}
	version := known.version + 1
	s.opened[path] = openFile{version: version, sum: sum}
	s.mu.Unlock()
	uri := fileURI(path)
	if !wasOpen {
		return s.conn.notify("textDocument/didOpen", map[string]any{"textDocument": map[string]any{
			"uri": uri, "languageId": s.def.languageID(s.key.Language, path), "version": version, "text": string(text),
		}})
	}
	return s.conn.notify("textDocument/didChange", map[string]any{
		"textDocument":   map[string]any{"uri": uri, "version": version},
		"contentChanges": []map[string]string{{"text": string(text)}},
	})
}

// catchUp tells the server about each file that changed on disk since it
// last looked, because a server rereads only what it is told changed: an open
// document gets its new text, any other file a change event.
func (s *server) catchUp(ctx context.Context) error {
	s.diskMu.Lock()
	defer s.diskMu.Unlock()
	changed, err := s.diskChanges(ctx)
	if err != nil {
		return fmt.Errorf("read what changed in %s: %w", s.key.Folder, err)
	}
	events := []map[string]any{}
	for _, path := range changed {
		s.mu.Lock()
		_, open := s.opened[path]
		s.mu.Unlock()
		if open {
			err := s.open(path)
			if err == nil {
				continue
			}
			if !errors.Is(err, os.ErrNotExist) {
				return err
			}
			s.mu.Lock()
			delete(s.opened, path)
			s.mu.Unlock()
			if err := s.conn.notify("textDocument/didClose", map[string]any{"textDocument": map[string]string{"uri": fileURI(path)}}); err != nil {
				return err
			}
		}
		kind := 2
		if !stampOf(path).exists {
			kind = 3
		}
		events = append(events, map[string]any{"uri": fileURI(path), "type": kind})
	}
	if len(events) == 0 {
		return nil
	}
	return s.conn.notify("workspace/didChangeWatchedFiles", map[string]any{"changes": events})
}

// diskChanges reads the folder's commit and dirty files and names the files
// that changed since the last read: those the commit moved, and dirty files
// that are new, edited again, or no longer dirty.
func (s *server) diskChanges(ctx context.Context) ([]string, error) {
	now, err := s.worktree(ctx, s.key.Folder, s.head)
	if err != nil {
		return nil, err
	}
	dirty := make(map[string]stamp, len(now.Dirty))
	changed := slices.Clone(now.Moved)
	for _, path := range now.Dirty {
		dirty[path] = stampOf(path)
		if before, ok := s.dirty[path]; !ok || before != dirty[path] {
			changed = append(changed, path)
		}
	}
	for path := range s.dirty {
		if _, ok := dirty[path]; !ok {
			changed = append(changed, path)
		}
	}
	s.head, s.dirty = now.Head, dirty
	slices.Sort(changed)
	return slices.Compact(changed), nil
}

type lspPosition struct {
	Line      int `json:"line"`
	Character int `json:"character"`
}

type lspLocation struct {
	URI       string `json:"uri"`
	TargetURI string `json:"targetUri"`
	Range     struct {
		Start lspPosition `json:"start"`
		End   lspPosition `json:"end"`
	} `json:"range"`
	TargetSelectionRange *struct {
		Start lspPosition `json:"start"`
		End   lspPosition `json:"end"`
	} `json:"targetSelectionRange"`
}

func (l lspLocation) location() (Location, bool) {
	uri, start, end := l.URI, l.Range.Start, l.Range.End
	if l.TargetURI != "" {
		uri = l.TargetURI
		if l.TargetSelectionRange != nil {
			start, end = l.TargetSelectionRange.Start, l.TargetSelectionRange.End
		}
	}
	path := uriPath(uri)
	if path == "" {
		return Location{}, false
	}
	return Location{Path: path, Line: start.Line + 1, Character: start.Character, EndLine: end.Line + 1, EndChar: end.Character}, true
}

func (s *server) position(path string, line, character int) map[string]any {
	return map[string]any{
		"textDocument": map[string]string{"uri": fileURI(path)},
		"position":     lspPosition{Line: line - 1, Character: character},
	}
}

func (s *server) locations(ctx context.Context, method string, params map[string]any) ([]Location, error) {
	var raw json.RawMessage
	if err := s.conn.call(ctx, method, params, &raw); err != nil {
		return nil, err
	}
	var many []lspLocation
	if err := json.Unmarshal(raw, &many); err != nil {
		var one lspLocation
		if json.Unmarshal(raw, &one) != nil {
			return nil, nil
		}
		many = []lspLocation{one}
	}
	locations := make([]Location, 0, len(many))
	for _, l := range many {
		if location, ok := l.location(); ok {
			locations = append(locations, location)
		}
	}
	return locations, nil
}

func (s *server) hover(ctx context.Context, path string, line, character int) (string, error) {
	var answer struct {
		Contents json.RawMessage `json:"contents"`
	}
	if err := s.conn.call(ctx, "textDocument/hover", s.position(path, line, character), &answer); err != nil {
		return "", err
	}
	return hoverText(answer.Contents), nil
}

// hoverText flattens the three shapes LSP allows for hover contents.
func hoverText(raw json.RawMessage) string {
	var markup struct {
		Value string `json:"value"`
	}
	if json.Unmarshal(raw, &markup) == nil && markup.Value != "" {
		return strings.TrimSpace(markup.Value)
	}
	var plain string
	if json.Unmarshal(raw, &plain) == nil {
		return strings.TrimSpace(plain)
	}
	var parts []json.RawMessage
	if json.Unmarshal(raw, &parts) == nil {
		texts := make([]string, 0, len(parts))
		for _, part := range parts {
			if text := hoverText(part); text != "" {
				texts = append(texts, text)
			}
		}
		return strings.Join(texts, "\n\n")
	}
	return ""
}

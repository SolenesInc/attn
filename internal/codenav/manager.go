package codenav

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/victorarias/attn/internal/procreap"
	"github.com/victorarias/attn/internal/stepclock"
)

// shutdownGrace is how long a server has to exit after shutdown before it is
// killed. gopls exits on attn 12 to 19 ms after it is asked.
const shutdownGrace = 5 * time.Second

func afterGrace() <-chan time.Time { return time.After(shutdownGrace) }

// Config is what a Manager needs from the daemon that owns it.
type Config struct {
	// BinDir holds the programs `attn nav link` linked; it is searched before PATH.
	BinDir string
	// ReapDir holds a procreap record for each running server.
	ReapDir string
	// Definitions are the definitions in force for a repository.
	Definitions func(repository string) Definitions
	// Inputs lists the files under folder that a prepare's globs match.
	Inputs func(ctx context.Context, folder string, globs []string) ([]string, error)
	// IdleStop arms the timer that stops a server no document needs.
	IdleStop func(fn func()) stepclock.Timer
	// Changed reports that a server's state changed, for the documents that need it.
	Changed func(key Key, documents []string)
	Logf    func(format string, args ...any)
}

// Manager keeps the language servers of one machine: started on need, never
// restarted on their own, stopped once idle.
type Manager struct {
	cfg     Config
	ctx     context.Context
	cancel  context.CancelFunc
	mu      sync.Mutex
	entries map[Key]*entry
	prepped map[Key]string
}

type entry struct {
	key     Key
	def     Definition
	server  *server
	state   string
	detail  string
	needers map[string]bool
	idle    stepclock.Timer
	// launched closes once a start has a server, or has failed or found the program missing.
	launched chan struct{}
}

func NewManager(cfg Config) *Manager {
	ctx, cancel := context.WithCancel(context.Background())
	return &Manager{cfg: cfg, ctx: ctx, cancel: cancel, entries: map[Key]*entry{}, prepped: map[Key]string{}}
}

// Reap ends servers a daemon that did not stop cleanly left behind.
func Reap(clock *stepclock.Clock, reapDir string) []procreap.ReapResult {
	results := procreap.ReapDir(clock, reapDir, shutdownGrace)
	for _, result := range results {
		if result.Outcome == procreap.ReapSurvived || result.Path == "" {
			continue
		}
		_ = procreap.RemoveEntry(result.Path)
	}
	return results
}

// Need marks the servers for these files as needed by document and starts each
// that is not running. It returns the languages the files belong to.
func (m *Manager) Need(document, repository string, files []string) []Key {
	defs := m.cfg.Definitions(repository)
	seen := map[Key]bool{}
	var keys []Key
	for _, file := range files {
		language := defs.LanguageOf(file)
		if language == "" {
			continue
		}
		def := defs[language]
		key := Key{Folder: def.workspaceFolder(repository, file), Language: language}
		if seen[key] {
			continue
		}
		seen[key] = true
		keys = append(keys, key)
		m.ensure(key, def, document, false)
	}
	return keys
}

// Release drops document's need of every server, arming the idle stop of each
// server nothing else needs.
func (m *Manager) Release(document string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, e := range m.entries {
		if e.needers[document] {
			delete(e.needers, document)
			m.armIdleLocked(e)
		}
	}
}

// ensure returns the key's entry with a server running or on its way, starting
// one when none is, or when the last one failed and restart is set.
func (m *Manager) ensure(key Key, def Definition, document string, restart bool) *entry {
	m.mu.Lock()
	defer m.mu.Unlock()
	e := m.entries[key]
	if e == nil {
		e = &entry{key: key, def: def, needers: map[string]bool{}}
		m.entries[key] = e
	}
	if document != "" && !e.needers[document] {
		e.needers[document] = true
		if e.idle != nil {
			e.idle.Stop()
			e.idle = nil
		}
	}
	switch {
	case e.server != nil:
		if state, _ := e.server.status(); state == StateFailed && restart {
			m.startLocked(e, def)
		}
	case e.state == StateStarting:
	case e.state == StateFailed && !restart:
	default:
		m.startLocked(e, def)
	}
	m.armIdleLocked(e)
	return e
}

func (m *Manager) startLocked(e *entry, def Definition) {
	e.def = def
	e.launched = make(chan struct{})
	program, err := Locate(m.cfg.BinDir, e.def.Command[0])
	if err != nil {
		e.server, e.state, e.detail = nil, StateMissing, err.Error()
		close(e.launched)
		return
	}
	e.server, e.state, e.detail = nil, StateStarting, ""
	if e.def.Prepare == nil {
		m.launchLocked(e, program)
		return
	}
	e.detail = "preparing: " + e.def.Prepare.Command[0]
	go m.prepareThenLaunch(e, program)
}

func (m *Manager) prepareThenLaunch(e *entry, program string) {
	err := m.prepare(e.key, e.def)
	m.mu.Lock()
	if m.entries[e.key] != e {
		e.state, e.detail = StateFailed, "the server stopped before it started: no shown review needs it"
		close(e.launched)
		m.mu.Unlock()
		return
	}
	if err != nil {
		e.state, e.detail = StateFailed, err.Error()
		close(e.launched)
	} else {
		m.launchLocked(e, program)
	}
	needers := e.neededBy()
	m.mu.Unlock()
	m.cfg.Changed(e.key, needers)
}

func (m *Manager) launchLocked(e *entry, program string) {
	var s *server
	s, err := startServer(e.key, e.def, program, m.cfg.ReapDir, func() {
		m.mu.Lock()
		current := m.entries[e.key] == e && e.server == s
		needers := e.neededBy()
		m.mu.Unlock()
		if current {
			m.cfg.Changed(e.key, needers)
		}
	})
	defer close(e.launched)
	if err != nil {
		e.state, e.detail = StateFailed, err.Error()
		return
	}
	e.server, e.state, e.detail = s, "", ""
}

// prepare runs the definition's prepare command when its inputs changed since
// it last ran, or it never ran in this daemon.
func (m *Manager) prepare(key Key, def Definition) error {
	ctx := m.ctx
	files, err := m.cfg.Inputs(ctx, key.Folder, def.Prepare.Inputs)
	if err != nil {
		return fmt.Errorf("list the prepare inputs %v: %w", def.Prepare.Inputs, err)
	}
	signature := inputsSignature(key.Folder, files)
	m.mu.Lock()
	done := m.prepped[key] == signature
	m.mu.Unlock()
	if done {
		return nil
	}
	cmd := exec.CommandContext(ctx, def.Prepare.Command[0], def.Prepare.Command[1:]...)
	cmd.Dir = key.Folder
	output := &tail{}
	cmd.Stdout, cmd.Stderr = output, output
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("prepare %v: %v: %s", def.Prepare.Command, err, output.String())
	}
	m.mu.Lock()
	m.prepped[key] = signature
	m.mu.Unlock()
	return nil
}

func inputsSignature(folder string, files []string) string {
	sort.Strings(files)
	sum := sha256.New()
	for _, file := range files {
		info, err := os.Stat(filepath.Join(folder, file))
		if err != nil {
			fmt.Fprintf(sum, "%s gone\n", file)
			continue
		}
		fmt.Fprintf(sum, "%s %d %d\n", file, info.Size(), info.ModTime().UnixNano())
	}
	return hex.EncodeToString(sum.Sum(nil))
}

func (e *entry) neededBy() []string {
	documents := make([]string, 0, len(e.needers))
	for document := range e.needers {
		documents = append(documents, document)
	}
	sort.Strings(documents)
	return documents
}

func (m *Manager) armIdleLocked(e *entry) {
	if len(e.needers) > 0 || e.idle != nil {
		return
	}
	var timer stepclock.Timer
	timer = m.cfg.IdleStop(func() {
		m.mu.Lock()
		if m.entries[e.key] != e || e.idle != timer {
			m.mu.Unlock()
			return
		}
		delete(m.entries, e.key)
		s := e.server
		m.mu.Unlock()
		if s != nil {
			m.cfg.Logf("code navigation: stopping %s, which no shown review needs", e.key)
			s.stop()
		}
	})
	e.idle = timer
}

// State is what the reader shows for a server: its state and the detail.
func (m *Manager) State(key Key) (string, string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e := m.entries[key]
	if e == nil {
		return StateStarting, ""
	}
	return e.status()
}

func (e *entry) status() (string, string) {
	if e.server != nil {
		return e.server.status()
	}
	return e.state, e.detail
}

// Kinds of question a reader asks about a name.
const (
	KindDefinition = "definition"
	KindReferences = "references"
	KindHover      = "hover"
)

// Answer is a server's answer to one question.
type Answer struct {
	Language  string
	Program   string
	Locations []Location
	Text      string
}

// Ask asks the server for path's language about the name at line and
// character, starting the server when it is not running. document, when set,
// needs the server from then on.
func (m *Manager) Ask(ctx context.Context, document, repository, path string, line, character int, kind string) (Answer, error) {
	defs := m.cfg.Definitions(repository)
	language := defs.LanguageOf(path)
	if language == "" {
		return Answer{}, NoLanguage{Path: path}
	}
	def := defs[language]
	key := Key{Folder: def.workspaceFolder(repository, path), Language: language}
	e := m.ensure(key, def, document, true)
	answer := Answer{Language: language, Program: def.Command[0]}
	s, err := m.awaitServer(ctx, e)
	if err != nil {
		return answer, err
	}
	if err := s.awaitReady(ctx); err != nil {
		return answer, err
	}
	if err := s.open(path); err != nil {
		return answer, err
	}
	switch kind {
	case KindDefinition:
		answer.Locations, err = s.locations(ctx, "textDocument/definition", s.position(path, line, character))
	case KindReferences:
		params := s.position(path, line, character)
		params["context"] = map[string]bool{"includeDeclaration": false}
		answer.Locations, err = s.locations(ctx, "textDocument/references", params)
	case KindHover:
		answer.Text, err = s.hover(ctx, path, line, character)
	default:
		err = fmt.Errorf("kind %q is not a question; ask definition, references or hover", kind)
	}
	return answer, err
}

// awaitServer waits through a prepare for the server to be launched.
func (m *Manager) awaitServer(ctx context.Context, e *entry) (*server, error) {
	m.mu.Lock()
	launched := e.launched
	m.mu.Unlock()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-launched:
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if e.server == nil {
		return nil, errors.New(e.detail)
	}
	return e.server, nil
}

// NoLanguage is a file no server definition serves.
type NoLanguage struct{ Path string }

func (n NoLanguage) Error() string {
	return fmt.Sprintf("no language server serves %s: no built-in or set definition names the extension %q. Add one with `attn nav set`", n.Path, filepath.Ext(n.Path))
}

// Running is one server, as `attn nav status` lists it.
type Running struct {
	Key     Key
	Program string
	PID     int
	State   string
	Detail  string
	Needers []string
}

func (m *Manager) Running() []Running {
	m.mu.Lock()
	defer m.mu.Unlock()
	var running []Running
	for _, e := range m.entries {
		r := Running{Key: e.key, Program: e.def.Command[0], Needers: e.neededBy()}
		r.State, r.Detail = e.status()
		if e.server != nil {
			r.Program, r.PID = e.server.program, e.server.cmd.Process.Pid
		}
		running = append(running, r)
	}
	sort.Slice(running, func(i, j int) bool { return running[i].Key.String() < running[j].Key.String() })
	return running
}

// Stop stops every server, for a daemon that stops.
func (m *Manager) Stop() {
	m.cancel()
	m.mu.Lock()
	entries := m.entries
	m.entries = map[Key]*entry{}
	m.mu.Unlock()
	var wg sync.WaitGroup
	for _, e := range entries {
		if e.idle != nil {
			e.idle.Stop()
		}
		if s := e.server; s != nil {
			wg.Go(s.stop)
		}
	}
	wg.Wait()
}

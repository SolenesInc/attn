package testworld

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/victorarias/attn/internal/pty"
	"github.com/victorarias/attn/internal/ptybackend"
)

const (
	terminalPasteStart = "\x1b[200~"
	terminalPasteEnd   = "\x1b[201~"
	terminalComposer   = "❯ "
)

type Terminals struct {
	mu        sync.Mutex
	terminals map[string]*Terminal
	onExit    func(ptybackend.ExitInfo)
	onState   func(string, pty.Observation)
}

type TerminalInput struct {
	Data string
	At   time.Time
}

type Terminal struct {
	owner     *Terminals
	Options   ptybackend.SpawnOptions
	running   bool
	line      []rune
	pasting   bool
	pasteAt   int
	pasted    []string
	submitted []string
	inputs    []TerminalInput
	screen    []string
	streams   []chan ptybackend.OutputEvent
	onSubmit  func(string)
	stall     *terminalStall
}

type terminalStall struct {
	alive bool
	err   error
}

func NewTerminals() *Terminals {
	return &Terminals{terminals: map[string]*Terminal{}}
}

func (b *Terminals) Terminal(sessionID string) *Terminal {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.terminals[sessionID]
}

func (b *Terminals) Spawn(_ context.Context, opts ptybackend.SpawnOptions) error {
	b.mu.Lock()
	if existing := b.terminals[opts.ID]; existing != nil && existing.running {
		b.mu.Unlock()
		return fmt.Errorf("session %s already running", opts.ID)
	}
	term := &Terminal{owner: b, Options: opts, running: true, screen: []string{terminalComposer}}
	b.terminals[opts.ID] = term
	b.mu.Unlock()
	return nil
}

func (b *Terminals) lookup(sessionID string) (*Terminal, error) {
	term := b.terminals[sessionID]
	if term == nil {
		return nil, fmt.Errorf("%w: %s", pty.ErrSessionNotFound, sessionID)
	}
	return term, nil
}

func (b *Terminals) Attach(_ context.Context, sessionID, _ string, _ ...ptybackend.AttachOptions) (ptybackend.AttachInfo, ptybackend.Stream, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	term, err := b.lookup(sessionID)
	if err != nil {
		return ptybackend.AttachInfo{}, nil, err
	}
	events := make(chan ptybackend.OutputEvent, 256)
	term.streams = append(term.streams, events)
	return ptybackend.AttachInfo{Cols: term.Options.Cols, Rows: term.Options.Rows, Running: term.running},
		&terminalStream{owner: b, term: term, events: events}, nil
}

func (b *Terminals) Input(_ context.Context, sessionID string, data []byte) error {
	b.mu.Lock()
	term, err := b.lookup(sessionID)
	if err != nil || !term.running {
		b.mu.Unlock()
		if err == nil {
			err = fmt.Errorf("session %s has exited", sessionID)
		}
		return err
	}
	term.inputs = append(term.inputs, TerminalInput{Data: string(data), At: time.Now()})
	submitted := term.consume(string(data))
	notify := term.onSubmit
	b.mu.Unlock()
	if notify != nil {
		for _, prompt := range submitted {
			go notify(prompt)
		}
	}
	return nil
}

func (t *Terminal) consume(input string) []string {
	var submitted []string
	for len(input) > 0 {
		switch {
		case strings.HasPrefix(input, terminalPasteStart):
			t.pasting = true
			t.pasteAt = len(t.line)
			input = input[len(terminalPasteStart):]
		case strings.HasPrefix(input, terminalPasteEnd):
			t.pasting = false
			t.pasted = append(t.pasted, string(t.line[t.pasteAt:]))
			input = input[len(terminalPasteEnd):]
		case input[0] == '\r' || input[0] == '\n':
			if t.pasting {
				t.line = append(t.line, '\n')
			} else if prompt := string(t.line); strings.TrimSpace(prompt) != "" {
				t.line = nil
				t.submitted = append(t.submitted, prompt)
				submitted = append(submitted, prompt)
				t.paintLocked(terminalComposer + prompt)
			}
			input = input[1:]
		default:
			r := []rune(input)[0]
			t.line = append(t.line, r)
			t.emitLocked(string(r))
			input = input[len(string(r)):]
		}
	}
	return submitted
}

func (b *Terminals) Resize(_ context.Context, sessionID string, cols, rows, _, _ uint16) (ptybackend.ResizeResult, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	term, err := b.lookup(sessionID)
	if err != nil {
		return ptybackend.ResizeResult{}, err
	}
	changed := term.Options.Cols != cols || term.Options.Rows != rows
	term.Options.Cols, term.Options.Rows = cols, rows
	return ptybackend.ResizeResult{Changed: changed}, nil
}

func (b *Terminals) SetTheme(context.Context, string, pty.TerminalTheme) error { return nil }

func (b *Terminals) Kill(_ context.Context, sessionID string, sig syscall.Signal) error {
	b.mu.Lock()
	term, err := b.lookup(sessionID)
	b.mu.Unlock()
	if err != nil {
		return err
	}
	term.Exit(128 + int(sig))
	return nil
}

func (b *Terminals) Remove(_ context.Context, sessionID string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	term := b.terminals[sessionID]
	if term == nil {
		return nil
	}
	for _, events := range term.streams {
		close(events)
	}
	term.streams = nil
	term.running = false
	delete(b.terminals, sessionID)
	return nil
}

func (b *Terminals) SessionIDs(context.Context) []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	ids := make([]string, 0, len(b.terminals))
	for id, term := range b.terminals {
		if term.running && term.stall == nil {
			ids = append(ids, id)
		}
	}
	return ids
}

func (b *Terminals) Recover(ctx context.Context) (ptybackend.RecoveryReport, error) {
	b.mu.Lock()
	var report ptybackend.RecoveryReport
	for _, term := range b.terminals {
		if term.stall != nil {
			report.Missing++
		}
	}
	b.mu.Unlock()
	if report.Missing > 0 {
		<-ctx.Done()
	}
	return report, nil
}

func (b *Terminals) SessionLikelyAlive(_ context.Context, sessionID string) (bool, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if term := b.terminals[sessionID]; term != nil && term.stall != nil {
		return term.stall.alive, term.stall.err
	}
	return false, nil
}

func (b *Terminals) Shutdown(context.Context) error { return nil }

func (b *Terminals) SetExitHandler(fn func(ptybackend.ExitInfo)) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.onExit = fn
}

func (b *Terminals) SetStateHandler(fn func(string, pty.Observation)) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.onState = fn
}

func (b *Terminals) SessionInfo(_ context.Context, sessionID string) (ptybackend.SessionInfo, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	term, err := b.lookup(sessionID)
	if err != nil {
		return ptybackend.SessionInfo{}, err
	}
	return ptybackend.SessionInfo{
		SessionID: sessionID, Agent: term.Options.Agent, CWD: term.Options.CWD,
		Running: term.running, Cols: term.Options.Cols, Rows: term.Options.Rows,
	}, nil
}

func (b *Terminals) ScreenSnapshot(_ context.Context, sessionID string) (pty.ScreenSnapshotInfo, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	term, err := b.lookup(sessionID)
	if err != nil {
		return pty.ScreenSnapshotInfo{}, err
	}
	text := strings.Join(term.screen, "\n") + string(term.line)
	return pty.ScreenSnapshotInfo{
		Cols: term.Options.Cols, Rows: term.Options.Rows, Running: term.running,
		Screen: &pty.ViewportSnapshot{Text: text, HasText: true, Cols: term.Options.Cols, Rows: term.Options.Rows},
	}, nil
}

func (t *Terminal) OnSubmit(fn func(prompt string)) {
	t.owner.mu.Lock()
	defer t.owner.mu.Unlock()
	t.onSubmit = fn
}

func (t *Terminal) Stall(alive bool, probeErr error) (answer func()) {
	t.owner.mu.Lock()
	defer t.owner.mu.Unlock()
	t.stall = &terminalStall{alive: alive, err: probeErr}
	return func() {
		t.owner.mu.Lock()
		defer t.owner.mu.Unlock()
		t.stall = nil
	}
}

func (t *Terminal) Submitted() []string {
	t.owner.mu.Lock()
	defer t.owner.mu.Unlock()
	return append([]string(nil), t.submitted...)
}

func (t *Terminal) Inputs() []TerminalInput {
	t.owner.mu.Lock()
	defer t.owner.mu.Unlock()
	return append([]TerminalInput(nil), t.inputs...)
}

func (t *Terminal) Pasted() []string {
	t.owner.mu.Lock()
	defer t.owner.mu.Unlock()
	return append([]string(nil), t.pasted...)
}

func (t *Terminal) paintLocked(text string) {
	t.screen = append(t.screen, text, terminalComposer)
	t.emitLocked("\r\n" + text + "\r\n" + terminalComposer)
}

func (t *Terminal) emitLocked(text string) {
	for _, events := range t.streams {
		select {
		case events <- ptybackend.OutputEvent{Kind: ptybackend.OutputEventKindOutput, Data: []byte(text)}:
		default:
		}
	}
}

func (t *Terminal) Heartbeat(claim, detail string) {
	t.owner.mu.Lock()
	report := t.owner.onState
	t.owner.mu.Unlock()
	if report != nil {
		report(t.Options.ID, pty.Observation{Source: pty.SourceHeartbeat, Claim: claim, Detail: detail, At: time.Now()})
	}
}

func (t *Terminal) Exit(code int) {
	t.owner.mu.Lock()
	if !t.running {
		t.owner.mu.Unlock()
		return
	}
	t.running = false
	report := t.owner.onExit
	info := ptybackend.ExitInfo{ID: t.Options.ID, ExitCode: code, LifecycleID: t.Options.LifecycleID}
	for _, events := range t.streams {
		select {
		case events <- ptybackend.OutputEvent{Kind: ptybackend.OutputEventKindExit}:
		default:
		}
	}
	t.owner.mu.Unlock()
	if report != nil {
		report(info)
	}
}

type terminalStream struct {
	owner  *Terminals
	term   *Terminal
	events chan ptybackend.OutputEvent
	once   sync.Once
}

func (s *terminalStream) Events() <-chan ptybackend.OutputEvent { return s.events }

func (s *terminalStream) Close() error {
	s.once.Do(func() {
		s.owner.mu.Lock()
		defer s.owner.mu.Unlock()
		for i, events := range s.term.streams {
			if events == s.events {
				s.term.streams = append(s.term.streams[:i], s.term.streams[i+1:]...)
				close(events)
				return
			}
		}
	})
	return nil
}

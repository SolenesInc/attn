package testworld

import (
	"context"
	"sync"
	"syscall"
	"time"

	"github.com/victorarias/attn/internal/buildinfo"
	"github.com/victorarias/attn/internal/harness"
	"github.com/victorarias/attn/internal/pty"
	"github.com/victorarias/attn/internal/ptybackend"
)

type InProcessPTY struct {
	manager *pty.Manager
}

func (b *InProcessPTY) SessionTerminalBuild(harness.TerminalID) (string, bool) {
	return buildinfo.SnapshotFormat, true
}

func NewInProcessPTY(manager *pty.Manager) *InProcessPTY {
	if manager == nil {
		manager = pty.NewManager(nil)
	}
	return &InProcessPTY{manager: manager}
}

func (b *InProcessPTY) SetExitHandler(handler func(ptybackend.ExitInfo)) {
	if handler == nil {
		b.manager.SetExitHandler(nil)
		return
	}
	b.manager.SetExitHandler(func(info pty.ExitInfo) {
		handler(ptybackend.ExitInfo{ID: harness.TerminalID(info.ID), ExitCode: info.ExitCode, Signal: info.Signal, LifecycleID: info.LifecycleID})
	})
}

func (b *InProcessPTY) SetStateHandler(handler func(id harness.TerminalID, obs pty.Observation)) {
	if handler == nil {
		b.manager.SetStateHandler(nil)
		return
	}
	b.manager.SetStateHandler(func(id string, obs pty.Observation) { handler(harness.TerminalID(id), obs) })
}

func (b *InProcessPTY) SetTitleHandler(handler func(id harness.TerminalID, title string)) {
	if handler == nil {
		b.manager.SetTitleHandler(nil)
		return
	}
	b.manager.SetTitleHandler(func(id, title string) { handler(harness.TerminalID(id), title) })
}

func (b *InProcessPTY) Spawn(_ context.Context, opts ptybackend.SpawnOptions) error {
	if err := opts.Validate(); err != nil {
		return err
	}
	return b.manager.Spawn(opts.PTY())
}

func (b *InProcessPTY) Attach(_ context.Context, id harness.TerminalID, subscriberID string, opts ...ptybackend.AttachOptions) (ptybackend.AttachInfo, ptybackend.Stream, error) {
	sessionID := string(id)
	events := make(chan ptybackend.OutputEvent, 128)
	stream := &inProcessStream{
		events: events,
		closeFn: func() {
			b.manager.Detach(sessionID, subscriberID)
		},
	}

	send := func(data []byte, seq uint32) bool {
		payload := append([]byte(nil), data...)
		return stream.publish(ptybackend.OutputEvent{Kind: ptybackend.OutputEventKindOutput, Data: payload, Seq: seq})
	}
	onDrop := func(reason string) {
		_ = stream.publish(ptybackend.OutputEvent{Kind: ptybackend.OutputEventKindDesync, Reason: reason})
		stream.Close()
	}
	onPlacements := pty.OnPlacements(func(update pty.PlacementUpdate) {
		if !stream.publish(ptybackend.OutputEvent{
			Kind:       ptybackend.OutputEventKindPlacements,
			Seq:        update.Seq,
			Placements: update.Placements,
		}) {
			onDrop("buffer_overflow")
		}
	})
	onResize := pty.OnResize(func(update pty.ResizeUpdate) {
		if !stream.publish(ptybackend.OutputEvent{
			Kind: ptybackend.OutputEventKindResize,
			Cols: update.Cols, Rows: update.Rows,
			XPixel: update.XPixel, YPixel: update.YPixel,
		}) {
			onDrop("buffer_overflow")
		}
	})

	omitReplay := len(opts) > 0 && opts[len(opts)-1].OmitReplay
	var info pty.AttachInfo
	var err error
	if omitReplay {
		info, err = b.manager.Subscribe(sessionID, subscriberID, send, onDrop, onPlacements, onResize)
	} else {
		info, err = b.manager.Attach(
			sessionID,
			subscriberID,
			send,
			onDrop,
			onPlacements,
			onResize,
		)
	}
	if err != nil {
		stream.Close()
		return ptybackend.AttachInfo{}, nil, err
	}

	return ptybackend.AttachInfo{
		LastSeq:                    info.LastSeq,
		Cols:                       info.Cols,
		Rows:                       info.Rows,
		PID:                        info.PID,
		Running:                    info.Running,
		ExitCode:                   info.ExitCode,
		ExitSignal:                 info.ExitSignal,
		GhosttySnapshot:            info.GhosttySnapshot,
		GhosttySnapshotFormat:      info.GhosttySnapshotFormat,
		GhosttyBlocks:              info.GhosttyBlocks,
		GhosttyPlacements:          info.GhosttyPlacements,
		GhosttyScrollbackTruncated: info.GhosttyScrollbackTruncated,
	}, stream, nil
}

func (b *InProcessPTY) KittyImage(_ context.Context, id harness.TerminalID, imageID uint32) (pty.KittyImage, error) {
	return b.manager.KittyImage(string(id), imageID)
}

func (b *InProcessPTY) ScreenSnapshot(_ context.Context, id harness.TerminalID) (pty.ScreenSnapshotInfo, error) {
	return b.manager.ScreenSnapshot(string(id))
}

func (b *InProcessPTY) Input(ctx context.Context, id harness.TerminalID, data []byte) error {
	return b.manager.Input(ctx, string(id), data)
}

func (b *InProcessPTY) PlaceInput(_ context.Context, id harness.TerminalID, data []byte, within time.Duration) error {
	return b.manager.PlaceInput(string(id), data, within)
}

func (b *InProcessPTY) Resize(_ context.Context, id harness.TerminalID, cols, rows, xpixel, ypixel uint16) (ptybackend.ResizeResult, error) {
	changed, err := b.manager.Resize(string(id), cols, rows, xpixel, ypixel)
	return ptybackend.ResizeResult{Changed: changed, StreamOrdered: true}, err
}

func (b *InProcessPTY) SetTheme(_ context.Context, id harness.TerminalID, theme pty.TerminalTheme) error {
	return b.manager.SetTheme(string(id), theme)
}

func (b *InProcessPTY) Kill(_ context.Context, id harness.TerminalID, sig syscall.Signal) error {
	return b.manager.Kill(string(id), sig)
}

func (b *InProcessPTY) Remove(_ context.Context, id harness.TerminalID) error {
	b.manager.Remove(string(id))
	return nil
}

func (b *InProcessPTY) RuntimeLifecycle(id harness.TerminalID) (string, bool) {
	return b.manager.LifecycleID(string(id))
}

func (b *InProcessPTY) TerminalIDs(_ context.Context) []harness.TerminalID {
	sessions := b.manager.SessionIDs()
	ids := make([]harness.TerminalID, len(sessions))
	for i, session := range sessions {
		ids[i] = harness.TerminalID(session)
	}
	return ids
}

func (b *InProcessPTY) Recover(_ context.Context) (ptybackend.RecoveryReport, error) {
	return ptybackend.RecoveryReport{Recovered: len(b.manager.SessionIDs())}, nil
}

func (b *InProcessPTY) Shutdown(_ context.Context) error {
	for _, id := range b.manager.SessionIDs() {
		_ = b.manager.Kill(id, syscall.SIGKILL)
	}
	b.manager.Shutdown()
	return nil
}

func (b *InProcessPTY) SessionInfo(_ context.Context, id harness.TerminalID) (ptybackend.SessionInfo, error) {
	sessionID := string(id)
	info, err := b.manager.SessionInfo(sessionID)
	if err != nil {
		return ptybackend.SessionInfo{}, err
	}
	result := ptybackend.SessionInfo{
		SessionID:  info.SessionID,
		Agent:      info.Agent,
		CWD:        info.CWD,
		Title:      b.manager.Title(sessionID),
		Running:    info.Running,
		Cols:       info.Cols,
		Rows:       info.Rows,
		PID:        info.PID,
		LastSeq:    info.LastSeq,
		ExitCode:   info.ExitCode,
		ExitSignal: info.ExitSignal,
	}
	result.LastSignal, result.HasLastSignal = b.manager.LastSignal(sessionID)
	return result, nil
}

type inProcessStream struct {
	events    chan ptybackend.OutputEvent
	closeFn   func()
	closeOnce sync.Once
	mu        sync.Mutex
	closed    bool
}

func (s *inProcessStream) Events() <-chan ptybackend.OutputEvent {
	return s.events
}

func (s *inProcessStream) Close() error {
	s.closeOnce.Do(func() {
		s.mu.Lock()
		s.closed = true
		s.mu.Unlock()
		if s.closeFn != nil {
			s.closeFn()
		}
		s.mu.Lock()
		close(s.events)
		s.mu.Unlock()
	})
	return nil
}

func (s *inProcessStream) publish(evt ptybackend.OutputEvent) (ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return false
	}
	// The last slot is reserved for the overflow desync; without it the client never reattaches.
	if evt.Kind != ptybackend.OutputEventKindDesync && len(s.events) >= cap(s.events)-1 {
		return false
	}
	select {
	case s.events <- evt:
		return true
	default:
		return false
	}
}

func (b *InProcessPTY) ReadText(_ context.Context, id harness.TerminalID, mode string, lines int) (pty.ReadTextInfo, error) {
	return b.manager.ReadText(string(id), mode, lines)
}
func (b *InProcessPTY) WaitBlock(ctx context.Context, id harness.TerminalID, params pty.WaitBlockParams) (pty.WaitBlockResult, error) {
	return b.manager.WaitBlock(ctx, string(id), params)
}

package ptybackend

import (
	"context"
	"sync"
	"syscall"

	"github.com/victorarias/attn/internal/buildinfo"
	"github.com/victorarias/attn/internal/harness"
	"github.com/victorarias/attn/internal/pty"
)

type EmbeddedBackend struct {
	manager *pty.Manager
}

func (b *EmbeddedBackend) SessionTerminalBuild(harness.TerminalID) (string, bool) {
	return buildinfo.SnapshotFormat, true
}

func NewEmbedded(manager *pty.Manager) *EmbeddedBackend {
	if manager == nil {
		manager = pty.NewManager(nil)
	}
	return &EmbeddedBackend{manager: manager}
}

func (b *EmbeddedBackend) SetExitHandler(handler func(ExitInfo)) {
	if handler == nil {
		b.manager.SetExitHandler(nil)
		return
	}
	b.manager.SetExitHandler(func(info pty.ExitInfo) {
		handler(ExitInfo{ID: harness.TerminalID(info.ID), ExitCode: info.ExitCode, Signal: info.Signal, LifecycleID: info.LifecycleID})
	})
}

func (b *EmbeddedBackend) SetStateHandler(handler func(id harness.TerminalID, obs pty.Observation)) {
	if handler == nil {
		b.manager.SetStateHandler(nil)
		return
	}
	b.manager.SetStateHandler(func(id string, obs pty.Observation) { handler(harness.TerminalID(id), obs) })
}

func (b *EmbeddedBackend) Spawn(_ context.Context, opts SpawnOptions) error {
	if err := validateSpawnOptions(opts); err != nil {
		return err
	}
	return b.manager.Spawn(toPTYSpawnOptions(opts))
}

func (b *EmbeddedBackend) Attach(_ context.Context, id harness.TerminalID, subscriberID string, opts ...AttachOptions) (AttachInfo, Stream, error) {
	sessionID := string(id)
	events := make(chan OutputEvent, 128)
	stream := &embeddedStream{
		events: events,
		closeFn: func() {
			b.manager.Detach(sessionID, subscriberID)
		},
	}

	send := func(data []byte, seq uint32) bool {
		payload := append([]byte(nil), data...)
		return stream.publish(OutputEvent{Kind: OutputEventKindOutput, Data: payload, Seq: seq})
	}
	onDrop := func(reason string) {
		_ = stream.publish(OutputEvent{Kind: OutputEventKindDesync, Reason: reason})
		stream.Close()
	}
	onPlacements := pty.OnPlacements(func(update pty.PlacementUpdate) {
		if !stream.publish(OutputEvent{
			Kind:       OutputEventKindPlacements,
			Seq:        update.Seq,
			Placements: update.Placements,
		}) {
			onDrop("buffer_overflow")
		}
	})
	onResize := pty.OnResize(func(update pty.ResizeUpdate) {
		if !stream.publish(OutputEvent{
			Kind: OutputEventKindResize,
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
		return AttachInfo{}, nil, err
	}

	return AttachInfo{
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

func (b *EmbeddedBackend) KittyImage(_ context.Context, id harness.TerminalID, imageID uint32) (pty.KittyImage, error) {
	return b.manager.KittyImage(string(id), imageID)
}

func (b *EmbeddedBackend) ScreenSnapshot(_ context.Context, id harness.TerminalID) (pty.ScreenSnapshotInfo, error) {
	return b.manager.ScreenSnapshot(string(id))
}

func (b *EmbeddedBackend) Input(ctx context.Context, id harness.TerminalID, data []byte) error {
	return b.manager.Input(ctx, string(id), data)
}

func (b *EmbeddedBackend) Resize(_ context.Context, id harness.TerminalID, cols, rows, xpixel, ypixel uint16) (ResizeResult, error) {
	changed, err := b.manager.Resize(string(id), cols, rows, xpixel, ypixel)
	return ResizeResult{Changed: changed, StreamOrdered: true}, err
}

func (b *EmbeddedBackend) SetTheme(_ context.Context, id harness.TerminalID, theme pty.TerminalTheme) error {
	return b.manager.SetTheme(string(id), theme)
}

func (b *EmbeddedBackend) Kill(_ context.Context, id harness.TerminalID, sig syscall.Signal) error {
	return b.manager.Kill(string(id), sig)
}

func (b *EmbeddedBackend) Remove(_ context.Context, id harness.TerminalID) error {
	b.manager.Remove(string(id))
	return nil
}

func (b *EmbeddedBackend) TerminalIDs(_ context.Context) []harness.TerminalID {
	return terminalIDs(b.manager.SessionIDs())
}

func (b *EmbeddedBackend) Recover(_ context.Context) (RecoveryReport, error) {
	return RecoveryReport{Recovered: len(b.manager.SessionIDs())}, nil
}

func (b *EmbeddedBackend) Shutdown(_ context.Context) error {
	b.manager.Shutdown()
	return nil
}

func (b *EmbeddedBackend) SessionInfo(_ context.Context, id harness.TerminalID) (SessionInfo, error) {
	sessionID := string(id)
	info, err := b.manager.SessionInfo(sessionID)
	if err != nil {
		return SessionInfo{}, err
	}
	result := SessionInfo{
		SessionID:  info.SessionID,
		Agent:      info.Agent,
		CWD:        info.CWD,
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

type embeddedStream struct {
	events    chan OutputEvent
	closeFn   func()
	closeOnce sync.Once
	mu        sync.Mutex
	closed    bool
}

func (s *embeddedStream) Events() <-chan OutputEvent {
	return s.events
}

func (s *embeddedStream) Close() error {
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

func (s *embeddedStream) publish(evt OutputEvent) (ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return false
	}
	// The last slot is reserved for the overflow desync; without it the client never reattaches.
	if evt.Kind != OutputEventKindDesync && len(s.events) >= cap(s.events)-1 {
		return false
	}
	select {
	case s.events <- evt:
		return true
	default:
		return false
	}
}

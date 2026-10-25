package daemon

import (
	"context"
	"errors"

	"github.com/victorarias/attn/internal/harness"
	"github.com/victorarias/attn/internal/pty"
	"github.com/victorarias/attn/internal/ptybackend"
)

func (d *Daemon) terminalNeedsResync(parent context.Context, terminal harness.TerminalID) bool {
	provider, canProbe := d.ptyBackend.(ptybackend.SessionInfoProvider)
	if !canProbe || d.stopping() {
		return false
	}
	ctx, cancel := context.WithTimeout(parent, endedStreamProbeTimeout)
	defer cancel()
	info, err := provider.SessionInfo(ctx, terminal)
	if errors.Is(err, pty.ErrSessionNotFound) || err == nil && !info.Running {
		return false
	}
	if sessionID, shown := d.shownIn(terminal); shown && d.store.GetSessionExit(sessionID) != nil {
		return false
	}
	return true
}

type terminalOutputStream struct {
	ptybackend.Stream
	events chan ptybackend.OutputEvent
	cancel context.CancelFunc
}

func (s *terminalOutputStream) Events() <-chan ptybackend.OutputEvent { return s.events }

func (s *terminalOutputStream) Close() error {
	s.cancel()
	return s.Stream.Close()
}

func (d *Daemon) watchTerminalStream(terminal harness.TerminalID, source ptybackend.Stream) ptybackend.Stream {
	ctx, cancel := context.WithCancel(d.life.Context())
	stream := &terminalOutputStream{Stream: source, events: make(chan ptybackend.OutputEvent), cancel: cancel}
	if !d.life.Go("watchTerminalStream", func() {
		defer close(stream.events)
		defer stream.Close()
		for event := range source.Events() {
			select {
			case stream.events <- event:
			case <-ctx.Done():
				return
			}
			if event.Kind == ptybackend.OutputEventKindDesync {
				return
			}
		}
		if ctx.Err() == nil && d.terminalNeedsResync(ctx, terminal) {
			select {
			case stream.events <- ptybackend.OutputEvent{Kind: ptybackend.OutputEventKindDesync, Reason: "runtime_stream_ended"}:
			case <-ctx.Done():
			}
		}
	}) {
		_ = stream.Close()
		close(stream.events)
	}
	return stream
}

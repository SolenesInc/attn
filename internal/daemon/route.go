package daemon

import (
	"context"
	"fmt"

	"github.com/victorarias/attn/internal/harness"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/ptybackend"
)

type machinePart func(context.Context, protocol.Placed) (any, error)

type Router struct {
	here   map[string]machinePart
	attach func(context.Context, harness.TerminalID, string, ptybackend.AttachOptions) (ptybackend.AttachInfo, ptybackend.Stream, error)
}

func (d *Daemon) router() *Router {
	d.routerOnce.Do(func() {
		d.routes = &Router{here: d.machineParts(), attach: func(ctx context.Context, t harness.TerminalID, subscriber string, opts ptybackend.AttachOptions) (ptybackend.AttachInfo, ptybackend.Stream, error) {
			return d.ptyBackend.Attach(ctx, t, subscriber, opts)
		}}
	})
	return d.routes
}

func Call[R any](ctx context.Context, r *Router, msg protocol.Placed) (R, error) {
	var answer R
	part, ok := r.here[msg.Command()]
	if !ok {
		return answer, fmt.Errorf("no machine part runs %s", msg.Command())
	}
	result, err := part(ctx, msg)
	if err != nil {
		return answer, err
	}
	answer, ok = result.(R)
	if !ok {
		return answer, fmt.Errorf("%s answers %T, not %T", msg.Command(), result, answer)
	}
	return answer, nil
}

func (r *Router) Attach(ctx context.Context, t harness.TerminalID, subscriber string, opts ptybackend.AttachOptions) (ptybackend.AttachInfo, ptybackend.Stream, error) {
	return r.attach(ctx, t, subscriber, opts)
}

func runs[M protocol.Placed, R any](parts map[string]machinePart, run func(context.Context, M) (R, error)) {
	var command M
	parts[command.Command()] = func(ctx context.Context, msg protocol.Placed) (any, error) {
		typed, ok := msg.(M)
		if !ok {
			return nil, fmt.Errorf("%s carries %T, not %T", msg.Command(), msg, command)
		}
		return run(ctx, typed)
	}
}

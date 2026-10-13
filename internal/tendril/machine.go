package tendril

import (
	"context"
	"errors"
	"fmt"
	"go/token"
	"go/types"
	"io"
	"runtime"
)

type Limits struct {
	Fuel       int64
	Memory     int64
	Depth      int
	Goroutines int
}

var DefaultLimits = Limits{Fuel: 10_000_000, Memory: 64 << 20, Depth: 512, Goroutines: 256}

type Stats struct {
	Fuel   int64
	Memory int64
}

var (
	ErrFuel   = errors.New("fuel exhausted")
	ErrMemory = errors.New("memory budget exhausted")
	ErrDepth  = errors.New("call depth exceeded")
)

type RuntimeError struct {
	Pos   token.Position
	Err   error
	Trace []token.Position
}

func (e *RuntimeError) Error() string { return fmt.Sprintf("%s: %v", e.Pos, e.Err) }
func (e *RuntimeError) Unwrap() error { return e.Err }

type halt struct{ err error }

type scriptPanic struct{ v Value }

const ctxCheckInterval = 4096

type machine struct {
	ctx        context.Context
	hctx       context.Context
	prog       *Program
	limits     Limits
	fuel       int64
	checkpoint int64
	mem        int64
	depth      int
	pos        token.Pos
	globals    []Value
	halted     error
	frames     []*frame
	journal    *Journal
	replayed   int
	label      *types.Label
	panicking  *panicState
	sched      *scheduler
	main       *gor
	g          *gor
	exited     bool
	selectSeq  int
	haltedBy   *gor
	stdout     io.Writer
	stderr     io.Writer
}

type ExitError struct{ Code int }

func (e *ExitError) Error() string { return fmt.Sprintf("exit status %d", e.Code) }

func machineFrom(ctx context.Context) *machine {
	m, _ := ctx.Value(machineKey{}).(*machine)
	return m
}

func (m *machine) write(w io.Writer, s string) {
	m.alloc(int64(len(s)))
	if w != nil {
		_, _ = io.WriteString(w, s)
	}
}

func newMachine(ctx context.Context, p *Program, lim Limits) *machine {
	m := &machine{ctx: ctx, prog: p, limits: lim, fuel: lim.Fuel, mem: lim.Memory}
	m.checkpoint = m.fuel - ctxCheckInterval
	m.hctx = context.WithValue(ctx, machineKey{}, m)
	return m
}

func (m *machine) burn(n int64) {
	m.fuel -= n
	if m.fuel < m.checkpoint {
		m.checkpointReached()
	}
}

func (m *machine) stop(err error) {
	m.halted, m.haltedBy = err, m.g
	panic(halt{err})
}

func (m *machine) checkpointReached() {
	if m.fuel < 0 {
		m.fuel = 0
		m.stop(ErrFuel)
	}
	if err := m.ctx.Err(); err != nil {
		m.stop(err)
	}
	m.checkpoint = m.fuel - ctxCheckInterval
	if s := m.sched; s != nil {
		s.mu.Lock()
		waiting := len(s.runq) > 0
		s.mu.Unlock()
		if waiting {
			m.yield()
		}
	}
}

func (m *machine) alloc(bytes int64) {
	if bytes < 0 || bytes > m.mem {
		m.mem = 0
		m.stop(ErrMemory)
	}
	m.mem -= bytes
}

func (m *machine) enter() {
	m.depth++
	if m.depth > m.limits.Depth {
		m.stop(ErrDepth)
	}
	m.burn(1)
}

func (m *machine) stats() Stats {
	return Stats{Fuel: m.limits.Fuel - m.fuel, Memory: m.limits.Memory - m.mem}
}

func (m *machine) run(f func()) (err error) {
	defer func() {
		r := recover()
		if r == nil {
			return
		}
		pos := m.prog.fset.Position(m.pos)
		switch r := r.(type) {
		case halt:
			if re, ok := r.err.(*RuntimeError); ok {
				err = re
			} else {
				err = &RuntimeError{Pos: pos, Err: r.err}
			}
		case scriptPanic:
			err = &RuntimeError{Pos: pos, Err: fmt.Errorf("panic: %s", m.formatPanic(r.v))}
		case runtimeFault:
			err = &RuntimeError{Pos: pos, Err: fmt.Errorf("runtime error: %s", string(r))}
		case hostPanic:
			err = &RuntimeError{Pos: pos, Err: r}
		case runtime.Error:
			err = &RuntimeError{Pos: pos, Err: fmt.Errorf("runtime error: %s", trimRuntimePrefix(r.Error()))}
		case error:
			err = &RuntimeError{Pos: pos, Err: r}
		default:
			err = &RuntimeError{Pos: pos, Err: fmt.Errorf("panic: %v", r)}
		}
	}()
	f()
	return nil
}

func trimRuntimePrefix(s string) string {
	const prefix = "runtime error: "
	if len(s) > len(prefix) && s[:len(prefix)] == prefix {
		return s[len(prefix):]
	}
	return s
}

type hostPanic struct {
	fn string
	v  any
}

func (h hostPanic) Error() string { return fmt.Sprintf("host function %s panicked: %v", h.fn, h.v) }

type machineKey struct{}

func Charge(ctx context.Context, bytes int64) {
	if m, ok := ctx.Value(machineKey{}).(*machine); ok {
		m.alloc(bytes)
	}
}

func Burn(ctx context.Context, fuel int64) {
	if m, ok := ctx.Value(machineKey{}).(*machine); ok {
		m.burn(fuel)
	}
}

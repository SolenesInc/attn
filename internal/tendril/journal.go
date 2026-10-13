package tendril

import (
	"context"
	"errors"
	"fmt"
	"reflect"
)

var ErrDiverged = errors.New("script diverged from its journal")

type Journal struct {
	Entries []JournalEntry
}

type JournalEntry struct {
	Func    string
	Args    string
	Results []any
}

func (p *Package) Journaled() *Package {
	p.journaled = true
	return p
}

func (p *Program) Resume(ctx context.Context, lim Limits, j *Journal, name string, args ...any) ([]any, Stats, error) {
	return p.call(ctx, lim, j, name, args...)
}

func (m *machine) journaledCall(h *hostFunc, in []reflect.Value, live func() []reflect.Value) []reflect.Value {
	start := 0
	if h.ctx {
		start = 1
	}
	digest := fmt.Sprint(valuesOf(in[start:])...)
	if m.replayed < len(m.journal.Entries) {
		e := m.journal.Entries[m.replayed]
		m.replayed++
		if e.Func != h.name || e.Args != digest {
			m.stop(fmt.Errorf("%w: call %d was %s(%s), now %s(%s)", ErrDiverged, m.replayed, e.Func, e.Args, h.name, digest))
		}
		out := make([]reflect.Value, len(e.Results))
		for i, r := range e.Results {
			out[i] = reflect.New(h.fn.Type().Out(i)).Elem()
			if r != nil {
				out[i].Set(reflect.ValueOf(r))
			}
		}
		return out
	}
	out := live()
	m.journal.Entries = append(m.journal.Entries, JournalEntry{Func: h.name, Args: digest, Results: valuesOf(out)})
	m.replayed++
	return out
}

func valuesOf(vs []reflect.Value) []any {
	out := make([]any, len(vs))
	for i, v := range vs {
		out[i] = v.Interface()
	}
	return out
}

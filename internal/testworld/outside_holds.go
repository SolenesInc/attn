package testworld

import (
	"fmt"
	"sync"
	"testing"
)

type outsideHolds struct {
	t      testing.TB
	pauses *Pauses
	mu     sync.Mutex
	armed  int
}

func newOutsideHolds(t testing.TB) *outsideHolds {
	h := &outsideHolds{t: t, pauses: NewPauses()}
	t.Cleanup(func() {
		h.pauses.FailUnreached()
		h.pauses.Close()
	})
	return h
}

func (h *outsideHolds) arm(what string) (string, *Pause) {
	h.mu.Lock()
	h.armed++
	name := fmt.Sprintf("%s #%d", what, h.armed)
	h.mu.Unlock()
	return name, h.pauses.Arm(h.t, name, false)
}

func (h *outsideHolds) hold(name string) {
	h.pauses.Hold(name, nil)
}

func (h *outsideHolds) arrive(name string, release func()) {
	h.pauses.arrive(name, 0, nil, true, release)
}

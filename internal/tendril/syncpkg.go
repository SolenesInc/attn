package tendril

import (
	"go/types"
	"slices"
	"sort"
)

const syncSource = `package sync

// A WaitGroup waits for a collection of goroutines to finish.
type WaitGroup struct{ state uintptr }

// Add adds delta, which may be negative, to the WaitGroup counter.
func (wg *WaitGroup) Add(delta int)

// Done decrements the WaitGroup counter by one.
func (wg *WaitGroup) Done()

// Wait blocks until the WaitGroup counter is zero.
func (wg *WaitGroup) Wait()

// Go calls f in a new goroutine and adds that task to the WaitGroup.
func (wg *WaitGroup) Go(f func())

// A Mutex is a mutual exclusion lock.
type Mutex struct{ state uintptr }

func (m *Mutex) Lock()
func (m *Mutex) Unlock()
func (m *Mutex) TryLock() bool

// A RWMutex is a reader/writer mutual exclusion lock.
type RWMutex struct{ state uintptr }

func (rw *RWMutex) Lock()
func (rw *RWMutex) Unlock()
func (rw *RWMutex) RLock()
func (rw *RWMutex) RUnlock()

// Once is an object that will perform exactly one action.
type Once struct{ state uintptr }

func (o *Once) Do(f func())
`

const errgroupSource = `package errgroup

// A Group is a collection of goroutines working on subtasks that are part of
// the same overall task. A zero Group is valid.
type Group struct{ state uintptr }

// Go calls f in a new goroutine. The first call to return a non-nil error
// is the error Wait returns. Go blocks while the group is at its limit.
func (g *Group) Go(f func() error)

// TryGo calls f in a new goroutine only if the group is below its limit.
func (g *Group) TryGo(f func() error) bool

// Wait blocks until all calls from Go have returned, then returns the first
// non-nil error, if any.
func (g *Group) Wait() error

// SetLimit limits the number of active goroutines in this group to at most n.
// A negative value indicates no limit.
func (g *Group) SetLimit(n int)
`

func nativeState[T any](recv Value) *T {
	box := derefBox(recv)
	if st, ok := box.f[0].(*T); ok {
		return st
	}
	st := new(T)
	box.f[0] = st
	return st
}

type waitGroup struct {
	n       int
	waiters []*gor
}

func (wg *waitGroup) add(m *machine, delta int) {
	wg.n += delta
	if wg.n < 0 {
		throw("sync: negative WaitGroup counter")
	}
	if wg.n == 0 {
		for _, g := range wg.waiters {
			m.ready(g)
		}
		wg.waiters = nil
	}
}

func (wg *waitGroup) wait(m *machine) {
	if wg.n == 0 {
		return
	}
	m.ensureSched()
	wg.waiters = append(wg.waiters, m.g)
	m.park()
}

type mutex struct {
	locked  bool
	readers int
	waiters []mutexWaiter
}

type mutexWaiter struct {
	g      *gor
	reader bool
}

func (mu *mutex) lock(m *machine, reader bool) {
	if !mu.locked && (!reader || len(mu.waiters) == 0) && (reader || mu.readers == 0) {
		if reader {
			mu.readers++
		} else {
			mu.locked = true
		}
		return
	}
	m.ensureSched()
	mu.waiters = append(mu.waiters, mutexWaiter{g: m.g, reader: reader})
	m.park()
}

func (mu *mutex) unlock(m *machine, reader bool) {
	switch {
	case reader && mu.readers == 0:
		throw("sync: RUnlock of unlocked RWMutex")
	case reader:
		mu.readers--
	case !mu.locked:
		throw("sync: unlock of unlocked mutex")
	default:
		mu.locked = false
	}
	for len(mu.waiters) > 0 && !mu.locked {
		w := mu.waiters[0]
		if !w.reader && mu.readers > 0 {
			return
		}
		mu.waiters = mu.waiters[1:]
		if w.reader {
			mu.readers++
		} else {
			mu.locked = true
		}
		m.ready(w.g)
	}
}

type once struct {
	done    bool
	running bool
	waiters []*gor
}

type group struct {
	active  int
	limit   int
	err     Value
	waiters []*gor
	slots   []*gor
}

func (g *group) start(m *machine, f Value) {
	g.active++
	m.spawn(func(fr *frame) {
		if r := fr.m.callValue(f, nil); r != nil && g.err == nil {
			g.err = r
		}
		g.active--
		if len(g.slots) > 0 {
			next := g.slots[0]
			g.slots = g.slots[1:]
			fr.m.ready(next)
		}
		if g.active == 0 {
			for _, w := range g.waiters {
				fr.m.ready(w)
			}
			g.waiters = nil
		}
	})
}

func stdSync() *Package {
	return NewPackage("sync").Source(syncSource).
		Native("WaitGroup.Add", func(m *machine, _ []types.Type, a []Value) Value {
			nativeState[waitGroup](a[0]).add(m, a[1].(int))
			return nil
		}).
		Native("WaitGroup.Done", func(m *machine, _ []types.Type, a []Value) Value {
			nativeState[waitGroup](a[0]).add(m, -1)
			return nil
		}).
		Native("WaitGroup.Wait", func(m *machine, _ []types.Type, a []Value) Value {
			nativeState[waitGroup](a[0]).wait(m)
			return nil
		}).
		Native("WaitGroup.Go", func(m *machine, _ []types.Type, a []Value) Value {
			wg := nativeState[waitGroup](a[0])
			wg.add(m, 1)
			f := a[1]
			m.spawn(func(fr *frame) {
				fr.m.callValue(f, nil)
				wg.add(fr.m, -1)
			})
			return nil
		}).
		Native("Mutex.Lock", func(m *machine, _ []types.Type, a []Value) Value {
			nativeState[mutex](a[0]).lock(m, false)
			return nil
		}).
		Native("Mutex.Unlock", func(m *machine, _ []types.Type, a []Value) Value {
			nativeState[mutex](a[0]).unlock(m, false)
			return nil
		}).
		Native("Mutex.TryLock", func(m *machine, _ []types.Type, a []Value) Value {
			mu := nativeState[mutex](a[0])
			if mu.locked {
				return false
			}
			mu.locked = true
			return true
		}).
		Native("RWMutex.Lock", func(m *machine, _ []types.Type, a []Value) Value {
			nativeState[mutex](a[0]).lock(m, false)
			return nil
		}).
		Native("RWMutex.Unlock", func(m *machine, _ []types.Type, a []Value) Value {
			nativeState[mutex](a[0]).unlock(m, false)
			return nil
		}).
		Native("RWMutex.RLock", func(m *machine, _ []types.Type, a []Value) Value {
			nativeState[mutex](a[0]).lock(m, true)
			return nil
		}).
		Native("RWMutex.RUnlock", func(m *machine, _ []types.Type, a []Value) Value {
			nativeState[mutex](a[0]).unlock(m, true)
			return nil
		}).
		Native("Once.Do", func(m *machine, _ []types.Type, a []Value) Value {
			o := nativeState[once](a[0])
			for o.running {
				m.ensureSched()
				o.waiters = append(o.waiters, m.g)
				m.park()
			}
			if o.done {
				return nil
			}
			o.running = true
			m.callValue(a[1], nil)
			o.running, o.done = false, true
			for _, w := range o.waiters {
				m.ready(w)
			}
			o.waiters = nil
			return nil
		})
}

func stdErrgroup() *Package {
	return NewPackage("golang.org/x/sync/errgroup").Source(errgroupSource).
		Native("Group.Go", func(m *machine, _ []types.Type, a []Value) Value {
			g := nativeState[group](a[0])
			for g.limit > 0 && g.active >= g.limit {
				m.ensureSched()
				g.slots = append(g.slots, m.g)
				m.park()
			}
			g.start(m, a[1])
			return nil
		}).
		Native("Group.TryGo", func(m *machine, _ []types.Type, a []Value) Value {
			g := nativeState[group](a[0])
			if g.limit > 0 && g.active >= g.limit {
				return false
			}
			g.start(m, a[1])
			return true
		}).
		Native("Group.Wait", func(m *machine, _ []types.Type, a []Value) Value {
			g := nativeState[group](a[0])
			if g.active > 0 {
				m.ensureSched()
				g.waiters = append(g.waiters, m.g)
				m.park()
			}
			return g.err
		}).
		Native("Group.SetLimit", func(m *machine, _ []types.Type, a []Value) Value {
			nativeState[group](a[0]).limit = a[1].(int)
			return nil
		})
}

const sortSource = `package sort

// Strings sorts a slice of strings in increasing order.
func Strings(x []string)

// Ints sorts a slice of ints in increasing order.
func Ints(x []int)

// Float64s sorts a slice of float64s in increasing order.
func Float64s(x []float64)

// Slice sorts the slice x given the provided less function.
func Slice(x any, less func(i, j int) bool)

// SliceStable sorts x keeping the original order of equal elements.
func SliceStable(x any, less func(i, j int) bool)

// Search returns the smallest index i in [0, n) at which f(i) is true.
func Search(n int, f func(int) bool) int
`

type scriptSort struct {
	m    *machine
	s    []Value
	less Value
}

func (x scriptSort) Len() int      { return len(x.s) }
func (x scriptSort) Swap(i, j int) { x.s[i], x.s[j] = x.s[j], x.s[i] }
func (x scriptSort) Less(i, j int) bool {
	return x.m.callValue(x.less, []Value{i, j}).(bool)
}

func sortValues(m *machine, _ []types.Type, a []Value) Value {
	s := a[0].([]Value)
	m.burn(int64(len(s)) * int64(1+bitsLen(len(s))) / 4)
	slices.SortStableFunc(s, compareKeys)
	return nil
}

func stdSort() *Package {
	byLess := func(stable bool) nativeImpl {
		return func(m *machine, _ []types.Type, a []Value) Value {
			if a[0] == nil {
				return nil
			}
			s, ok := a[0].(iface).v.([]Value)
			if !ok {
				throw("sort: Slice called with a non-slice value")
			}
			if stable {
				sort.Stable(scriptSort{m: m, s: s, less: a[1]})
			} else {
				sort.Sort(scriptSort{m: m, s: s, less: a[1]})
			}
			return nil
		}
	}
	return NewPackage("sort").Source(sortSource).
		Native("Strings", sortValues).
		Native("Ints", sortValues).
		Native("Float64s", sortValues).
		Native("Slice", byLess(false)).
		Native("SliceStable", byLess(true)).
		Native("Search", func(m *machine, _ []types.Type, a []Value) Value {
			return sort.Search(a[0].(int), func(i int) bool { return m.callValue(a[1], []Value{i}).(bool) })
		})
}

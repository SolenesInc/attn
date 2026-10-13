package tendril

type chanVal struct {
	buf    []Value
	size   int
	closed bool
	recvq  []*waiter
	sendq  []*waiter
}

type waiter struct {
	g     *gor
	val   Value
	ok    bool
	sel   *selection
	which int
}

type selection struct {
	done  bool
	which int
	val   Value
	ok    bool
}

func (w *waiter) stale() bool { return w.sel != nil && w.sel.done }

func (w *waiter) fire(m *machine, val Value, ok bool) {
	if w.sel != nil {
		w.sel.done, w.sel.which, w.sel.val, w.sel.ok = true, w.which, val, ok
	} else {
		w.val, w.ok = val, ok
	}
	m.ready(w.g)
}

func dequeue(q *[]*waiter) *waiter {
	for len(*q) > 0 {
		w := (*q)[0]
		*q = (*q)[1:]
		if !w.stale() {
			return w
		}
	}
	return nil
}

func (c *chanVal) trySend(m *machine, v Value) bool {
	if c.closed {
		throw("send on closed channel")
	}
	if w := dequeue(&c.recvq); w != nil {
		w.fire(m, v, true)
		return true
	}
	if len(c.buf) < c.size {
		m.alloc(slotBytes)
		c.buf = append(c.buf, v)
		return true
	}
	return false
}

func (c *chanVal) tryRecv(m *machine, zero func() Value) (Value, bool, bool) {
	if len(c.buf) > 0 {
		v := c.buf[0]
		c.buf = c.buf[1:]
		if w := dequeue(&c.sendq); w != nil {
			c.buf = append(c.buf, w.sendValue())
			w.fire(m, nil, true)
		}
		return v, true, true
	}
	if w := dequeue(&c.sendq); w != nil {
		v := w.sendValue()
		w.fire(m, nil, true)
		return v, true, true
	}
	if c.closed {
		return zero(), false, true
	}
	return nil, false, false
}

func (w *waiter) sendValue() Value { return w.val }

func (m *machine) send(c *chanVal, v Value) {
	m.ensureSched()
	if c == nil {
		m.park()
		return
	}
	if c.trySend(m, v) {
		return
	}
	w := &waiter{g: m.g, val: v}
	c.sendq = append(c.sendq, w)
	m.park()
	if !w.ok {
		throw("send on closed channel")
	}
}

func (m *machine) recv(c *chanVal, zero func() Value) (Value, bool) {
	m.ensureSched()
	if c == nil {
		m.park()
		return zero(), false
	}
	if v, ok, done := c.tryRecv(m, zero); done {
		return v, ok
	}
	w := &waiter{g: m.g}
	c.recvq = append(c.recvq, w)
	m.park()
	if !w.ok {
		return zero(), false
	}
	return w.val, true
}

func (m *machine) closeChan(c *chanVal) {
	if c == nil {
		throw("close of nil channel")
	}
	if c.closed {
		throw("close of closed channel")
	}
	c.closed = true
	m.ensureSched()
	for w := dequeue(&c.recvq); w != nil; w = dequeue(&c.recvq) {
		w.fire(m, nil, false)
	}
	for w := dequeue(&c.sendq); w != nil; w = dequeue(&c.sendq) {
		w.fire(m, nil, false)
	}
}

type selectCase struct {
	ch   *chanVal
	send bool
	val  Value
	zero func() Value
}

func (m *machine) selectOp(cases []selectCase, hasDefault bool) (int, Value, bool) {
	m.ensureSched()
	n := len(cases)
	start := 0
	if n > 0 {
		start = m.selectSeq % n
		m.selectSeq++
	}
	for k := range n {
		i := (start + k) % n
		c := cases[i]
		if c.ch == nil {
			continue
		}
		if c.send {
			if c.ch.trySend(m, c.val) {
				return i, nil, false
			}
			continue
		}
		if v, ok, done := c.ch.tryRecv(m, c.zero); done {
			return i, v, ok
		}
	}
	if hasDefault {
		return -1, nil, false
	}
	sel := &selection{}
	var waiters []*waiter
	for i, c := range cases {
		if c.ch == nil {
			continue
		}
		w := &waiter{g: m.g, sel: sel, which: i, val: c.val}
		waiters = append(waiters, w)
		if c.send {
			c.ch.sendq = append(c.ch.sendq, w)
		} else {
			c.ch.recvq = append(c.ch.recvq, w)
		}
	}
	m.park()
	for i, c := range cases {
		if c.ch != nil && i != sel.which {
			c.ch.recvq = without(c.ch.recvq, sel)
			c.ch.sendq = without(c.ch.sendq, sel)
		}
	}
	c := cases[sel.which]
	if c.send {
		if !sel.ok {
			throw("send on closed channel")
		}
		return sel.which, nil, false
	}
	if !sel.ok {
		return sel.which, c.zero(), false
	}
	return sel.which, sel.val, true
}

func without(q []*waiter, sel *selection) []*waiter {
	out := q[:0]
	for _, w := range q {
		if w.sel != sel {
			out = append(out, w)
		}
	}
	return out
}

package pty

// Refs are native memory: every retirement path must free them, and one marker's
// ref can back two blocks (self-heal), hence the reference counting.

const maxBlocks = 200

type sharedRef struct {
	ref blockRef
	rc  int
}

func newSharedRef(r blockRef) *sharedRef { return &sharedRef{ref: r} }

func (s *sharedRef) acquire() { s.rc++ }

func (s *sharedRef) release() {
	s.rc--
	if s.rc <= 0 {
		s.rc = 0
		if s.ref != nil {
			s.ref.Free()
			s.ref = nil
		}
	}
}

func (s *sharedRef) freeIfUnheld() {
	if s.rc == 0 && s.ref != nil {
		s.ref.Free()
		s.ref = nil
	}
}

func (s *sharedRef) point() (x, y int, ok bool) {
	if s == nil || s.ref == nil {
		return 0, 0, false
	}
	return s.ref.ScreenPoint()
}

type trackedBlock struct {
	id         uint64
	promptRef  *sharedRef
	inputRef   *sharedRef
	outputRef  *sharedRef
	endRef     *sharedRef
	command    *string
	exitCode   *int32
	hasCommand bool
	altScreen  bool
}

func (b *trackedBlock) release() {
	// Fields can share a *sharedRef (self-heal); releasing each acquire keeps
	// the count balanced.
	for _, r := range []*sharedRef{b.promptRef, b.inputRef, b.outputRef, b.endRef} {
		if r != nil {
			r.release()
		}
	}
}

// blockTable's methods all run under replayMu (via blockFeeder), so it holds no
// lock of its own.
type blockTable struct {
	completed []*trackedBlock
	pending   *trackedBlock
	nextID    uint64
}

func newBlockTable() *blockTable {
	return &blockTable{nextID: 1}
}

func (bt *blockTable) ApplyMarker(m osc133Marker, ref blockRef, altScreen bool) {
	cur := newSharedRef(ref)

	// Self-heal a lost command-end: a new command context while a command already
	// ran means the previous 133;D never arrived, so close the open block here.
	if bt.pending != nil && bt.pending.hasCommand &&
		(m.Kind == osc133PromptStart || m.Kind == osc133InputStart || m.Kind == osc133PreExec) {
		bt.complete(bt.pending, cur, nil)
		bt.pending = nil
	}

	switch m.Kind {
	case osc133PromptStart:
		// A redrawn prompt replaces the open block; retire the displaced one
		// or its refs leak.
		if bt.pending != nil {
			bt.pending.release()
		}
		bt.pending = &trackedBlock{id: bt.nextID, promptRef: cur, altScreen: altScreen}
		cur.acquire()
		bt.nextID++
	case osc133InputStart:
		if bt.pending == nil {
			bt.pending = bt.openPending(cur, altScreen)
		}
		// A repeated input-start re-pins; release the ref it replaces.
		if bt.pending.inputRef != nil {
			bt.pending.inputRef.release()
		}
		bt.pending.inputRef = cur
		cur.acquire()
	case osc133PreExec:
		if bt.pending == nil {
			bt.pending = bt.openPending(cur, altScreen)
		}
		// No release-on-replace guard: a repeated pre-exec always trips
		// self-heal above and arrives with a fresh pending block.
		bt.pending.outputRef = cur
		cur.acquire()
		bt.pending.command = m.Cmdline
		bt.pending.hasCommand = true
	case osc133CommandEnd:
		p := bt.pending
		bt.pending = nil
		switch {
		case p != nil && p.hasCommand:
			bt.complete(p, cur, m.ExitCode)
		case p != nil:
			// Bare Enter at the prompt: nothing copyable; free the refs.
			p.release()
		}
	}

	// A marker whose position no block kept must not leak its native ref.
	cur.freeIfUnheld()
}

func (bt *blockTable) openPending(promptRef *sharedRef, altScreen bool) *trackedBlock {
	b := &trackedBlock{id: bt.nextID, promptRef: promptRef, altScreen: altScreen}
	promptRef.acquire()
	bt.nextID++
	return b
}

func (bt *blockTable) complete(p *trackedBlock, endRef *sharedRef, exitCode *int32) {
	p.endRef = endRef
	endRef.acquire()
	p.exitCode = exitCode
	bt.appendCompleted(p)
}

func (bt *blockTable) appendCompleted(b *trackedBlock) {
	bt.completed = append(bt.completed, b)
	if len(bt.completed) > maxBlocks {
		evicted := bt.completed[:len(bt.completed)-maxBlocks]
		bt.completed = append([]*trackedBlock(nil), bt.completed[len(bt.completed)-maxBlocks:]...)
		for _, e := range evicted {
			e.release()
		}
	}
}

func (bt *blockTable) SnapshotBlocks() []AttachBlockData {
	out := make([]AttachBlockData, 0, len(bt.completed)+1)
	for _, b := range bt.completed {
		if d, ok := b.resolve(false); ok {
			out = append(out, d)
		}
	}
	if bt.pending != nil {
		if d, ok := bt.pending.resolve(true); ok {
			out = append(out, d)
		}
	}
	return out
}

func pinAnchor(pin func(x, y int) blockRef, row *int32, col int) *sharedRef {
	if row == nil {
		return nil
	}
	r := pinShared(pin, col, int(*row))
	if r == nil {
		return nil
	}
	r.acquire()
	return r
}

func (bt *blockTable) Restore(blocks []AttachBlockData, pin func(x, y int) blockRef) {
	for _, d := range blocks {
		promptRef := pinShared(pin, 0, int(d.PromptRow))
		if promptRef == nil {
			continue
		}
		b := &trackedBlock{id: d.ID, promptRef: promptRef}
		promptRef.acquire()
		inputCol := 0
		if d.InputCol != nil {
			inputCol = int(*d.InputCol)
		}
		b.inputRef = pinAnchor(pin, d.InputRow, inputCol)
		b.outputRef = pinAnchor(pin, d.OutputStartRow, 0)
		b.endRef = pinAnchor(pin, d.EndRow, 0)
		if d.Command != nil {
			cmd := *d.Command
			b.command = &cmd
			b.hasCommand = true
		}
		if d.ExitCode != nil {
			code := *d.ExitCode
			b.exitCode = &code
		}
		if d.ID >= bt.nextID {
			bt.nextID = d.ID + 1
		}
		if d.Pending {
			// At most one block is pending; a later one replaces an earlier,
			// which must not leak its refs.
			if bt.pending != nil {
				bt.pending.release()
			}
			bt.pending = b
			continue
		}
		bt.appendCompleted(b)
	}
}

func pinShared(pin func(x, y int) blockRef, x, y int) *sharedRef {
	ref := pin(x, y)
	if ref == nil {
		return nil
	}
	return newSharedRef(ref)
}

// The table is unusable afterwards.
func (bt *blockTable) Close() {
	for _, b := range bt.completed {
		b.release()
	}
	bt.completed = nil
	if bt.pending != nil {
		bt.pending.release()
		bt.pending = nil
	}
}

func (b *trackedBlock) resolve(pending bool) (AttachBlockData, bool) {
	if b.altScreen {
		return AttachBlockData{}, false
	}
	_, promptY, ok := b.promptRef.point()
	if !ok {
		return AttachBlockData{}, false
	}
	d := AttachBlockData{ID: b.id, Pending: pending, PromptRow: int32(promptY)}
	if x, y, ok := b.inputRef.point(); ok {
		row, col := int32(y), int32(x)
		d.InputRow = &row
		d.InputCol = &col
	}
	if _, y, ok := b.outputRef.point(); ok {
		row := int32(y)
		d.OutputStartRow = &row
	}
	if !pending {
		_, y, ok := b.endRef.point()
		if !ok {
			// The end position is essential; drop rather than show a wrong row.
			return AttachBlockData{}, false
		}
		row := int32(y)
		d.EndRow = &row
	}
	if b.hasCommand {
		cmd := ""
		if b.command != nil {
			cmd = *b.command
		}
		d.Command = &cmd
	}
	d.ExitCode = b.exitCode
	return d, true
}

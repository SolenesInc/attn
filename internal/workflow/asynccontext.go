package workflow

type pathContextTracker struct {
	stack *pathStack

	saved *stackState
}

func newPathContextTracker(stack *pathStack) *pathContextTracker {
	return &pathContextTracker{stack: stack}
}

func (t *pathContextTracker) Grab() interface{} {
	return t.stack.captureState()
}

func (t *pathContextTracker) Resumed(ctx interface{}) {
	prev := t.stack.captureState()
	t.saved = &prev
	if s, ok := ctx.(stackState); ok {
		t.stack.restoreState(s)
	}
}

func (t *pathContextTracker) Exited() {
	if t.saved != nil {
		t.stack.restoreState(*t.saved)
		t.saved = nil
	}
}

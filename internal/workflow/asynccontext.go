package workflow

// Carries the engine's pathStack across every await / .then boundary: without it an agent()
// call after an await reads an empty path. Exited restores the pre-Resumed state.
type pathContextTracker struct {
	stack *pathStack

	saved *stackState
}

func newPathContextTracker(stack *pathStack) *pathContextTracker {
	return &pathContextTracker{stack: stack}
}

// Returns a value-type stackState, not a pointer into the live stack, so the
// bound context is immune to later mutation.
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

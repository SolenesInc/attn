//go:build (darwin && arm64) || (linux && amd64) || (linux && arm64)

package ghosttyvt

/*
#include <ghostty/vt.h>

static GhosttyPoint ghosttyvt_point(GhosttyPointTag tag, uint16_t x, uint32_t y) {
	GhosttyPoint p;
	p.value._padding[0] = 0;
	p.value._padding[1] = 0;
	p.tag = tag;
	p.value.coordinate.x = x;
	p.value.coordinate.y = y;
	return p;
}
*/
import "C"

import "sync/atomic"

// Block-table tests assert this returns to baseline at teardown, so a missed Free on any retirement path is a red test, not a native leak.
var liveTrackedRefs atomic.Int64

func LiveTrackedRefs() int { return int(liveTrackedRefs.Load()) }

// The handle stays freeable after the owning Terminal dies; it just reports no value.
type TrackedRef struct {
	ref C.GhosttyTrackedGridRef
}

func (t *Terminal) TrackCursor() *TrackedRef {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return nil
	}
	cx, cy := t.cursorXYLocked()
	return trackLocked(t.term, C.GHOSTTY_POINT_TAG_ACTIVE, cx, cy)
}

func trackLocked(term C.GhosttyTerminal, tag C.GhosttyPointTag, x, y int) *TrackedRef {
	p := C.ghosttyvt_point(tag, C.uint16_t(x), C.uint32_t(y))
	var ref C.GhosttyTrackedGridRef
	if rc := C.ghostty_terminal_grid_ref_track(term, p, &ref); rc != C.GHOSTTY_SUCCESS {
		return nil
	}
	liveTrackedRefs.Add(1)
	return &TrackedRef{ref: ref}
}

// Callers must synchronize with Terminal writes externally.
func (r *TrackedRef) ScreenPoint() (x, y int, ok bool) {
	if r.ref == nil {
		return 0, 0, false
	}
	var out C.GhosttyPointCoordinate
	if rc := C.ghostty_tracked_grid_ref_point(r.ref, C.GHOSTTY_POINT_TAG_SCREEN, &out); rc != C.GHOSTTY_SUCCESS {
		return 0, 0, false
	}
	return int(out.x), int(out.y), true
}

func (r *TrackedRef) Free() {
	if r.ref == nil {
		return
	}
	C.ghostty_tracked_grid_ref_free(r.ref)
	r.ref = nil
	liveTrackedRefs.Add(-1)
}

// SCREEN-space cell: scrollback + active area, 0-indexed from the top of retained scrollback — the space AttachBlockData rows resolve in.
func (t *Terminal) TrackPoint(x, y int) *TrackedRef {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed || x < 0 || y < 0 {
		return nil
	}
	return trackLocked(t.term, C.GHOSTTY_POINT_TAG_SCREEN, x, y)
}

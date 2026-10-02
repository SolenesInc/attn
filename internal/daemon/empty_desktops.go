package daemon

import (
	"os"
	"sync"
	"time"
)

// emptyDesktopGrace outlasts a launch picked from an empty desktop's launcher after the user moves on.
// Tests that cannot fake the clock shorten it with ATTN_EMPTY_DESKTOP_GRACE.
const emptyDesktopGrace = 30 * time.Second

// emptyDesktopRemoval holds one timer, armed for the earliest due removal and only while one is due.
type emptyDesktopRemoval struct {
	grace time.Duration
	mu    sync.Mutex
	timer *time.Timer
	due   time.Time
}

func emptyDesktopGraceFromEnv() time.Duration {
	if grace, err := time.ParseDuration(os.Getenv("ATTN_EMPTY_DESKTOP_GRACE")); err == nil && grace >= 0 {
		return grace
	}
	return emptyDesktopGrace
}

func (d *Daemon) desktopEmptied(at time.Time) {
	d.scheduleEmptyDesktopRemoval(at.Add(d.emptyDesktops.grace))
}

func (d *Daemon) scheduleEmptyDesktopRemoval(due time.Time) {
	r := &d.emptyDesktops
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.timer != nil {
		if !due.Before(r.due) {
			return
		}
		r.timer.Stop()
	}
	r.due = due
	r.timer = d.life.AfterFunc("removeEmptyDesktops", time.Until(due), d.removeEmptyDesktops)
}

func (d *Daemon) removeEmptyDesktops() {
	r := &d.emptyDesktops
	r.mu.Lock()
	r.timer = nil
	r.mu.Unlock()
	profileIDs, next, err := d.store.RemoveEmptyDesktops(r.grace)
	if err != nil {
		d.logf("removing empty desktops: %v", err)
		return
	}
	for _, profileID := range profileIDs {
		d.publishArrangementChanged(profileID)
	}
	if !next.IsZero() {
		d.scheduleEmptyDesktopRemoval(next)
	}
}

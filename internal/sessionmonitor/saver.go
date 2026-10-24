package sessionmonitor

import (
	"time"

	"github.com/victorarias/attn/internal/jobs"
	"github.com/victorarias/attn/internal/protocol"
)

type Saver interface {
	SaveRuntime(session protocol.SessionID, saved, view View) error
}

type savedView struct {
	view       View
	retry      *time.Timer
	retryDelay time.Duration
}

func (e *Entry) Session() protocol.SessionID {
	if e == nil {
		return ""
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.session
}

func (e *Entry) publish(view View) bool {
	e.mu.Lock()
	stopped := e.stopped
	e.mu.Unlock()
	if stopped || !e.save(view) {
		return false
	}
	return e.home.Applied(view)
}

func (e *Entry) save(view View) bool {
	saver := e.registry.saver
	e.mu.Lock()
	session := e.session
	e.mu.Unlock()
	if saver == nil || session == "" {
		return true
	}
	e.saveMu.Lock()
	defer e.saveMu.Unlock()
	e.mu.Lock()
	saved, stopped := e.saved.view, e.stopped
	e.mu.Unlock()
	if stopped {
		return false
	}
	if saved.Revision.V >= view.Revision.V {
		return true
	}
	err := saver.SaveRuntime(session, saved, view)
	e.mu.Lock()
	defer e.mu.Unlock()
	if err != nil {
		e.logf("monitor save failed: session=%s: %v", session, err)
		e.retrySaveLocked()
		return false
	}
	e.saved.view = view
	e.stopSaveRetryLocked()
	return true
}

func (e *Entry) retrySaveLocked() {
	host := e.registry.host
	if e.saved.retry != nil || e.stopped || host.Ended() {
		return
	}
	if e.saved.retryDelay == 0 {
		e.saved.retryDelay = jobs.DefaultBackoffBase
	} else {
		e.saved.retryDelay = min(e.saved.retryDelay*2, jobs.DefaultBackoffCap)
	}
	var timer *time.Timer
	timer = host.AfterFunc("monitorSaveRetry", e.saved.retryDelay, func() {
		e.mu.Lock()
		if e.saved.retry != timer {
			e.mu.Unlock()
			return
		}
		e.saved.retry = nil
		e.mu.Unlock()
		e.Refresh()
	})
	e.saved.retry = timer
}

func (e *Entry) stopSaveRetryLocked() {
	if e.saved.retry != nil {
		e.saved.retry.Stop()
		e.saved.retry = nil
	}
	e.saved.retryDelay = 0
}

func (e *Entry) Refresh() bool {
	if e == nil {
		return false
	}
	e.mu.Lock()
	view := e.viewLocked()
	e.mu.Unlock()
	return e.publish(view)
}

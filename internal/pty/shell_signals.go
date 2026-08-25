package pty

import (
	"fmt"
	"sync"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

const (
	// shellForegroundPollInterval: one ioctl per second per shell pane; the
	// resolver ticks at the same rate, so polling faster buys nothing.
	shellForegroundPollInterval = time.Second

	shellCommandDetailLimit = 80
)

type shellSignalArbiter struct {
	mu sync.Mutex

	shellPgid int

	seg feedSegmenter

	markerAtPrompt   bool
	ownerPgid        int
	lastPolledFgPgid int

	lastClaim string
	lastEmit  time.Time
}

func newShellSignalArbiter(shellPgid int) *shellSignalArbiter {
	return &shellSignalArbiter{shellPgid: shellPgid}
}

func (a *shellSignalArbiter) ObservePoll(fgPgid int, now time.Time) (Observation, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.lastPolledFgPgid = fgPgid

	var claim, detail string
	switch {
	case fgPgid == a.shellPgid:
		claim, detail = claimNotBusy, "shell at prompt"
		a.markerAtPrompt, a.ownerPgid = false, 0
	case a.markerAtPrompt && fgPgid == a.ownerPgid:
		claim, detail = claimNotBusy, "inner shell at prompt"
	default:
		claim, detail = claimBusy, "foreground command running"
		a.markerAtPrompt, a.ownerPgid = false, 0
	}
	return a.emit(claim, detail, now)
}

func (a *shellSignalArbiter) ObserveOutput(chunk []byte, now time.Time) []Observation {
	if a == nil || len(chunk) == 0 {
		return nil
	}
	var out []Observation
	// Marker handling shares claim state with the poller, so hold the lock across it.
	a.mu.Lock()
	defer a.mu.Unlock()
	a.seg.Feed(chunk, func(seg feedSegment) {
		if seg.Marker == nil {
			return
		}
		if obs, ok := a.observeMarker(seg.Marker, now); ok {
			out = append(out, obs)
		}
	})
	return out
}

// observeMarker folds one marker into the merged claim. Caller holds mu.
func (a *shellSignalArbiter) observeMarker(m *osc133Marker, now time.Time) (Observation, bool) {
	switch m.Kind {
	case osc133PreExec:
		a.markerAtPrompt, a.ownerPgid = false, 0
		detail := "command started"
		if m.Cmdline != nil && *m.Cmdline != "" {
			detail = "command started: " + truncateDetail(*m.Cmdline, shellCommandDetailLimit)
		}
		return a.emitEdge(claimBusy, detail, now), true
	case osc133CommandEnd:
		a.bindPromptVerdict()
		detail := "command finished"
		if m.ExitCode != nil {
			detail = fmt.Sprintf("command exited %d", *m.ExitCode)
		}
		return a.emitEdge(claimNotBusy, detail, now), true
	case osc133PromptStart:
		a.bindPromptVerdict()
		return a.emit(claimNotBusy, "shell at prompt", now)
	default:
		return Observation{}, false
	}
}

func (a *shellSignalArbiter) bindPromptVerdict() {
	a.markerAtPrompt = true
	a.ownerPgid = 0
	if a.lastPolledFgPgid != 0 && a.lastPolledFgPgid != a.shellPgid {
		a.ownerPgid = a.lastPolledFgPgid
	}
}

// emit applies the change-or-keepalive rule. Caller holds mu.
func (a *shellSignalArbiter) emit(claim, detail string, now time.Time) (Observation, bool) {
	if claim == a.lastClaim && now.Sub(a.lastEmit) < heartbeatKeepalive {
		return Observation{}, false
	}
	return a.emitEdge(claim, detail, now), true
}

// emitEdge emits unconditionally, still feeding the dedup state so a level
// restate right after an edge stays suppressed. Caller holds mu.
func (a *shellSignalArbiter) emitEdge(claim, detail string, now time.Time) Observation {
	a.lastClaim = claim
	a.lastEmit = now
	return newObservation(SourceHeartbeat, claim, detail, now)
}

func truncateDetail(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	return s[:limit] + "…"
}

func (s *Session) runShellForegroundPoller(interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-s.exited:
			return
		case <-ticker.C:
			fgPgid, ok := s.foregroundProcessGroup()
			if !ok {
				continue
			}
			if obs, ok := s.shellSignals.ObservePoll(fgPgid, time.Now()); ok {
				s.emitSignal(obs)
			}
		}
	}
}

func (s *Session) childProcessGroup() int {
	pid := s.child.processID()
	if pgid, err := syscall.Getpgid(pid); err == nil && pgid > 0 {
		return pgid
	}
	return pid
}

// foregroundProcessGroup reads which group owns the terminal's foreground. Takes
// writeMu for the same reason resize does: Fd() must not race the ptmx close.
func (s *Session) foregroundProcessGroup() (int, bool) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if s.ptmxClosed || s.ptmx == nil {
		return 0, false
	}
	var pgid int
	err := s.withPTMXFd(func(fd uintptr) error {
		var ioctlErr error
		pgid, ioctlErr = unix.IoctlGetInt(int(fd), unix.TIOCGPGRP)
		return ioctlErr
	})
	if err != nil || pgid <= 0 {
		return 0, false
	}
	return pgid, true
}

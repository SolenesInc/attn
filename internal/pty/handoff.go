package pty

import (
	"errors"
	"fmt"
	"syscall"
	"time"

	"github.com/victorarias/attn/internal/ghosttyvt"
)

// In-place worker upgrade, PTY half. A worker whose terminal library no longer
// matches the app replaces its own binary with execve, which keeps the pid —
// and therefore keeps the agent as ITS child, so waiting on the child still
// yields a status. What the new image cannot inherit is the parsed terminal:
// the model lives in the old image's memory and the binary snapshot format is
// exactly what the upgrade is moving away from. So the screen crosses as plain
// VT (ghosttyvt.HandoffVT), and everything else the session knows about itself
// crosses as this struct. Receipts, including what does NOT cross:
// docs/plans/2026-08-22-worker-inplace-upgrade.md.

// quiesceTimeout bounds the wait for the read loop to stop. It is a tripwire,
// not a budget: the loop stops as soon as the deadline ends its pending read,
// measured at 38µs on a session under a 15MB/s stream. Anything near a second
// means the loop is wedged, and a wedged loop must fail the upgrade rather
// than exec on top of a terminal nobody finished writing to.
const quiesceTimeout = 2 * time.Second

var (
	// ErrSessionExited says the child died before the handoff could take it.
	ErrSessionExited = errors.New("session already exited")
	// ErrHandoffInProgress guards a second concurrent handoff of one session.
	ErrHandoffInProgress = errors.New("handoff already in progress")
)

// HandoffState is everything the next worker image needs to keep a session it
// did not spawn. Kitty images are the one deliberate loss: the VT dump carries
// no image data, and an image scrolled into history has no cell left to be
// placed at.
type HandoffState struct {
	SessionID string
	CWD       string
	Agent     string

	// ChildPID stays our child across the exec, which is what keeps Wait.
	ChildPID int
	// PtmxFD is dup'd with CLOEXEC cleared, so it survives into the new image.
	PtmxFD int

	Cols   uint16
	Rows   uint16
	CellW  uint16
	CellH  uint16
	PixelW uint16
	PixelH uint16

	// VTDump replays the whole screen, scrollback included, into a terminal of
	// any libghostty-vt version.
	VTDump []byte
	// Carryover is the tail of PTY output the read loop was holding at an
	// unfinished escape: never applied, never sent, so the new image feeds it
	// as its first bytes.
	Carryover []byte
	// Blocks are OSC 133 command blocks resolved to screen rows. A VT replay
	// rebuilds none of them, so the new image re-pins these rows.
	Blocks []AttachBlockData
	// LastSeq continues the attach stream instead of restarting it.
	LastSeq uint32

	Theme TerminalTheme
	// ReportedScheme is the light/dark answer the child was last told, and
	// SchemeReportsEnabled whether it subscribed (DECSET 2031). Both carry so
	// the new image does not re-announce a scheme, or send a report to a child
	// that never asked for one.
	ReportedScheme       int
	SchemeReportsEnabled bool

	// LastSignal is the most recent state observation, kept so a session that
	// is parked at its prompt (writing nothing) still has a readable level.
	LastSignal *Observation

	StartedAt time.Time
	// CleanupDir is the shell-startup overlay whose removal moves with the
	// session.
	CleanupDir string
}

// Handoff stops a session's read loop at a chunk boundary, captures everything
// the next image needs, and drops the session from this manager. The PTY master
// and the child survive in the returned state; this image's copy of the
// terminal does not. There is no resume: dumping the screen consumes it.
//
// A failure AFTER the read loop has stopped leaves a session nothing can read
// again, so it is killed rather than left running with nobody listening.
func (m *Manager) Handoff(sessionID string) (HandoffState, error) {
	session, err := m.getSession(sessionID)
	if err != nil {
		return HandoffState{}, err
	}
	state, err := session.handoff()
	if err != nil {
		if session.hasQuiesced() {
			m.forget(sessionID)
			_ = session.kill(syscall.SIGTERM, defaultKillTimeout)
			session.closePTY()
			return HandoffState{}, fmt.Errorf("handoff of session %s failed after its read loop stopped; session killed: %w", sessionID, err)
		}
		// Nothing moved: the loop is still reading and a later attempt is free
		// to try again.
		session.quiescing.Store(false)
		return HandoffState{}, err
	}
	m.forget(sessionID)
	session.closePTY()
	return state, nil
}

// forget drops a session from the manager without touching its PTY or child.
func (m *Manager) forget(sessionID string) {
	m.mu.Lock()
	delete(m.sessions, sessionID)
	m.mu.Unlock()
}

// hasQuiesced reports whether the read loop stopped for a handoff.
func (s *Session) hasQuiesced() bool {
	select {
	case <-s.quiesced:
		return true
	default:
		return false
	}
}

func (s *Session) handoff() (HandoffState, error) {
	if !s.quiescing.CompareAndSwap(false, true) {
		return HandoffState{}, ErrHandoffInProgress
	}
	s.exitMu.RLock()
	running := s.running
	s.exitMu.RUnlock()
	if !running {
		return HandoffState{}, ErrSessionExited
	}

	// A deadline in the past ends the read blocked in the kernel without
	// consuming anything; what the reader already pulled comes back with the
	// error and is applied before the loop stops. See ptmx.go.
	if err := s.ptmx.SetReadDeadline(time.Now().Add(-time.Second)); err != nil {
		return HandoffState{}, fmt.Errorf("stop read loop: %w", err)
	}
	select {
	case <-s.quiesced:
	case <-s.exited:
		return HandoffState{}, ErrSessionExited
	case <-time.After(quiesceTimeout):
		return HandoffState{}, fmt.Errorf("read loop did not stop within %s", quiesceTimeout)
	}

	state := HandoffState{
		SessionID:  s.id,
		CWD:        s.cwd,
		Agent:      s.agent,
		ChildPID:   s.child.processID(),
		Carryover:  s.handoffCarryover,
		StartedAt:  s.startedAt,
		CleanupDir: s.cleanupDir,
	}
	s.metaMu.RLock()
	state.Cols, state.Rows = s.cols, s.rows
	state.CellW, state.CellH = s.cellW, s.cellH
	state.PixelW, state.PixelH = s.pixelW, s.pixelH
	s.metaMu.RUnlock()
	s.themeMu.RLock()
	state.Theme = s.theme
	state.ReportedScheme = int(s.reportedScheme)
	s.themeMu.RUnlock()
	state.SchemeReportsEnabled = s.colorSchemeReports.Load()
	if obs, ok := s.LastSignal(); ok {
		state.LastSignal = &obs
	}

	// One hold, like the attach snapshot: the dump, the block rows, and the
	// watermark describe the same terminal.
	s.replayMu.Lock()
	if s.ghostty != nil {
		dump := s.ghostty.HandoffVT()
		state.VTDump = dump.VTDump
	}
	if s.wireFeed != nil {
		state.Blocks = s.wireFeed.snapshotBlocks()
	}
	state.LastSeq = s.lastReplaySeq
	s.replayMu.Unlock()

	// dup(2) returns a descriptor without CLOEXEC — the one thing that carries
	// the master past execve. The session's own file is left alone; the image
	// it belongs to is about to be replaced. Through withPTMXFd, because
	// File.Fd() would clear O_NONBLOCK on the very file description the dup'd
	// descriptor shares and inherits.
	var fd int
	s.writeMu.Lock()
	err := s.withPTMXFd(func(master uintptr) error {
		dup, dupErr := syscall.Dup(int(master))
		if dupErr != nil {
			return dupErr
		}
		fd = dup
		return nil
	})
	s.writeMu.Unlock()
	if err != nil {
		return HandoffState{}, fmt.Errorf("dup pty master for handoff: %w", err)
	}
	state.PtmxFD = fd
	return state, nil
}

// Adopt rebuilds a session around an inherited PTY master and child pid: the
// other half of Handoff, run by the image that replaced the one that made it.
func (m *Manager) Adopt(st HandoffState) error {
	if st.SessionID == "" {
		return errors.New("missing session id")
	}
	if st.ChildPID <= 0 {
		return fmt.Errorf("session %s: missing child pid", st.SessionID)
	}
	if st.PtmxFD <= 0 {
		return fmt.Errorf("session %s: missing pty master fd", st.SessionID)
	}
	if st.Cols == 0 {
		st.Cols = 80
	}
	if st.Rows == 0 {
		st.Rows = 24
	}

	m.mu.Lock()
	if _, exists := m.sessions[st.SessionID]; exists {
		m.mu.Unlock()
		return fmt.Errorf("session %s already exists", st.SessionID)
	}
	m.mu.Unlock()

	ptmx, err := adoptPTMX(st.PtmxFD)
	if err != nil {
		return fmt.Errorf("adopt session %s: %w", st.SessionID, err)
	}

	session := &Session{
		id:          st.SessionID,
		cwd:         st.CWD,
		agent:       st.Agent,
		cols:        st.Cols,
		rows:        st.Rows,
		cellW:       st.CellW,
		cellH:       st.CellH,
		pixelW:      st.PixelW,
		pixelH:      st.PixelH,
		ptmx:        ptmx,
		child:       &childProcess{pid: st.ChildPID},
		subscribers: make(map[string]*sessionSubscriber),
		running:     true,
		exited:      make(chan struct{}),
		quiesced:    make(chan struct{}),
		startedAt:   st.StartedAt,
		theme:       st.Theme,
		cleanupDir:  st.CleanupDir,
	}
	if session.startedAt.IsZero() {
		session.startedAt = time.Now()
	}
	session.initialCarryover = st.Carryover
	session.reportedScheme = colorScheme(st.ReportedScheme)
	session.colorSchemeReports.Store(st.SchemeReportsEnabled)
	if st.LastSignal != nil {
		obs := *st.LastSignal
		session.lastSignal = &obs
	}

	kittyLimit := kittyStorageLimit(m.logf)
	gt, err := ghosttyvt.New(int(st.Cols), int(st.Rows), ghosttyvt.Options{
		KittyImageStorageLimit: kittyLimit,
	})
	if err != nil {
		_ = ptmx.Close()
		return fmt.Errorf("adopt session %s: ghostty terminal construction failed: %w", st.SessionID, err)
	}
	session.ghostty = gt
	if err := session.SetTheme(st.Theme); err != nil {
		gt.Close()
		_ = ptmx.Close()
		return fmt.Errorf("adopt session %s: ghostty terminal theme failed: %w", st.SessionID, err)
	}
	if st.CellW > 0 && st.CellH > 0 {
		gt.SetCellPixelSize(int(st.CellW), int(st.CellH))
	}
	// Replay before the wire feeder exists: the feeder's kitty baseline is read
	// at construction, and these bytes are ours, not the child's — nothing on
	// the wire, nothing observed.
	gt.Write(st.VTDump)
	gt.DrainResponses()

	// A NEW epoch, deliberately: the old image's images did not cross, and a
	// client holding pixels from the generation before the upgrade must not
	// draw them (see mintKittyEpoch).
	session.kittyEpoch = mintKittyEpoch()
	session.wireFeed = newWireFeeder(gt, session.kittyEpoch, m.logf, kittyLimit)
	if session.wireFeed != nil && len(st.Blocks) > 0 {
		session.replayMu.Lock()
		session.wireFeed.restoreBlocks(st.Blocks)
		session.replayMu.Unlock()
	}
	// Continue the attach stream rather than restarting it: a client that
	// reconnects dedups on seq > last_seq.
	session.seqCounter.Store(st.LastSeq)
	session.lastReplaySeq = st.LastSeq

	m.logf("pty adopt: id=%s agent=%s pid=%d dump=%dB carryover=%dB blocks=%d last_seq=%d",
		st.SessionID, session.agent, st.ChildPID, len(st.VTDump), len(st.Carryover), len(st.Blocks), st.LastSeq)
	// No lifecycle id: on the worker path it lives in ptybackend, and pty never
	// sees one to carry across.
	m.start(session, "")
	return nil
}

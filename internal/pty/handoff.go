package pty

import (
	"errors"
	"fmt"
	"syscall"
	"time"

	"github.com/victorarias/attn/internal/ghosttyvt"
)

// Design: docs/plans/2026-08-22-worker-inplace-upgrade.md

// quiesceTimeout is a tripwire, not a budget: quiescing measured at 38µs under a
// 15MB/s stream, so anything near a second means the read loop is wedged.
const quiesceTimeout = 2 * time.Second

var (
	ErrSessionExited     = errors.New("session already exited")
	ErrHandoffInProgress = errors.New("handoff already in progress")
)

type HandoffState struct {
	SessionID string
	CWD       string
	Agent     string

	ChildPID int
	PtmxFD   int

	Cols   uint16
	Rows   uint16
	CellW  uint16
	CellH  uint16
	PixelW uint16
	PixelH uint16

	VTDump []byte
	// Carryover was never applied nor sent: the new image feeds it as its first bytes.
	Carryover []byte
	// A VT replay rebuilds no OSC 133 block, so Blocks must carry them.
	Blocks  []AttachBlockData
	LastSeq uint32

	Theme TerminalTheme
	// Carried so the new image neither re-announces a scheme nor reports to a
	// child that never asked (DECSET 2031).
	ReportedScheme       int
	SchemeReportsEnabled bool

	LastSignal *Observation

	StartedAt  time.Time
	CleanupDir string
}

// No resume: dumping the screen consumes it, so a late failure kills the session.
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
		session.quiescing.Store(false)
		return HandoffState{}, err
	}
	m.forget(sessionID)
	session.closePTY()
	return state, nil
}

func (m *Manager) forget(sessionID string) {
	m.mu.Lock()
	delete(m.sessions, sessionID)
	m.mu.Unlock()
}

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

	// A deadline in the past ends the blocked read without consuming anything.
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

	// One hold: dump, block rows, and watermark must describe the same terminal.
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

	// dup(2) returns a descriptor without CLOEXEC, which carries the master past execve.
	// Through withPTMXFd, because File.Fd() would clear O_NONBLOCK on the description.
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
	// Replay before the wire feeder exists: it reads its kitty baseline at construction.
	gt.Write(st.VTDump)
	gt.DrainResponses()

	// A new epoch, deliberately: pixels held from before the upgrade must not draw.
	session.kittyEpoch = mintKittyEpoch()
	session.wireFeed = newWireFeeder(gt, session.kittyEpoch, m.logf, kittyLimit)
	if session.wireFeed != nil && len(st.Blocks) > 0 {
		session.replayMu.Lock()
		session.wireFeed.restoreBlocks(st.Blocks)
		session.replayMu.Unlock()
	}
	// Continue the attach stream: a reconnecting client dedups on seq > last_seq.
	session.seqCounter.Store(st.LastSeq)
	session.lastReplaySeq = st.LastSeq

	m.logf("pty adopt: id=%s agent=%s pid=%d dump=%dB carryover=%dB blocks=%d last_seq=%d",
		st.SessionID, session.agent, st.ChildPID, len(st.VTDump), len(st.Carryover), len(st.Blocks), st.LastSeq)
	m.start(session, "")
	return nil
}

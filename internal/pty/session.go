package pty

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"strconv"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/victorarias/attn/internal/buildinfo"
	"github.com/victorarias/attn/internal/ghosttyvt"
)

// Colors are "#rrggbb"; a zero-value field falls back to the dark default.
type TerminalTheme struct {
	Foreground  string
	Background  string
	Cursor      string
	ANSIPalette [16]string
}

const (
	defaultThemeForeground = "#d4d4d4"
	defaultThemeBackground = "#1e1e1e"
	defaultThemeCursor     = "#d4d4d4"
)

var infoSnapshotHook func()

var readLoopSeqGapHook func()

var colorSchemeReplyHook func()

type sessionSubscriber struct {
	id           string
	send         func(data []byte, seq uint32) bool
	onDrop       func(reason string)
	onPlacements func(update PlacementUpdate)
}

type terminalQueries struct {
	da1 bool
	cpr bool
	// Occurrences, not presence: each query needs its own reply or the program hangs.
	osc10 int
	osc11 int
	osc12 int
	// In ask order: clients pair replies positionally.
	oscQueryOrder []int
	colorScheme   int
	da1BeforeCPR  bool
}

type Session struct {
	id    string
	cwd   string
	agent string

	resizeMu sync.Mutex
	metaMu   sync.RWMutex
	cols     uint16
	rows     uint16
	// Cell size in device pixels; zero until the first measured fit.
	cellW        uint16
	cellH        uint16
	pixelW       uint16
	pixelH       uint16
	resizeFailed bool

	ptmx  *os.File
	child *childProcess
	// A path, not a closure: an in-place worker upgrade hands it to the image
	// that adopts the session.
	cleanupDir string

	ghostty *ghosttyvt.Terminal
	// wireFeed owns writes into ghostty and returns the bytes the wire carries
	// instead. nil exactly when ghostty is nil; every use is nil-guarded.
	wireFeed *wireFeeder
	// wireFeed holds the same epoch for the placement half; this one serves
	// kittyImage, and the two must fold the same value.
	kittyEpoch uint64
	seqCounter atomic.Uint32

	// replayMu makes ghostty feeds and lastReplaySeq atomic for snapshots, so a
	// chunk landing between payload and watermark is never dropped.
	replayMu      sync.Mutex
	lastReplaySeq uint32

	subMu       sync.RWMutex
	subscribers map[string]*sessionSubscriber

	// writeMu guards every ptmx access that is not a Read: Fd() must not race
	// Close, and ptmxClosed makes a late caller a no-op rather than a dead fd.
	writeMu    sync.Mutex
	ptmxClosed bool

	themeMu        sync.RWMutex
	theme          TerminalTheme
	reportedScheme colorScheme
	// A child that never subscribed with DECSET 2031 must receive no report.
	colorSchemeReports atomic.Bool

	// Both read off the RAW stream and alter no bytes. shellSignals is nil for
	// non-shell agents.
	harnessSignals *harnessSignalObserver
	shellSignals   *shellSignalArbiter
	onState        func(obs Observation)

	// Kept so a restarted daemon can read the level: an agent parked at its
	// prompt writes nothing, so no evidence would arrive until the user typed.
	lastSignalMu sync.RWMutex
	lastSignal   *Observation

	// Carryovers are unfinished escapes: what the loop held at quiesce, and what
	// an adopted session starts with. See handoff.go.
	quiescing        atomic.Bool
	quiesced         chan struct{}
	handoffCarryover []byte
	initialCarryover []byte

	exitMu     sync.RWMutex
	running    bool
	exitCode   *int
	exitSignal *string
	exited     chan struct{}
	exitOnce   sync.Once
	startedAt  time.Time
}

func (s *Session) addSubscriber(subID string, send func([]byte, uint32) bool, onDrop func(reason string), opts ...SubscriberOption) {
	sub := &sessionSubscriber{
		id:     subID,
		send:   send,
		onDrop: onDrop,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(sub)
		}
	}
	s.subMu.Lock()
	defer s.subMu.Unlock()
	s.subscribers[subID] = sub
}

func (s *Session) removeSubscriber(subID string) {
	s.subMu.Lock()
	defer s.subMu.Unlock()
	delete(s.subscribers, subID)
}

func (s *Session) fanOut(data []byte, seq uint32) {
	s.subMu.RLock()
	if len(s.subscribers) == 0 {
		s.subMu.RUnlock()
		return
	}
	subs := make([]*sessionSubscriber, 0, len(s.subscribers))
	for _, sub := range s.subscribers {
		subs = append(subs, sub)
	}
	s.subMu.RUnlock()

	payload := append([]byte(nil), data...)
	var dropIDs []string
	for _, sub := range subs {
		if sub.send == nil {
			continue
		}
		if !sub.send(payload, seq) {
			dropIDs = append(dropIDs, sub.id)
			if sub.onDrop != nil {
				sub.onDrop("buffer_overflow")
			}
		}
	}

	if len(dropIDs) > 0 {
		s.subMu.Lock()
		for _, id := range dropIDs {
			delete(s.subscribers, id)
		}
		s.subMu.Unlock()
	}
}

// Call AFTER the chunk's bytes are fanned out and with replayMu released: the
// update describes the grid those bytes produce, so it must not arrive first.
func (s *Session) fanOutPlacements(update PlacementUpdate) {
	s.subMu.RLock()
	var subs []*sessionSubscriber
	for _, sub := range s.subscribers {
		if sub.onPlacements != nil {
			subs = append(subs, sub)
		}
	}
	s.subMu.RUnlock()

	for _, sub := range subs {
		sub.onPlacements(update)
	}
}

// Call with replayMu released; the callbacks take their own locks.
func (s *Session) forceResync(reason string) {
	s.subMu.Lock()
	subs := make([]*sessionSubscriber, 0, len(s.subscribers))
	for id, sub := range s.subscribers {
		subs = append(subs, sub)
		delete(s.subscribers, id)
	}
	s.subMu.Unlock()

	for _, sub := range subs {
		if sub.onDrop != nil {
			sub.onDrop(reason)
		}
	}
}

// macOS pty reads return ~100-byte chunks under load, and message COUNT, not
// byte volume, balloons the WebKit frontend.
const (
	ptyReadBufBytes     = 16 * 1024
	ptyCoalesceMaxBytes = 256 * 1024
	ptyCoalesceWindow   = 5 * time.Millisecond
)

type ptyRead struct {
	data []byte
	err  error
}

// The returned error belongs to the last read folded in; callers must not
// receive after it.
func nextCoalescedRead(reads <-chan ptyRead, maxBytes int, window time.Duration) ([]byte, error) {
	first := <-reads
	if first.err != nil {
		return first.data, first.err
	}

	var batch []byte
	select {
	case r := <-reads:
		batch = append(make([]byte, 0, maxBytes+ptyReadBufBytes), first.data...)
		batch = append(batch, r.data...)
		if r.err != nil {
			return batch, r.err
		}
	default:
		return first.data, nil
	}

	timer := time.NewTimer(window)
	defer timer.Stop()
	for len(batch) < maxBytes {
		select {
		case r := <-reads:
			batch = append(batch, r.data...)
			if r.err != nil {
				return batch, r.err
			}
		case <-timer.C:
			return batch, nil
		}
	}
	return batch, nil
}

func (s *Session) readLoop(onExit func(exitCode int, signal string), logf func(string, ...interface{})) {
	handedOver := false
	defer func() {
		if handedOver {
			return
		}
		s.closePTMX()
		removeShellOverlay(s.cleanupDir)
	}()

	reads := make(chan ptyRead, 4)
	go func() {
		for {
			buf := make([]byte, ptyReadBufBytes)
			n, err := s.ptmx.Read(buf)
			reads <- ptyRead{data: buf[:n], err: err}
			if err != nil {
				return
			}
		}
	}()

	carryover := make([]byte, 0, 64)
	if len(s.initialCarryover) > 0 {
		carryover = append(carryover, s.initialCarryover...)
		s.initialCarryover = nil
	}

	for {
		batch, err := nextCoalescedRead(reads, ptyCoalesceMaxBytes, ptyCoalesceWindow)
		if len(batch) > 0 {
			chunk := make([]byte, len(carryover)+len(batch))
			copy(chunk, carryover)
			copy(chunk[len(carryover):], batch)

			boundary := findSafeBoundary(chunk)
			if boundary < len(chunk) {
				carryover = append(carryover[:0], chunk[boundary:]...)
			} else {
				carryover = carryover[:0]
			}

			if boundary > 0 {
				data := chunk[:boundary]
				queries := detectTerminalQueries(data)

				s.trackColorSchemeReports(data)
				// The worker is the single responder for CPR, DA1 and OSC
				// 10/11/12; the frontend answers none of these.
				if len(queries.oscQueryOrder) > 0 {
					s.writeOSCColorResponses(queries, logf)
				}
				if queries.colorScheme > 0 {
					s.writeColorSchemeResponses(queries.colorScheme, logf)
					if colorSchemeReplyHook != nil {
						colorSchemeReplyHook()
					}
				}

				seq := s.seqCounter.Add(1)
				if readLoopSeqGapHook != nil {
					readLoopSeqGapHook()
				}
				wire, resync := data, ""
				var placements []KittyPlacement
				placementsMoved := false
				s.replayMu.Lock()
				if s.wireFeed != nil {
					// Feed under the same lock as the seq watermark so a snapshot
					// stays atomic with it.
					wire, resync = s.wireFeed.feed(data)
					placements, placementsMoved = s.wireFeed.changedPlacements()
				}
				s.lastReplaySeq = seq
				s.replayMu.Unlock()
				s.drainGhosttyResponses(logf)
				// After the chunk is applied, in ask order: fish sends ESC[6n
				// ESC[0c and blocks its prompt redraw until it gets both.
				if queries.da1BeforeCPR {
					s.writeDeviceAttributesResponse(logf)
					s.writeCursorPositionResponse(logf)
				} else {
					if queries.cpr {
						s.writeCursorPositionResponse(logf)
					}
					if queries.da1 {
						s.writeDeviceAttributesResponse(logf)
					}
				}
				// An empty wire chunk means the feeder holds an unterminated
				// escape; dedup (`seq > last_seq`) tolerates the missing seq.
				if len(wire) > 0 {
					s.fanOut(wire, seq)
				}
				if placementsMoved {
					s.fanOutPlacements(PlacementUpdate{Seq: seq, Placements: placements})
				}
				if resync != "" {
					if logf != nil {
						logf("pty layout resync: session=%s reason=%s", s.id, resync)
					}
					s.forceResync(resync)
				}
				if s.harnessSignals != nil && s.onState != nil {
					for _, obs := range s.harnessSignals.Observe(data, time.Now()) {
						s.emitSignal(obs)
					}
				}
				if s.shellSignals != nil && s.onState != nil {
					for _, obs := range s.shellSignals.ObserveOutput(data, time.Now()) {
						s.emitSignal(obs)
					}
				}
			}
		}
		if err != nil {
			// A deadline we asked for is a handoff, not an ending: the child is
			// NOT reaped, and the rest of the output waits in the kernel.
			if errors.Is(err, os.ErrDeadlineExceeded) && s.quiescing.Load() {
				handedOver = true
				s.handoffCarryover = append([]byte(nil), carryover...)
				close(s.quiesced)
				return
			}
			if !errors.Is(err, io.EOF) && !errors.Is(err, os.ErrClosed) && logf != nil {
				logf("pty read error for session %s: %v", s.id, err)
			}
			break
		}
	}

	if len(carryover) > 0 {
		seq := s.seqCounter.Add(1)
		wire, resync := carryover, ""
		var placements []KittyPlacement
		placementsMoved := false
		s.replayMu.Lock()
		if s.wireFeed != nil {
			wire, resync = s.wireFeed.feed(carryover)
			placements, placementsMoved = s.wireFeed.changedPlacements()
		}
		s.lastReplaySeq = seq
		s.replayMu.Unlock()
		s.drainGhosttyResponses(logf)
		if len(wire) > 0 {
			s.fanOut(wire, seq)
		}
		if placementsMoved {
			s.fanOutPlacements(PlacementUpdate{Seq: seq, Placements: placements})
		}
		if resync != "" {
			if logf != nil {
				logf("pty layout resync: session=%s reason=%s", s.id, resync)
			}
			s.forceResync(resync)
		}
	}

	waitErr := s.child.wait()
	exitCode, signal := parseExitStatus(waitErr)
	s.markExited(exitCode, signal)

	if onExit != nil {
		onExit(exitCode, signal)
	}
}

// Takes replayMu itself; call with it released.
func (s *Session) drainGhosttyResponses(logf func(string, ...interface{})) {
	// The nil check and the drain are one critical section: teardown nils the
	// field under replayMu, so checking outside would drain a freed terminal.
	s.replayMu.Lock()
	var drained []byte
	if s.ghostty != nil {
		drained = s.ghostty.DrainResponses()
	}
	s.replayMu.Unlock()
	if len(drained) == 0 {
		return
	}
	gap := stripScannerOwnedResponses(drained)
	if len(gap) == 0 {
		return
	}
	s.writeMu.Lock()
	_, _ = s.ptmx.Write(gap)
	s.writeMu.Unlock()
	if logf != nil {
		logf("pty ghostty gap reply: session=%s bytes=%d", s.id, len(gap))
	}
}

func stripScannerOwnedResponses(resp []byte) []byte {
	out := make([]byte, 0, len(resp))
	for i := 0; i < len(resp); {
		if resp[i] != 0x1b || i+1 >= len(resp) {
			out = append(out, resp[i])
			i++
			continue
		}
		switch resp[i+1] {
		case '[':
			j := i + 2
			for j < len(resp) && !(resp[j] >= 0x40 && resp[j] <= 0x7e) {
				j++
			}
			if j >= len(resp) {
				out = append(out, resp[i:]...)
				i = len(resp)
				continue
			}
			final := resp[j]
			seq := resp[i : j+1]
			// CPR (R) and DA (c) are the scanner's; keep the rest.
			if final != 'R' && final != 'c' {
				out = append(out, seq...)
			}
			i = j + 1
		case ']':
			j := i + 2
			for j < len(resp) {
				if resp[j] == 0x07 {
					j++
					break
				}
				if resp[j] == 0x1b && j+1 < len(resp) && resp[j+1] == '\\' {
					j += 2
					break
				}
				j++
			}
			seq := resp[i:j]
			if !isOSCColorReport(seq) {
				out = append(out, seq...)
			}
			i = j
		default:
			out = append(out, resp[i], resp[i+1])
			i += 2
		}
	}
	return out
}

func isOSCColorReport(seq []byte) bool {
	const prefixLen = 5 // ESC ] 1 X ;
	if len(seq) < prefixLen || seq[0] != 0x1b || seq[1] != ']' || seq[2] != '1' {
		return false
	}
	return (seq[3] == '0' || seq[3] == '1' || seq[3] == '2') && seq[4] == ';'
}

// An adopted session holds only a pid: the in-place upgrade execve'd the worker
// and kept its pid, so the agent is still this process's child.
type childProcess struct {
	cmd *exec.Cmd // nil when adopted
	pid int
}

func (c *childProcess) processID() int {
	if c == nil {
		return 0
	}
	return c.pid
}

func (c *childProcess) wait() error {
	if c == nil {
		return errors.New("no child process")
	}
	if c.cmd != nil {
		return c.cmd.Wait()
	}
	proc, err := os.FindProcess(c.pid)
	if err != nil {
		return err
	}
	state, err := proc.Wait()
	if err != nil {
		return err
	}
	if state.Success() {
		return nil
	}
	return &exec.ExitError{ProcessState: state}
}

func parseExitStatus(waitErr error) (int, string) {
	if waitErr == nil {
		return 0, ""
	}

	exitErr, ok := waitErr.(*exec.ExitError)
	if !ok {
		return 1, ""
	}

	status, ok := exitErr.Sys().(syscall.WaitStatus)
	if !ok {
		return exitErr.ExitCode(), ""
	}

	if status.Signaled() {
		return -1, status.Signal().String()
	}
	return status.ExitStatus(), ""
}

func (s *Session) emitSignal(obs Observation) {
	s.lastSignalMu.Lock()
	stored := obs
	s.lastSignal = &stored
	s.lastSignalMu.Unlock()
	s.onState(obs)
}

func (s *Session) LastSignal() (Observation, bool) {
	s.lastSignalMu.RLock()
	defer s.lastSignalMu.RUnlock()
	if s.lastSignal == nil {
		return Observation{}, false
	}
	return *s.lastSignal, true
}

func (s *Session) markExited(exitCode int, signal string) {
	s.exitMu.Lock()
	defer s.exitMu.Unlock()

	s.running = false
	s.exitCode = &exitCode
	if signal != "" {
		signalCopy := signal
		s.exitSignal = &signalCopy
	}
	s.exitOnce.Do(func() {
		close(s.exited)
	})
}

func (s *Session) sessionInfo() SessionInfo {
	s.metaMu.RLock()
	cols := s.cols
	rows := s.rows
	s.metaMu.RUnlock()

	s.exitMu.RLock()
	running := s.running
	var exitCode *int
	if s.exitCode != nil {
		val := *s.exitCode
		exitCode = &val
	}
	var exitSignal *string
	if s.exitSignal != nil {
		val := *s.exitSignal
		exitSignal = &val
	}
	s.exitMu.RUnlock()

	s.replayMu.Lock()
	lastSeq := s.lastReplaySeq
	s.replayMu.Unlock()

	return SessionInfo{
		SessionID:  s.id,
		Agent:      s.agent,
		CWD:        s.cwd,
		Running:    running,
		Cols:       cols,
		Rows:       rows,
		PID:        s.child.processID(),
		LastSeq:    lastSeq,
		ExitCode:   exitCode,
		ExitSignal: exitSignal,
	}
}

func (s *Session) subscriptionInfo() AttachInfo {
	metadata := s.sessionInfo()
	return AttachInfo{
		LastSeq:    metadata.LastSeq,
		Cols:       metadata.Cols,
		Rows:       metadata.Rows,
		PID:        metadata.PID,
		Running:    metadata.Running,
		ExitCode:   metadata.ExitCode,
		ExitSignal: metadata.ExitSignal,
	}
}

func (s *Session) info() AttachInfo {
	metadata := s.sessionInfo()

	// Serialize and read the watermark atomically, or a chunk written between
	// the two is lost.
	s.replayMu.Lock()
	var ghosttySnapshot []byte
	// libghostty-vt surfaces no scrollback-truncation flag yet.
	var ghosttyTruncated bool
	if s.ghostty != nil {
		snapshot := s.ghostty.Serialize()
		ghosttySnapshot = snapshot.Payload
	}
	// Same hold: {dump, blocks, placements, watermark} is one atomic quadruple.
	var ghosttyBlocks []AttachBlockData
	var ghosttyPlacements []KittyPlacement
	if s.wireFeed != nil {
		ghosttyBlocks = s.wireFeed.snapshotBlocks()
		ghosttyPlacements, _ = s.wireFeed.snapshotPlacements()
	}
	replayWatermark := s.lastReplaySeq
	s.replayMu.Unlock()

	// After the unlock, or it deadlocks the read loop.
	if infoSnapshotHook != nil {
		infoSnapshotHook()
	}

	// LastSeq is the dedup boundary; screenSnapshot() must report the same
	// covered-chunk semantics or the first live chunk after an attach is lost.
	return AttachInfo{
		LastSeq:                    replayWatermark,
		Cols:                       metadata.Cols,
		Rows:                       metadata.Rows,
		PID:                        metadata.PID,
		Running:                    metadata.Running,
		ExitCode:                   metadata.ExitCode,
		ExitSignal:                 metadata.ExitSignal,
		GhosttySnapshot:            ghosttySnapshot,
		GhosttySnapshotFormat:      snapshotFormat(ghosttySnapshot),
		GhosttyBlocks:              ghosttyBlocks,
		GhosttyPlacements:          ghosttyPlacements,
		GhosttyScrollbackTruncated: ghosttyTruncated,
	}
}

func snapshotFormat(snapshot []byte) string {
	if len(snapshot) == 0 {
		return ""
	}
	return buildinfo.SnapshotFormat
}

// Under replayMu like every terminal read: teardown nils the terminal there.
func (s *Session) kittyImage(imageID uint32) (KittyImage, error) {
	s.replayMu.Lock()
	defer s.replayMu.Unlock()
	if s.ghostty == nil {
		return KittyImage{}, fmt.Errorf("%w: image %d (session has no terminal)", ErrKittyImageNotFound, imageID)
	}
	img, ok := s.ghostty.KittyImage(imageID)
	if !ok {
		return KittyImage{}, fmt.Errorf("%w: image %d", ErrKittyImageNotFound, imageID)
	}
	// readPlacements folds the same epoch; the two halves must agree or the
	// pull repeats forever.
	img.Generation += s.kittyEpoch
	return img, nil
}

// seqCounter would be wrong as the watermark here: the read loop increments it
// BEFORE applying the chunk, so a snapshot in that gap overclaims.
func (s *Session) screenSnapshot() ScreenSnapshotInfo {
	s.metaMu.RLock()
	cols := s.cols
	rows := s.rows
	s.metaMu.RUnlock()

	s.exitMu.RLock()
	running := s.running
	s.exitMu.RUnlock()

	info := ScreenSnapshotInfo{
		Cols:    cols,
		Rows:    rows,
		Running: running,
	}
	s.replayMu.Lock()
	if s.ghostty != nil {
		viewportText := s.ghostty.ViewportText()
		snapshot := s.ghostty.SerializeViewport()
		if snapshot.VTDump != nil {
			info.Screen = &ViewportSnapshot{
				Payload: snapshot.VTDump,
				Text:    viewportText,
				HasText: true,
				Cols:    uint16(snapshot.Cols),
				Rows:    uint16(snapshot.Rows),
			}
		}
	}
	info.LastSeq = s.lastReplaySeq
	s.replayMu.Unlock()
	return info
}

func (s *Session) input(data []byte) error {
	s.exitMu.RLock()
	running := s.running
	s.exitMu.RUnlock()
	if !running {
		return errors.New("session not running")
	}

	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	_, err := s.ptmx.Write(data)
	return err
}

// xpixel/ypixel are the pane's TOTAL device pixels; zero means none, and the
// remembered cell size then implies the totals a reconcile must not blank out.
func (s *Session) resize(cols, rows, xpixel, ypixel uint16) (bool, error) {
	s.resizeMu.Lock()
	defer s.resizeMu.Unlock()

	cellW, cellH := uint16(0), uint16(0)
	if xpixel > 0 && ypixel > 0 && cols > 0 && rows > 0 {
		cellW, cellH = xpixel/cols, ypixel/rows
	}
	s.metaMu.Lock()
	prevCols, prevRows := s.cols, s.rows
	prevCellW, prevCellH := s.cellW, s.cellH
	prevPixelW, prevPixelH := s.pixelW, s.pixelH
	previousFailed := s.resizeFailed
	if cellW == 0 || cellH == 0 {
		cellW, cellH = s.cellW, s.cellH
		if cellW > 0 && cellH > 0 {
			if cols == prevCols && rows == prevRows && prevPixelW > 0 && prevPixelH > 0 {
				// Exact totals, division remainders included, on a same-grid
				// reconcile that carries no new measurement.
				xpixel, ypixel = prevPixelW, prevPixelH
			} else {
				xpixel, ypixel = cols*cellW, rows*cellH
			}
		}
	}
	if !previousFailed && prevCols == cols && prevRows == rows &&
		prevCellW == cellW && prevCellH == cellH &&
		prevPixelW == xpixel && prevPixelH == ypixel {
		s.metaMu.Unlock()
		return false, nil
	}
	s.cols, s.rows = cols, rows
	s.cellW, s.cellH = cellW, cellH
	s.pixelW, s.pixelH = xpixel, ypixel
	s.resizeFailed = false
	s.metaMu.Unlock()
	// No-reflow because every client frame is (app/src/utils/ghosttyResize.ts):
	// row-indexed wire mappings ride on the grids staying equal.
	s.replayMu.Lock()
	if s.ghostty != nil {
		// Before the grid resize, or a size report answers with the old cell.
		if cellW > 0 && cellH > 0 {
			s.ghostty.SetCellPixelSize(int(cellW), int(cellH))
		}
		if prevCols != cols || prevRows != rows {
			s.ghostty.ResizeNoReflow(int(cols), int(rows))
		}
	}
	// A resize produces no output, so no chunk carries the correction and an
	// idle session would never get one.
	var placements []KittyPlacement
	placementsHeld := false
	if s.wireFeed != nil {
		placements, placementsHeld = s.wireFeed.snapshotPlacements()
	}
	seq := s.lastReplaySeq
	s.replayMu.Unlock()

	// The replay watermark, not a fresh seq: no bytes were produced.
	if placementsHeld {
		s.fanOutPlacements(PlacementUpdate{Seq: seq, Placements: placements})
	}

	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if s.ptmxClosed {
		return true, nil
	}
	err := s.withPTMXFd(func(fd uintptr) error {
		return setWinsize(fd, cols, rows, xpixel, ypixel)
	})
	if err != nil {
		s.metaMu.Lock()
		s.resizeFailed = true
		s.metaMu.Unlock()
	}
	return true, err
}

func (s *Session) closePTMX() {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if s.ptmxClosed {
		return
	}
	s.ptmxClosed = true
	_ = s.ptmx.Close()
}

// Interactive shells ignore SIGTERM by design, but every shell honors hangup.
const sigtermToHUPGrace = 2 * time.Second

func (s *Session) kill(sig syscall.Signal, waitTimeout time.Duration) error {
	return s.killWithEscalation(sig, waitTimeout, nil)
}

func (s *Session) killWithEscalation(sig syscall.Signal, waitTimeout time.Duration, onEscalate func(syscall.Signal)) error {
	s.exitMu.RLock()
	running := s.running
	s.exitMu.RUnlock()
	if !running {
		return nil
	}

	pid := s.child.processID()
	if pid <= 0 {
		return errors.New("process unavailable")
	}

	pgid := pid
	if actualPGID, err := syscall.Getpgid(pid); err == nil && actualPGID > 0 {
		pgid = actualPGID
	}

	if err := syscall.Kill(-pgid, sig); err != nil && !errors.Is(err, syscall.ESRCH) {
		return err
	}

	deadline := time.Now().Add(waitTimeout)

	if sig == syscall.SIGTERM {
		grace := sigtermToHUPGrace
		if half := waitTimeout / 2; grace > half {
			grace = half
		}
		select {
		case <-s.exited:
			return nil
		case <-time.After(grace):
			if onEscalate != nil {
				onEscalate(syscall.SIGHUP)
			}
			_ = syscall.Kill(-pgid, syscall.SIGHUP)
		}
	}

	select {
	case <-s.exited:
		return nil
	case <-time.After(time.Until(deadline)):
		if onEscalate != nil {
			onEscalate(syscall.SIGKILL)
		}
		_ = syscall.Kill(-pgid, syscall.SIGKILL)
		<-s.exited
		return nil
	}
}

// Both fields are nil'd under replayMu so an in-flight attach sees absence
// rather than a freed handle.
func (s *Session) closePTY() {
	s.closePTMX()

	s.replayMu.Lock()
	wireFeed, ghostty := s.wireFeed, s.ghostty
	s.wireFeed, s.ghostty = nil, nil
	if wireFeed != nil {
		wireFeed.close()
	}
	if ghostty != nil {
		ghostty.Close()
	}
	s.replayMu.Unlock()
}

func detectTerminalQueries(data []byte) terminalQueries {
	da1Idx := indexDA1Query(data)
	cprIdx := indexCPRQuery(data)
	oscOrder := scanOSCColorQueries(data)
	var osc10, osc11, osc12 int
	for _, code := range oscOrder {
		switch code {
		case 10:
			osc10++
		case 11:
			osc11++
		case 12:
			osc12++
		}
	}
	return terminalQueries{
		colorScheme:   countColorSchemeQueries(data),
		da1:           da1Idx >= 0,
		cpr:           cprIdx >= 0,
		da1BeforeCPR:  da1Idx >= 0 && cprIdx >= 0 && da1Idx < cprIdx,
		osc10:         osc10,
		osc11:         osc11,
		osc12:         osc12,
		oscQueryOrder: oscOrder,
	}
}

func (s *Session) SetTheme(theme TerminalTheme) error {
	if s.ghostty != nil {
		if err := s.ghostty.SetColorTheme(ghosttyColorTheme(theme)); err != nil {
			return err
		}
	}
	s.themeMu.Lock()
	s.theme = theme
	scheme := themeColorScheme(theme)
	changed := scheme != s.reportedScheme
	s.reportedScheme = scheme
	s.themeMu.Unlock()
	if changed && s.colorSchemeReports.Load() {
		s.writeColorSchemeReport(scheme)
	}
	return nil
}

func parseThemeColor(value, fallback string) uint32 {
	if len(value) != 7 || value[0] != '#' {
		value = fallback
	}
	parsed, err := strconv.ParseUint(value[1:], 16, 32)
	if err != nil {
		parsed, _ = strconv.ParseUint(fallback[1:], 16, 32)
	}
	return uint32(parsed)
}

func ghosttyColorTheme(theme TerminalTheme) ghosttyvt.ColorTheme {
	result := ghosttyvt.ColorTheme{
		Foreground:    parseThemeColor(theme.Foreground, defaultThemeForeground),
		Background:    parseThemeColor(theme.Background, defaultThemeBackground),
		Cursor:        parseThemeColor(theme.Cursor, defaultThemeCursor),
		HasForeground: true,
		HasBackground: true,
		HasCursor:     true,
	}
	for i, value := range theme.ANSIPalette {
		if len(value) != 7 || value[0] != '#' {
			return result
		}
		parsed, err := strconv.ParseUint(value[1:], 16, 32)
		if err != nil {
			return result
		}
		result.ANSIPalette[i] = uint32(parsed)
	}
	result.HasANSIPalette = true
	return result
}

func (s *Session) currentTheme() TerminalTheme {
	s.themeMu.RLock()
	defer s.themeMu.RUnlock()
	return s.theme
}

// One reply per query, in ask order: clients pair them positionally.
func (s *Session) writeOSCColorResponses(queries terminalQueries, logf func(string, ...interface{})) {
	theme := s.currentTheme()
	fg := hexColorToOSCValue(theme.Foreground, defaultThemeForeground)
	bg := hexColorToOSCValue(theme.Background, defaultThemeBackground)
	cursor := hexColorToOSCValue(theme.Cursor, defaultThemeCursor)

	s.writeMu.Lock()
	for _, code := range queries.oscQueryOrder {
		switch code {
		case 10:
			_, _ = fmt.Fprintf(s.ptmx, "\x1b]10;%s\x1b\\", fg)
		case 11:
			_, _ = fmt.Fprintf(s.ptmx, "\x1b]11;%s\x1b\\", bg)
		case 12:
			_, _ = fmt.Fprintf(s.ptmx, "\x1b]12;%s\x1b\\", cursor)
		}
	}
	s.writeMu.Unlock()

	if logf != nil {
		logf(
			"pty terminal-query reply: session=%s osc10=%d osc11=%d osc12=%d",
			s.id,
			queries.osc10,
			queries.osc11,
			queries.osc12,
		)
	}
}

type colorScheme int

const (
	colorSchemeUnknown colorScheme = iota
	colorSchemeDark
	colorSchemeLight
)

// WCAG relative luminance with the >= 0.5 cut pi applies to the OSC 11 color it
// falls back to (pi 0.83.0, theme.ts getThemeForRgbColor).
func themeColorScheme(theme TerminalTheme) colorScheme {
	background := theme.Background
	if !isValidHexColor(background) {
		background = defaultThemeBackground
	}
	channel := func(hex string) float64 {
		value, _ := strconv.ParseUint(hex, 16, 32)
		linear := float64(value) / 255
		if linear <= 0.03928 {
			return linear / 12.92
		}
		return math.Pow((linear+0.055)/1.055, 2.4)
	}
	luminance := 0.2126*channel(background[1:3]) + 0.7152*channel(background[3:5]) + 0.0722*channel(background[5:7])
	if luminance >= 0.5 {
		return colorSchemeLight
	}
	return colorSchemeDark
}

// pi asks this before falling back to OSC 11, so an unanswered query leaves it
// on an environment guess.
func (s *Session) writeColorSchemeResponses(count int, logf func(string, ...interface{})) {
	s.themeMu.Lock()
	scheme := themeColorScheme(s.theme)
	s.reportedScheme = scheme
	s.themeMu.Unlock()

	s.writeMu.Lock()
	for i := 0; i < count; i++ {
		_, _ = s.ptmx.Write(colorSchemeReport(scheme))
	}
	s.writeMu.Unlock()

	if logf != nil {
		logf("pty color-scheme reply: session=%s scheme=%d count=%d", s.id, scheme, count)
	}
}

func (s *Session) writeColorSchemeReport(scheme colorScheme) {
	s.writeMu.Lock()
	_, _ = s.ptmx.Write(colorSchemeReport(scheme))
	s.writeMu.Unlock()
}

// The color-palette-notification DSR reply: `CSI ? 997 ; 1 n` dark, `; 2 n` light.
func colorSchemeReport(scheme colorScheme) []byte {
	if scheme == colorSchemeLight {
		return []byte("\x1b[?997;2n")
	}
	return []byte("\x1b[?997;1n")
}

// Last DECSET/DECRST 2031 in the chunk wins.
func (s *Session) trackColorSchemeReports(data []byte) {
	set := bytes.LastIndex(data, []byte("\x1b[?2031h"))
	reset := bytes.LastIndex(data, []byte("\x1b[?2031l"))
	if set < 0 && reset < 0 {
		return
	}
	s.colorSchemeReports.Store(set > reset)
}

func countColorSchemeQueries(data []byte) int {
	return bytes.Count(data, []byte("\x1b[?996n"))
}

// XTerm-style OSC replies want "rgb:RRRR/GGGG/BBBB": each 8-bit channel is its
// hex pair repeated.
func hexColorToOSCValue(value, fallbackHex string) string {
	if !isValidHexColor(value) {
		value = fallbackHex
	}
	r, g, b := value[1:3], value[3:5], value[5:7]
	return fmt.Sprintf("rgb:%s%s/%s%s/%s%s", r, r, g, g, b, b)
}

func isValidHexColor(value string) bool {
	if len(value) != 7 || value[0] != '#' {
		return false
	}
	for i := 1; i < 7; i++ {
		c := value[i]
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			return false
		}
	}
	return true
}

// The daemon is the single CPR responder; the frontend deliberately answers
// none, so there is no double-reply.
func (s *Session) writeCursorPositionResponse(logf func(string, ...any)) {
	row, col := 1, 1
	// Under replayMu: teardown nils the terminal under that lock.
	s.replayMu.Lock()
	if s.ghostty != nil {
		x, y := s.ghostty.CursorPos()
		row, col = y+1, x+1
	}
	s.replayMu.Unlock()
	s.writeMu.Lock()
	_, _ = fmt.Fprintf(s.ptmx, "\x1b[%d;%dR", row, col)
	s.writeMu.Unlock()
	if logf != nil {
		logf("pty cpr reply: session=%s row=%d col=%d", s.id, row, col)
	}
}

// The daemon is the single responder: after a reattach the frontend can be
// mid-remount and miss it, and fish stalls for its ~10s query timeout.
func (s *Session) writeDeviceAttributesResponse(logf func(string, ...any)) {
	// VT100 with Advanced Video Option.
	s.writeMu.Lock()
	_, _ = s.ptmx.Write([]byte("\x1b[?1;2c"))
	s.writeMu.Unlock()
	if logf != nil {
		logf("pty da1 reply: session=%s", s.id)
	}
}

// DA1 is ESC [ c or ESC [ 0 c; DA2 (ESC [ > c) is ignored.
func indexDA1Query(data []byte) int {
	for i := 0; i < len(data)-2; i++ {
		if data[i] != 0x1b || data[i+1] != '[' {
			continue
		}
		j := i + 2
		for j < len(data) && ((data[j] >= '0' && data[j] <= '9') || data[j] == ';') {
			j++
		}
		if j < len(data) && data[j] == 'c' {
			return i
		}
	}
	return -1
}

func indexCPRQuery(data []byte) int {
	for i := 0; i < len(data)-3; i++ {
		if data[i] == 0x1b && data[i+1] == '[' && data[i+2] == '6' && data[i+3] == 'n' {
			return i
		}
	}
	return -1
}

func containsCPRQuery(data []byte) bool { return indexCPRQuery(data) >= 0 }

// An OSC color SET (no "?") never matches these.
var oscColorQueryPrefixes = [...]struct {
	code   int
	prefix []byte
}{
	{10, []byte("\x1b]10;?")},
	{11, []byte("\x1b]11;?")},
	{12, []byte("\x1b]12;?")},
}

func scanOSCColorQueries(data []byte) []int {
	var codes []int
	for i := 0; i < len(data); {
		matched := false
		for _, p := range oscColorQueryPrefixes {
			if i+len(p.prefix) <= len(data) && bytes.Equal(data[i:i+len(p.prefix)], p.prefix) {
				codes = append(codes, p.code)
				i += len(p.prefix)
				matched = true
				break
			}
		}
		if !matched {
			i++
		}
	}
	return codes
}

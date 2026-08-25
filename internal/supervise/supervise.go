package supervise

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type DesiredState string

const (
	DesiredRunning DesiredState = "running"
	DesiredStopped DesiredState = "stopped"
)

// Phase strings are reported verbatim on attn's wire.
type Phase string

const (
	PhaseStarting  Phase = "starting"
	PhaseConnected Phase = "connected"
	PhaseBackoff   Phase = "backoff"
	PhaseStopped   Phase = "stopped"
	PhaseParked    Phase = "parked"
)

var RestartBackoff = []time.Duration{
	250 * time.Millisecond,
	500 * time.Millisecond,
	time.Second,
	2 * time.Second,
	4 * time.Second,
	8 * time.Second,
	16 * time.Second,
	30 * time.Second,
}

const DisconnectGrace = 5 * time.Second

const StableConnection = 60 * time.Second

// Tripwire, not a receipt: at the pinned backoff ten restarts cost 121.75s of waiting.
const DefaultGiveUpAfter = 10

type Exit struct {
	At       time.Time
	ExitCode *int
	Signal   string
	Error    string
}

func (e Exit) String() string {
	detail := strings.TrimSpace(e.Error)
	if detail == "" && e.Signal != "" {
		detail = "signal " + e.Signal
	}
	if detail == "" && e.ExitCode != nil {
		detail = fmt.Sprintf("exit code %d", *e.ExitCode)
	}
	if detail == "" {
		detail = "process exited"
	}
	if e.At.IsZero() {
		return detail
	}
	return fmt.Sprintf("%s: %s", e.At.Format(time.RFC3339), detail)
}

type Snapshot struct {
	Desired        DesiredState
	Phase          Phase
	Generation     uint64
	Running        bool
	Connected      bool
	RestartAttempt int
	StartedAt      time.Time
	ConnectedAt    time.Time
	NextRestartAt  time.Time
	ParkedAt       time.Time
	LastExit       *Exit
}

// ParkedAt is the moment the give-up happened, never the moment it was restored.
type Park struct {
	ParkedAt       time.Time
	RestartAttempt int
	LastExit       *Exit
}

type Process interface {
	Wait() Exit
	Kill() error
}

// Log is closed once StartFunc returns: the launcher must hand it to the child, not keep it.
type StartRequest struct {
	Name       string
	Generation uint64
	Log        io.Writer
}

type StartFunc func(StartRequest) (Process, error)

type Timer interface {
	Stop() bool
}

type Clock interface {
	Now() time.Time
	AfterFunc(time.Duration, func()) Timer
}

type realClock struct{}

func (realClock) Now() time.Time { return time.Now() }
func (realClock) AfterFunc(delay time.Duration, fn func()) Timer {
	return time.AfterFunc(delay, fn)
}

type Options struct {
	Clock  Clock
	LogDir string
	// GiveUpAfter overrides DefaultGiveUpAfter. A negative value never parks.
	GiveUpAfter int
	// OnChange and OnGiveUp are called without the supervisor lock held.
	OnChange func(name string)
	OnGiveUp func(name string, snapshot Snapshot)
	Logf     func(format string, args ...any)
}

type child struct {
	name    string
	start   StartFunc
	desired DesiredState
	phase   Phase

	generation     uint64
	process        Process
	restartAttempt int
	startedAt      time.Time
	connectedAt    time.Time
	nextRestartAt  time.Time
	parkedAt       time.Time
	lastExit       *Exit

	restartTimer    Timer
	disconnectTimer Timer
	stabilityTimer  Timer
}

type Supervisor struct {
	mu          sync.Mutex
	children    map[string]*child
	clock       Clock
	logDir      string
	giveUpAfter int
	onChange    func(string)
	onGiveUp    func(string, Snapshot)
	logf        func(string, ...any)
	shutdown    bool
}

func New(opts Options) *Supervisor {
	clock := opts.Clock
	if clock == nil {
		clock = realClock{}
	}
	giveUpAfter := opts.GiveUpAfter
	if giveUpAfter == 0 {
		giveUpAfter = DefaultGiveUpAfter
	}
	logf := opts.Logf
	if logf == nil {
		logf = func(string, ...any) {}
	}
	return &Supervisor{
		children:    make(map[string]*child),
		clock:       clock,
		logDir:      opts.LogDir,
		giveUpAfter: giveUpAfter,
		onChange:    opts.OnChange,
		onGiveUp:    opts.OnGiveUp,
		logf:        logf,
	}
}

var ErrParked = errors.New("supervise: child is parked")

func (s *Supervisor) Ensure(name string, start StartFunc) error {
	return s.ensure(name, start, true)
}

// Ensure resets the restart budget, so a hot path calling it would make the give-up tripwire unreachable.
// it would make the give-up tripwire unreachable.
func (s *Supervisor) EnsureUnlessParked(name string, start StartFunc) error {
	return s.ensure(name, start, false)
}

func (s *Supervisor) AdoptParked(name string, park Park) error {
	if err := validateName(name); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.shutdown {
		return fmt.Errorf("supervise: supervisor is shut down, cannot adopt %q", name)
	}
	if s.children[name] != nil {
		return fmt.Errorf("supervise: child %q is already supervised, so a persisted park cannot be adopted onto it", name)
	}
	s.children[name] = &child{
		name:           name,
		desired:        DesiredRunning,
		phase:          PhaseParked,
		parkedAt:       park.ParkedAt,
		restartAttempt: park.RestartAttempt,
		lastExit:       copyExit(park.LastExit),
	}
	return nil
}

func (s *Supervisor) ensure(name string, start StartFunc, revive bool) error {
	if err := validateName(name); err != nil {
		return err
	}
	if start == nil {
		return fmt.Errorf("supervise: child %q needs a start function", name)
	}
	s.mu.Lock()
	if s.shutdown {
		s.mu.Unlock()
		return fmt.Errorf("supervise: supervisor is shut down, cannot start %q", name)
	}
	c := s.children[name]
	if c == nil {
		c = &child{name: name, start: start, desired: DesiredRunning, phase: PhaseStarting}
		s.children[name] = c
	} else {
		c.start = start
		c.desired = DesiredRunning
		if c.process != nil || c.restartTimer != nil {
			s.mu.Unlock()
			return nil
		}
		if c.phase == PhaseParked {
			if !revive {
				s.mu.Unlock()
				return ErrParked
			}
			c.restartAttempt = 0
		}
	}
	err := s.spawnLocked(c)
	parked, snapshot := c.phase == PhaseParked, snapshotOf(c)
	s.mu.Unlock()
	if parked {
		s.reportGiveUp(name, snapshot)
	}
	s.notify(name)
	return err
}

// Reset the budget: without it a stop-then-start revives a parked child with none left, making the way back from parked a door that opens once.
func (s *Supervisor) Stop(name string) {
	s.mu.Lock()
	c := s.children[name]
	if c == nil {
		s.mu.Unlock()
		return
	}
	c.desired = DesiredStopped
	c.phase = PhaseStopped
	c.restartAttempt = 0
	c.generation++
	c.connectedAt = time.Time{}
	c.nextRestartAt = time.Time{}
	c.parkedAt = time.Time{}
	stopTimer(&c.restartTimer)
	stopTimer(&c.disconnectTimer)
	stopTimer(&c.stabilityTimer)
	process := c.process
	c.process = nil
	s.mu.Unlock()
	if process != nil {
		_ = process.Kill()
	}
	s.notify(name)
}

func (s *Supervisor) TerminateGeneration(name string, generation uint64) (bool, error) {
	s.mu.Lock()
	c := s.children[name]
	if c == nil || generation == 0 || generation != c.generation || c.desired != DesiredRunning || c.process == nil {
		s.mu.Unlock()
		return false, nil
	}
	process := c.process
	s.mu.Unlock()

	return true, process.Kill()
}

func (s *Supervisor) Shutdown() {
	s.mu.Lock()
	if s.shutdown {
		s.mu.Unlock()
		return
	}
	s.shutdown = true
	names := make([]string, 0, len(s.children))
	for name := range s.children {
		names = append(names, name)
	}
	s.mu.Unlock()
	for _, name := range names {
		s.Stop(name)
	}
}

func (s *Supervisor) NoteConnected(name string, generation uint64) bool {
	s.mu.Lock()
	c := s.children[name]
	if c == nil {
		s.mu.Unlock()
		return true
	}
	if generation == 0 || generation != c.generation || c.desired != DesiredRunning || c.process == nil {
		s.mu.Unlock()
		return false
	}
	c.phase = PhaseConnected
	c.connectedAt = s.clock.Now()
	c.nextRestartAt = time.Time{}
	stopTimer(&c.disconnectTimer)
	stopTimer(&c.stabilityTimer)
	capturedGeneration := c.generation
	c.stabilityTimer = s.clock.AfterFunc(StableConnection, func() {
		s.markStable(name, capturedGeneration)
	})
	s.mu.Unlock()
	s.notify(name)
	return true
}

func (s *Supervisor) NoteDisconnected(name string, generation uint64) {
	s.mu.Lock()
	c := s.children[name]
	if c == nil || generation != c.generation || c.desired != DesiredRunning || c.process == nil {
		s.mu.Unlock()
		return
	}
	c.phase = PhaseStarting
	c.connectedAt = time.Time{}
	stopTimer(&c.stabilityTimer)
	stopTimer(&c.disconnectTimer)
	capturedProcess := c.process
	capturedGeneration := c.generation
	c.disconnectTimer = s.clock.AfterFunc(DisconnectGrace, func() {
		s.disconnectExpired(name, capturedGeneration, capturedProcess)
	})
	s.mu.Unlock()
	s.notify(name)
}

func (s *Supervisor) Snapshot(name string) (Snapshot, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := s.children[name]
	if c == nil {
		return Snapshot{}, false
	}
	return snapshotOf(c), true
}

func snapshotOf(c *child) Snapshot {
	snapshot := Snapshot{
		Desired:        c.desired,
		Phase:          c.phase,
		Generation:     c.generation,
		Running:        c.process != nil,
		Connected:      c.phase == PhaseConnected,
		RestartAttempt: c.restartAttempt,
		StartedAt:      c.startedAt,
		ConnectedAt:    c.connectedAt,
		NextRestartAt:  c.nextRestartAt,
		ParkedAt:       c.parkedAt,
	}
	snapshot.LastExit = copyExit(c.lastExit)
	return snapshot
}

// Deep copy: the supervisor keeps mutating its copy and ExitCode is a pointer.
func copyExit(from *Exit) *Exit {
	if from == nil {
		return nil
	}
	exit := *from
	if from.ExitCode != nil {
		code := *from.ExitCode
		exit.ExitCode = &code
	}
	return &exit
}

func (s *Supervisor) spawnLocked(c *child) error {
	c.generation++
	c.phase = PhaseStarting
	c.nextRestartAt = time.Time{}
	c.parkedAt = time.Time{}
	stopTimer(&c.restartTimer)
	generation := c.generation
	process, err := s.startChild(c, generation)
	if err != nil {
		exit := Exit{At: s.clock.Now(), Error: err.Error()}
		c.lastExit = &exit
		s.scheduleRestartLocked(c)
		return err
	}
	c.process = process
	c.startedAt = s.clock.Now()
	stopTimer(&c.disconnectTimer)
	name := c.name
	capturedProcess := process
	c.disconnectTimer = s.clock.AfterFunc(DisconnectGrace, func() {
		s.disconnectExpired(name, generation, capturedProcess)
	})
	go func(name string, generation uint64, process Process) {
		exit := process.Wait()
		s.processExited(name, generation, process, exit)
	}(name, generation, process)
	return nil
}

func (s *Supervisor) startChild(c *child, generation uint64) (Process, error) {
	req := StartRequest{Name: c.name, Generation: generation}
	if file := s.openLog(c.name, generation); file != nil {
		defer func() { _ = file.Close() }()
		req.Log = file
	}
	return c.start(req)
}

func (s *Supervisor) openLog(name string, generation uint64) *os.File {
	if s.logDir == "" {
		return nil
	}
	if err := os.MkdirAll(s.logDir, 0o700); err != nil {
		s.logf("supervise: log dir %s: %v", s.logDir, err)
		return nil
	}
	path := filepath.Join(s.logDir, name+".log")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		s.logf("supervise: log file %s: %v", path, err)
		return nil
	}
	fmt.Fprintf(file, "\n=== %s starting %s generation %d ===\n", s.clock.Now().Format(time.RFC3339), name, generation)
	return file
}

func (s *Supervisor) processExited(name string, generation uint64, process Process, exit Exit) {
	s.mu.Lock()
	c := s.children[name]
	if c == nil || generation != c.generation || c.process != process {
		s.mu.Unlock()
		return
	}
	c.process = nil
	c.connectedAt = time.Time{}
	stopTimer(&c.disconnectTimer)
	stopTimer(&c.stabilityTimer)
	exit.At = s.clock.Now()
	c.lastExit = &exit
	if c.desired == DesiredRunning && !s.shutdown {
		s.scheduleRestartLocked(c)
	} else {
		c.phase = PhaseStopped
		c.nextRestartAt = time.Time{}
	}
	parked, snapshot := c.phase == PhaseParked, snapshotOf(c)
	s.mu.Unlock()
	if parked {
		s.reportGiveUp(name, snapshot)
	}
	s.notify(name)
}

func (s *Supervisor) scheduleRestartLocked(c *child) {
	stopTimer(&c.restartTimer)
	if s.giveUpAfter > 0 && c.restartAttempt >= s.giveUpAfter {
		c.phase = PhaseParked
		c.nextRestartAt = time.Time{}
		c.parkedAt = s.clock.Now()
		detail := "no exit recorded"
		if c.lastExit != nil {
			detail = c.lastExit.String()
		}
		s.logf("supervise: giving up on %q after %d restarts with no stable connection; last exit: %s", c.name, c.restartAttempt, detail)
		return
	}
	c.restartAttempt++
	index := c.restartAttempt - 1
	if index >= len(RestartBackoff) {
		index = len(RestartBackoff) - 1
	}
	delay := RestartBackoff[index]
	c.phase = PhaseBackoff
	c.nextRestartAt = s.clock.Now().Add(delay)
	name := c.name
	capturedGeneration := c.generation
	c.restartTimer = s.clock.AfterFunc(delay, func() {
		s.restart(name, capturedGeneration)
	})
}

func (s *Supervisor) restart(name string, generation uint64) {
	s.mu.Lock()
	c := s.children[name]
	if c == nil || generation != c.generation || c.desired != DesiredRunning || s.shutdown {
		s.mu.Unlock()
		return
	}
	c.restartTimer = nil
	_ = s.spawnLocked(c)
	parked, snapshot := c.phase == PhaseParked, snapshotOf(c)
	s.mu.Unlock()
	if parked {
		s.reportGiveUp(name, snapshot)
	}
	s.notify(name)
}

func (s *Supervisor) markStable(name string, generation uint64) {
	s.mu.Lock()
	c := s.children[name]
	if c == nil || generation != c.generation || c.phase != PhaseConnected {
		s.mu.Unlock()
		return
	}
	c.restartAttempt = 0
	c.stabilityTimer = nil
	s.mu.Unlock()
	s.notify(name)
}

func (s *Supervisor) disconnectExpired(name string, generation uint64, process Process) {
	s.mu.Lock()
	c := s.children[name]
	if c == nil || generation != c.generation || c.process != process || c.phase == PhaseConnected || c.desired != DesiredRunning {
		s.mu.Unlock()
		return
	}
	c.disconnectTimer = nil
	s.mu.Unlock()
	_ = process.Kill()
}

func (s *Supervisor) notify(name string) {
	if s.onChange != nil {
		s.onChange(name)
	}
}

func (s *Supervisor) reportGiveUp(name string, snapshot Snapshot) {
	if s.onGiveUp != nil {
		s.onGiveUp(name, snapshot)
	}
}

// Keeps a name usable as a log file name, so it can never write outside LogDir.
func validateName(name string) error {
	if strings.TrimSpace(name) == "" {
		return errors.New("supervise: child name is required")
	}
	if name != filepath.Base(name) || name == "." || name == ".." || strings.ContainsRune(name, os.PathSeparator) {
		return fmt.Errorf("supervise: child name %q must be a plain file name", name)
	}
	return nil
}

func stopTimer(timer *Timer) {
	if *timer != nil {
		(*timer).Stop()
		*timer = nil
	}
}

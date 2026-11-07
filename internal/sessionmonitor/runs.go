package sessionmonitor

import (
	"errors"
	"time"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/sessioncost"
	"github.com/victorarias/attn/internal/statemarker"
	"github.com/victorarias/attn/internal/transcript"
)

type RunStart string

const (
	RunStartUser      RunStart = "user"
	RunStartRing      RunStart = "ring"
	RunStartHeartbeat RunStart = "heartbeat"
	RunStartLaunch    RunStart = "launch"
	RunStartAgent     RunStart = "agent"
)

type RunUsage string

const (
	RunUsageKnown       RunUsage = "known"
	RunUsageUnavailable RunUsage = "unavailable"
	RunUsageIncomplete  RunUsage = "incomplete"
)

type TurnAfterStop string

const (
	TurnAfterStopOwed    TurnAfterStop = "owed"
	TurnAfterStopSnoozed TurnAfterStop = "snoozed"
	TurnAfterStopNone    TurnAfterStop = "none"
)

var ErrRunMessageUnavailable = errors.New("the harness reported no answer and the session has no transcript")

type Run struct {
	StartedBy     RunStart
	StartedAt     time.Time
	EndedAt       time.Time
	Ended         protocol.SessionState
	OpenedTurn    bool
	QuietStop     bool
	TurnRequested bool
	TurnAfterStop TurnAfterStop
	Answer        *string
	UserInputAt   time.Time
	Approvals     int
	Usage         RunUsage
	Tokens        sessioncost.Ledger
}

type trackedRun struct {
	Run
	startKnown     bool
	costAtStart    map[string]*sessioncost.Source
	awaitsBaseline bool
}

type runStop struct {
	openedTurn bool
	quiet      bool
}

type runRecorder struct {
	open       *trackedRun
	ended      []trackedRun
	unreadStop time.Time
}

func (r *runState) start() RunStart {
	switch {
	case r.credited:
		return RunStartUser
	case r.notification == InboxRing:
		return RunStartRing
	case r.notification == CrewHeartbeat:
		return RunStartHeartbeat
	case r.launch:
		return RunStartLaunch
	default:
		return RunStartAgent
	}
}

func (m *Monitor) observeRunLocked(previous protocol.SessionState, cause Cause, stop runStop, now time.Time) []Run {
	phase, open := m.state.State, m.runs.open
	if runsIn(previous) && !runsIn(phase) {
		m.runs.unreadStop = now
	}
	switch {
	case phase == protocol.SessionStateWorking && open == nil:
		_, recovered := cause.(StartupRecovery)
		if recovered || runsIn(previous) || m.input.run == nil {
			return nil
		}
		m.runs.open = &trackedRun{Run: Run{StartedBy: m.input.run.start(), StartedAt: now}, startKnown: m.input.run.prompted || !m.runStartInFlightLocked(), costAtStart: m.cost, awaitsBaseline: !m.runs.unreadStop.IsZero()}
	case open == nil, phase == protocol.SessionStateWorking:
	case phase == protocol.SessionStatePendingApproval:
		if previous != protocol.SessionStatePendingApproval {
			open.Approvals++
		}
	default:
		open.EndedAt, open.Ended, open.OpenedTurn = now, phase, stop.openedTurn
		open.QuietStop, open.TurnAfterStop = stop.quiet, m.turnAfterStopLocked(now)
		m.runs.open = nil
		m.runs.ended = append(m.runs.ended, *open)
		if watcher := m.transcriptWatch; watcher == nil || watcherStopped(watcher) {
			return m.takeEndedRunsLocked(time.Time{}, true)
		}
	}
	return nil
}

func runsIn(state protocol.SessionState) bool {
	return state == protocol.SessionStateWorking || state == protocol.SessionStatePendingApproval
}

func (m *Monitor) runStartInFlightLocked() bool {
	return m.input.userSubmit || m.input.placing || m.evidence.InitialPromptOwed
}

func (m *Monitor) runSteeredLocked() {
	if open := m.runs.open; open != nil {
		open.startKnown = true
	}
}

func (m *Monitor) runStartedDuringPlacementLocked() {
	if open := m.runs.open; open != nil && m.input.run != nil {
		open.StartedBy, open.startKnown = m.input.run.start(), true
	}
}

func (m *Monitor) runHeardUserLocked(now time.Time) {
	if open := m.runs.open; open != nil && open.UserInputAt.IsZero() {
		open.UserInputAt = now
	}
}

func (m *Monitor) turnAfterStopLocked(now time.Time) TurnAfterStop {
	switch {
	case m.turnOwedLocked():
		return TurnAfterStopOwed
	case m.turn.SnoozedUntil.After(now):
		return TurnAfterStopSnoozed
	default:
		return TurnAfterStopNone
	}
}

func (m *Monitor) runAskedForTurnLocked(asked bool) {
	if open := m.runs.open; open != nil {
		open.TurnRequested = asked
	}
}

func (m *Monitor) RecordRunAnswer(answer string) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if open := m.runs.open; open != nil {
		open.Answer = &answer
	}
}

func (m *Monitor) runPromptedLocked() {
	open := m.runs.open
	if open == nil || open.startKnown {
		return
	}
	open.startKnown = true
	open.StartedBy = m.input.run.start()
}

func (m *Monitor) takeEndedRunsLocked(measuredFrom time.Time, unmeasured bool) []Run {
	var taken []Run
	kept := m.runs.ended[:0]
	for _, run := range m.runs.ended {
		switch {
		case run.EndedAt.Before(measuredFrom):
			taken = append(taken, run.measured(m.cost))
		case unmeasured:
			run.Usage = RunUsageUnavailable
			taken = append(taken, run.Run)
		default:
			kept = append(kept, run)
		}
	}
	m.runs.ended = kept
	if unmeasured || !m.runs.unreadStop.Before(measuredFrom) {
		return taken
	}
	m.runs.unreadStop = time.Time{}
	if open := m.runs.open; open != nil && open.awaitsBaseline {
		open.costAtStart, open.awaitsBaseline = m.cost, false
	}
	return taken
}

func (run trackedRun) measured(costAtEnd map[string]*sessioncost.Source) Run {
	start := sessioncost.Checkpoint{Sources: run.costAtStart}.Usage()
	end := sessioncost.Checkpoint{Sources: costAtEnd}.Usage()
	measured := run.Run
	measured.Usage = RunUsageKnown
	switch {
	case run.awaitsBaseline:
		measured.Usage = RunUsageIncomplete
		return measured
	case end.UsageUnavailable:
		measured.Usage = RunUsageUnavailable
		return measured
	case end.MeasurementIncomplete:
		measured.Usage = RunUsageIncomplete
		return measured
	}
	tokens, ok := end.Ledger.Since(start.Ledger)
	if !ok {
		measured.Usage = RunUsageIncomplete
		return measured
	}
	measured.Tokens = tokens
	return measured
}

func (m *Monitor) Agent() protocol.SessionAgent {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.agent
}

func (m *Monitor) RunLastMessage(run Run, maxChars int) (string, error) {
	if run.Answer != nil {
		message := statemarker.Strip(*run.Answer)
		return message[max(0, len(message)-maxChars):], nil
	}
	path := m.transcriptPathFor("")
	if path == "" {
		return "", ErrRunMessageUnavailable
	}
	message, err := transcript.ExtractLastAssistantMessageBetween(path, maxChars, run.StartedAt, run.EndedAt)
	return statemarker.Strip(message), err
}

func (m *Monitor) handOverEndedRuns(measuredFrom time.Time, unmeasured bool) {
	m.mu.Lock()
	runs := m.takeEndedRunsLocked(measuredFrom, unmeasured)
	m.mu.Unlock()
	m.reportRuns(runs)
}

func (m *Monitor) reportRuns(runs []Run) {
	for _, run := range runs {
		m.registry.host.RunEnded(m, run)
	}
}

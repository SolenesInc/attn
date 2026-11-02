package sessionmonitor

import (
	"time"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/sessioncost"
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

type Run struct {
	StartedBy  RunStart
	StartedAt  time.Time
	EndedAt    time.Time
	Ended      protocol.SessionState
	OpenedTurn bool
	Approvals  int
	Usage      RunUsage
	Tokens     sessioncost.Ledger
}

type trackedRun struct {
	Run
	startKnown     bool
	costAtStart    map[string]*sessioncost.Source
	awaitsBaseline bool
}

type runRecorder struct {
	open  *trackedRun
	ended []trackedRun
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

func (m *Monitor) observeRunLocked(previous protocol.SessionState, cause Cause, opened bool, now time.Time) []Run {
	phase, open := m.state.State, m.runs.open
	switch {
	case phase == protocol.SessionStateWorking && open == nil:
		_, recovered := cause.(StartupRecovery)
		if recovered || previous == protocol.SessionStateWorking || previous == protocol.SessionStatePendingApproval || m.input.run == nil {
			return nil
		}
		m.runs.open = &trackedRun{Run: Run{StartedBy: m.input.run.start(), StartedAt: now}, startKnown: m.input.run.prompted || !m.runStartInFlightLocked(), costAtStart: m.cost, awaitsBaseline: len(m.runs.ended) > 0}
	case open == nil, phase == protocol.SessionStateWorking:
	case phase == protocol.SessionStatePendingApproval:
		if previous != protocol.SessionStatePendingApproval {
			open.Approvals++
		}
	default:
		open.EndedAt, open.Ended, open.OpenedTurn = now, phase, opened
		m.runs.open = nil
		m.runs.ended = append(m.runs.ended, *open)
		if watcher := m.transcriptWatch; watcher == nil || watcherStopped(watcher) {
			return m.takeEndedRunsLocked(time.Time{}, true)
		}
	}
	return nil
}

func (m *Monitor) runStartInFlightLocked() bool {
	return m.input.userSubmit || m.input.placing || m.evidence.InitialPromptOwed
}

func (m *Monitor) runSteeredLocked() {
	if open := m.runs.open; open != nil {
		open.startKnown = true
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
	if open := m.runs.open; open != nil && open.awaitsBaseline && len(kept) == 0 && !unmeasured {
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

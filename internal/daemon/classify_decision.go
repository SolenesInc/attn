package daemon

import (
	"errors"
	"strings"
	"time"

	agentdriver "github.com/victorarias/attn/internal/agent"
	"github.com/victorarias/attn/internal/classifier"
	"github.com/victorarias/attn/internal/protocol"
)

type classifyAction int

const (
	classifyApply classifyAction = iota
	classifyReadTranscript
	classifyRunClassifier
	classifySkip
)

type classifyDecision struct {
	action classifyAction
	state  string
	// reason is the diagnostic label logged with the decision; names are kept
	// stable so log searches for past incidents still find them.
	reason string
}

// yielded marks a stop with background work still running: every no-answer
// outcome files nothing, because a turn that may resume must not settle.
type stopClassification struct {
	yielded                bool
	runningBackgroundTasks int
}

func classifyPreTranscript(pendingTodos int, transcriptEnabled, classifierEnabled bool, stop stopClassification) classifyDecision {
	switch {
	case stop.yielded && (!transcriptEnabled || !classifierEnabled):
		return classifyDecision{action: classifySkip, reason: "yield_unjudgeable"}
	case !stop.yielded && pendingTodos > 0:
		return classifyDecision{action: classifyApply, state: protocol.StateWaitingInput, reason: "pending_todos"}
	case !transcriptEnabled:
		return classifyDecision{action: classifyApply, state: protocol.StateIdle, reason: "transcript_disabled"}
	case !classifierEnabled:
		return classifyDecision{action: classifyApply, state: protocol.StateIdle, reason: "classifier_disabled"}
	default:
		return classifyDecision{action: classifyReadTranscript}
	}
}

// classifyPostTranscript decides from the transcript read. ErrNoNewAssistantTurn
// means already classified — a skip, NOT "unknown", which overwrites a good state.
func classifyPostTranscript(lastMessage string, err error, stop stopClassification) classifyDecision {
	if err != nil {
		if errors.Is(err, agentdriver.ErrNoNewAssistantTurn) {
			return classifyDecision{action: classifySkip, reason: "no_new_assistant_turn"}
		}
		if stop.yielded {
			return classifyDecision{action: classifySkip, reason: "yield_transcript_parse_error"}
		}
		return classifyDecision{action: classifyApply, state: protocol.StateUnknown, reason: "transcript_parse_error"}
	}
	if strings.TrimSpace(lastMessage) == "" {
		if stop.yielded {
			return classifyDecision{action: classifySkip, reason: "yield_empty_message"}
		}
		return classifyDecision{action: classifyApply, state: protocol.StateIdle, reason: "empty_last_message"}
	}
	return classifyDecision{action: classifyRunClassifier}
}

func classifyVerdict(state string, err error, stop stopClassification) classifyDecision {
	if err != nil {
		return classifyDecision{action: classifyApply, state: protocol.StateUnknown, reason: "classifier_error"}
	}
	if state == protocol.StateUnknown {
		return classifyDecision{action: classifyApply, state: protocol.StateUnknown, reason: "classifier_unknown_response"}
	}
	if state == classifier.VerdictParked && !stop.yielded {
		return classifyDecision{action: classifyApply, state: protocol.StateUnknown, reason: "classifier_parked_without_yield"}
	}
	return classifyDecision{action: classifyApply, state: state, reason: "classifier"}
}

func (d *Daemon) classifySessionState(sessionID, transcriptPath string) {
	d.classifyStop(sessionID, transcriptPath, stopClassification{})
}

func (d *Daemon) classifyStop(sessionID, transcriptPath string, stop stopClassification) {
	// Captured BEFORE any work: applyState rejects an older classifierObservation,
	// so a slow classifier cannot clobber a newer live signal.
	classificationStartTime := time.Now()
	d.logf("classifySessionState: starting for session=%s, transcript=%s", sessionID, transcriptPath)

	session := d.store.Get(sessionID)
	if session == nil {
		d.logf("classifySessionState: session %s not found, aborting", sessionID)
		return
	}

	d.recordClassifierStarted(sessionID, classificationStartTime)
	defer d.recordClassifierFinished(sessionID)

	apply := func(decision classifyDecision) {
		if decision.action != classifyApply {
			d.logf("classifySessionState: session=%s no state applied reason=%s", sessionID, decision.reason)
			d.traceStateSkip(sessionID, stateSourceClassifier, decision.reason)
			return
		}
		d.logf("classifySessionState: session=%s state=%s reason=%s", sessionID, decision.state, decision.reason)
		d.recordClassifierEvidence(sessionID, decision.state, classificationStartTime)
		d.traceStateEvidence(
			sessionID,
			stateOrigin{
				source:     stateSourceClassifier,
				detail:     decision.reason,
				observedAt: classificationStartTime,
			},
			decision.state,
		)
	}

	transcriptEnabled := true
	classifierEnabled := true
	if driver := agentdriver.Get(string(session.Agent)); driver != nil {
		caps := agentdriver.EffectiveCapabilities(driver)
		transcriptEnabled = caps.HasTranscript
		classifierEnabled = caps.HasClassifier
	}

	// Todos are stored as "[✓] task", "[→] task", "[ ] task".
	pendingTodos := 0
	for _, todo := range session.Todos {
		if !strings.HasPrefix(todo, "[✓]") {
			pendingTodos++
		}
	}
	d.logf("classifySessionState: session %s has %d total todos, %d pending", sessionID, len(session.Todos), pendingTodos)

	decision := classifyPreTranscript(pendingTodos, transcriptEnabled, classifierEnabled, stop)
	if decision.action != classifyReadTranscript {
		apply(decision)
		return
	}

	resolvedTranscriptPath := d.resolveTranscriptPathForSession(session, transcriptPath)
	if resolvedTranscriptPath != transcriptPath {
		d.logf(
			"classifySessionState: session %s resolved transcript path %q -> %q",
			sessionID,
			transcriptPath,
			resolvedTranscriptPath,
		)
	}

	d.logf("classifySessionState: parsing transcript for session %s", sessionID)
	extract := d.extractLastAssistantMessage
	if d.classificationTranscriptExtractor != nil {
		extract = d.classificationTranscriptExtractor
	}
	lastMessage, assistantTurnID, err := extract(session, resolvedTranscriptPath, 500, classificationStartTime)
	if err != nil {
		d.logf("classifySessionState: transcript read failed for %s: %v (transcript=%s)", sessionID, err, resolvedTranscriptPath)
	}
	if strings.TrimSpace(assistantTurnID) != "" && err == nil {
		defer d.clearClassifyingTurn(sessionID)
	}

	decision = classifyPostTranscript(lastMessage, err, stop)
	if decision.action != classifyRunClassifier {
		apply(decision)
		return
	}
	lastMessage = strings.TrimSpace(lastMessage)

	logMsg := lastMessage
	if len(logMsg) > 100 {
		logMsg = logMsg[:100] + "..."
	}
	d.logf("classifySessionState: last message for session %s: %s", sessionID, logMsg)

	classifierInput := lastMessage
	if stop.yielded {
		classifierInput = classifier.ComposeYieldInput(lastMessage, stop.runningBackgroundTasks)
	}

	// Can be slow — 30+ seconds.
	d.logf("classifySessionState: calling classifier for session %s", sessionID)
	state, err := d.runClassifier(session, classifierInput, 30*time.Second)
	if err != nil {
		d.logf("classifySessionState: classifier error for %s: %v", sessionID, err)
	}
	decision = classifyVerdict(state, err, stop)
	if strings.TrimSpace(assistantTurnID) != "" {
		d.setClassifiedTurnID(sessionID, assistantTurnID)
	}
	apply(decision)
}

func (d *Daemon) runClassifier(session *protocol.Session, text string, timeout time.Duration) (string, error) {
	if d.classifier != nil {
		return d.classifier.Classify(text, timeout)
	}
	if session != nil {
		driver := agentdriver.Get(string(session.Agent))
		if state, err, ok := agentdriver.ClassifyWithDriver(
			driver,
			text,
			d.store.GetSetting(executableSettingKey(string(session.Agent))),
			session.Directory,
			timeout,
		); ok {
			return state, err
		}
	}
	claude := agentdriver.Get("claude")
	if state, err, ok := agentdriver.ClassifyWithDriver(
		claude,
		text,
		d.store.GetSetting(canonicalExecutableSettingKey("claude")),
		"",
		timeout,
	); ok {
		return state, err
	}
	return protocol.StateUnknown, errors.New("no classifier backend available")
}

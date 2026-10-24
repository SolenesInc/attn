package sessionmonitor

import (
	"context"
	"errors"
	"strings"
	"time"

	agentdriver "github.com/victorarias/attn/internal/agent"
	"github.com/victorarias/attn/internal/classifier"
	"github.com/victorarias/attn/internal/headless"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/statemarker"
	"github.com/victorarias/attn/internal/statetrace"
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
	reason string
}

type StopClassification struct {
	Yielded                bool
	RunningBackgroundTasks int
}

func classifyPreTranscript(transcriptEnabled, classifierEnabled bool, stop StopClassification) classifyDecision {
	switch {
	case stop.Yielded && (!transcriptEnabled || !classifierEnabled):
		return classifyDecision{action: classifySkip, reason: "yield_unjudgeable"}
	case !transcriptEnabled:
		return classifyDecision{action: classifyApply, state: protocol.StateIdle, reason: "transcript_disabled"}
	case !classifierEnabled:
		return classifyDecision{action: classifyApply, state: protocol.StateIdle, reason: "classifier_disabled"}
	default:
		return classifyDecision{action: classifyReadTranscript}
	}
}

func classifyPostTranscript(lastMessage string, err error, stop StopClassification) classifyDecision {
	if err != nil {
		if errors.Is(err, agentdriver.ErrNoNewAssistantTurn) {
			return classifyDecision{action: classifySkip, reason: "no_new_assistant_turn"}
		}
		if stop.Yielded {
			return classifyDecision{action: classifySkip, reason: "yield_transcript_parse_error"}
		}
		return classifyDecision{action: classifyApply, state: protocol.StateUnknown, reason: "transcript_parse_error"}
	}
	if strings.TrimSpace(lastMessage) == "" {
		if stop.Yielded {
			return classifyDecision{action: classifySkip, reason: "yield_empty_message"}
		}
		return classifyDecision{action: classifyApply, state: protocol.StateIdle, reason: "empty_last_message"}
	}
	return classifyDecision{action: classifyRunClassifier}
}

func classifyVerdict(state string, err error, stop StopClassification) classifyDecision {
	if err != nil {
		return classifyDecision{action: classifyApply, state: protocol.StateUnknown, reason: "classifier_error"}
	}
	if state == protocol.StateUnknown {
		return classifyDecision{action: classifyApply, state: protocol.StateUnknown, reason: "classifier_unknown_response"}
	}
	if state == classifier.VerdictParked && !stop.Yielded {
		return classifyDecision{action: classifyApply, state: protocol.StateUnknown, reason: "classifier_parked_without_yield"}
	}
	return classifyDecision{action: classifyApply, state: state, reason: "classifier"}
}

func (m *Monitor) ClassifyStop(transcriptPath string, stop StopClassification) {
	if m == nil || !m.current() {
		return
	}
	classificationStartTime := time.Now()
	label := m.Label()
	m.logf("classifySessionState: starting for session=%s, transcript=%s", label, transcriptPath)
	m.mu.Lock()
	agent, directory, headlessTasks := m.agent, m.directory, m.policy.Headless
	m.mu.Unlock()

	m.recordClassifierStarted(classificationStartTime)
	apply := func(decision classifyDecision) {
		if decision.action != classifyApply {
			m.logf("classifySessionState: session=%s no state applied reason=%s", label, decision.reason)
			m.Trace(statetrace.Observation{Source: StateSourceClassifier, Reason: decision.reason, Outcome: statetrace.OutcomeSkipped})
			m.concludeClassification(nil)
			return
		}
		m.logf("classifySessionState: session=%s state=%s reason=%s", label, decision.state, decision.reason)
		m.Trace(statetrace.Observation{Source: StateSourceClassifier, Detail: decision.reason, ObservedAt: classificationStartTime, Claim: decision.state, Outcome: statetrace.OutcomeObserved})
		m.concludeClassification(classifierVerdictMutation(decision.state, classificationStartTime))
	}

	transcriptEnabled := true
	classifierEnabled := true
	if driver := agentdriver.Get(string(agent)); driver != nil {
		caps := agentdriver.EffectiveCapabilities(driver)
		transcriptEnabled = caps.HasTranscript
		classifierEnabled = caps.HasClassifier
	}

	decision := classifyPreTranscript(transcriptEnabled, classifierEnabled, stop)
	if decision.action != classifyReadTranscript {
		apply(decision)
		return
	}

	resolvedTranscriptPath := m.transcriptPathFor(transcriptPath)
	if resolvedTranscriptPath != transcriptPath {
		m.logf("classifySessionState: session %s resolved transcript path %q -> %q", label, transcriptPath, resolvedTranscriptPath)
	}

	m.logf("classifySessionState: parsing transcript for session %s", label)
	lastMessage, assistantTurnID, err := m.extractLastAssistantMessage(agent, resolvedTranscriptPath, 500, classificationStartTime)
	if err != nil {
		m.logf("classifySessionState: transcript read failed for %s: %v (transcript=%s)", label, err, resolvedTranscriptPath)
	}
	if strings.TrimSpace(assistantTurnID) != "" && err == nil {
		defer m.clearClassifyingTurn()
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
	m.logf("classifySessionState: last message for session %s: %s", label, logMsg)

	classifierInput := lastMessage
	if stop.Yielded {
		classifierInput = classifier.ComposeYieldInput(lastMessage, stop.RunningBackgroundTasks)
	}

	m.logf("classifySessionState: calling classifier for session %s", label)
	state, err := m.runClassifier(ClassifyRequest{Agent: agent, Directory: directory, Text: classifierInput, Timeout: 30 * time.Second}, headlessTasks)
	if err != nil {
		m.logf("classifySessionState: classifier error for %s: %v", label, err)
	}
	decision = classifyVerdict(state, err, stop)
	if strings.TrimSpace(assistantTurnID) != "" {
		m.setClassifiedTurnID(assistantTurnID)
	}
	apply(decision)
}

func (m *Monitor) runClassifier(request ClassifyRequest, headlessTasks bool) (string, error) {
	if !headlessTasks {
		m.logf("headless task refused (classifier)")
		return ClassifyFromMarker(m.registry.host.Logf, m.Label(), request.Text)
	}
	ctx, cancel := context.WithTimeout(context.Background(), request.Timeout)
	defer cancel()
	return m.registry.host.Classify(ctx, request)
}

func ClassifyFromMarker(logf func(string, ...any), label, text string) (string, error) {
	state, err := statemarker.Parse(text)
	switch {
	case err != nil:
		logf("classifySessionState: session=%s state marker unusable: %v", label, err)
		return protocol.StateUnknown, err
	case state == "":
		return protocol.StateUnknown, headless.Refusal("classifier")
	}
	logf("classifySessionState: session=%s state marker verdict=%s (headless tasks off)", label, state)
	return state, nil
}

func (m *Monitor) extractLastAssistantMessage(agent protocol.SessionAgent, transcriptPath string, maxChars int, classificationStart time.Time) (string, string, error) {
	driver := agentdriver.Get(string(agent))
	lastMessage, turnID, err := agentdriver.ExtractLastAssistantForClassification(
		driver,
		transcriptPath,
		maxChars,
		classificationStart,
		m.classifiedTurnID(),
	)
	if err != nil {
		return "", "", err
	}
	turnID = strings.TrimSpace(turnID)
	if turnID != "" && !m.beginClassifyingTurn(turnID) {
		return "", "", agentdriver.ErrNoNewAssistantTurn
	}
	return lastMessage, turnID, nil
}

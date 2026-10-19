package daemon

import (
	"context"
	"errors"
	"os"
	"strings"
	"time"

	agentdriver "github.com/victorarias/attn/internal/agent"
	"github.com/victorarias/attn/internal/classifier"
	"github.com/victorarias/attn/internal/headless"
	"github.com/victorarias/attn/internal/modeltiers"
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

type stopClassification struct {
	yielded                bool
	runningBackgroundTasks int
}

func classifyPreTranscript(transcriptEnabled, classifierEnabled bool, stop stopClassification) classifyDecision {
	switch {
	case stop.yielded && (!transcriptEnabled || !classifierEnabled):
		return classifyDecision{action: classifySkip, reason: "yield_unjudgeable"}
	case !transcriptEnabled:
		return classifyDecision{action: classifyApply, state: protocol.StateIdle, reason: "transcript_disabled"}
	case !classifierEnabled:
		return classifyDecision{action: classifyApply, state: protocol.StateIdle, reason: "classifier_disabled"}
	default:
		return classifyDecision{action: classifyReadTranscript}
	}
}

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

func (e *monitorEntry) classifyStop(d *Daemon, sessionID protocol.SessionID, transcriptPath string, stop stopClassification) {
	if e == nil || d.monitorEntry(sessionID) != e {
		return
	}

	classificationStartTime := time.Now()
	d.logf("classifySessionState: starting for session=%s, transcript=%s", sessionID, transcriptPath)

	session := d.store.Get(sessionID)
	if session == nil {
		d.logf("classifySessionState: session %s not found, aborting", sessionID)
		return
	}

	e.recordClassifierStarted(d, sessionID, classificationStartTime)
	apply := func(decision classifyDecision) {
		if decision.action != classifyApply {
			d.logf("classifySessionState: session=%s no state applied reason=%s", sessionID, decision.reason)
			d.recordMonitorObservation(e, sessionID, statetrace.Observation{Source: stateSourceClassifier, Reason: decision.reason, Outcome: statetrace.OutcomeSkipped})
			e.concludeClassification(d, sessionID, nil)
			return
		}
		d.logf("classifySessionState: session=%s state=%s reason=%s", sessionID, decision.state, decision.reason)
		d.recordMonitorObservation(e, sessionID, statetrace.Observation{Source: stateSourceClassifier, Detail: decision.reason, ObservedAt: classificationStartTime, Claim: decision.state, Outcome: statetrace.OutcomeObserved})
		e.concludeClassification(d, sessionID, classifierVerdictMutation(decision.state, classificationStartTime))
	}

	transcriptEnabled := true
	classifierEnabled := true
	if driver := agentdriver.Get(session.Agent); driver != nil {
		caps := agentdriver.EffectiveCapabilities(driver)
		transcriptEnabled = caps.HasTranscript
		classifierEnabled = caps.HasClassifier
	}

	decision := classifyPreTranscript(transcriptEnabled, classifierEnabled, stop)
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
	extract := e.extractLastAssistantMessage
	lastMessage, assistantTurnID, err := extract(session, resolvedTranscriptPath, 500, classificationStartTime)
	if err != nil {
		d.logf("classifySessionState: transcript read failed for %s: %v (transcript=%s)", sessionID, err, resolvedTranscriptPath)
	}
	if strings.TrimSpace(assistantTurnID) != "" && err == nil {
		defer e.clearClassifyingTurn()
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

	d.logf("classifySessionState: calling classifier for session %s", sessionID)
	state, err := d.runClassifier(session, classifierInput, 30*time.Second)
	if err != nil {
		d.logf("classifySessionState: classifier error for %s: %v", sessionID, err)
	}
	decision = classifyVerdict(state, err, stop)
	if strings.TrimSpace(assistantTurnID) != "" {
		e.setClassifiedTurnID(assistantTurnID)
	}
	apply(decision)
}

func (d *Daemon) runClassifier(session *protocol.Session, text string, timeout time.Duration) (string, error) {
	if d.headlessTaskRefused("classifier") {
		return d.classifyFromMarker(session, text)
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	if session != nil {
		driver := agentdriver.Get(session.Agent)
		if state, err, ok := agentdriver.ClassifyWithDriver(
			ctx,
			driver,
			text,
			d.store.GetSetting(executableSettingKey(session.Agent)),
			session.Directory,
			d.classifierModel(ctx, session.Agent),
			timeout,
		); ok {
			return state, err
		}
	}
	claude := agentdriver.Get("claude")
	if state, err, ok := agentdriver.ClassifyWithDriver(
		ctx,
		claude,
		text,
		d.store.GetSetting(canonicalExecutableSettingKey("claude")),
		"",
		d.classifierModel(ctx, "claude"),
		timeout,
	); ok {
		return state, err
	}
	return protocol.StateUnknown, errors.New("no classifier backend available")
}

func (d *Daemon) classifyFromMarker(session *protocol.Session, text string) (string, error) {
	sessionID := ""
	if session != nil {
		sessionID = string(session.ID)
	}
	state, err := statemarker.Parse(text)
	switch {
	case err != nil:
		d.logf("classifySessionState: session=%s state marker unusable: %v", sessionID, err)
		return protocol.StateUnknown, err
	case state == "":
		return protocol.StateUnknown, headless.Refusal("classifier")
	}
	d.logf("classifySessionState: session=%s state marker verdict=%s (headless tasks off)", sessionID, state)
	return state, nil
}

func (d *Daemon) classifierModel(ctx context.Context, harness string) string {
	fallback := ""
	switch harness {
	case "claude":
		fallback = classifier.ClaudeClassifierModel()
	case "codex":
		fallback = classifier.CodexClassifierModel()
	default:
		return ""
	}
	explicit := os.Getenv("ATTN_" + strings.ToUpper(harness) + "_CLASSIFIER_MODEL")
	return d.resolveTierModel(ctx, harness, "", modeltiers.Light, explicit, fallback)
}

func (e *monitorEntry) extractLastAssistantMessage(session *protocol.Session, transcriptPath string, maxChars int, classificationStart time.Time) (string, string, error) {
	driver := agentdriver.Get(session.Agent)
	lastMessage, turnID, err := agentdriver.ExtractLastAssistantForClassification(
		driver,
		transcriptPath,
		maxChars,
		classificationStart,
		e.classifiedTurnID(),
	)
	if err != nil {
		return "", "", err
	}
	turnID = strings.TrimSpace(turnID)
	if turnID != "" && !e.beginClassifyingTurn(turnID) {
		return "", "", agentdriver.ErrNoNewAssistantTurn
	}
	return lastMessage, turnID, nil
}

package agent

import (
	"errors"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/transcript"
)

var ErrNoNewAssistantTurn = errors.New("no new assistant turn")

type RecoveredStatePolicyProvider interface {
	RecoveredRunningState(ptyState string) (protocol.SessionState, bool)
}

type ResumePolicyProvider interface {
	ResolveSpawnResumeSessionID(existingSessionID, requestedResumeID, storedResumeID string) string
	SpawnResumeSessionID(sessionID, resolvedResumeID string, resumePicker bool) string
	ResumeSessionIDFromStopTranscriptPath(transcriptPath string) string
}

// ResumeAvailabilityProvider reports whether a resolved resume target exists on disk.
// Claude writes its transcript lazily, so a zero-turn session kills a resuming agent.
type ResumeAvailabilityProvider interface {
	// ResumeAvailable reports whether resumeID can be resumed. resumeID is already
	// resolved and is never empty when called.
	ResumeAvailable(resumeID string) bool
}

type TranscriptClassificationExtractor interface {
	ExtractLastAssistantForClassification(
		transcriptPath string,
		maxChars int,
		classificationStart time.Time,
		lastClassifiedTurnID string,
	) (content string, turnID string, err error)
}

type ExecutableClassifierProvider interface {
	ClassifyWithExecutable(text, executable, workDir string, timeout time.Duration) (string, error)
}

func RecoveredRunningSessionState(d Driver, ptyState string) (protocol.SessionState, bool) {
	if p, ok := d.(RecoveredStatePolicyProvider); ok {
		return p.RecoveredRunningState(ptyState)
	}
	return recoveredStateFromPTYClaim(ptyState)
}

func recoveredStateFromPTYClaim(ptyState string) (protocol.SessionState, bool) {
	switch ptyState {
	case protocol.StateWaitingInput:
		return protocol.SessionStateWaitingInput, true
	case protocol.StatePendingApproval:
		return protocol.SessionStatePendingApproval, true
	default:
		return "", false
	}
}

func ResolveSpawnResumeSessionID(d Driver, existingSessionID, requestedResumeID, storedResumeID string) string {
	requested := strings.TrimSpace(requestedResumeID)
	stored := strings.TrimSpace(storedResumeID)
	if p, ok := d.(ResumePolicyProvider); ok {
		return strings.TrimSpace(p.ResolveSpawnResumeSessionID(existingSessionID, requested, stored))
	}
	return requested
}

func ResumeAvailable(d Driver, resumeID string) bool {
	if strings.TrimSpace(resumeID) == "" {
		return false
	}
	if p, ok := d.(ResumeAvailabilityProvider); ok {
		return p.ResumeAvailable(resumeID)
	}
	return true
}

func SpawnResumeSessionID(d Driver, sessionID, resolvedResumeID string, resumePicker bool) string {
	if p, ok := d.(ResumePolicyProvider); ok {
		return strings.TrimSpace(p.SpawnResumeSessionID(sessionID, resolvedResumeID, resumePicker))
	}
	return ""
}

func ResumeSessionIDFromStopTranscriptPath(d Driver, transcriptPath string) string {
	if p, ok := d.(ResumePolicyProvider); ok {
		return strings.TrimSpace(p.ResumeSessionIDFromStopTranscriptPath(transcriptPath))
	}
	return ""
}

func ExtractLastAssistantForClassification(
	d Driver,
	transcriptPath string,
	maxChars int,
	classificationStart time.Time,
	lastClassifiedTurnID string,
) (content string, turnID string, err error) {
	if p, ok := d.(TranscriptClassificationExtractor); ok {
		return p.ExtractLastAssistantForClassification(
			transcriptPath,
			maxChars,
			classificationStart,
			lastClassifiedTurnID,
		)
	}
	content, err = transcript.ExtractLastAssistantMessage(transcriptPath, maxChars)
	return content, "", err
}

func ClassifyWithDriver(d Driver, text, executable, workDir string, timeout time.Duration) (state string, err error, ok bool) {
	cp, hasClassifier := GetClassifier(d)
	if !hasClassifier {
		return "", nil, false
	}
	if ecp, supportsExecutable := cp.(ExecutableClassifierProvider); supportsExecutable {
		state, err = ecp.ClassifyWithExecutable(text, strings.TrimSpace(executable), strings.TrimSpace(workDir), timeout)
		return state, err, true
	}
	state, err = cp.Classify(text, timeout)
	return state, err, true
}

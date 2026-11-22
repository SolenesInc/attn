package agent

import (
	"strings"

	"github.com/victorarias/attn/internal/protocol"
)

type ResumePolicyProvider interface {
	ResolveSpawnResumeSessionID(existingSessionID, requestedResumeID, storedResumeID string) string
	SpawnResumeSessionID(sessionID, resolvedResumeID string, resumePicker bool) string
	ResumeSessionIDFromTranscriptPath(transcriptPath string) string
}

type ResumeAvailabilityProvider interface {
	ResumeAvailable(resumeID string) bool
}

func ResolveSpawnResumeSessionID(d Driver, existingSessionID protocol.SessionID, requestedResumeID string, storedResumeID string) string {
	requested := strings.TrimSpace(requestedResumeID)
	stored := strings.TrimSpace(storedResumeID)
	if p, ok := d.(ResumePolicyProvider); ok {
		return strings.TrimSpace(p.ResolveSpawnResumeSessionID(string(existingSessionID), requested, stored))
	}
	return requested
}

func preferStoredResumeSessionID(existingSessionID, requestedResumeID, storedResumeID string) string {
	requested := strings.TrimSpace(requestedResumeID)
	stored := strings.TrimSpace(storedResumeID)
	if stored != "" && (requested == "" || requested == strings.TrimSpace(existingSessionID)) {
		return stored
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

func ResumeSessionIDFromTranscriptPath(d Driver, transcriptPath string) string {
	if p, ok := d.(ResumePolicyProvider); ok {
		return strings.TrimSpace(p.ResumeSessionIDFromTranscriptPath(transcriptPath))
	}
	return ""
}

// ConversationKeeper is implemented by harnesses that prune their own conversations.
// ConversationFiles maps each ID to its selected main transcript first, followed by auxiliary paths.
type ConversationKeeper interface {
	ConversationFiles(resumeIDs []string) map[string][]string
}

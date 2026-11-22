package agent

import (
	"strings"
)

type ResumePolicyProvider interface {
	ResumeSessionIDFromTranscriptPath(transcriptPath string) string
}

type ResumeAvailabilityProvider interface {
	ResumeAvailable(resumeID string) bool
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

package sessionmonitor

import (
	"crypto/sha256"
	"errors"
	"strings"

	"github.com/victorarias/attn/internal/transcript"
)

const sessionTitleBriefCharCap = 1500

var ErrNoTitleRunner = errors.New("no runner takes title jobs")

func (m *Monitor) TitleFromTranscript(transcriptPath string) {
	if m == nil {
		return
	}
	if !m.wantsTitle() {
		return
	}
	path := m.transcriptPathFor(transcriptPath)
	if strings.TrimSpace(path) == "" {
		return
	}
	slice, err := transcript.ExtractConversationSlice(path, transcript.SliceOptions{
		MaxRescopingTurns: 2,
		MaxAgentTurns:     1,
		TurnCharCap:       sessionTitleBriefCharCap,
		SummaryCharCap:    2000,
	})
	if err != nil || slice.Empty() || slice.Brief == "" {
		return
	}
	m.enqueueTitle(slice.Render(), "transcript")
}

func (m *Monitor) TitleFromPrompt(prompt string, userConversation bool) {
	if m == nil {
		return
	}
	prompt = strings.TrimSpace(prompt)
	if m == nil || prompt == "" || !m.wantsTitle() {
		return
	}
	if !userConversation && !m.matchesInitialPrompt(prompt) {
		return
	}
	if runes := []rune(prompt); len(runes) > sessionTitleBriefCharCap {
		prompt = string(runes[:sessionTitleBriefCharCap])
	}
	m.enqueueTitle(transcript.ConversationSlice{Brief: prompt, HumanCount: 1}.Render(), "prompt")
}

func (m *Monitor) RememberInitialPrompt(prompt string) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	prompt = strings.TrimSpace(prompt)
	m.titleInitialPrompt = nil
	if prompt != "" {
		fingerprint := sha256.Sum256([]byte(prompt))
		m.titleInitialPrompt = &fingerprint
	}
}

func (m *Monitor) ForgetInitialPrompt() {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.titleInitialPrompt = nil
	m.mu.Unlock()
}

func (m *Monitor) matchesInitialPrompt(prompt string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.titleInitialPrompt != nil && *m.titleInitialPrompt == sha256.Sum256([]byte(prompt))
}

func (m *Monitor) wantsTitle() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.policy.TitlesAllowed && m.policy.Headless && !m.titleAttempted
}

func (m *Monitor) enqueueTitle(conversation string, source string) {
	m.mu.Lock()
	if m.titleAttempted {
		m.mu.Unlock()
		return
	}
	m.titleAttempted = true
	fingerprint := m.titleInitialPrompt
	m.titleInitialPrompt = nil
	label := m.label
	m.mu.Unlock()

	err := m.registry.host.EnqueueTitle(m, conversation, source)
	if err == nil {
		return
	}
	if !errors.Is(err, ErrNoTitleRunner) {
		m.logf("session title %s: enqueue: %v", label, err)
	}
	m.mu.Lock()
	m.titleAttempted = false
	if fingerprint != nil && m.titleInitialPrompt == nil {
		m.titleInitialPrompt = fingerprint
	}
	m.mu.Unlock()
}

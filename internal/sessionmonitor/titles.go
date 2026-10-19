package sessionmonitor

import (
	"crypto/sha256"
	"errors"
	"strings"

	"github.com/victorarias/attn/internal/transcript"
)

const sessionTitleBriefCharCap = 1500

var ErrNoTitleRunner = errors.New("no runner takes title jobs")

func (e *Entry) TitleFromTranscript(transcriptPath string) {
	if e == nil {
		return
	}
	if !e.wantsTitle() {
		return
	}
	path := e.transcriptPathFor(transcriptPath)
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
	e.enqueueTitle(slice.Render(), "transcript")
}

func (e *Entry) TitleFromPrompt(prompt string, userConversation bool) {
	if e == nil {
		return
	}
	prompt = strings.TrimSpace(prompt)
	if e == nil || prompt == "" || !e.wantsTitle() {
		return
	}
	if !userConversation && !e.matchesInitialPrompt(prompt) {
		return
	}
	if runes := []rune(prompt); len(runes) > sessionTitleBriefCharCap {
		prompt = string(runes[:sessionTitleBriefCharCap])
	}
	e.enqueueTitle(transcript.ConversationSlice{Brief: prompt, HumanCount: 1}.Render(), "prompt")
}

func (e *Entry) RememberInitialPrompt(prompt string) {
	if e == nil {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	prompt = strings.TrimSpace(prompt)
	e.titleInitialPrompt = nil
	if prompt != "" {
		fingerprint := sha256.Sum256([]byte(prompt))
		e.titleInitialPrompt = &fingerprint
	}
}

func (e *Entry) ForgetInitialPrompt() {
	if e == nil {
		return
	}
	e.mu.Lock()
	e.titleInitialPrompt = nil
	e.mu.Unlock()
}

func (e *Entry) matchesInitialPrompt(prompt string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.titleInitialPrompt != nil && *e.titleInitialPrompt == sha256.Sum256([]byte(prompt))
}

func (e *Entry) wantsTitle() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.policy.TitlesAllowed && e.policy.Headless && !e.titleAttempted
}

func (e *Entry) enqueueTitle(conversation string, source string) {
	e.mu.Lock()
	if e.titleAttempted {
		e.mu.Unlock()
		return
	}
	e.titleAttempted = true
	fingerprint := e.titleInitialPrompt
	e.titleInitialPrompt = nil
	label := e.label
	e.mu.Unlock()

	err := e.home.EnqueueTitle(conversation, source)
	if err == nil {
		return
	}
	if !errors.Is(err, ErrNoTitleRunner) {
		e.logf("session title %s: enqueue: %v", label, err)
	}
	e.mu.Lock()
	e.titleAttempted = false
	if fingerprint != nil && e.titleInitialPrompt == nil {
		e.titleInitialPrompt = fingerprint
	}
	e.mu.Unlock()
}

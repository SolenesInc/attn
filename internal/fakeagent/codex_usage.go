package fakeagent

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"github.com/google/uuid"
)

const (
	codexDefaultModel   = "gpt-5.5"
	methodGuardian      = "guardian_review"
	methodCodexSettings = "codex_settings"
)

type codexSettingsParams struct {
	Model       string `json:"model"`
	ServiceTier string `json:"service_tier"`
}

func (r *Run) CodexSettings(model, serviceTier string) {
	r.t.Helper()
	r.call(methodCodexSettings, codexSettingsParams{Model: model, ServiceTier: serviceTier}, nil)
}

func (a *agent) handleCodexSettings(params json.RawMessage) (any, error) {
	var p codexSettingsParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, err
	}
	c, ok := a.conv.(*codex)
	if !ok {
		return nil, fmt.Errorf("%T is not Codex", a.conv)
	}
	a.turn.Lock()
	defer a.turn.Unlock()
	c.model, c.serviceTier = p.Model, p.ServiceTier
	return struct{}{}, nil
}

type guardianReviewer interface {
	guardianReview(text string) error
}

func (r *Run) GuardianReview(text string) {
	r.t.Helper()
	r.call(methodGuardian, textParams{Text: text}, nil)
}

func (a *agent) handleGuardian(params json.RawMessage) (any, error) {
	var p textParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, err
	}
	reviewer, ok := a.conv.(guardianReviewer)
	if !ok {
		return nil, fmt.Errorf("%T does not script a guardian review", a.conv)
	}
	a.turn.Lock()
	defer a.turn.Unlock()
	return struct{}{}, reviewer.guardianReview(p.Text)
}

func (c *codex) usageLines(text string) []any {
	model := c.model
	if model == "" {
		model = codexDefaultModel
	}
	return []any{
		map[string]any{"timestamp": now(), "type": "turn_context", "payload": map[string]any{"model": model}},
		map[string]any{"timestamp": now(), "type": "event_msg", "payload": map[string]any{
			"type": "thread_settings_applied", "thread_settings": map[string]any{"model": model, "service_tier": c.serviceTier},
		}},
		map[string]any{"timestamp": now(), "type": "event_msg", "payload": map[string]any{
			"type": "token_count",
			"info": map[string]any{"last_token_usage": map[string]any{
				"input_tokens": len(text), "cached_input_tokens": 0, "output_tokens": len(text),
			}},
		}},
	}
}

func (c *codex) subagent(text string) error {
	parent := c.lastThread
	if parent == "" {
		parent = c.conversation
	}
	id, err := c.childRollout(map[string]any{"thread_spawn": map[string]any{"parent_thread_id": parent}}, text)
	if err == nil {
		c.lastThread = id
	}
	return err
}

func (c *codex) guardianReview(text string) error {
	_, err := c.childRollout(map[string]any{"other": "guardian"}, text)
	return err
}

func (c *codex) childRollout(subagent map[string]any, text string) (string, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return "", err
	}
	started := time.Now().UTC()
	path := filepath.Join(c.sessionsDir(), started.Format("2006/01/02"),
		"rollout-"+started.Format("2006-01-02T15-04-05")+"-"+id.String()+".jsonl")
	lines := append([]any{map[string]any{
		"timestamp": started.Format(time.RFC3339Nano),
		"type":      "session_meta",
		"payload": map[string]any{
			"id":     id.String(),
			"cwd":    c.cwd,
			"source": map[string]any{"subagent": subagent},
		},
	}}, c.usageLines(text)...)
	return id.String(), appendLines(path, lines...)
}

func (c *codex) stream(string) error {
	return errors.New("the codex fake does not script a streamed reply")
}

func (c *codex) deleteSubagentTranscripts() error {
	return errors.New("the codex fake does not script deleting subagent rollouts")
}

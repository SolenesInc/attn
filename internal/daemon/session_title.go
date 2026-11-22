package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/harness"
	"github.com/victorarias/attn/internal/prompts"

	"github.com/victorarias/attn/internal/jobs"
	"github.com/victorarias/attn/internal/modeltiers"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/sessionmonitor"
	"github.com/victorarias/attn/internal/store"
)

const (
	maxSessionNameRunes  = store.SessionNameRunes
	sessionTitleTimeout  = 90 * time.Second
	sessionTitleKind     = "session_title"
	sessionTitleAttempts = 3
)

func sessionTitleInstructions() string {
	return prompts.RenderText("session-title", "instructions", nil)
}

type sessionTitlePayload struct {
	Conversation string             `json:"conversation"`
	SessionID    protocol.SessionID `json:"session_id"`
	Source       string             `json:"source"`
}

func (d *Daemon) enqueueSessionTitle(monitor *sessionmonitor.Monitor, conversation string, source string) error {
	sessionID := monitor.Session()
	runner := d.jobQueueRef()
	if runner == nil || runner.Disabled() {
		return sessionmonitor.ErrNoTitleRunner
	}
	_, err := runner.Enqueue(sessionTitleKind, jobs.EnqueueOptions{
		UniqueKey:   monitor.TitleKey(),
		Payload:     sessionTitlePayload{Conversation: conversation, Source: source, SessionID: sessionID},
		MaxAttempts: sessionTitleAttempts,
	})
	return err
}

func (d *Daemon) sessionTitleHandler(ctx context.Context, job *jobs.Job) (any, error) {
	var payload sessionTitlePayload
	if err := job.DecodePayload(&payload); err != nil {
		return nil, err
	}
	var monitor *sessionmonitor.Monitor
	for _, candidate := range d.monitors().registry.Monitors() {
		matches := candidate.TitleKey() == job.UniqueKey
		if payload.SessionID != "" {
			matches = candidate.Session() == payload.SessionID
		} else if !matches {
			matches = candidate.Session() == protocol.SessionID(job.UniqueKey)
		}
		if matches {
			monitor = candidate
			break
		}
	}
	if monitor == nil {
		d.logf("session title %s: skipped job %s: no monitor for key %q", payload.SessionID, job.ID, job.UniqueKey)
		return nil, nil
	}
	session, allowed := monitor.TitleTarget()
	if !allowed {
		return nil, nil
	}
	result, err := d.sessionTitleExec(ctx, session, payload.Conversation)
	if err != nil {
		return nil, fmt.Errorf("session %s: %w", session.ID, err)
	}
	title := sanitizeSessionTitle(result)
	if title == "" {
		return nil, nil
	}

	session, allowed = monitor.TitleTarget()
	if !allowed {
		return nil, nil
	}
	if !job.CommitGuard.Enter() {
		return nil, context.Canceled
	}
	defer job.CommitGuard.Leave()
	if !monitor.ReportHarnessTitle(title) {
		return nil, fmt.Errorf("session %s: harness title view was not applied", session.ID)
	}
	d.logf("session title %s: %q from %s", session.ID, title, payload.Source)
	d.mirrorLabel(session.ID)
	return title, nil
}

func (d *Daemon) execSessionTitle(ctx context.Context, session *protocol.Session, conversation string) (string, error) {
	providerAgent := d.titleProviderAgent(session.Agent)
	if providerAgent == "" {
		return "", fmt.Errorf("no title provider available for agent %q", session.Agent)
	}
	return d.execSessionTitleHeadless(ctx, providerAgent, d.sessionTitleModel(ctx, providerAgent), conversation)
}

func (d *Daemon) titleProviderAgent(sessionAgent string) string {
	candidates := []string{sessionAgent, "claude", "codex"}
	seen := make(map[string]struct{}, len(candidates))
	for _, name := range candidates {
		if name == "" {
			continue
		}
		if _, dup := seen[name]; dup {
			continue
		}
		seen[name] = struct{}{}
		if _, err := d.oneShot(name); err == nil {
			return name
		}
	}
	return ""
}

func (d *Daemon) execSessionTitleHeadless(ctx context.Context, agent, model, conversation string) (string, error) {
	oneShot, err := d.oneShot(agent)
	if err != nil {
		return "", err
	}
	request := harness.OneShotRequest{
		Model:  model,
		System: sessionTitleInstructions(),
		Input:  prompts.RenderText("session-title", "generate", prompts.Values{"conversation": conversation}),
		Effort: d.factsOrNone(agent).Tiers[modeltiers.Light].Effort,
		Bound:  harness.Bound{MaxTurns: 2, MaxCostUSD: "0.05"},
	}

	result, err := oneShot.Run(ctx, request)
	if err != nil {
		return "", jobs.WithDiagnostic(fmt.Errorf("%s title run failed: %w", agent, err), result.FailureOutput)
	}
	return result.Text, nil
}

var sessionTitleQuoteClosers = map[rune]rune{
	'"':  '"',
	'\'': '\'',
	'`':  '`',
	'“':  '”',
}

func sanitizeSessionTitle(raw string) string {
	var line string
	for _, candidate := range strings.Split(raw, "\n") {
		if trimmed := strings.TrimSpace(candidate); trimmed != "" {
			line = trimmed
			break
		}
	}
	if line == "" {
		return ""
	}

	if runes := []rune(line); len(runes) >= 2 {
		if want, ok := sessionTitleQuoteClosers[runes[0]]; ok && runes[len(runes)-1] == want {
			line = strings.TrimSpace(string(runes[1 : len(runes)-1]))
		}
	}

	if lower := strings.ToLower(line); strings.HasPrefix(lower, "title:") {
		line = strings.TrimSpace(line[len("title:"):])
	}

	line = strings.Join(strings.Fields(line), " ")
	line = strings.TrimRight(line, ".,;:! \t")

	if runes := []rune(line); len(runes) > maxSessionNameRunes {
		line = strings.TrimRight(string(runes[:maxSessionNameRunes]), "-_. \t")
	}

	return line
}

func sessionAutoTitleEnabled() bool {
	switch strings.TrimSpace(strings.ToLower(os.Getenv("ATTN_SESSION_AUTO_TITLE"))) {
	case "0", "false", "off":
		return false
	default:
		return true
	}
}

func (d *Daemon) sessionTitleModel(ctx context.Context, agent string) string {
	explicit := os.Getenv("ATTN_" + strings.ToUpper(agent) + "_TITLE_MODEL")
	return d.resolveTierModel(ctx, agent, modeltiers.Light, explicit, d.factsOrNone(agent).Tiers[modeltiers.Light].Model)
}

func (d *Daemon) bindMonitorTitle(monitor *sessionmonitor.Monitor) {
	runner := d.jobQueueRef()
	if runner == nil || runner.Disabled() {
		return
	}
	err := runner.UpdatePayload(sessionTitleKind, monitor.TitleKey(), func(raw json.RawMessage) (any, error) {
		var payload sessionTitlePayload
		if err := json.Unmarshal(raw, &payload); err != nil {
			return nil, err
		}
		if payload.SessionID != "" {
			return nil, nil
		}
		payload.SessionID = monitor.Session()
		return payload, nil
	})
	if err != nil {
		d.logf("session title binding %s: %v", monitor.Session(), err)
	}
}

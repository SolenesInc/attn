package daemon

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/prompts"

	agentdriver "github.com/victorarias/attn/internal/agent"
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

var sessionTitleInstructions = prompts.RenderText("session-title", "instructions", nil)

type sessionTitlePayload struct {
	Conversation string             `json:"conversation"`
	SessionID    protocol.SessionID `json:"session_id"`
	Source       string             `json:"source"`
}

func (d *Daemon) enqueueSessionTitle(monitor *sessionmonitor.Monitor, conversation string, source string) error {
	sessionID := monitor.Session()
	runner := d.jobQueueRef()
	if runner == nil || runner.Disabled() || sessionID == "" {
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
	for _, candidate := range d.monitors().each() {
		matches := candidate.TitleKey() == job.UniqueKey
		if payload.SessionID == "" {
			matches = candidate.Session() == protocol.SessionID(job.UniqueKey)
		}
		if matches {
			monitor = candidate
			break
		}
	}
	if monitor == nil {
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
	providerAgent := titleProviderAgent(session.Agent)
	if providerAgent == "" {
		return "", fmt.Errorf("no title provider available for agent %q", session.Agent)
	}
	return d.execSessionTitleHeadless(ctx, providerAgent, d.sessionTitleModel(ctx, providerAgent), conversation)
}

func titleProviderAgent(sessionAgent string) string {
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
		driver := agentdriver.Get(name)
		if _, ok := driver.(agentdriver.HeadlessTaskProvider); !ok {
			continue
		}
		if available, _ := agentdriver.HeadlessTaskAvailability(driver); !available {
			continue
		}
		return name
	}
	return ""
}

func (d *Daemon) execSessionTitleHeadless(ctx context.Context, agent, model, conversation string) (string, error) {
	driver := agentdriver.Get(agent)
	if driver == nil {
		return "", fmt.Errorf("%s driver unavailable", agent)
	}
	provider, ok := driver.(agentdriver.HeadlessTaskProvider)
	if !ok {
		return "", fmt.Errorf("%s driver does not support headless tasks", agent)
	}
	executable, err := exec.LookPath(driver.ResolveExecutable(d.store.GetSetting(canonicalExecutableSettingKey(agent))))
	if err != nil {
		return "", fmt.Errorf("resolve %s executable: %w", agent, err)
	}
	workDir, err := os.MkdirTemp("", "attn-session-title-*")
	if err != nil {
		return "", fmt.Errorf("create title scratch dir: %w", err)
	}
	defer os.RemoveAll(workDir)

	request := agentdriver.HeadlessTaskRequest{
		Executable:   executable,
		Model:        model,
		SystemPrompt: sessionTitleInstructions,
		Prompt:       prompts.RenderText("session-title", "generate", prompts.Values{"conversation": conversation}),
		WorkDir:      workDir,
		DisableTools: true,
	}
	switch agent {
	case "claude":
		request.MaxTurns = 2
		request.MaxBudgetUSD = "0.05"
	case "codex":
		request.ReasoningEffort = "low"
	}

	result, err := provider.RunHeadlessTask(ctx, request)
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
	return d.resolveTierModel(ctx, agent, "", modeltiers.Light, explicit, fallbackSessionTitleModel(agent))
}

func fallbackSessionTitleModel(agent string) string {
	switch agent {
	case "claude":
		return "haiku"
	case "codex":
		return "gpt-5.6-luna"
	default:
		return ""
	}
}

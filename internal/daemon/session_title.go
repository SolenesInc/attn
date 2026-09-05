package daemon

import (
	"context"
	"crypto/sha256"
	"fmt"
	"github.com/victorarias/attn/internal/prompts"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	agentdriver "github.com/victorarias/attn/internal/agent"
	"github.com/victorarias/attn/internal/jobs"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/transcript"
)

const (
	// Distinct from maxDelegationNameRunes (delegate.go), the clamp on delegation names.
	maxSessionTitleRunes     = 48
	sessionTitleTimeout      = 90 * time.Second
	sessionTitleBriefCharCap = 1500
	sessionTitleKind         = "session_title"
	sessionTitleAttempts     = 3
)

var sessionTitleInstructions = prompts.RenderText("session-title", "instructions", nil)

type sessionTitlePayload struct {
	Conversation string `json:"conversation"`
	Source       string `json:"source"`
}

func (d *Daemon) maybeGenerateSessionTitle(sessionID, transcriptPath string) {
	if !d.sessionWantsAutoTitle(sessionID) {
		return
	}
	session := d.store.Get(sessionID)
	path := d.resolveTranscriptPathForSession(session, transcriptPath)
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
		// Leave the attempted-guard unmarked so a later Stop retries rather than
		// permanently skipping this session.
		return
	}
	d.enqueueSessionTitle(sessionID, slice.Render(), "transcript")
}

// UserPromptSubmit also delivers maintenance and peer-agent text; only a
// correlated user turn or the session's own initial prompt may title it.
func (d *Daemon) maybeGenerateSessionTitleFromPrompt(sessionID, prompt string, origin sessionInputOrigin) {
	prompt = strings.TrimSpace(prompt)
	if prompt == "" || !d.sessionWantsAutoTitle(sessionID) {
		return
	}
	if origin.kind != sessionInputOriginUserConversation && !d.matchSessionTitleInitialPrompt(sessionID, prompt) {
		return
	}
	if runes := []rune(prompt); len(runes) > sessionTitleBriefCharCap {
		prompt = string(runes[:sessionTitleBriefCharCap])
	}
	d.enqueueSessionTitle(sessionID, transcript.ConversationSlice{Brief: prompt, HumanCount: 1}.Render(), "prompt")
}

// A fingerprint, not the prompt: the body can be 1 MiB and the marker may
// outlive the attempt on durable sessions that never settle a title.
func (d *Daemon) rememberSessionTitleInitialPrompt(sessionID, prompt string) {
	d.sessionTitleMu.Lock()
	defer d.sessionTitleMu.Unlock()
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		delete(d.sessionTitleInitialPrompt, sessionID)
		return
	}
	if d.sessionTitleInitialPrompt == nil {
		d.sessionTitleInitialPrompt = make(map[string][sha256.Size]byte)
	}
	d.sessionTitleInitialPrompt[sessionID] = sha256.Sum256([]byte(prompt))
}

func (d *Daemon) forgetSessionTitleInitialPrompt(sessionID string) {
	d.sessionTitleMu.Lock()
	defer d.sessionTitleMu.Unlock()
	delete(d.sessionTitleInitialPrompt, sessionID)
}

func (d *Daemon) matchSessionTitleInitialPrompt(sessionID, prompt string) bool {
	d.sessionTitleMu.Lock()
	defer d.sessionTitleMu.Unlock()
	remembered, ok := d.sessionTitleInitialPrompt[sessionID]
	return ok && remembered == sha256.Sum256([]byte(prompt))
}

func (d *Daemon) sessionWantsAutoTitle(sessionID string) bool {
	if !sessionAutoTitleEnabled() || d.sessionTitleExec == nil {
		return false
	}
	session := d.store.Get(sessionID)
	if session == nil || !d.sessionMayBeAutoTitled(session) {
		return false
	}
	d.sessionTitleMu.Lock()
	defer d.sessionTitleMu.Unlock()
	_, attempted := d.sessionTitleAttempted[sessionID]
	return !attempted
}

func (d *Daemon) enqueueSessionTitle(sessionID, conversation, source string) {
	// Ahead of the attempted-mark: a refused title must stay retryable.
	runner := d.headlessJobQueue("session_title")
	if runner == nil {
		return
	}

	// The early check and this mark are separate critical sections, so a prompt
	// submit and a Stop can both pass it; re-check under the lock.
	d.sessionTitleMu.Lock()
	if _, attempted := d.sessionTitleAttempted[sessionID]; attempted {
		d.sessionTitleMu.Unlock()
		return
	}
	if d.sessionTitleAttempted == nil {
		d.sessionTitleAttempted = make(map[string]struct{})
	}
	d.sessionTitleAttempted[sessionID] = struct{}{}
	fingerprint, hadFingerprint := d.sessionTitleInitialPrompt[sessionID]
	delete(d.sessionTitleInitialPrompt, sessionID)
	d.sessionTitleMu.Unlock()

	_, err := runner.Enqueue(sessionTitleKind, jobs.EnqueueOptions{
		UniqueKey:   sessionID,
		Payload:     sessionTitlePayload{Conversation: conversation, Source: source},
		MaxAttempts: sessionTitleAttempts,
	})
	if err == nil {
		return
	}
	d.logf("session title %s: enqueue: %v", sessionID, err)
	// The job never existed, so the attempt is still available; a marker a
	// concurrent caller set meanwhile is theirs to keep.
	d.sessionTitleMu.Lock()
	delete(d.sessionTitleAttempted, sessionID)
	if _, newer := d.sessionTitleInitialPrompt[sessionID]; hadFingerprint && !newer {
		d.sessionTitleInitialPrompt[sessionID] = fingerprint
	}
	d.sessionTitleMu.Unlock()
}

func (d *Daemon) sessionTitleHandler(ctx context.Context, job *jobs.Job) (any, error) {
	sessionID := jobSubject(job)
	var payload sessionTitlePayload
	if err := job.DecodePayload(&payload); err != nil {
		return nil, err
	}
	session := d.store.Get(sessionID)
	if session == nil || !d.sessionMayBeAutoTitled(session) {
		return nil, nil
	}
	result, err := d.sessionTitleExec(ctx, session, payload.Conversation)
	if err != nil {
		return nil, fmt.Errorf("session %s: %w", sessionID, err)
	}
	title := sanitizeSessionTitle(result)
	if title == "" {
		return nil, nil
	}

	session = d.store.Get(sessionID)
	if session == nil || !d.sessionMayBeAutoTitled(session) {
		return nil, nil
	}
	if !job.CommitGuard.Enter() {
		return nil, context.Canceled
	}
	defer job.CommitGuard.Leave()
	d.store.UpdateSessionLabel(sessionID, title)
	session.Label = title
	d.logf("session title %s: %q from %s", sessionID, title, payload.Source)
	d.publishFact(FactSessionRenamed, sessionID, nil)
	return title, nil
}

// The member check is the backstop for a member session renamed back to the cwd
// basename the launch gave it.
func (d *Daemon) sessionMayBeAutoTitled(session *protocol.Session) bool {
	if session.Label != defaultSessionLabel(session.Directory, session.ID) {
		return false
	}
	return d.crewMemberBoundTo(session.ID) == ""
}

// Wired onto d.sessionTitleExec in New(); test daemons leave it nil.
func (d *Daemon) execSessionTitle(ctx context.Context, session *protocol.Session, conversation string) (string, error) {
	var sessionAgent string
	if session != nil {
		sessionAgent = string(session.Agent)
	}
	providerAgent := titleProviderAgent(sessionAgent)
	if providerAgent == "" {
		return "", fmt.Errorf("no title provider available for agent %q", sessionAgent)
	}
	model := sessionTitleModel(sessionAgent)
	if model == "" {
		model = sessionTitleModel(providerAgent)
	}
	return d.execSessionTitleHeadless(ctx, providerAgent, model, conversation)
}

// A title is a cheap summary, so any headless-capable CLI will do: prefer the
// session's own agent, else fall back to the first available native one.
func titleProviderAgent(sessionAgent string) string {
	candidates := []string{sessionAgent, "claude", "codex", "copilot"}
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
		// Receipt: $0.0056 on haiku with no tool definitions and this system prompt
		// (was $0.055 with them, over this budget). A ~9x tripwire.
		request.MaxBudgetUSD = "0.05"
	case "codex":
		request.ReasoningEffort = "low"
	}

	result, err := provider.RunHeadlessTask(ctx, request)
	if err != nil {
		return "", fmt.Errorf("%s title run failed: %w\n%s", agent, err, result.FailureOutput)
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

	if runes := []rune(line); len(runes) > maxSessionTitleRunes {
		line = strings.TrimRight(string(runes[:maxSessionTitleRunes]), "-_. \t")
	}

	return line
}

// Mirrors the recovery-path default in daemon.go.
func defaultSessionLabel(cwd, sessionID string) string {
	label := filepath.Base(cwd)
	if label == "" || label == "." || label == string(filepath.Separator) {
		return sessionID
	}
	return label
}

func sessionAutoTitleEnabled() bool {
	switch strings.TrimSpace(strings.ToLower(os.Getenv("ATTN_SESSION_AUTO_TITLE"))) {
	case "0", "false", "off":
		return false
	default:
		return true
	}
}

func sessionTitleModel(agent string) string {
	switch agent {
	case "claude":
		if v := strings.TrimSpace(os.Getenv("ATTN_CLAUDE_TITLE_MODEL")); v != "" {
			return v
		}
		return "haiku"
	case "codex":
		if v := strings.TrimSpace(os.Getenv("ATTN_CODEX_TITLE_MODEL")); v != "" {
			return v
		}
		return "gpt-5.6-luna"
	case "copilot":
		if v := strings.TrimSpace(os.Getenv("ATTN_COPILOT_TITLE_MODEL")); v != "" {
			return v
		}
		return "claude-haiku-4.5"
	// Plugin agents title through the fallback provider above; an override
	// travels raw to that provider's CLI, so it must name one of its models.
	case "pi":
		if v := strings.TrimSpace(os.Getenv("ATTN_PI_TITLE_MODEL")); v != "" {
			return v
		}
		return ""
	case "nisse":
		if v := strings.TrimSpace(os.Getenv("ATTN_NISSE_TITLE_MODEL")); v != "" {
			return v
		}
		return ""
	default:
		return ""
	}
}

package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	agentdriver "github.com/victorarias/attn/internal/agent"
	"github.com/victorarias/attn/internal/headless"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/transcript"
)

const (
	// Distinct from maxDelegationNameRunes (delegate.go), the clamp on delegation names.
	maxSessionTitleRunes     = 48
	sessionTitleTimeout      = 90 * time.Second
	sessionTitleBriefCharCap = 1500
)

const sessionTitleOutputSchema = `{
	"type": "object",
	"properties": {
		"title": { "type": "string" }
	},
	"required": ["title"],
	"additionalProperties": false
}`

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
	d.generateSessionTitle(sessionID, slice, "transcript")
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
	d.generateSessionTitle(sessionID, transcript.ConversationSlice{Brief: prompt, HumanCount: 1}, "prompt")
}

func (d *Daemon) rememberSessionTitleInitialPrompt(sessionID, prompt string) {
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return
	}
	d.sessionTitleMu.Lock()
	defer d.sessionTitleMu.Unlock()
	if d.sessionTitleInitialPrompt == nil {
		d.sessionTitleInitialPrompt = make(map[string]string)
	}
	d.sessionTitleInitialPrompt[sessionID] = prompt
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
	if !ok || remembered != prompt {
		return false
	}
	delete(d.sessionTitleInitialPrompt, sessionID)
	return true
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

func (d *Daemon) generateSessionTitle(sessionID string, slice transcript.ConversationSlice, source string) {
	// Ahead of the attempted-mark: a refused title must stay retryable.
	if d.headlessTaskRefused("session_title") {
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
	d.sessionTitleMu.Unlock()

	session := d.store.Get(sessionID)
	if session == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), sessionTitleTimeout)
	defer cancel()
	result, err := d.sessionTitleExec(ctx, session, slice)
	if err != nil {
		d.logf("session title %s: %v", sessionID, err)
		return
	}
	title := sanitizeSessionTitle(result)
	if title == "" {
		return
	}

	session = d.store.Get(sessionID)
	if session == nil || !d.sessionMayBeAutoTitled(session) {
		return
	}
	d.store.UpdateSessionLabel(sessionID, title)
	session.Label = title
	d.logf("session title %s: %q from %s", sessionID, title, source)
	d.publishFact(FactSessionRenamed, sessionID, nil)
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
func (d *Daemon) execSessionTitle(ctx context.Context, session *protocol.Session, slice transcript.ConversationSlice) (string, error) {
	agent := string(session.Agent)
	switch agent {
	case "claude", "codex":
		return d.execSessionTitleHeadless(ctx, agent, slice)
	case "copilot":
		return execSessionTitleCopilot(ctx, buildSessionTitlePrompt(slice), sessionTitleModel(agent))
	default:
		return "", fmt.Errorf("unsupported agent for title generation: %s", agent)
	}
}

func (d *Daemon) execSessionTitleHeadless(ctx context.Context, agent string, slice transcript.ConversationSlice) (string, error) {
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
		Model:        sessionTitleModel(agent),
		Prompt:       buildSessionTitlePrompt(slice),
		WorkDir:      workDir,
		DisableTools: true,
	}
	switch agent {
	case "claude":
		request.MaxTurns = 2
		request.MaxBudgetUSD = "0.05"
		request.OutputSchema = json.RawMessage(sessionTitleOutputSchema)
	case "codex":
		request.ReasoningEffort = "low"
	}

	result, err := provider.RunHeadlessTask(ctx, request)
	if err != nil {
		return "", fmt.Errorf("%s title run failed: %w", agent, err)
	}
	if agent == "claude" && len(result.StructuredOutput) > 0 {
		var parsed struct {
			Title string `json:"title"`
		}
		if jsonErr := json.Unmarshal(result.StructuredOutput, &parsed); jsonErr == nil {
			return parsed.Title, nil
		}
	}
	return result.Text, nil
}

// Mirrors the command shape of classifier.ClassifyWithCopilot without importing
// it: stop-time classification is internal/classifier's alone.
func execSessionTitleCopilot(ctx context.Context, prompt, model string) (string, error) {
	if !headless.Enabled() {
		return "", headless.Refusal("session_title")
	}
	executable := strings.TrimSpace(os.Getenv("ATTN_COPILOT_EXECUTABLE"))
	if executable == "" {
		executable = "copilot"
	}
	workDir, err := os.MkdirTemp("", "attn-session-title-*")
	if err != nil {
		return "", fmt.Errorf("create title scratch dir: %w", err)
	}
	defer os.RemoveAll(workDir)

	args := []string{
		"-p", prompt,
		"-s",
		"--model", model,
		"--no-color",
		"--no-custom-instructions",
	}
	cmd := exec.CommandContext(ctx, executable, args...)
	cmd.Dir = workDir
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("copilot title run failed: %w", err)
	}
	return string(output), nil
}

func buildSessionTitlePrompt(slice transcript.ConversationSlice) string {
	return fmt.Sprintf(`You generate short titles for AI-agent terminal sessions. Based on the conversation excerpt below, produce a concise title (3-7 words, at most 48 characters) that captures what the user is working on. Respond with only the title text - no quotes, no trailing punctuation, no explanation.

<conversation>
%s
</conversation>`, slice.Render())
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
	default:
		return ""
	}
}

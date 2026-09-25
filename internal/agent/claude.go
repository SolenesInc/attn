package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/classifier"
	"github.com/victorarias/attn/internal/hooks"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/toolhome"
	"github.com/victorarias/attn/internal/transcript"
)

type Claude struct{}

var _ Driver = (*Claude)(nil)
var _ HookProvider = (*Claude)(nil)
var _ TranscriptFinder = (*Claude)(nil)
var _ TranscriptWatcherBehaviorProvider = (*Claude)(nil)
var _ ClassifierProvider = (*Claude)(nil)
var _ ExecutableClassifierProvider = (*Claude)(nil)
var _ LaunchPreparer = (*Claude)(nil)
var _ RecoveredStatePolicyProvider = (*Claude)(nil)
var _ ResumePolicyProvider = (*Claude)(nil)
var _ ResumeAvailabilityProvider = (*Claude)(nil)
var _ TranscriptClassificationExtractor = (*Claude)(nil)
var _ HeadlessTaskProvider = (*Claude)(nil)
var _ HeadlessTaskAvailabilityProvider = (*Claude)(nil)

const (
	claudeTranscriptRetryWindow   = 2 * time.Second
	claudeTranscriptRetryInterval = 100 * time.Millisecond
	claudeTranscriptFreshnessSkew = 5 * time.Second
)

var claudePeerTools = []string{"ListAgents"}

func init() {
	Register(&Claude{})
}

func (c *Claude) Name() string              { return "claude" }
func (c *Claude) DisplayName() string       { return "Claude Code" }
func (c *Claude) DefaultExecutable() string { return "claude" }
func (c *Claude) ExecutableEnvVar() string  { return "ATTN_CLAUDE_EXECUTABLE" }

func (c *Claude) ResolveExecutable(configured string) string {
	return resolveExec(c.ExecutableEnvVar(), configured, c.DefaultExecutable())
}

func (c *Claude) Capabilities() Capabilities {
	return Capabilities{
		HasHooks:              true,
		HasTranscript:         true,
		HasTranscriptWatcher:  true,
		HasClassifier:         true,
		HarnessSignals:        HarnessSignalsClaude,
		HasResume:             true,
		HasYolo:               true,
		HasInitialPrompt:      true,
		HasLaunchInstructions: true,
		HasModelPin:           true,
		HasEffortPin:          true,
	}
}

func (c *Claude) BuildCommand(opts SpawnOpts) *exec.Cmd {
	args := []string{}

	useSessionID := true
	if opts.ResumeSessionID != "" || opts.ResumePicker {
		useSessionID = false
	}
	if useSessionID {
		args = append(args, "--session-id", opts.SessionID)
	}

	if strings.TrimSpace(opts.SettingsPath) != "" {
		args = append(args, "--settings", opts.SettingsPath)
	}
	if instructions := opts.launchGuidance(); instructions != "" {
		args = append(args, "--append-system-prompt", instructions)
	}

	if enabled, _ := boolEnv("ATTN_CLAUDE_PEER_MESSAGING"); !enabled {
		args = append(args, "--disallowed-tools")
		args = append(args, claudePeerTools...)
	}

	args = append(args, opts.addDirArgs()...)

	if model := strings.TrimSpace(opts.Model); model != "" {
		args = append(args, "--model", model)
	}
	if effort := strings.TrimSpace(opts.Effort); effort != "" {
		args = append(args, "--effort", effort)
	}

	if opts.ResumeSessionID != "" {
		args = append(args, "-r", opts.ResumeSessionID)
	} else if opts.ResumePicker {
		args = append(args, "-r")
	}
	if opts.YoloMode {
		args = append(args, "--dangerously-skip-permissions")
	} else if opts.AutoApprove {
		args = append(args, "--permission-mode", "auto")
	}
	if strings.TrimSpace(opts.InitialPrompt) != "" {
		args = append(args, "--", opts.InitialPrompt)
	}

	return exec.Command(opts.Executable, args...)
}

func (c *Claude) BuildEnv(opts SpawnOpts) []string {
	var env []string
	if strings.TrimSpace(opts.NotebookRoot) != "" {
		env = append(env, "ATTN_CHIEF_GUIDANCE=append_system_prompt")
	} else {
		env = append(env, "ATTN_AGENT_GUIDANCE=append_system_prompt")
	}
	if opts.AutoCompactWindow > 0 {
		env = append(env, "CLAUDE_CODE_AUTO_COMPACT_WINDOW="+strconv.Itoa(opts.AutoCompactWindow))
	}
	if opts.Executable != "" && opts.Executable != c.DefaultExecutable() {
		env = append(env, c.ExecutableEnvVar()+"="+opts.Executable)
	}
	return env
}

var claudeNativeDefaultTools = []string{"Read", "Write", "Edit", "Grep", "Glob"}

func (c *Claude) RunHeadlessTask(ctx context.Context, request HeadlessTaskRequest) (HeadlessTaskResult, error) {
	var args []string
	if request.usesNativeToolsPath() {
		args = claudeHeadlessArgs(request)
	} else {
		built, err := buildClaudeHeadlessArgs(request)
		if err != nil {
			return HeadlessTaskResult{}, err
		}
		args = built
	}

	runDir := strings.TrimSpace(request.CWD)
	if runDir == "" {
		runDir = request.WorkDir
	}

	result, stdout, err := runHeadlessCommand(ctx, request.Executable, args, runDir, "claude")
	if err != nil {
		if text := parseClaudeFinalText(stdout); text != "" {
			result.FailureOutput = strings.TrimSpace("result: " + text + "\n" + result.FailureOutput)
		}
		return result, err
	}
	result.Text = parseClaudeFinalText(stdout)
	meta := parseClaudeResultMeta(stdout)
	result.StructuredOutput = meta.StructuredOutput
	result.TotalCostUSD = meta.TotalCostUSD
	result.NumTurns = meta.NumTurns
	return result, nil
}

type claudeResultMeta struct {
	StructuredOutput json.RawMessage `json:"structured_output"`
	TotalCostUSD     float64         `json:"total_cost_usd"`
	NumTurns         int             `json:"num_turns"`
}

func parseClaudeResultMeta(stdout []byte) claudeResultMeta {
	trimmed := bytes.TrimSpace(stdout)
	if len(trimmed) == 0 {
		return claudeResultMeta{}
	}

	type resultEvent struct {
		Type string `json:"type"`
		claudeResultMeta
	}

	var single resultEvent
	if err := json.Unmarshal(trimmed, &single); err == nil && single.Type == "result" {
		return single.claudeResultMeta
	}

	var events []json.RawMessage
	if err := json.Unmarshal(trimmed, &events); err == nil {
		for i := len(events) - 1; i >= 0; i-- {
			var ev resultEvent
			if json.Unmarshal(events[i], &ev) != nil {
				continue
			}
			if ev.Type == "result" {
				return ev.claudeResultMeta
			}
		}
	}
	return claudeResultMeta{}
}

func buildClaudeHeadlessArgs(request HeadlessTaskRequest) ([]string, error) {
	serverName := strings.TrimSpace(request.MCPServerName)
	if serverName == "" {
		serverName = "attn_context"
	}

	mcpServers := map[string]any{
		serverName: map[string]any{
			"type":    "stdio",
			"command": request.MCPServerCommand,
			"args":    request.MCPServerArgs,
		},
	}
	for _, spec := range request.ExtraMCPServers {
		name := strings.TrimSpace(spec.Name)
		if name == "" {
			continue
		}
		mcpServers[name] = map[string]any{
			"type":    "stdio",
			"command": spec.Command,
			"args":    spec.Args,
		}
	}
	config, err := json.Marshal(map[string]any{"mcpServers": mcpServers})
	if err != nil {
		return nil, fmt.Errorf("encode MCP config: %w", err)
	}

	prefixed := claudePrefixedTools(serverName, headlessToolNames(request.ToolName))
	for _, spec := range request.ExtraMCPServers {
		name := strings.TrimSpace(spec.Name)
		if name == "" {
			continue
		}
		prefixed = append(prefixed, claudePrefixedTools(name, spec.EnabledTools)...)
	}

	if request.Sandbox == "workspace-write" {
		prefixed = append(prefixed, "Edit", "Write", "MultiEdit", "Bash")
	}

	tools := strings.Join(prefixed, ",")
	args := []string{"--print"}
	args = append(args, claudeHeadlessIsolationArgs()...)
	if model := strings.TrimSpace(request.Model); model != "" {
		args = append(args, "--model", model)
	}
	args = append(args,
		"--no-session-persistence",
		"--strict-mcp-config",
		"--mcp-config", string(config),
		"--disable-slash-commands",
		"--no-chrome",
		"--tools", tools,
		"--allowedTools", tools,
		"--permission-mode", "dontAsk",
		"--output-format", "json",
		request.Prompt,
	)
	return args, nil
}

func claudeHeadlessArgs(request HeadlessTaskRequest) []string {
	tools := request.AllowedTools
	if len(tools) == 0 && !request.DisableTools {
		tools = claudeNativeDefaultTools
	}
	args := []string{"--print"}
	args = append(args, claudeHeadlessIsolationArgs()...)
	args = append(args, "--strict-mcp-config")
	if model := strings.TrimSpace(request.Model); model != "" {
		args = append(args, "--model", model)
	}
	if effort := strings.TrimSpace(request.ReasoningEffort); effort != "" {
		args = append(args, "--effort", effort)
	}
	if request.MaxTurns > 0 {
		args = append(args, "--max-turns", strconv.Itoa(request.MaxTurns))
	}
	if budget := strings.TrimSpace(request.MaxBudgetUSD); budget != "" {
		args = append(args, "--max-budget-usd", budget)
	}
	if len(request.OutputSchema) > 0 {
		args = append(args, "--json-schema", string(request.OutputSchema))
	}
	if prompt := strings.TrimSpace(request.SystemPrompt); prompt != "" {
		args = append(args, "--system-prompt", prompt)
	}
	args = append(args,
		"--no-session-persistence",
		"--disable-slash-commands",
		"--no-chrome",
	)
	if request.DisableTools && len(request.OutputSchema) == 0 {
		args = append(args, "--disallowedTools", "*")
	} else {
		args = append(args, "--allowedTools", strings.Join(tools, ","))
	}
	args = append(args,
		"--permission-mode", "dontAsk",
		"--output-format", "json",
		request.Prompt,
	)
	return args
}

func claudePrefixedTools(serverName string, names []string) []string {
	prefix := "mcp__" + serverName + "__"
	out := make([]string, len(names))
	for i, n := range names {
		out[i] = prefix + n
	}
	return out
}

func parseClaudeFinalText(stdout []byte) string {
	trimmed := bytes.TrimSpace(stdout)
	if len(trimmed) == 0 {
		return ""
	}

	var single struct {
		Type   string          `json:"type"`
		Result json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(trimmed, &single); err == nil {
		if text := claudeResultString(single.Result); text != "" {
			return text
		}
	}

	var events []json.RawMessage
	if err := json.Unmarshal(trimmed, &events); err == nil {
		for i := len(events) - 1; i >= 0; i-- {
			var ev struct {
				Type   string          `json:"type"`
				Result json.RawMessage `json:"result"`
			}
			if json.Unmarshal(events[i], &ev) != nil {
				continue
			}
			if ev.Type == "result" {
				if text := claudeResultString(ev.Result); text != "" {
					return text
				}
			}
		}
		for i := len(events) - 1; i >= 0; i-- {
			if text := claudeAssistantText(events[i]); text != "" {
				return text
			}
		}
	}
	return ""
}

func claudeResultString(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return ""
	}
	return strings.TrimSpace(s)
}

func claudeAssistantText(raw json.RawMessage) string {
	var ev struct {
		Type    string `json:"type"`
		Message struct {
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"message"`
	}
	if json.Unmarshal(raw, &ev) != nil || ev.Type != "assistant" {
		return ""
	}
	var parts []string
	for _, block := range ev.Message.Content {
		if block.Type == "text" && strings.TrimSpace(block.Text) != "" {
			parts = append(parts, block.Text)
		}
	}
	return strings.TrimSpace(strings.Join(parts, ""))
}

func (c *Claude) HeadlessTaskAvailability() (bool, string) {
	return true, ""
}

func claudeHeadlessIsolationArgs() []string {
	if claudeHasBareModeAuthentication() {
		return []string{"--bare"}
	}
	return []string{"--setting-sources", ""}
}

func claudeHasBareModeAuthentication() bool {
	for _, name := range []string{
		"ANTHROPIC_API_KEY",
		"CLAUDE_CODE_USE_BEDROCK",
		"CLAUDE_CODE_USE_VERTEX",
		"CLAUDE_CODE_USE_FOUNDRY",
	} {
		if strings.TrimSpace(os.Getenv(name)) != "" {
			return true
		}
	}
	return false
}

func (c *Claude) PrepareLaunch(opts SpawnOpts) error {
	if _, err := EnsureClaudeSkillInstalled(); err != nil {
		return err
	}
	if strings.TrimSpace(opts.ResumeSessionID) == "" {
		return nil
	}
	return copyTranscriptForResume(opts.ResumeSessionID, opts.CWD)
}

func (c *Claude) GenerateHooksConfig(opts SpawnOpts) string {
	return hooks.Generate(opts.SessionID, opts.SocketPath, opts.WrapperPath, claudeSettingsEnv(opts))
}

func claudeSettingsEnv(opts SpawnOpts) map[string]string {
	if opts.AutoCompactWindow <= 0 {
		return nil
	}
	return map[string]string{
		"CLAUDE_CODE_AUTO_COMPACT_WINDOW": strconv.Itoa(opts.AutoCompactWindow),
	}
}

func (c *Claude) FindTranscript(sessionID, cwd string, startedAt time.Time) string {
	return transcript.FindClaudeTranscript(sessionID)
}

func (c *Claude) FindTranscriptForResume(resumeID string) string {
	return transcript.FindClaudeTranscript(resumeID)
}

func (c *Claude) ResumeAvailable(resumeID string) bool {
	return transcript.FindClaudeTranscript(resumeID) != ""
}

func (c *Claude) BootstrapBytes() int64 {
	return 256 * 1024
}

func (c *Claude) NewTranscriptUsageSourceResolver(rootPath string) transcript.UsageSourceResolver {
	return transcript.NewClaudeUsageSourceResolver(rootPath)
}

func (c *Claude) NewTranscriptWatcherBehavior() TranscriptWatcherBehavior {
	return &claudeTranscriptWatcherBehavior{}
}

func (c *Claude) RecoveredRunningState(ptyState string) (protocol.SessionState, bool) {
	return recoveredStateFromPTYClaim(ptyState)
}

func (c *Claude) ResolveSpawnResumeSessionID(existingSessionID, requestedResumeID, storedResumeID string) string {
	return preferStoredResumeSessionID(existingSessionID, requestedResumeID, storedResumeID)
}

func (c *Claude) SpawnResumeSessionID(sessionID, resolvedResumeID string, resumePicker bool) string {
	resolved := strings.TrimSpace(resolvedResumeID)
	if resolved != "" {
		return resolved
	}
	if !resumePicker {
		return strings.TrimSpace(sessionID)
	}
	return ""
}

func (c *Claude) ResumeSessionIDFromTranscriptPath(transcriptPath string) string {
	clean := strings.TrimSpace(transcriptPath)
	if clean == "" {
		return ""
	}
	base := filepath.Base(clean)
	if !strings.HasSuffix(base, ".jsonl") {
		return ""
	}
	return strings.TrimSpace(strings.TrimSuffix(base, ".jsonl"))
}

func (c *Claude) ExtractLastAssistantForClassification(
	transcriptPath string,
	maxChars int,
	classificationStart time.Time,
	lastClassifiedTurnID string,
) (content string, turnID string, err error) {
	return c.extractLastAssistantForClassification(
		transcriptPath,
		maxChars,
		classificationStart,
		lastClassifiedTurnID,
		claudeTranscriptRetryWindow,
		claudeTranscriptRetryInterval,
	)
}

func (c *Claude) extractLastAssistantForClassification(
	transcriptPath string,
	maxChars int,
	classificationStart time.Time,
	lastClassifiedTurnID string,
	retryWindow time.Duration,
	retryInterval time.Duration,
) (content string, turnID string, err error) {
	deadline := time.Now().Add(retryWindow)
	minAssistantTimestamp := classificationStart.Add(-claudeTranscriptFreshnessSkew)
	lastClassified := strings.TrimSpace(lastClassifiedTurnID)
	for {
		turn, turnErr := transcript.ExtractLastAssistantTurnAfterLastUserSince(
			transcriptPath,
			maxChars,
			minAssistantTimestamp,
		)
		if turnErr == nil && strings.TrimSpace(turn.Content) != "" {
			turnUUID := strings.TrimSpace(turn.UUID)
			if turnUUID != "" && turnUUID == lastClassified {
				turnErr = ErrNoNewAssistantTurn
			} else {
				return turn.Content, turnUUID, nil
			}
		}
		if !time.Now().Before(deadline) {
			if turnErr == nil {
				turnErr = ErrNoNewAssistantTurn
			}
			return "", "", turnErr
		}
		time.Sleep(retryInterval)
	}
}

func (c *Claude) Classify(text string, timeout time.Duration) (string, error) {
	return c.ClassifyWithExecutable(text, "", "", timeout)
}

func (c *Claude) ClassifyWithExecutable(text, executable, workDir string, timeout time.Duration) (string, error) {
	if strings.TrimSpace(text) == "" {
		classifier.DefaultLogger("classifier: empty text, returning idle")
		return "idle", nil
	}
	classifier.DefaultLogger("classifier: input text (%d chars): %q", len(text), text)

	resolved, err := exec.LookPath(c.ResolveExecutable(executable))
	if err != nil {
		classifier.DefaultLogger("classifier: claude executable unresolved: %v", err)
		return "unknown", fmt.Errorf("resolve claude executable: %w", err)
	}

	scratchDir, err := os.MkdirTemp("", "attn-claude-classifier-*")
	if err != nil {
		return "unknown", fmt.Errorf("create classifier scratch dir: %w", err)
	}
	defer os.RemoveAll(scratchDir)

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	model := classifier.ClaudeClassifierModel()
	classifier.DefaultLogger(
		"classifier: calling claude CLI executable=%s model=%s timeout=%d seconds",
		resolved,
		model,
		int(timeout.Seconds()),
	)

	result, err := c.RunHeadlessTask(ctx, HeadlessTaskRequest{
		Executable:   resolved,
		Model:        model,
		Prompt:       classifier.BuildPrompt(text),
		WorkDir:      scratchDir,
		DisableTools: true,
		MaxTurns:     classifier.ClaudeMaxTurns,
		OutputSchema: json.RawMessage(classifier.ClaudeVerdictSchema),
	})
	if err != nil {
		classifier.DefaultLogger("classifier: claude CLI failed model=%s err=%v output=%s",
			model, err, classifier.TruncateForLog(result.FailureOutput))
		return "unknown", fmt.Errorf("claude cli: %w", err)
	}

	classifier.DefaultLogger(
		"classifier: claude CLI run num_turns=%d cost_usd=%.4f structured_output=%s text=%s",
		result.NumTurns,
		result.TotalCostUSD,
		classifier.TruncateForLog(string(result.StructuredOutput)),
		classifier.TruncateForLog(result.Text),
	)

	if state, ok := classifier.ParseVerdict(result.StructuredOutput, result.Text); ok {
		return state, nil
	}
	classifier.DefaultLogger("classifier: claude response missing explicit WAITING/DONE verdict, returning unknown")
	return "unknown", nil
}

func copyTranscriptForResume(resumeSessionID, cwd string) error {
	srcPath := transcript.FindClaudeTranscript(resumeSessionID)
	if srcPath == "" {
		return fmt.Errorf("resume transcript not found for session %s", resumeSessionID)
	}

	destDir := claudeProjectDir(cwd)
	if destDir == "" {
		return fmt.Errorf("could not determine Claude project directory")
	}
	if err := os.MkdirAll(destDir, 0700); err != nil {
		return fmt.Errorf("failed to create project directory: %w", err)
	}

	destPath := filepath.Join(destDir, resumeSessionID+".jsonl")
	if srcPath == destPath {
		return nil
	}

	src, err := os.Open(srcPath)
	if err != nil {
		return fmt.Errorf("failed to open source transcript: %w", err)
	}
	defer src.Close()

	dst, err := os.OpenFile(destPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return fmt.Errorf("failed to create destination transcript: %w", err)
	}
	defer dst.Close()

	if _, err := io.Copy(dst, src); err != nil {
		return fmt.Errorf("failed to copy transcript: %w", err)
	}
	return nil
}

func claudeProjectDir(cwd string) string {
	homeDir, err := toolhome.Dir()
	if err != nil {
		return ""
	}
	escapedPath := strings.ReplaceAll(cwd, "/", "-")
	escapedPath = strings.ReplaceAll(escapedPath, ".", "-")
	return filepath.Join(homeDir, ".claude", "projects", escapedPath)
}

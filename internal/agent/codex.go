package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/victorarias/attn/internal/harness"
	"github.com/victorarias/attn/internal/headless"
	"github.com/victorarias/attn/internal/hooks"
	"github.com/victorarias/attn/internal/toolhome"
	"github.com/victorarias/attn/internal/transcript"
)

type Codex struct{}

var _ Driver = (*Codex)(nil)
var _ TranscriptFinder = (*Codex)(nil)
var _ ConfigOverrideProvider = (*Codex)(nil)
var _ ResumePolicyProvider = (*Codex)(nil)
var _ headless.TaskProvider = (*Codex)(nil)

func init() {
	Register(&Codex{})
}

func (c *Codex) Name() string              { return "codex" }
func (c *Codex) DisplayName() string       { return "Codex" }
func (c *Codex) DefaultExecutable() string { return "codex" }
func (c *Codex) ExecutableEnvVar() string  { return "ATTN_CODEX_EXECUTABLE" }

func (c *Codex) ResolveExecutable(configured string) string {
	return resolveExec(c.ExecutableEnvVar(), configured, c.DefaultExecutable())
}

func (c *Codex) Capabilities() Capabilities {
	return Capabilities{
		HasHooks:              true,
		HasTranscript:         true,
		HasTranscriptWatcher:  true,
		HasClassifier:         true,
		HarnessSignals:        harness.TitlesCodex,
		HasResume:             true,
		HasYolo:               true,
		HasAutoApprove:        true,
		HasInitialPrompt:      true,
		HasLaunchInstructions: true,
		HasModelPin:           true,
		HasEffortPin:          true,
		HasContextWindowCap:   true,
		ConversationIsSession: true,
	}
}

func (c *Codex) BuildCommand(opts SpawnOpts) *exec.Cmd {
	args := []string{}
	if opts.ResumeSessionID != "" {
		args = append(args, "resume", opts.ResumeSessionID)
	} else if opts.ResumePicker {
		args = append(args, "resume")
	}
	if opts.CodexRemote != "" {
		args = append(args, "--remote", opts.CodexRemote, "-c", "check_for_update_on_startup=false")
	}

	for _, override := range opts.ConfigOverrides {
		if strings.TrimSpace(override) == "" {
			continue
		}
		args = append(args, "-c", override)
	}

	args = append(args, "-C", opts.CWD)
	if opts.CodexRemote == "" {
		args = append(args, opts.addDirArgs()...)
		args = append(args, codexPermissionArgs(opts)...)
	}
	if model := strings.TrimSpace(opts.Model); model != "" {
		args = append(args, "--model", model)
	}
	args = append(args, codexContextWindowCapArgs(opts.AutoCompactWindow)...)
	if effort := strings.TrimSpace(opts.Effort); effort != "" {
		args = append(args, "-c", `model_reasoning_effort="`+effort+`"`)
	}
	if strings.TrimSpace(opts.InitialPrompt) != "" {
		args = append(args, "--", opts.InitialPrompt)
	}

	return exec.Command(opts.Executable, args...)
}

func codexPermissionArgs(opts SpawnOpts) []string {
	if opts.YoloMode {
		return []string{"--dangerously-bypass-approvals-and-sandbox"}
	}
	if opts.AutoApprove {
		return []string{"-c", `approval_policy="on-request"`, "-c", `approvals_reviewer="auto_review"`}
	}
	return nil
}

func (c *Codex) BuildEnv(opts SpawnOpts) []string {
	env := []string{
		"ATTN_TERMINAL_ID=" + string(opts.TerminalID),
		"ATTN_AGENT=codex",
	}
	if wrapper := strings.TrimSpace(opts.WrapperPath); wrapper != "" {
		env = append(env, "ATTN_WRAPPER_PATH="+wrapper)
	}
	if opts.SocketPath != "" {
		env = append(env, "ATTN_SOCKET_PATH="+opts.SocketPath)
	}
	if opts.AgentTools != nil && opts.CodexRemote == "" {
		env = append(env, AgentCredentialEnv+"="+opts.AgentTools.Credential)
	}
	if opts.Executable != "" && opts.Executable != c.DefaultExecutable() {
		env = append(env, c.ExecutableEnvVar()+"="+opts.Executable)
	}
	return env
}

func (c *Codex) RunHeadlessTask(ctx context.Context, request headless.TaskRequest) (headless.TaskResult, error) {
	window := headless.ContextWindowCap()
	schemaPath, removeSchema, err := writeCodexOutputSchema(request)
	if err != nil {
		return headless.TaskResult{}, err
	}
	defer removeSchema()
	instructionsPath, removeInstructions, err := headless.WriteSystemPrompt(request)
	if err != nil {
		return headless.TaskResult{}, err
	}
	defer removeInstructions()
	args := codexHeadlessArgs(request, window, instructionsPath)
	if request.DisableTools {
		args = codexToolFreeHeadlessArgs(request, window, instructionsPath)
	}
	if schemaPath != "" {
		args = append(args, "--output-schema", schemaPath)
	}
	args = append(args, codexPromptFromStdin)
	result, stdout, err := headless.RunCommand(ctx, request.Executable, args, request.Prompt, request.WorkDir, "codex", codexHeadlessEnv, codexReportedFailure)
	if err != nil {
		return result, err
	}
	result.Text = parseCodexFinalText(stdout)
	return result, nil
}

func writeCodexOutputSchema(request headless.TaskRequest) (string, func(), error) {
	if len(request.OutputSchema) == 0 {
		return "", func() {}, nil
	}
	path, remove, err := headless.WriteFile("codex-output-schema-*.json", request.OutputSchema)
	if err != nil {
		return "", func() {}, fmt.Errorf("write Codex output schema: %w", err)
	}
	return path, remove, nil
}

const codexPromptFromStdin = "-"

var codexFeatureLockSettings = []hooks.CodexSetting{
	{Key: "features.apps", Value: false},
	{Key: "features.hooks", Value: false},
	{Key: "features.plugins", Value: false},
	{Key: "features.browser_use", Value: false},
	{Key: "features.in_app_browser", Value: false},
	{Key: "features.computer_use", Value: false},
	{Key: "features.image_generation", Value: false},
	{Key: "features.memories", Value: false},
	{Key: "features.multi_agent", Value: false},
	{Key: "features.goals", Value: false},
	{Key: "features.shell_snapshot", Value: false},
	{Key: "features.standalone_web_search", Value: false},
	{Key: "features.tool_suggest", Value: false},
	{Key: "features.workspace_dependencies", Value: false},
}

var codexNoToolSettings = []hooks.CodexSetting{
	{Key: "approval_policy", Value: "never"},
	{Key: "features.shell_tool", Value: false},
	{Key: "features.unified_exec", Value: false},
	{Key: "web_search", Value: "disabled"},
	{Key: "features.view_image", Value: false},
	{Key: "features.sleep_tool", Value: false},
	{Key: "tools.experimental_request_user_input.enabled", Value: false},
	{Key: "agents.enabled", Value: false},
	{Key: "skills.include_instructions", Value: false},
	{Key: "include_permissions_instructions", Value: false},
	{Key: "include_apps_instructions", Value: false},
	{Key: "include_collaboration_mode_instructions", Value: false},
	{Key: "include_environment_context", Value: false},
}

func codexSettingArgs(settings []hooks.CodexSetting) []string {
	args := make([]string, 0, 2*len(settings))
	for _, setting := range settings {
		args = append(args, "-c", setting.Override())
	}
	return args
}

func codexFeatureLocks() []string {
	return codexSettingArgs(codexFeatureLockSettings)
}

func CodexToolFreeConfig(userMCPServers []string) map[string]any {
	config := map[string]any{}
	for _, setting := range slices.Concat(codexNoToolSettings, codexFeatureLockSettings) {
		config[setting.Key] = setting.Value
	}
	if window := headless.ContextWindowCap(); window > 0 {
		config["model_auto_compact_token_limit"] = window
	}
	for _, name := range userMCPServers {
		config["mcp_servers."+name+".enabled"] = false
	}
	return config
}

func codexContextWindowCapArgs(window int) []string {
	if window <= 0 {
		return nil
	}
	return []string{"-c", "model_auto_compact_token_limit=" + strconv.Itoa(window)}
}

func parseCodexFinalText(stdout []byte) string {
	last := ""
	for _, raw := range bytes.Split(stdout, []byte("\n")) {
		line := bytes.TrimSpace(raw)
		if len(line) == 0 {
			continue
		}
		var event struct {
			Type string `json:"type"`
			Item struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"item"`
		}
		if err := json.Unmarshal(line, &event); err != nil {
			continue
		}
		if event.Type == "item.completed" && event.Item.Type == "agent_message" {
			if text := strings.TrimSpace(event.Item.Text); text != "" {
				last = text
			}
		}
	}
	return last
}

func codexReportedFailure(stdout []byte) string {
	var messages []string
	for _, raw := range bytes.Split(stdout, []byte("\n")) {
		var event struct {
			Type    string `json:"type"`
			Message string `json:"message"`
			Error   struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal(bytes.TrimSpace(raw), &event) != nil {
			continue
		}
		switch event.Type {
		case "error":
			messages = append(messages, event.Message)
		case "turn.failed":
			messages = append(messages, event.Error.Message)
		}
	}
	return strings.Join(messages, "\n")
}

func codexHeadlessArgs(request headless.TaskRequest, window int, instructionsPath string) []string {
	args := []string{
		"exec",
		"--json",
		"--ephemeral",
		"--ignore-user-config",
		"--ignore-rules",
		"--strict-config",
		"--skip-git-repo-check",
		"--sandbox", "workspace-write",
		"-m", strings.TrimSpace(request.Model),
		"-c", `approval_policy="never"`,
	}
	if effort := strings.TrimSpace(request.ReasoningEffort); effort != "" {
		args = append(args, "-c", `model_reasoning_effort="`+effort+`"`)
	}
	for _, root := range request.ExtraWritableRoots {
		root = strings.TrimSpace(root)
		if root == "" {
			continue
		}
		args = append(args, "--add-dir", root)
	}
	args = append(args, codexFeatureLocks()...)
	args = append(args, codexContextWindowCapArgs(window)...)
	return append(args, codexInstructionsArgs(instructionsPath)...)
}

func codexInstructionsArgs(instructionsPath string) []string {
	if instructionsPath == "" {
		return nil
	}
	return []string{"-c", "model_instructions_file=" + tomlString(instructionsPath)}
}

func tomlString(value string) string {
	quoted, _ := json.Marshal(value)
	return string(quoted)
}

func codexToolFreeHeadlessArgs(request headless.TaskRequest, window int, instructionsPath string) []string {
	args := []string{
		"exec",
		"--json",
		"--ephemeral",
		"--ignore-user-config",
		"--ignore-rules",
		"--strict-config",
		"--skip-git-repo-check",
		"--sandbox", "read-only",
		"-m", strings.TrimSpace(request.Model),
	}
	args = append(args, codexSettingArgs(codexNoToolSettings)...)
	if effort := strings.TrimSpace(request.ReasoningEffort); effort != "" {
		args = append(args, "-c", `model_reasoning_effort="`+effort+`"`)
	}
	args = append(args, codexFeatureLocks()...)
	args = append(args, codexContextWindowCapArgs(window)...)
	return append(args, codexInstructionsArgs(instructionsPath)...)
}

func (c *Codex) GenerateConfigOverrides(opts SpawnOpts) []string {
	var overrides []string
	if opts.CrewPriming != "" && opts.CodexRemote == "" {
		overrides = append(overrides, "features.memories=false", "memories.generate_memories=false")
	}
	if opts.CodexRemote == "" {
		overrides = append(overrides, hooks.GenerateCodexConfigOverrides(string(opts.TerminalID), opts.SocketPath, opts.WrapperPath, opts.launchSpec())...)
		if tools := opts.AgentTools; tools != nil {
			for _, setting := range hooks.CodexAgentToolServers(tools.URL, hooks.CodexSetting{Key: "bearer_token_env_var", Value: AgentCredentialEnv}) {
				overrides = append(overrides, setting.Override())
			}
		}
	}
	return overrides
}

func (c *Codex) FindTranscriptForResume(resumeID string) string {
	return transcript.FindCodexTranscriptForResume(resumeID)
}

func (c *Codex) ResumeAvailable(resumeID string) bool {
	return transcript.FindCodexTranscriptForResume(resumeID) != ""
}

func (c *Codex) NewTranscriptUsageSourceResolver(rootPath string) transcript.UsageSourceResolver {
	return transcript.NewCodexUsageSourceResolver(rootPath)
}

func (c *Codex) NewTranscriptWatcherBehavior() harness.TranscriptWatcher {
	return &codexTranscriptWatcherBehavior{}
}

func (c *Codex) ResolveSpawnResumeSessionID(existingSessionID, requestedResumeID, storedResumeID string) string {
	return preferStoredResumeSessionID(existingSessionID, requestedResumeID, storedResumeID)
}

func (c *Codex) SpawnResumeSessionID(sessionID, resolvedResumeID string, resumePicker bool) string {
	return strings.TrimSpace(resolvedResumeID)
}

func (c *Codex) ResumeSessionIDFromTranscriptPath(transcriptPath string) string {
	return ""
}

var codexHeadlessEnv = headless.Env{
	Names:    []string{"CODEX_HOME", "CODEX_ACCESS_TOKEN", "CODEX_API_KEY", "CODEX_CA_CERTIFICATE"},
	Prefixes: []string{"OPENAI_", "AZURE_OPENAI_", "AWS_", "GOOGLE_"},
	Extra: func([]string) []string {
		if strings.TrimSpace(os.Getenv("CODEX_HOME")) != "" {
			return nil
		}
		if homeDir, err := toolhome.Dir(); err == nil && strings.TrimSpace(homeDir) != "" {
			return []string{"CODEX_HOME=" + filepath.Join(homeDir, ".codex")}
		}
		return nil
	},
}

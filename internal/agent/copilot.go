package agent

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/classifier"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/transcript"
)

type Copilot struct{}

var _ Driver = (*Copilot)(nil)
var _ InstructionsFileProvider = (*Copilot)(nil)
var _ TranscriptFinder = (*Copilot)(nil)
var _ TranscriptWatcherBehaviorProvider = (*Copilot)(nil)
var _ ClassifierProvider = (*Copilot)(nil)
var _ RecoveredStatePolicyProvider = (*Copilot)(nil)
var _ HeadlessTaskProvider = (*Copilot)(nil)
var _ ToolFreeOnlyHeadlessTaskProvider = (*Copilot)(nil)

func init() {
	Register(&Copilot{})
}

const copilotInstructionsDirsEnv = "COPILOT_CUSTOM_INSTRUCTIONS_DIRS"

func (c *Copilot) Name() string              { return "copilot" }
func (c *Copilot) DisplayName() string       { return "Copilot" }
func (c *Copilot) DefaultExecutable() string { return "copilot" }
func (c *Copilot) ExecutableEnvVar() string  { return "ATTN_COPILOT_EXECUTABLE" }

func (c *Copilot) ResolveExecutable(configured string) string {
	return resolveExec(c.ExecutableEnvVar(), configured, c.DefaultExecutable())
}

func (c *Copilot) Capabilities() Capabilities {
	return Capabilities{
		HasHooks:             false,
		HasTranscript:        true,
		HasTranscriptWatcher: true,
		HasClassifier:        true,
		HasResume:            true,
		HasYolo:              true,
		HasInitialPrompt:     true,
	}
}

func (c *Copilot) BuildCommand(opts SpawnOpts) *exec.Cmd {
	var args []string
	if opts.ResumeSessionID != "" {
		args = append(args, "--resume", opts.ResumeSessionID)
	} else if opts.ResumePicker {
		args = append(args, "--resume")
	}
	if opts.YoloMode {
		args = append(args, "--yolo")
	}
	// -i/--interactive keeps the session alive for steering; -p/--prompt exits and tears
	// the delegated session down. Verified against copilot CLI v1.0.63.
	if strings.TrimSpace(opts.InitialPrompt) != "" {
		args = append(args, "--interactive", opts.InitialPrompt)
	}
	return exec.Command(opts.Executable, args...)
}

func (c *Copilot) BuildEnv(opts SpawnOpts) []string {
	var env []string
	// A bare `attn copilot` generates the session id rather than inheriting it, and copilot
	// has no hooks carrying it, so every attn command the agent runs needs it from here.
	if id := strings.TrimSpace(opts.SessionID); id != "" {
		env = append(env, "ATTN_SESSION_ID="+id)
	}
	if opts.Executable != "" && opts.Executable != c.DefaultExecutable() {
		env = append(env, c.ExecutableEnvVar()+"="+opts.Executable)
	}
	if dirs := copilotInstructionsDirs(os.Getenv(copilotInstructionsDirsEnv), opts.InstructionsDir); dirs != "" {
		env = append(env, copilotInstructionsDirsEnv+"="+dirs)
	}
	return env
}

// Copilot reads every *.instructions.md under a COPILOT_CUSTOM_INSTRUCTIONS_DIRS entry and
// inlines it; AGENTS.md and copilot-instructions.md are only read from the git root and cwd.
func (c *Copilot) GenerateInstructionsFile(opts SpawnOpts) (string, string) {
	// Measured on 1.0.81: 256 KB of instructions load, 512 KB silently drops every custom
	// instruction including the user's own. The guidance is ~10 KB today.
	return "attn.instructions.md", opts.launchGuidance()
}

// The user's own entries stay first and keep their order; attn appends its own.
func copilotInstructionsDirs(inherited, dir string) string {
	dir = strings.TrimSpace(dir)
	entries := make([]string, 0, 4)
	for _, entry := range strings.Split(inherited, ",") {
		if entry = strings.TrimSpace(entry); entry != "" && entry != dir {
			entries = append(entries, entry)
		}
	}
	if dir != "" {
		entries = append(entries, dir)
	}
	return strings.Join(entries, ",")
}

func (c *Copilot) FindTranscript(sessionID, cwd string, startedAt time.Time) string {
	return transcript.FindCopilotTranscript(cwd, startedAt)
}

func (c *Copilot) FindTranscriptForResume(resumeID string) string {
	return transcript.FindCopilotTranscriptForResume(resumeID)
}

func (c *Copilot) BootstrapBytes() int64 { return 512 * 1024 }

func (c *Copilot) NewTranscriptWatcherBehavior() TranscriptWatcherBehavior {
	return &copilotTranscriptWatcherBehavior{}
}

func (c *Copilot) RecoveredRunningState(ptyState string) (protocol.SessionState, bool) {
	if ptyState == protocol.StatePendingApproval {
		return protocol.SessionStatePendingApproval, true
	}
	return "", false
}

func (c *Copilot) Classify(text string, timeout time.Duration) (string, error) {
	return classifier.ClassifyWithCopilot(text, timeout)
}

func (c *Copilot) HeadlessTasksAreToolFreeOnly() bool { return true }

func (c *Copilot) RunHeadlessTask(ctx context.Context, request HeadlessTaskRequest) (HeadlessTaskResult, error) {
	if !request.usesNativeToolsPath() {
		return HeadlessTaskResult{}, errors.New("copilot headless tasks support only the native tool-free path")
	}
	if !request.DisableTools || len(request.AllowedTools) > 0 || len(request.ExtraWritableRoots) > 0 {
		return HeadlessTaskResult{}, errors.New("copilot headless tasks require tools to be disabled")
	}
	result, stdout, err := runHeadlessCommand(
		ctx,
		request.Executable,
		copilotToolFreeHeadlessArgs(request),
		request.WorkDir,
		"copilot",
	)
	if err != nil {
		return result, err
	}
	result.Text = strings.TrimSpace(string(stdout))
	return result, nil
}

func copilotToolFreeHeadlessArgs(request HeadlessTaskRequest) []string {
	args := []string{
		"-p", copilotPrompt(request),
		"-s",
	}
	if model := strings.TrimSpace(request.Model); model != "" {
		args = append(args, "--model", model)
	}
	if effort := strings.TrimSpace(request.ReasoningEffort); effort != "" {
		args = append(args, "--effort", effort)
	}
	return append(args,
		"--available-tools=ask_user",
		"--disable-builtin-mcps",
		"--no-ask-user",
		"--no-auto-update",
		"--no-bash-env",
		"--no-color",
		"--no-custom-instructions",
		"--no-experimental",
		"--no-remote",
		"--no-remote-export",
	)
}

func copilotPrompt(request HeadlessTaskRequest) string {
	system := strings.TrimSpace(request.SystemPrompt)
	if system == "" {
		return request.Prompt
	}
	return system + "\n\n" + request.Prompt
}

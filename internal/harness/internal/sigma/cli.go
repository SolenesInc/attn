package sigma

import (
	"os/exec"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/harness"
	"github.com/victorarias/attn/internal/harness/appserver"
)

const (
	displayName       = "Sigma"
	defaultExecutable = "sigma"
	executableEnvVar  = "ATTN_SIGMA_EXECUTABLE"
)

func Facts() harness.Facts {
	return harness.ApplyCapabilityEnv(harness.Facts{
		ID:          id,
		DisplayName: displayName,
		Logo:        harness.Logo{View: 24, Body: logo},
		Options: harness.Options{
			Resume:               true,
			Yolo:                 true,
			AutoApprove:          true,
			AutomationExecutable: true,
			InitialPrompt:        true,
			LaunchInstructions:   true,
			ModelPin:             true,
			EffortPin:            true,
			Picker:               true,
			Provider:             true,
		},
		Executable:     harness.Executable{Default: defaultExecutable, Env: executableEnvVar},
		SkillDirs:      skillDirs,
		CacheTTL:       time.Hour,
		Signals:        harness.Signals{Heartbeat: 500 * time.Millisecond},
		Recovery:       harness.ByServer,
		ModelDiscovery: true,
		Activity:       true,
	})
}

func serverDeclaration() appserver.Declaration {
	return appserver.Declaration{
		Protocol:                 appserver.Protocol,
		Executable:               defaultExecutable,
		ExecutableEnv:            executableEnvVar,
		ServeArgs:                []string{"serve"},
		HomeEnv:                  "SIGMA_HOME",
		DefaultHome:              ".sigma",
		SocketName:               "sigma.sock",
		Abilities:                []appserver.Ability{appserver.AbilityCompact, appserver.AbilityWatch, appserver.AbilitySteer, appserver.AbilityQuestionSafe},
		CallerConversationEnv:    "SIGMA_ROOT_CONVERSATION",
		CallerConversationHeader: "Sigma-Root-Conversation",
		OnClose:                  "interrupt",
		Recovery:                 "last-run",
	}
}

type directLauncher struct{}

func Launcher() harness.DirectLauncher { return directLauncher{} }

func (directLauncher) Facts() harness.Facts { return Facts() }

func (directLauncher) ResolveExecutable(configured string) string {
	return harness.ResolveExecutable(executableEnvVar, configured, defaultExecutable)
}

func (directLauncher) Prepare(harness.DirectLaunchOpts) error { return nil }

func (directLauncher) Build(opts harness.DirectLaunchOpts) (*exec.Cmd, []string) {
	return buildCommand(opts), buildEnv(opts)
}

func (directLauncher) FindTranscript(string) string { return "" }

func buildCommand(opts harness.DirectLaunchOpts) *exec.Cmd {
	args := []string{"--conversation", sigmaConversation(opts)}
	if opts.YoloMode || opts.AutoApprove {
		args = append(args, "--auto")
	}
	if provider, model := splitModel(opts.Model); model != "" {
		if provider != "" {
			args = append(args, "--provider", provider)
		}
		args = append(args, "--model", model)
	}
	if effort := strings.TrimSpace(opts.Effort); effort != "" {
		args = append(args, "--effort", effort)
	}
	guidance := opts.EffectiveGuidance().Instructions()
	if tools := opts.SharedAgentTools; tools != nil {
		args = append(args, "--mcp-server", tools.Name+"="+tools.Description)
		guidance = strings.TrimSpace(guidance + "\n\n" + tools.Instructions)
	}
	if guidance != "" {
		args = append(args, "--instructions", guidance)
	}
	if strings.TrimSpace(opts.InitialPrompt) != "" {
		args = append(args, "--", opts.InitialPrompt)
	}
	return exec.Command(opts.Executable, args...)
}

func splitModel(named string) (provider, model string) {
	model = strings.TrimSpace(named)
	if provider, name, split := strings.Cut(model, "/"); split {
		return provider, name
	}
	return "", model
}

func sigmaConversation(opts harness.DirectLaunchOpts) string {
	if opts.ResumeSessionID != "" {
		return opts.ResumeSessionID
	}
	return string(opts.TerminalID)
}

func buildEnv(opts harness.DirectLaunchOpts) []string {
	env := []string{"ATTN_TERMINAL_ID=" + string(opts.TerminalID), "ATTN_AGENT=" + id}
	if opts.Executable != "" && opts.Executable != defaultExecutable {
		env = append(env, executableEnvVar+"="+opts.Executable)
	}
	return env
}

package agent

import (
	"os/exec"
	"strings"

	"github.com/victorarias/attn/internal/harness/appserver"
)

type Sigma struct{}

var _ Driver = (*Sigma)(nil)
var _ ServerDeclarer = (*Sigma)(nil)

func init() {
	Register(&Sigma{})
}

func (s *Sigma) Name() string              { return "sigma" }
func (s *Sigma) DisplayName() string       { return "Sigma" }
func (s *Sigma) DefaultExecutable() string { return "sigma" }
func (s *Sigma) ExecutableEnvVar() string  { return "ATTN_SIGMA_EXECUTABLE" }

func (s *Sigma) ResolveExecutable(configured string) string {
	return resolveExec(s.ExecutableEnvVar(), configured, s.DefaultExecutable())
}

func (s *Sigma) Capabilities() Capabilities {
	return Capabilities{
		HasResume:             true,
		HasYolo:               true,
		HasAutoApprove:        true,
		HasInitialPrompt:      true,
		HasLaunchInstructions: true,
		HasModelPin:           true,
		HasEffortPin:          true,
	}
}

func (s *Sigma) Server() appserver.Declaration {
	return appserver.Declaration{
		Protocol:                 appserver.Protocol,
		Executable:               s.DefaultExecutable(),
		ExecutableEnv:            s.ExecutableEnvVar(),
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

func (s *Sigma) BuildCommand(opts SpawnOpts) *exec.Cmd {
	args := []string{"--conversation", sigmaConversation(opts)}
	if opts.YoloMode || opts.AutoApprove {
		args = append(args, "--auto")
	}
	if provider, model := SigmaModel(opts.Model); model != "" {
		if provider != "" {
			args = append(args, "--provider", provider)
		}
		args = append(args, "--model", model)
	}
	if effort := strings.TrimSpace(opts.Effort); effort != "" {
		args = append(args, "--effort", effort)
	}
	guidance := opts.launchGuidance()
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

func SigmaModel(named string) (provider, model string) {
	model = strings.TrimSpace(named)
	if provider, name, split := strings.Cut(model, "/"); split {
		return provider, name
	}
	return "", model
}

func sigmaConversation(opts SpawnOpts) string {
	if opts.ResumeSessionID != "" {
		return opts.ResumeSessionID
	}
	return string(opts.TerminalID)
}

func (s *Sigma) BuildEnv(opts SpawnOpts) []string {
	env := []string{"ATTN_TERMINAL_ID=" + string(opts.TerminalID), "ATTN_AGENT=sigma"}
	if opts.Executable != "" && opts.Executable != s.DefaultExecutable() {
		env = append(env, s.ExecutableEnvVar()+"="+opts.Executable)
	}
	return env
}

package agent

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"github.com/BurntSushi/toml"
)

type Sigma struct{}

var _ Driver = (*Sigma)(nil)
var _ ServerDeclarer = (*Sigma)(nil)
var _ ResumePolicyProvider = (*Sigma)(nil)
var _ SkillDirsResolver = (*Sigma)(nil)

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

func (s *Sigma) Server() ServerDeclaration {
	return ServerDeclaration{
		Protocol:              ServerProtocol,
		Executable:            s.DefaultExecutable(),
		ExecutableEnv:         s.ExecutableEnvVar(),
		ServeArgs:             []string{"serve"},
		HomeEnv:               "SIGMA_HOME",
		DefaultHome:           ".sigma",
		SocketName:            "sigma.sock",
		Initialize:            ServerHandshake{Protocol: ServerProtocolVersion{Min: 1}},
		CallerConversationEnv: "SIGMA_ROOT_CONVERSATION",
		OnClose:               "interrupt",
		Recovery:              "last-run",
		SigmaWire:             true,
	}
}

func (s *Sigma) BuildCommand(opts SpawnOpts) *exec.Cmd {
	args := []string{"--conversation", sigmaConversation(opts)}
	if opts.YoloMode || opts.AutoApprove {
		args = append(args, "--auto")
	}
	if model := strings.TrimSpace(opts.Model); model != "" {
		if provider, name, split := strings.Cut(model, "/"); split {
			args = append(args, "--provider", provider)
			model = name
		}
		args = append(args, "--model", model)
	}
	if effort := strings.TrimSpace(opts.Effort); effort != "" {
		args = append(args, "--effort", effort)
	}
	if guidance := opts.launchGuidance(); guidance != "" {
		args = append(args, "--instructions", guidance)
	}
	if strings.TrimSpace(opts.InitialPrompt) != "" {
		args = append(args, "--", opts.InitialPrompt)
	}
	return exec.Command(opts.Executable, args...)
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

func (s *Sigma) ResolveSpawnResumeSessionID(existingSessionID, requestedResumeID, storedResumeID string) string {
	return preferStoredResumeSessionID(existingSessionID, requestedResumeID, storedResumeID)
}

func (s *Sigma) SpawnResumeSessionID(terminalID, resolvedResumeID string, _ bool) string {
	if resolvedResumeID != "" {
		return resolvedResumeID
	}
	return terminalID
}

func (s *Sigma) ResumeSessionIDFromTranscriptPath(string) string { return "" }

func (s *Sigma) SkillDirs(lookupEnv func(string) string, homeDir string) ([]string, error) {
	sigmaHome := envHome(lookupEnv("SIGMA_HOME"), filepath.Join(homeDir, ".sigma"))
	claudeHome := envHome(lookupEnv("CLAUDE_CONFIG_DIR"), filepath.Join(homeDir, ".claude"))
	providers, relative := sigmaClaudeProviderHomes(sigmaHome, homeDir)
	if len(relative) > 0 {
		return nil, fmt.Errorf("the Claude providers %s in Sigma's config.toml name a relative config_dir or env.CLAUDE_CONFIG_DIR; make it absolute (config_dir may start with ~/) so attn can install the skills Attn roles need there", strings.Join(relative, ", "))
	}
	dirs := []string{filepath.Join(sigmaHome, "skills"), filepath.Join(claudeHome, "skills")}
	for _, home := range providers {
		dirs = append(dirs, filepath.Join(home, "skills"))
	}
	return dirs, nil
}

func envHome(value, fallback string) string {
	if value = strings.TrimSpace(value); value != "" {
		return value
	}
	return fallback
}

type sigmaProvider struct {
	Driver    string            `toml:"driver"`
	ConfigDir string            `toml:"config_dir"`
	Env       map[string]string `toml:"env"`
}

func sigmaClaudeProviderHomes(sigmaHome, homeDir string) (homes, relative []string) {
	raw, err := os.ReadFile(filepath.Join(sigmaHome, "config.toml"))
	if err != nil {
		return nil, nil
	}
	var config struct {
		Providers map[string]sigmaProvider `toml:"providers"`
	}
	if _, err := toml.Decode(string(raw), &config); err != nil {
		return nil, nil
	}
	for id, provider := range config.Providers {
		if provider.Driver != "claude" {
			continue
		}
		dir := provider.Env["CLAUDE_CONFIG_DIR"]
		if provider.ConfigDir != "" {
			dir = expandUserHome(provider.ConfigDir, homeDir)
		}
		switch {
		case dir == "":
		case filepath.IsAbs(dir):
			if !slices.Contains(homes, dir) {
				homes = append(homes, dir)
			}
		default:
			relative = append(relative, id)
		}
	}
	slices.Sort(homes)
	slices.Sort(relative)
	return homes, relative
}

func expandUserHome(path, homeDir string) string {
	if path == "~" || strings.HasPrefix(path, "~/") {
		return filepath.Join(homeDir, path[1:])
	}
	return path
}

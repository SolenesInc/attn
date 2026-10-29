package agent

import (
	"os/exec"
	"strings"
)

type Nisse struct{}

var _ Driver = (*Nisse)(nil)
var _ ResumePolicyProvider = (*Nisse)(nil)

func init() {
	Register(&Nisse{})
}

func (n *Nisse) Name() string              { return "nisse" }
func (n *Nisse) DisplayName() string       { return "Nisse" }
func (n *Nisse) DefaultExecutable() string { return "nisse" }
func (n *Nisse) ExecutableEnvVar() string  { return "ATTN_NISSE_EXECUTABLE" }

func (n *Nisse) ResolveExecutable(configured string) string {
	return resolveExec(n.ExecutableEnvVar(), configured, n.DefaultExecutable())
}

func (n *Nisse) Capabilities() Capabilities {
	return Capabilities{
		HasClassifier:         true,
		HasResume:             true,
		HasYolo:               true,
		HasInitialPrompt:      true,
		HasModelPin:           true,
		HasEffortPin:          true,
		ConversationIsSession: true,
	}
}

func (n *Nisse) BuildCommand(opts SpawnOpts) *exec.Cmd {
	conversation := opts.ResumeSessionID
	if conversation == "" {
		conversation = string(opts.TerminalID)
	}
	args := []string{"--conversation", conversation}
	if opts.YoloMode {
		args = append(args, "--auto")
	}
	if model := strings.TrimSpace(opts.Model); model != "" {
		if provider, name, pinned := strings.Cut(model, "/"); pinned {
			args = append(args, "--provider", provider)
			model = name
		}
		args = append(args, "--model", model)
	}
	if effort := strings.TrimSpace(opts.Effort); effort != "" {
		args = append(args, "--effort", effort)
	}
	if strings.TrimSpace(opts.InitialPrompt) != "" {
		args = append(args, "--", opts.InitialPrompt)
	}
	return exec.Command(opts.Executable, args...)
}

func (n *Nisse) BuildEnv(SpawnOpts) []string { return nil }

func (n *Nisse) ResolveSpawnResumeSessionID(existingSessionID, requestedResumeID, storedResumeID string) string {
	return preferStoredResumeSessionID(existingSessionID, requestedResumeID, storedResumeID)
}

func (n *Nisse) SpawnResumeSessionID(sessionID, resolvedResumeID string, resumePicker bool) string {
	if resolved := strings.TrimSpace(resolvedResumeID); resolved != "" {
		return resolved
	}
	return strings.TrimSpace(sessionID)
}

func (n *Nisse) ResumeSessionIDFromTranscriptPath(string) string { return "" }

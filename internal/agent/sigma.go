package agent

import "os/exec"

type Sigma struct{}

var _ Driver = (*Sigma)(nil)
var _ ResumePolicyProvider = (*Sigma)(nil)

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

func (s *Sigma) Capabilities() Capabilities { return appServerCapabilities }

func (s *Sigma) BuildCommand(opts SpawnOpts) *exec.Cmd {
	return exec.Command(opts.Executable, append([]string{"--attach"}, appServerViewArgs(opts)...)...)
}

func (s *Sigma) BuildEnv(SpawnOpts) []string { return nil }

func (s *Sigma) ResolveSpawnResumeSessionID(existingSessionID, requestedResumeID, storedResumeID string) string {
	return preferStoredResumeSessionID(existingSessionID, requestedResumeID, storedResumeID)
}

func (s *Sigma) SpawnResumeSessionID(sessionID, resolvedResumeID string, _ bool) string {
	return conversationOrSession(sessionID, resolvedResumeID)
}

func (s *Sigma) ResumeSessionIDFromTranscriptPath(string) string { return "" }

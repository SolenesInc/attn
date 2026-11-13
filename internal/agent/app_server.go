package agent

import "strings"

var appServerCapabilities = Capabilities{
	HasClassifier:         true,
	HasResume:             true,
	HasYolo:               true,
	HasInitialPrompt:      true,
	HasModelPin:           true,
	HasEffortPin:          true,
	HasLaunchInstructions: true,
	ConversationIsSession: true,
}

func appServerViewArgs(opts SpawnOpts) []string {
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
	if instructions := opts.launchGuidance(); instructions != "" {
		args = append(args, "--instructions", instructions)
	}
	if strings.TrimSpace(opts.InitialPrompt) != "" {
		args = append(args, "--", opts.InitialPrompt)
	}
	return args
}

func conversationOrSession(sessionID, resolvedResumeID string) string {
	if resolved := strings.TrimSpace(resolvedResumeID); resolved != "" {
		return resolved
	}
	return strings.TrimSpace(sessionID)
}

package agent

import "github.com/victorarias/attn/internal/harness"

// Facts are what a built-in driver says about its harness, before the harness adds its own.
func Facts(driver Driver) harness.Facts {
	caps := EffectiveCapabilities(driver)
	_, declaresServer := DeclaredServer(driver)
	return harness.Facts{
		ID:          driver.Name(),
		DisplayName: driver.DisplayName(),
		Options: harness.Options{
			Resume:               caps.HasResume,
			Yolo:                 caps.HasYolo,
			AutoApprove:          caps.HasAutoApprove,
			AutomationExecutable: caps.HasAutoApprove,
			InitialPrompt:        caps.HasInitialPrompt,
			LaunchInstructions:   caps.HasLaunchInstructions,
			ModelPin:             caps.HasModelPin,
			EffortPin:            caps.HasEffortPin,
			ContextCap:           caps.HasContextWindowCap,
			Picker:               true,
			Provider:             declaresServer,
		},
		Executable:            harness.Executable{Default: driver.DefaultExecutable(), Env: driver.ExecutableEnvVar()},
		Signals:               stopSignals(withTitles(hookSignals(caps.HasHooks), caps.HarnessSignals), caps),
		ConversationIsSession: caps.ConversationIsSession,
	}
}

func stopSignals(s harness.Signals, caps Capabilities) harness.Signals {
	s.StopsReadable, s.StopsClassified = caps.HasTranscript, caps.HasClassifier
	return s
}

func withTitles(s harness.Signals, titles harness.TitleKind) harness.Signals {
	s.Titles = titles
	return s
}

func hookSignals(hooks bool) harness.Signals {
	if !hooks {
		return harness.Signals{}
	}
	return harness.Signals{TurnStarts: harness.TurnStartsReported, ReportsTools: true}
}

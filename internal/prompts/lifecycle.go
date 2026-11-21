package prompts

func lifecycleRecipients() []Recipient {
	return []Recipient{
		crewRecipient(),
		{ID: "chief",
			Description: "Chief",
			Events:      []Event{On("seed-assignment", "user_message", "User assigned a seed to the chief to choose its working context.", template("chief.seed-assignment", "content/chief/seed-assignment.md", seedID))}},

		{ID: "turn-classifier",
			Description: "Separate model deciding whether a turn needs user input.",
			Events: []Event{
				On("classify", "headless_prompt", "internal/classifier.BuildPrompt",
					template("turn-classifier.classify", "content/turn-classifier/classify.md",
						TextField("message", "Message"))),
				On("quiet-stop", "headless_prompt", "internal/classifier.BuildQuietStopPrompt",
					template("turn-classifier.quiet-stop", "content/turn-classifier/quiet-stop.md",
						TextField("message", "Message"))),
			}},

		{ID: "terminal",
			Description: "A terminal an agent opened with a command.",
			Events: []Event{On("command-not-typed", "inbox_content", "terminal_open called as a tool answered before its command was typed, and typing it failed; a notice to the session that opened the terminal.",
				template("terminal.command-not-typed", "content/terminal/command-not-typed.md",
					TextField("terminal", "The opened terminal's id."),
					TextField("failure", "Why the command was not typed.")))}},

		{ID: "session-title",
			Description: "Session title",
			Events: []Event{On("instructions", "system_prompt", "Title generation instructions.", Use("session-title.instructions", "content/session-title/instructions.md")), On("generate", "user_message", "internal/daemon/session_title.go: execSessionTitleHeadless",
				template("session-title.generate", "content/session-title/generate.md",
					TextField("conversation", "Conversation")))}},
	}
}

package prompts

func lifecycleRecipients() []Recipient {
	return []Recipient{
		crewRecipient(),
		{ID: "chief",
			Description: "Chief",
			Events: []Event{On("seed-assignment", "user_message", "User assigned a seed to the chief to choose its working context.", template("chief.seed-assignment", "content/chief/seed-assignment.md", seedID)), On("inbox", "user_message", "internal/daemon/notebook.go: chiefInboxNudgePrompt",
				template("chief.inbox", "content/chief/inbox.md",
					TextField("inbox_path", "Inbox path")))}},

		{ID: "turn-classifier",
			Description: "Separate model deciding whether a turn needs user input.",
			Events: []Event{On("classify", "headless_prompt", "internal/classifier.BuildPrompt",
				template("turn-classifier.classify", "content/turn-classifier/classify.md",
					TextField("message", "Message")))}},

		{ID: "session-title",
			Description: "Session title",
			Events: []Event{On("instructions", "system_prompt", "Title generation instructions.", Use("session-title.instructions", "content/session-title/instructions.md")), On("generate", "user_message", "internal/daemon/session_title.go: execSessionTitleHeadless",
				template("session-title.generate", "content/session-title/generate.md",
					TextField("conversation", "Conversation")))}},
	}
}

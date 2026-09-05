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

		{ID: "ticket-reconciler",
			Description: "Ticket reconciler",
			Events: []Event{On("reconcile", "headless_prompt", "internal/daemon/ticket_reconcile.go: buildTicketReconcilePrompt",
				template("ticket-reconciler.reconcile", "content/ticket-reconciler/reconcile.md",
					TextField("ticket_id", "Ticket id"),
					TextField("title", "Title"),
					TextField("brief", "Brief"),
					TextField("status", "Status"),
					TextField("close_context", "Close context"),
					TextField("conversation", "Conversation")))}},

		{ID: "workflow-agent",
			Description: "Workflow agent",
			Events: []Event{On("run", "headless_prompt", "User task followed by the structured-result contract or its retry.", Join("", Input(brief), Choose(Enabled(FlagField("retry", "A previous attempt produced no valid result.")),
				template("workflow-agent.retry-instruction", "content/workflow-agent/retry-instruction.md"), template("workflow-agent.result-instruction", "content/workflow-agent/result-instruction.md")))), On("result-instruction", "user_message", "internal/workflow/driveragent.go: schemaCallInstruction",
				template("workflow-agent.result-instruction", "content/workflow-agent/result-instruction.md")),

				On("retry-instruction", "user_message", "internal/workflow/driveragent.go: correctiveInstruction",
					template("workflow-agent.retry-instruction", "content/workflow-agent/retry-instruction.md"))}},
	}
}

func sessionNudges() []Event {
	return []Event{On("legacy-ticket-nudge", "user_message", "internal/daemon/ticket_notify.go: ticketNudgePrompt",
		template("session.legacy-ticket-nudge", "content/session/legacy-ticket-nudge.md"))}
}

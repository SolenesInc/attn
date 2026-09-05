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

		{ID: "keeper",
			Description: "Background Notebook and workspace maintenance.",
			Events: []Event{On("transcript-paths", "message_fragment", "Resolved transcript paths or their absence.", Choose(Enabled(FlagField("has_paths", "At least one transcript resolved.")), template("keeper.transcript-paths", "content/keeper/transcript-paths.md", TextField("paths", "Resolved paths.")), Use("keeper.no-transcripts", "content/keeper/no-transcripts.md"))), On("compact", "headless_prompt", "Workspace context compaction",
				template("keeper.compact", "content/keeper/compact.md",
					TextField("source_path", "Source path"),
					TextField("candidate_path", "Candidate path"))),

				On("summarize", "headless_prompt", "internal/daemon/notebook_narration_prompts.go: buildSummarizeSessionPrompt",
					template("keeper.summarize", "content/keeper/summarize.md",
						TextField("transcript_path", "Transcript path"),
						TextField("session_id", "Session id"),
						TextField("raw_digest_path", "Raw digest path"))),

				On("narrate", "headless_prompt", "internal/daemon/notebook_narration_prompts.go: buildNarrateWorkspacePrompt",
					template("keeper.narrate", "content/keeper/narrate.md",
						TextField("workspace_title", "Workspace title"),
						TextField("workspace_id", "Workspace id"),
						TextField("context_snapshot_path", "Context snapshot path"),
						TextField("raw_sessions_dir", "Raw sessions dir"),
						ProducedBy(TextField("transcript_paths", "Transcript paths"), "keeper/transcript-paths"),
						TextField("journal_path", "Journal path"),
						TextField("journal_dir", "Journal dir"),
						TextField("knowledge_dir", "Knowledge dir"),
						TextField("is_removal_pass", "Is removal pass")))}},

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

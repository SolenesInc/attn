package prompts

func gardenAdvisorRecipient() Recipient {
	evidence := TextField("evidence", "Bounded seed and evidence serialized as JSON by the daemon.")
	return Recipient{ID: "garden-advisor", Description: "Tool-free model recommending a seed action or drafting its handoff.", Events: []Event{
		On("instructions", "system_prompt", "Shared output contract for the Garden advisor.", Use("garden-advisor.instructions", "content/garden-advisor/instructions.md")),
		On("advice", "user_message", "Recommend one available action for a seed.", template("garden-advisor.advice", "content/garden-advisor/advice.md", evidence)),
		On("handoff", "user_message", "Draft a handoff for a seed's next tender.", template("garden-advisor.handoff", "content/garden-advisor/handoff.md", evidence)),
	}}
}

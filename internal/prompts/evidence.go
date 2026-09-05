package prompts

func evidenceRecipient() Recipient {
	section := func(name string) Node {
		field := TextField(name, "Prepared conversation evidence.")
		return When(Enabled(FlagField("has_"+name, "The source section is present.")), template("conversation-evidence."+name, "content/evidence/"+name+".md", field))
	}
	return Recipient{ID: "conversation-evidence", Description: "Labeled evidence supplied to title generation and reconciliation.", Events: []Event{
		On("slice", "message_fragment", "The adapter joins and bounds the recorded turns.", Compose(section("brief"), section("rescoping"), section("summary"), section("agent_turns"))),
		On("truncated", "message_fragment", "The adapter applies the original byte limit.", template("conversation-evidence.truncated", "content/evidence/truncated.md", TextField("text", "Bounded evidence."), TextField("total", "Original length."))),
	}}
}

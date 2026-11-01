package prompts

func dictationRefinerRecipient() Recipient {
	instructions := func(format string) Node {
		return Compose(
			Use("dictation-refiner.instructions", "content/dictation-refiner/instructions.md"),
			Use("dictation-refiner."+format, "content/dictation-refiner/"+format+".md"),
		)
	}
	return Recipient{ID: "dictation-refiner", Description: "Tool-free model cleaning up a dictated transcript before it replaces the original.", Events: []Event{
		On("plain", "system_prompt", "Cleanup rules for text bound for a plain field or a terminal.", instructions("plain")),
		On("markdown", "system_prompt", "Cleanup rules for text bound for a Markdown editor.", instructions("markdown")),
	}}
}

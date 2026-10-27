package prompts

var askGuidance = Use("ask.guidance", "content/ask/guidance.md")

func askRecipient() Recipient {
	text := func(name, description string) Field { return TextField(name, description) }
	flag := func(name, description string) Condition { return Enabled(FlagField(name, description)) }
	fragment := func(name string, fields ...Field) Node {
		return template("ask."+name, "content/ask/"+name+".md", fields...)
	}
	picked := text("picked", "The labels the user picked; for a :::question-multi each is quoted, comma separated.")
	other := text("other", "The user's own answer to a choice question.")
	written := text("text", "The user's written answer, quoted by the adapter.")
	choices := Choose(flag("recommended", "The user picked the agent's recommendation alone."),
		fragment("entry-recommended", picked), fragment("entry-picked", picked))
	answer := Choose(flag("unanswered", "The user left this question unanswered."),
		fragment("entry-unanswered"),
		Compose(When(Present(picked), choices), When(Present(other), fragment("entry-other", other)), When(Present(written), Input(written))))
	entry := Use("ask.entry", "content/ask/entry.md",
		Bind("index", Input(text("index", "The question's position, from 1."))),
		Bind("prompt", Input(text("prompt", "The question as the agent asked it."))),
		Bind("answer", answer))
	entries := ProducedBy(text("entries", "Each question's entry in order, joined by the adapter."), "ask/entry")
	note := text("note", "The user's note, quoted by the adapter.")
	reply := Use("ask.answer", "content/ask/answer.md",
		Bind("subject", Input(text("subject", "Quoted ask subject."))),
		Bind("ask_id", Input(text("ask_id", "Ask ID."))),
		Bind("body", Compose(
			When(Present(entries), Input(entries)),
			When(Present(note), fragment("note", note)))))
	return Recipient{ID: "ask", Description: "Asks: questions an agent asks the user through attn, and the answers that come back.", Events: []Event{
		On("guidance", "message_fragment", "Launch guidance for a session that launched with asks.", askGuidance),
		On("answer", "inbox_content", "The user's answer, typed into the conversation of the ask's addressee: the seed's tender, or the asking agent when the ask is about no seed. A seed also keeps it as a note. The adapter validates answers against the ask.", reply),
		On("entry", "message_fragment", "One question and the user's answer to it.", entry),
	}}
}

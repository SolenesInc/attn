package prompts

var (
	askVisuals = Use("ask.visuals", "content/ask/visuals.md")
	askExample = Use("ask.example", "content/ask/example.md")
	askRules   = Use("ask.rules", "content/ask/rules.md", Bind("visuals", askVisuals))
	askCLI     = Use("ask.cli", "content/ask/cli.md", Bind("example", askExample))
)

func askRecipient() Recipient {
	text := func(name, description string) Field { return TextField(name, description) }
	flag := func(name, description string) Condition { return Enabled(FlagField(name, description)) }
	fragment := func(name string, fields ...Field) Node {
		return template("ask."+name, "content/ask/"+name+".md", fields...)
	}
	picked := text("picked", "The labels the user picked; for a :::question-multi each is quoted, comma separated.")
	other := text("other", "The user's own answer to a choice question.")
	condition := text("condition", "The user's condition on their pick.")
	written := text("text", "The user's written answer, quoted by the adapter.")
	questionFiles := Trimmed(text("question_files", "The files the user attached to a :::question-file, rendered by the adapter: one retrieval line each in an answer, their names in a follow-up."))
	choices := Choose(flag("recommended", "The user picked the agent's recommendation alone."),
		fragment("entry-recommended", picked), fragment("entry-picked", picked))
	answer := Choose(flag("unanswered", "The user left this question unanswered."),
		fragment("entry-unanswered"),
		Compose(When(Present(picked), choices), When(Present(condition), fragment("entry-condition", condition)), When(Present(other), fragment("entry-other", other)), When(Present(written), Input(written)), When(Present(questionFiles), Input(questionFiles))))
	entry := Use("ask.entry", "content/ask/entry.md",
		Bind("index", Input(text("index", "The question's position, from 1."))),
		Bind("prompt", Input(text("prompt", "The question as the agent asked it."))),
		Bind("answer", answer))
	entries := ProducedBy(text("entries", "Each question's entry in order, joined by the adapter."), "ask/entry")
	note := text("note", "The user's note, quoted by the adapter.")
	files := Trimmed(ProducedBy(text("files", "The files the answer carried, one retrieval line each, rendered by the adapter."), "session/quick-capture-file"))
	reply := Use("ask.answer", "content/ask/answer.md",
		Bind("subject", Input(text("subject", "Quoted ask subject."))),
		Bind("ask_id", Input(text("ask_id", "Ask ID."))),
		Bind("body", Compose(
			When(Present(entries), Input(entries)),
			When(Present(note), fragment("note", note)),
			When(Present(files), Use("ask.files", "content/ask/files.md", Bind("files", Input(files)))))))
	defaultDue := Use("ask.default-due", "content/ask/default-due.md",
		Bind("subject", Input(text("subject", "Quoted ask subject."))),
		Bind("ask_id", Input(text("ask_id", "Ask ID."))),
		Bind("prompt", Input(text("prompt", "The question that names the default, as the agent asked it."))),
		Bind("choice", Input(text("choice", "The default choice's label."))))
	followUp := Use("ask.follow-up", "content/ask/follow-up.md",
		Bind("subject", Input(text("subject", "Quoted ask subject."))),
		Bind("ask_id", Input(text("ask_id", "Ask ID."))),
		Bind("text", Input(text("text", "The user's follow-up, quoted by the adapter."))))
	return Recipient{ID: "ask", Description: "Asks: questions an agent asks the user through attn, and the answers that come back.", Events: []Event{
		On("rules", "message_fragment", "Launch guidance for every session that launched with asks, whether it asks through attn's tools or the CLI: when to ask, how to write for a cold reader, and which visual to show.", askRules),
		On("cli", "message_fragment", "Launch guidance for a session that launched with asks and asks through the CLI: the attn ask syntax with a worked example.", askCLI),
		On("visuals", "message_fragment", "Which visual shows what a decision turns on, for the ask tool's description and attn ask --help.", askVisuals),
		On("example", "message_fragment", "A worked ask's markdown, for the ask tool's description and attn ask --help.", askExample),
		On("answer", "inbox_content", "The user's answer, typed into the conversation of the ask's addressee: the seed's tender, or the asking agent when the ask is about no seed. A seed also keeps it as a note. The adapter validates answers against the ask.", reply),
		On("follow-up", "inbox_content", "The user's follow-up on an ask they already answered, typed into the conversation of whoever holds the ask now, as the answer was. A seed also keeps it as a note.", followUp),
		On("entry", "message_fragment", "One question and the user's answer to it.", entry),
		On("entry-answer", "message_fragment", "The user's answer to one question, without the question: what a follow-up ask shows the user under the earlier question.", answer),
		On("default-due", "inbox_content", "A notice to the ask's addressee once its default falls due with no answer: the agent goes with the default, and the ask stays open.", defaultDue),
	}}
}

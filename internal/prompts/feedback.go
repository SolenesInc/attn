package prompts

func feedbackEvents() []Event {
	approved := FlagField("approved", "The user approved the review round.")
	fields := []Field{TextField("round", "Review round number."), TextField("title", "Quoted presentation title."), TextField("presentation_id", "Presentation ID.")}
	notice := Choose(Enabled(approved), template("session.present-approved", "content/session/present-approved.md", fields...), template("session.present-submitted", "content/session/present-submitted.md", fields...))
	return []Event{
		On("inbox-notification", "user_message", "Generic notification directing the recipient to the durable inbox.", Use("session.inbox-notification", "content/session/inbox-notification.md")),
		On("inbox-empty", "cli_output", "No unread items remain.", Use("session.inbox-empty", "content/session/inbox-empty.md")),
		On("inbox-more", "cli_output", "Read the next batch of unread items.", template("session.inbox-more", "content/session/inbox-more.md", TextField("remaining", "Unread items remaining."))),
		On("inbox-item", "cli_output", "An inbox item other than a peer message or maintenance prompt.", template("session.inbox-item", "content/session/inbox-item.md", TextField("content", "Rendered inbox item."))),
		On("peer-message", "cli_output", "Attributed peer message, including the trust boundary and reply command.",
			template("session.peer-message", "content/session/peer-message.md", TextField("origin", "Sender identity."), TextField("message", "Literal peer message."), TextField("sender_id", "Reply target."))),
		On("present-feedback", "seed_note", "Review notice appended to the reporting seed.", notice),
		On("present-handback", "user_message", "Review notice delivered to an unbound session.", Use("session.present-handback", "content/session/present-handback.md", Bind("notice", notice))),
	}
}

func resourceRecipient() Recipient {
	return Recipient{ID: "authoring-agent", Description: "Agent-facing guidance loaded from CLI output or a generated project file.", Events: []Event{
		On("seed-guide", "cli_output", "attn seed guide: guidance for planning and reporting work.", Document("authoring.seed-guide", "content/resources/seed-guide.md")),
		On("app-guidance", "project_instructions", "AGENTS.md written by app scaffolding; the harness loads it from the generated project.", template("authoring.app-guidance", "content/resources/app-guidance.md", TextField("app_name", "App name."), TextField("sdk_module", "SDK import path."))),
	}}
}

func annotationRecipients() []Recipient {
	return []Recipient{markdownAnnotationRecipient(), terminalAnnotationRecipient()}
}

func markdownAnnotationRecipient() Recipient {
	text := func(name string) Field { return TextField(name, name) }
	flag := func(name string) Condition { return Enabled(FlagField(name, name)) }
	fragment := func(name string, fields ...Field) Node {
		return Exact(template("annotation-markdown."+name, "content/annotations/markdown-"+name+".md", fields...))
	}
	position := Choose(flag("orphaned"), fragment("moved", text("start_line")),
		Choose(flag("multiline"), fragment("range", text("start_line"), text("end_line")),
			fragment("line", text("start_line"))))
	line := When(flag("has_anchor"), Join(" ", position, Compose()))
	quickLabel := Join("", fragment("quick-label", text("label"), text("quote")),
		When(flag("has_tip"), fragment("comment-line", text("tip"))))
	body := Choose(flag("deletion"), fragment("deletion", text("quote")),
		Choose(flag("global"), fragment("global", text("comment")),
			Choose(flag("quick_label"), quickLabel, fragment("comment", text("quote"), text("comment")))))
	entry := Exact(Use("annotation-markdown.entry", "content/annotations/markdown-entry.md",
		Bind("index", Input(text("index"))), Bind("line", line), Bind("body", body)))
	seed := Use("annotation-markdown.seed", "content/annotations/markdown-seed.md",
		Bind("seed_id", Input(seedID)), Bind("title", When(flag("has_title"), fragment("title", text("title")))))
	submit := Use("annotation-markdown.submit", "content/annotations/markdown-submit.md",
		Bind("subject", Choose(flag("seed"), seed, fragment("file", text("path")))),
		Bind("count", Input(text("count"))),
		Bind("pieces", Choose(flag("singular"), fragment("piece"), fragment("pieces"))),
		Bind("entries", Input(ProducedBy(text("entries"), "annotation-markdown/entry"))),
		Bind("summary", When(Present(text("summary_rows")), fragment("summary", text("summary_rows")))))
	return Recipient{ID: "annotation-markdown", Description: "Feedback sent from a Markdown document or seed to a session or seed log.", Events: []Event{
		On("submit", "user_message", "Sorted feedback entries and label counts are prepared by the adapter.", submit),
		On("entry", "message_fragment", "One annotation, including line position, type and optional quick-label guidance.", entry),
	}}
}

func terminalAnnotationRecipient() Recipient {
	text := func(name string) Field { return TextField(name, name) }
	fragment := func(name string, fields ...Field) Node {
		return Exact(template("annotation-terminal."+name, "content/annotations/terminal-"+name+".md", fields...))
	}
	entry := Exact(Use("annotation-terminal.entry", "content/annotations/terminal-entry.md",
		Bind("index", Input(text("index"))),
		Bind("heading", Choose(Present(text("heading")), Input(text("heading")), fragment("comment-heading"))),
		Bind("quote", Input(text("quote"))),
		Bind("tip", When(Present(text("tip")), fragment("tip", text("tip")))),
		Bind("comment", When(Enabled(FlagField("has_comment", "The comment is nonempty.")), fragment("comment", text("comment"))))))
	submit := Use("annotation-terminal.submit", "content/annotations/terminal-submit.md",
		Bind("note", When(Present(text("note")), fragment("note", text("note")))),
		Bind("entries", Input(ProducedBy(text("entries"), "annotation-terminal/entry"))))
	return Recipient{ID: "annotation-terminal", Description: "Feedback sent from annotated terminal output.", Events: []Event{
		On("submit", "user_message", "Ordered entries and an optional overall note; the frontend trims trailing JavaScript whitespace.", submit),
		On("entry", "message_fragment", "One terminal selection; the adapter quotes lines and resolves a quick label.", entry),
	}}
}

func annotationLabelRecipient() Recipient {
	r := Recipient{ID: "annotation-label", Description: "Built-in quick-label instructions included with selected feedback."}
	for _, name := range []string{"verify-this", "show-the-receipt", "give-me-an-example", "consider-alternatives"} {
		r.Events = append(r.Events, On(name, "message_fragment", "Tip attached when this feedback label is selected.", Use("annotation-label."+name, "content/annotations/labels/"+name+".md")))
	}
	return r
}

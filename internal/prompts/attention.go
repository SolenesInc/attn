package prompts

func waitCheckEvent() Event {
	answer := TextField("answer", "The agent's answer to the previous check of this wait, when it answered one.")
	return On("wait-check", "inbox_content", "A supervisor notice to an agent whose background wait passed an elapsed checkpoint.",
		Use("attention.wait-check", "content/attention/wait-check.md",
			Bind("wait", Input(TextField("wait", "The wait: task and its quoted description and id, or wait when the harness listed no task."))),
			Bind("age", Input(TextField("age", "How long the wait has run, such as 20 min or 4 h."))),
			Bind("silent", Input(TextField("silent", "How long since the agent's last model request."))),
			Bind("ask", Compose(
				When(Present(answer), Use("attention.wait-check-answered", "content/attention/wait-check-answered.md", Bind("answer", Quoted(answer)))),
				Choose(Enabled(FlagField("last", "The 4 h check, attn's last of this wait.")),
					Use("attention.wait-check-last", "content/attention/wait-check-last.md"),
					Use("attention.wait-check-ask", "content/attention/wait-check-ask.md"))))))
}

func delegateCheckEvent() Event {
	return On("delegate-check", "inbox_content", "A supervisor notice to a live dispatcher whose delegate the supervisor judged open or finished, before attn opens the delegate's turn.",
		Use("attention.delegate-check", "content/attention/delegate-check.md",
			Bind("delegate", Input(TextField("delegate", "The delegate's name."))),
			Bind("session", Input(TextField("session", "The delegate's short session id."))),
			Bind("reason", Input(TextField("reason", "The verdict's reason."))),
			Bind("verdict", Choose(Enabled(FlagField("finished", "The verdict is finished; otherwise open.")),
				Use("attention.delegate-finished", "content/attention/delegate-finished.md"),
				Use("attention.delegate-open", "content/attention/delegate-open.md")))))
}

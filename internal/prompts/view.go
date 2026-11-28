package prompts

func viewRecipient() Recipient {
	text := func(name, description string) Field { return TextField(name, description) }
	sent := Use("view.answers-sent", "content/view/answers-sent.md",
		Bind("title", Input(text("title", "Quoted view title: its first heading, or its file's name."))),
		Bind("summary", Input(text("summary", "What the user did, counted by the adapter, such as 7 kept, 4 changed (clip-03, clip-05), 1 skipped (clip-07)."))),
		Bind("responses", Input(text("responses", "The path of the responses file, on the machine that holds the view."))))
	return Recipient{ID: "view", Description: "Views: markdown files with parts that attn draws in a tile, and the answers the user sends back.", Events: []Event{
		On("answers-sent", "inbox_content", "The user sent their answers to a view, typed into the conversation of the agent that opened it: one summary line and the path of the responses file beside the view.", sent),
	}}
}

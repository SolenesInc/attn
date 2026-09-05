package prompts

func activityRecipient() Recipient {
	reason := Trimmed(TextField("state_reason", "State reason."))
	previous := Trimmed(TextField("previous", "Previous status line."))
	window := Trimmed(TextField("window", "Rendered transcript events."))
	fallback := func(field Field, name string) Node {
		return Choose(Present(field), Input(field), Use("activity."+name, "content/activity/"+name+".md"))
	}
	return Recipient{ID: "activity", Description: "Separate model writing a session's dashboard status line.", Events: []Event{
		On("system", "system_prompt", "Invariant instructions, replacing the headless harness system prompt.",
			Trim(Part(Use("activity.system", "content/activity/baseline.md"), "{{USER}}", 0))),
		On("user", "user_message", "Run data and fallbacks for missing reason, previous line and events.",
			Trim(Part(Use("activity.user", "content/activity/baseline.md",
				Bind("STATE", Input(Trimmed(TextField("state", "Current state.")))),
				Bind("STATE_REASON", fallback(reason, "unspecified")),
				Bind("PREVIOUS", fallback(previous, "first-line")),
				Bind("WINDOW", fallback(window, "empty-window"))), "{{USER}}", 1))),
		On("state-reason", "message_fragment", "Also used by custom benchmark templates.", fallback(reason, "unspecified")),
		On("previous", "message_fragment", "Also used by custom benchmark templates.", fallback(previous, "first-line")),
		On("window", "message_fragment", "Also used by custom benchmark templates.", fallback(window, "empty-window")),
	}}
}

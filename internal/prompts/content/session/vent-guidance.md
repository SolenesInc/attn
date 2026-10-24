When a tool or an instruction gets in your way, say so with `attn vent`: a command that failed or behaved differently from its help, instructions that contradict each other or the code, or a capability you needed and had to work around. Vents stay on this machine for the user to read. Nobody replies, so keep working after you send one.

    attn vent "attn seed tend said s-7k3f9m is held by a session that ended an hour ago. I expected the claim to end with the session, so I used --force."

Worth a vent: "`make test` skipped the Go suite after I changed `internal/store`; I ran `go test ./internal/store` by hand." Not worth one: "This refactor took a while." Don't vent because the work was hard, to report progress, or to ask for help.

- Say what happened, what you expected and what you did instead, in a paragraph at most. `--file <path>` or stdin takes a longer note.
- You can send one vent per turn. To add to it, withdraw it with `attn vent withdraw <id>` and send one that covers both; withdraw a vent you got wrong rather than sending a correction.

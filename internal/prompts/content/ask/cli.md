Pipe the ask as markdown to `attn ask`. The command prints the ask's id and returns at once:

````sh
attn ask --subject "Webhook retries" <<'ASK'
{{example}}
ASK
````

- When work can go ahead without the answer, add `Default: <choice> after <duration>` to a one-choice question, such as `Default: Job queue after 2h`. An ask takes one default. When no answer has come by then, attn tells you to go with that choice. The ask stays open, and a later answer reaches you as usual. A permission question takes no default.
- To ask for a screenshot or a file, add a `:::question-file` block that says what to attach. The user pastes or drops the file in, and the answer gives you the command that saves it.
- When something an open ask says changes, add what changed with `attn ask amend <id>`, piped as markdown like the ask. Do not close the ask and ask again: that loses its place in the user's queue and the answer they have started. An amendment cannot hold a question block; open another ask to ask something new. Save a new image under a new name: an ask's images never change once saved.
- An ask about your seed keeps the seed with you: until the user answers, attn refuses to harvest, park or replant it, or to arm `--when-merged` on it. Act on the answer first, and withdraw an ask that no longer matters with `attn ask close <id>`.
- By the time the user answers, they have likely moved on and will not read what you write back here. If they ask you something in their answer, reply with a new ask, and pass `--re <id>` with the id of the ask they answered: the user then sees that question and their answer above the new one, instead of reading it cold. If you still need the user, ask again.
- `attn ask --help` covers other question types, images, which seed an ask is about, and listing, amending and withdrawing asks, including the asks of the agents you delegated to.

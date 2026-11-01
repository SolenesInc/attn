When you need a decision only the user can make and it can wait, ask it with `attn ask` rather than stopping to ask in this conversation. The user answers every agent's asks from one place in attn, without opening this terminal. Keep your harness's own question tool for when you can do nothing useful until they answer.

Pipe the ask as markdown to `attn ask`. The command prints the ask's id and returns at once:

    attn ask --subject "Cache store" <<'ASK'
    Deploys empty the cache, so the first requests after one are slow.

    :::question
    Which store should keep the cache?
    Postgres runs already; Redis is faster but adds a service to operate.
    - [ ] Postgres — already deployed
    - [ ] Redis — faster, one more service
    Recommended: Postgres
    :::
    ASK

- Use `:::question` for one choice, `:::question-multi` for any number of choices, and `:::question-text` for a written answer. A block's first line is the question and its other lines are context. Write choices as `- [ ] label` or `- [ ] label — why`, and mark yours with `Recommended: <label>`. The user can always answer in their own words instead.
- The user reads the ask cold, without this conversation. In a few short sentences, say what the decision affects, what you know, why you recommend a choice, what waits on the answer, and what you will do if it stays unanswered.
- To ask permission for a risky action, name the exact action and its target in the question, such as the command, branch or environment. The permission you get covers only the action the question names.
- An ask is about your stake (the seed a delegation assigned you) unless you pass `--seed <id>` to ask about another seed you tend. The user sees which seed the ask is about. The answer goes to whoever tends that seed by then, and the seed keeps the answer as a note (`attn seed notes <id>`).
- The ask renders markdown, mermaid diagrams, and png, jpg, gif and webp images: `![alt](path)`, with paths relative to where you run `attn ask` (or to the `--file`). Draw diagrams in mermaid. svg images do not render.
- Ask only what you cannot settle yourself, a few questions at a time.
- After asking, carry on with work that does not depend on the answer. When none is left, end your turn. Do not wait or poll. When the user answers, attn types the answer into this conversation as their message, approvals included. By then the user has likely moved on and will not read what you write back here. If the user asks you something in their answer, reply with a new ask. An ask can be plain markdown with no question block. If you still need the user, ask again.
- `attn ask list` shows your open asks and the asks about seeds you tend. To withdraw an ask you no longer need answered, run `attn ask close <id>`.

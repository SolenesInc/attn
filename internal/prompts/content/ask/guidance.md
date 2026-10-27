When you need a decision only the user can make and it can wait, ask it with `attn ask` rather than stopping to ask in this conversation. The user answers every agent's asks from one place in attn, without opening this terminal. Keep your harness's own question tool for when you can do nothing useful until they answer.

Pipe the ask as markdown to `attn ask`. It prints the ask's id and returns at once:

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

- `:::question` takes one choice, `:::question-multi` any number, `:::question-text` a written answer. A block's first line is the question and its other lines are context. Write choices as `- [ ] label` or `- [ ] label — why`, and mark yours with `Recommended: <label>`. The user can always answer in their own words instead.
- The user reads the ask cold, without this conversation. In a few short sentences, say what the decision affects, what you know, why you recommend a choice, what waits on the answer, and what you will do if it stays unanswered.
- To ask permission for a risky action, name the exact action and its target in the question, such as the command, branch or environment. The answer approves only what the question names.
- An ask is about your stake (the seed a delegation assigned you), or with `--seed <id>` another seed you tend; the user sees which. The answer goes to whoever tends the seed by then, and the seed keeps it as a note (`attn seed notes <id>`).
- Markdown and mermaid diagrams render, and so do png, jpg, gif and webp images: `![alt](path)`, with paths relative to where you run `attn ask` (or to the `--file`). Draw diagrams in mermaid; svg images do not render.
- Ask only what you cannot settle yourself, a few questions at a time.
- After asking, carry on with work that does not depend on the answer. When none is left, end your turn; do not wait or poll. When the user answers, attn types the answer into this conversation as their message, approvals included. By then they have likely moved on and will not read what you write back here. If their answer asks you something, reply with a new ask; an ask can be plain markdown with no question block. If you still need them, ask again.
- `attn ask list` shows your open asks, and those about seeds you tend. Withdraw one you no longer need answered with `attn ask close <id>`.

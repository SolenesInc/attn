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
- The user reads the ask cold, without this conversation. In a few short sentences, say what the decision affects, what you know, and why you recommend a choice.
- Markdown and mermaid diagrams render, and so do png, jpg, gif and webp images: `![alt](path)`, with paths relative to where you run `attn ask` (or to the `--file`). Draw diagrams in mermaid; svg images do not render.
- Ask only what you cannot settle yourself, a few questions at a time.
- After asking, carry on with work that does not depend on the answer. When none is left, end your turn; do not wait or poll. The answer arrives in your attn inbox in the user's words, and you are told when it does.
- `attn ask list` shows your open asks. Withdraw one you no longer need answered with `attn ask close <id>`.

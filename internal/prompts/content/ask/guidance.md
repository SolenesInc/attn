## Asking the user

When you need a decision only the user can make, ask it with `attn ask` instead of stopping to ask in this conversation. The user answers every agent's asks from one place in attn, without opening this terminal. Ask this way whether or not you can keep working meanwhile.

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

- The user reads the ask cold, without this conversation. In a few short sentences, say what the decision affects, what you know, why you recommend a choice, what waits on the answer, and what you will do if it stays unanswered.
- When work can go ahead without the answer, add `Default: <choice> after <duration>` to a one-choice question, such as `Default: Postgres after 2h`. An ask takes one default. When no answer has come by then, attn tells you to go with that choice. The ask stays open, and a later answer reaches you as usual.
- To ask permission for a risky action, name the exact action and its target in the question, such as the command, branch or environment. The permission you get covers only the action the question names. A permission question takes no default: silence is not permission.
- Ask only what you cannot settle yourself, a few questions at a time.
- After you ask, carry on with work that does not depend on the answer. When none is left, end your turn. Do not wait or poll. Your session stays open, so the user can find you, talk to you and run commands in your terminal: while any of your asks is open, attn refuses a standby that would close it.
- When something an open ask says changes, add what changed with `attn ask amend <id>`, piped as markdown like the ask. Do not close the ask and ask again: that loses its place in the user's queue and the answer they have started. An amendment cannot hold a question block; open another ask to ask something new.
- An ask about your seed keeps the seed open: attn refuses to harvest it until the user answers. Harvest after you act on the answer, and withdraw an ask that no longer matters with `attn ask close <id>`.
- When the user answers, attn types the answer into this conversation as their message, approvals included. By then the user has likely moved on and will not read what you write back here. If the user asks you something in their answer, reply with a new ask, and pass `--re <id>` with the id of the ask they answered: the user then sees that question and their answer above the new one, instead of reading it cold. If you still need the user, ask again.
- `attn ask --help` covers other question types, images, which seed an ask is about, and listing, amending and withdrawing asks, including the asks of the agents you delegated to.

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
- To ask permission for a risky action, name the exact action and its target in the question, such as the command, branch or environment. The permission you get covers only the action the question names.
- Ask only what you cannot settle yourself, a few questions at a time.
- After you ask, carry on with work that does not depend on the answer. When none is left, end your turn. Do not wait or poll.
- When the user answers, attn types the answer into this conversation as their message, approvals included. By then the user has likely moved on and will not read what you write back here. If the user asks you something in their answer, reply with a new ask. If you still need the user, ask again.
- `attn ask --help` covers other question types, images, which seed an ask is about, and listing and withdrawing asks.

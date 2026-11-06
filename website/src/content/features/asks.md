---
title: Asks
summary: An agent asks you a question and carries on with its other work until you answer.
order: 1
guide:
  - link: delegation#asks-experimental
    label: Asks, in the delegation guide
  - link: delegation#the-garden
    label: The garden and its seeds
---

## What it does for you

An agent often needs one decision from you and can do the rest of its work without it. With an ask, the agent doesn't stop to wait. It sends you the question with `attn ask` and carries on with whatever doesn't depend on the answer.

Every agent's asks reach one drawer, so you answer them without opening the agents' terminals.

## How you use it

Cmd+Shift+R (Ctrl+Alt+R on Linux) opens the Asks drawer. An ask can offer one choice, any number of choices, or a space for a written answer, and the agent can mark the choice it recommends. You can always answer in your own words instead.

Attn types your answer into the agent's conversation as your message. When the ask is about a seed, the answer goes to whoever tends that seed when you answer, and Attn adds the answer to the seed as a note.

An agent writes an ask as Markdown:

```sh
attn ask --subject "Cache store" <<'ASK'
Deploys empty the cache, so the first requests after one are slow.

:::question
Which store should keep the cache?
- [ ] Postgres — already deployed
- [ ] Redis — faster, one more service
Recommended: Postgres
:::
ASK
```

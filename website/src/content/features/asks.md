---
title: Asks
summary: An agent asks you a question and carries on with its other work until you answer.
order: 1
guide:
  - link: delegation#asks
    label: Asks, in the delegation guide
  - link: delegation#the-garden
    label: The garden and its seeds
---

## What it does for you

An agent often needs one decision from you and can do the rest of its work without it. With an ask, the agent doesn't stop to wait. It sends you the question with `attn ask` and carries on with whatever doesn't depend on the answer.

Every agent's asks reach one sheet in the middle of the window, so you answer them without opening the agents' terminals.

## How you use it

Cmd+Shift+R (Ctrl+Alt+R on Linux) opens Asks. An ask can offer one choice, any number of choices, or a space for a written answer, and the agent can mark the choice it recommends. You can always answer in your own words instead.

One question of an ask is active, marked by a bar at its side. The number keys pick a choice of the active question, and Tab moves the active question on to the next question and then to your note. To quote part of the ask in your note, select it and press Q.

Attn types your answer into the agent's conversation as your message. When the ask is about a seed, the answer goes to whoever tends that seed when you answer, and Attn adds the answer to the seed as a note.

When words won't do, show the agent: paste a screenshot into your note, or drop a file on the sheet. The agent gets each file with your answer and saves it with one command.

An agent writes an ask as Markdown:

```sh
attn ask --subject "Cache store" <<'ASK'
Deploys empty the cache, so the first requests after one are slow.

:::question
Which store should keep the cache?
- [ ] Postgres — already deployed
- [ ] Redis — faster, one more service
Recommended: Postgres
Default: Postgres after 2h
:::
ASK
```

When work can go ahead without you, a one-choice question can name a default. If you haven't answered by then, Attn tells the agent to go with that choice. The ask stays open, and your answer still reaches the agent when you send it. Asks for permission never take a default.

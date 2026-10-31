<p align="center"> <img src="docs/banner.png" alt="attn, attention hub"
width="100%" /> </p>

# attn

**attention hub**, because your head shouldn't feel like concrete by 3pm.

I built attn after noticing a pattern: I'd start the day sharp, spin up 4-5 AI
agents across different repos, and by mid-afternoon my brain was soup. Not from
the coding, from the _managing_. Which terminal has the agent that's stuck? Did
that one finish? Wait, who asked me a question 20 minutes ago? Alt-tab, alt-tab,
alt-tab, scroll, squint, repeat.

attn stands for Attention. It's an interface that is friendly to both human and
agent brains, built as a harness augmenter. What it does today:

- **Durability.** Restart the attn app, its daemon, even your computer. Open
  attn again and your agents and terminals are still there, waiting for you.
- **Bring your own harness.** attn runs Claude Code, Codex, Pi, and other
  CLI agents. You get their native experience, but better. A big lab ships
  a fancy new feature? You get it immediately, as they designed it. Tired of
  attn? Just use the harness in the terminal.
- **The queue mode.** A (currently non-default) mode for running many agents at
  once with lower cognitive overload. Agents are either "waiting for you" or
  "busy"; after every prompt attn moves you to the next one that needs you, so
  you don't waste cycles picking. Simple, very powerful. Trust me, you want to
  try it.
- **The Garden.** attn's own task tracking. Work items are seeds you plant,
  agents tend them, and they get harvested. Like beads, but inside attn.
- **Visible orchestration.** Agents delegate to other full-blown agents (not
  subagents) that you can inspect and steer, from any harness to any harness.
  Plan with Fable in Claude Code, implement with GLM 5.3 in Pi, review in Codex
  with Sol. Agents can even message each other and arrange a party.
- **Crew members.** Custom permanent agents, each with its own charter. There's
  also the Chief that keeps track of your agents and helps you delegate, the
  executive assistant for your neurospicy brain.
- **Automations.** Start a steerable agent on a schedule or on an event (a
  review request on a PR, say) and get back a ready review guide + assessment.
- **Annotate anything, send it to the agent.** Opt/alt-select text in a live
  terminal, or in a markdown file attn renders natively (Cmd+click a `.md` path,
  or have the agent run `attn open plan.md`), leave your comments, and send the
  whole batch to the agent as one message.

There are more features, but those are the ones I use every day and am proud about.

## Install

On a Mac (Apple Silicon), with Homebrew:

```bash
brew tap victorarias/attn https://github.com/victorarias/attn
brew install --cask victorarias/attn/attn
```

Or grab the DMG from the
[latest release](https://github.com/victorarias/attn/releases/latest), open it
and drag attn to Applications. The app tells you when a new release is out.

The Linux app is still in progress; build it from source as
[getting started](docs/guide/tutorials/getting-started.md#linux) describes. The
daemon and CLI run on Linux too.

## The guide

[Get started](docs/guide/tutorials/getting-started.md), then explore the
[user guide](docs/guide/README.md). The same pages are built into attn:
`attn guide` lists them, and `attn guide <topic>` prints one in your terminal.

For development, see [AGENTS.md](AGENTS.md) and
[instances](docs/internal/instances.md).

## Status

Alpha. I use it every day. It's rough around the edges, but it works really well
once you get going.

## A note about this project

I built attn because I needed it. Managing multiple agents was the bottleneck,
not the coding itself.

I work full time, and I have 2 dogs and 4 cats at home. Free time is not
abundant. attn is open source, but if you need changes I can't get to, **fork
it**. That's encouraged, not rude.

**Issues are welcome**, but please be detailed:

- Describe what happened and what you expected.
- Include the tail of `~/.attn/daemon.log` from right after you reproduced the
  issue.
- For visual bugs, attach a screenshot.

Vague "it doesn't work" reports aren't actionable and will sit there forever.

I'm a friendly person and happy to help when I can. But don't be entitled about
it; that's the one thing I can't deal with. Entitled behavior gets ignored, and
if I'm having a bad day, maybe banned.

## Built with

[Tauri](https://tauri.app) / [React](https://react.dev) / [Go](https://go.dev) /
[SQLite](https://sqlite.org)

## License

[GPL-3.0](LICENSE)

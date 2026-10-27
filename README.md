<p align="center"> <img src="docs/banner.png" alt="attn, attention hub"
width="100%" /> </p>

# attn

**attention hub**, because your head shouldn't feel like concrete by 3pm.

I built attn after noticing a pattern: I'd start the day sharp, spin up 4-5 AI
agents across different repos, and by mid-afternoon my brain was soup. Not from
the coding, from the _managing_. Which terminal has the agent that's stuck? Did
that one finish? Wait, who asked me a question 20 minutes ago? Alt-tab, alt-tab,
alt-tab, scroll, squint, repeat.

attn stands for Attention. It brings your coding agents into one app, keeps
their sessions and terminals through restarts, and shows who needs you. Use
Claude Code, Codex or Pi the way you already do. The Chief is an always-on
agent. Ask it to coordinate your work, manage attn for you, or simply go
straight to your agents.

## Install

### Homebrew cask (desktop app)

```bash
brew tap victorarias/attn https://github.com/victorarias/attn
brew install --cask victorarias/attn/attn
```

This installs `attn.app` with its bundled daemon/runtime binary.

### Direct DMG

Grab the [latest release](https://github.com/victorarias/attn/releases/latest),
open the DMG, drag to Applications.

### Updating

```bash
brew update && brew upgrade --cask victorarias/attn/attn
```

The app nudges you when a new release exists.

## The guide

[Get started](docs/guide/tutorials/getting-started.md), then explore the
[user guide](docs/guide/README.md). The same pages are built into attn:
`attn guide` lists them, and `attn guide <topic>` reads one in your terminal.

[Outposts](docs/guide/reference/using-outposts.md) connects attn on other computers.

For development, see [AGENTS.md](AGENTS.md) and
[instances](docs/internal/instances.md). Licensed under [GPL-3.0](LICENSE).

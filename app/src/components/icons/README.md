# Harness icons

The sidebar uses monochrome SVGs at 16 px, matching the existing Claude icon's
default size. `HarnessIcon` maps the session's harness name to the mark; shell
and unknown harnesses use local fallback drawings. Color comes from the sidebar
theme, and an accessible name and tooltip identify the harness.

| Component | Source | Local changes |
| --- | --- | --- |
| ClaudeIcon | Existing attn asset | None |
| CodexIcon | [Lobe Icons](https://github.com/lobehub/lobe-icons/blob/master/packages/static-svg/icons/codex.svg) | React attributes, inherited color, shared accessible label |
| PiIcon | [Dashboard Icons](https://github.com/homarr-labs/dashboard-icons/blob/main/svg/pi-coding-agent.svg) | Inherited color; viewBox cropped to the mark's bounds so it fills the icon box |
| CopilotIcon | [GitHub Octicons, 16 px](https://github.com/primer/octicons/blob/main/icons/copilot-16.svg) | React attributes, inherited color, shared accessible label |

The upstream license notices are retained in the adjacent `.LICENSE` files.
The Pi asset is a mirrored copy of the compact badge from [Pi's press kit](https://pi.dev/press-kit).

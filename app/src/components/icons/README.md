# Harness icons

The sidebar uses monochrome SVGs at 16 px, matching the existing Claude icon's
default size. `HarnessIcon` maps the session's harness name to the mark; shell
and unknown harnesses use local fallback drawings. Color comes from the sidebar
theme, and an accessible name and tooltip identify the harness.

| Component | Source | Local changes |
| --- | --- | --- |
| ClaudeIcon | Existing attn asset | None |
| CodexIcon | [Lobe Icons](https://github.com/lobehub/lobe-icons/blob/master/packages/static-svg/icons/codex.svg) | React attributes, inherited color, shared accessible label |

The upstream license notice is retained in the adjacent `.LICENSE` file.

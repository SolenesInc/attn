# Third-Party Notices

## tauri-plugin-webdriver

Parts of attn's browser automation executor and macOS JavaScript dialog handler
are adapted from `tauri-plugin-webdriver` 0.2.1:

https://github.com/Choochmeque/tauri-plugin-webdriver

The adapted code targets attn's dynamically created child `tauri::Webview`,
uses attn's authenticated daemon transport instead of the plugin's HTTP server,
and does not depend on the plugin at build or runtime.

Copyright (c) 2026 Vladimir Pankratov

Licensed under the MIT License. See `LICENSE.tauri-plugin-webdriver`.

## show-me (humanlayer/skills)

The attn agent skill's showing reference
(`internal/prompts/content/skills/attn/references/showing.md`, embedded in the attn
binary and installed into agent skill directories) adapts the forms and
examples of the `show-me` skill:

https://github.com/humanlayer/skills

Copyright (c) 2026 HumanLayer

Licensed under the MIT License. See `LICENSE.humanlayer-skills`.

## pstack (cursor/plugins)

The attn-workflow skill's technical writing reference
(`internal/prompts/content/skills/attn-workflow/references/technical-writing.md`,
embedded in the attn binary and installed into agent skill directories) copies
pstack's `technical-writing` skill, adapted where it names other pstack skills.
The skill's structure checks in `references/design.md`, the design-twice,
tradeoffs and alternatives guidance in `references/planning.md`, and the
redesign signals in `SKILL.md` adapt pstack's `architect` skill:

https://github.com/cursor/plugins/tree/main/pstack

Copyright (c) 2026 Lauren Tan

Licensed under the MIT License. See `LICENSE.pstack`.

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
Its correction reference (`references/correction.md`) adapts pstack's `correct`
skill, and its feature map reference (`references/feature-map.md`) adapts the
`create-verification-skill` skill and its feature-map example. The principle
files under `principles/` adapt pstack's `principle-*` skills, renamed and
rewritten in attn's terms; the redesign signals in
`principles/attack-the-premise.md` also draw on pstack's `architect` skill. The
structure checks in `references/design.md` and the design-twice, tradeoffs and
alternatives guidance in `references/planning.md` adapt the `architect` skill, and
the independent candidates in `references/prototype.md` adapt its `arena` skill. The independent-review, standing-brief and second-review
guidance in `references/orchestration.md` and `references/review.md` adapts
pstack's orchestrate and autopilot playbooks and its `interrogate` skill. The
repository's `audit-feature-map` skill (`.agents/skills/audit-feature-map`)
adapts pstack's `maintain-verification-skill` skill:

https://github.com/cursor/plugins/tree/main/pstack
https://github.com/backnotprop/pstack

Copyright (c) 2026 Lauren Tan

Licensed under the MIT License. See `LICENSE.pstack`.

## Hex, transcribe.cpp and the speech models

The private speech helper (`app/speech`, bundled as `attn-speech`) is extracted
from Hex's model preparation, session handling, resampling and speech detection:

https://github.com/anomalyco/hex

Copyright (c) 2026 Christopher Langton

Licensed under the MIT License. See `app/speech/LICENSES/hex.LICENSE`.

The helper statically links transcribe.cpp, which includes ggml and miniz:

https://github.com/handy-computer/transcribe.cpp

Copyright (c) 2026 The transcribe.cpp authors

Licensed under the MIT License. See `app/speech/LICENSES/transcribe.cpp.LICENSE`
and `app/speech/LICENSES/transcribe.cpp.THIRD-PARTY-LICENSES.md`.

Setup downloads two models into attn's model cache. They are not part of the
app bundle and carry their own terms:

- Parakeet Unified English (GGUF conversion of NVIDIA's parakeet-unified-en-0.6b),
  licensed by NVIDIA Corporation under the NVIDIA Open Model License. See
  `app/speech/LICENSES/parakeet-unified-en-0.6b-gguf.LICENSE` and `.NOTICE`.
- Silero VAD v6.2 (GGUF conversion), licensed under the MIT License. See
  `app/speech/LICENSES/silero-vad-v6.2-gguf.LICENSE` and `.NOTICE`.

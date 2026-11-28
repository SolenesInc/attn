The user asked you to go on standby until {{until}}. Put the seed you tend for this work on standby until then, and write in the note where you are and what to do when you resume. Pass this to `attn_do` as its `script`:

    seed_standby({seed: "<seed>", at: "{{until}}", next: "<what to do next>"})

If you tend no seed for this work, plant one first with `seed_plant`. attn closes this session and resumes it at that time, or earlier when mail reaches the seed.

If you have an open ask, attn refuses a standby that closes this session. When the ask is about that seed, keep the seed on standby and close yourself, in one `attn_do` script:

    seed_standby({seed: "<seed>", at: "{{until}}", next: "<what to do next>", stay: true})
    agent_close({self: true, reason: "The user asked me to go on standby until {{until}}."})

attn refuses that `agent_close` while the user's answer would then reach no agent: the ask is about no seed or about another seed that nobody else tends, or the user's settings do not resume a tender for an answer. Then stay open and end your turn.

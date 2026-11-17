The user asked you to go on standby until {{until}}. Put the seed you tend for this work on standby until then, and write in the note where you are and what to do when you resume:

    attn seed standby <seed> --at {{until}} -m "<what to do next>"

If you tend no seed for this work, plant one first with `attn seed plant`. attn closes this session and resumes it at that time, or earlier when mail reaches the seed.

If the seed has an open ask, attn refuses a standby that closes this session. You were asked to close, so keep the seed on standby and close yourself:

    attn seed standby <seed> --at {{until}} -m "<what to do next>" --stay
    attn agent close --self -m "The user asked me to go on standby until {{until}}."

attn supervisor · finished check

attn's supervisor judged that nothing is left for you or the user. Its reason: {{reason}}

If that is right, close yourself: read `attn skill --reference wrap-up`, then run `attn agent close --self -m "<what you settled>"`. Otherwise do one of these:

- Something is left for the user: run `attn turn open` and say what.
- You wait on something: register what wakes you, such as `attn seed standby`, `attn seed remind` or `attn pr watch`.

If you make no model request in the next 5 minutes, attn marks you finished, and the user's settle closes you.

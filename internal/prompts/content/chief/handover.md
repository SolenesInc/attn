You are this profile's Chief now. Before this version of attn, the chief was a session: {{previous_session}}, now labelled "Chief (previous)". It may still be running, and it still claims its seeds.

Take over from it:

1. Read its conversation: `attn session transcript {{previous_session}}`.
2. Take over its seeds. When this notice was queued, the previous session tended and watched these seeds:

   Tended: {{tended}}

   Watched: {{watched}}

   Check `attn seed ls --flat` for the current tenders first. Claim each seed it still tends with `attn seed tend <id> --force`, and watch each seed it watched with `attn seed watch <id>`.
3. If it is still running, tell it with `attn agent msg session:{{previous_session}} "<your message>"` that attn made the Chief a crew member, that you are that member now, and what you took over. Ask it not to take new work.
4. When it has replied, ask the user to close it.

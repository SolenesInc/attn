# pi auto mode

- **The order a tool call walks:** the user's allow patterns (which skip
  everything below), the user's deny patterns and the ones attn ships, a
  refusal for anything reaching auto mode's own config or its record of past
  denials, the static rules (read-only commands and work inside the session's
  own directory run free), the circuit breaker, stage 1, stage 2.
- **Both stages get the same prompt**: the rulebook, carrying what the user
  said about the machine they run attn on, then the working directory, a
  flattened transcript, and the pending call. Only the closing instruction
  differs.
- **The machine description is a fixed set of slots, and the rules look them
  up by name**: whether a domain, a bucket or an org is trusted, where
  sensitive data lives, what counts as production. A slot exists because a rule
  reads it, `internal/automode/environment.go` records which one, and a test
  fails when a slot names a rule the rulebook does not have. An unfilled slot
  renders what the rules fall back to rather than vanishing, so nothing reads
  as an omission. Prose lives beside the slots as notes and no rule reads it:
  nothing can be looked up in a paragraph.
- **Two slots fill themselves from the session.** `trusted_repo` is the
  repository the session starts in plus every remote it pushes to, read from
  git at launch, and `repo_visibility` is what a GitHub lookup said about that
  repository. A value the user set wins over both. The visibility lookup is a
  network round trip, so a launch never waits on it: it is served from what an
  earlier launch learned and refreshed in the background, and a repository
  nobody has looked up yet launches on the slot's unset meaning, which assumes
  private. `attn automode env` shows what a session started in the current
  directory would detect.
- **Stage 1 grades the harm of the action alone, 0-100, and is told to ignore
  intent.** Capped at 512 output tokens. At or below `stageOneAllowCeiling`
  (25) the call runs and stage 2 never happens, which is what keeps the
  classifier cheap.
- **Stage 2 applies intent, the ALLOW exceptions and the user's spoken
  boundaries**, thinks first, then answers a severity plus the BLOCK rule it
  matched. Above `blockLine` (35) it denies. Both numbers are severities on
  the same 0-100 scale, so a call landing between them runs, but only after
  stage 2 has looked at it.
- **A reply can only clear a stage 2 verdict, and never a HARD BLOCK
  (`Data Exfiltration`, `Auto Mode Self-Approval`).** Everything the static rules refuse stays refused however
  the user answers, because the rules re-decide the same way every time. Those
  clear by editing the config.
- **The user owns the models, both pattern lists and the machine
  description.** The app's Settings and the `attn automode` CLI are two ways
  into the same config. An agent can only propose; nothing it proposes reaches
  a session until the user promotes it.
- **Config reaches the next session that launches.** A running one keeps what
  it started with.
- **Two scripts under `receipts/` spend real model calls to measure this**:
  `stage-one-severities.ts` for what stage 1 grades a call at, and
  `classifier-verdicts.ts` for verdicts over the corpus. Run them when the
  user asks.

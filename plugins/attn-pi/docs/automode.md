# pi auto mode

- **Configured build caches are ordinary development access.** The security
  executor supplies its active cache paths to both classifier stages. Build,
  test and dependency commands can use them without separate consent for the
  path. Disabling or editing `/security caches` changes that context on the
  next review; command effects still follow the other rules. A supported scoped
  access request is not itself a bypass or self-modification.

- **Sandbox retries are reviewed with their scope.** A `bash` call can request
  temporary extra write directories or networking with a `sandbox` argument.
  The security executor validates the request, then uses the same classifier,
  conversation, denial record, and circuit breaker as ordinary calls. The
  classifier sees both the command and requested access. A command allow
  pattern cannot skip this review; hard-deny patterns still refuse it.
  Approval applies to that execution and its children, never saved settings.

- **The order a tool call walks:** configured hard-deny patterns, review of any
  extra sandbox access, configured allow patterns, static rules, the circuit
  breaker, stage 1, then stage 2. Ordinary project writes remain automatic.
  Sandbox path protections apply independently of the approval decision.
- **Both stages get the same prompt**: the rulebook, carrying what the user
  said about the machine they run attn on, then the working directory, a
  structured transcript, and the pending call's complete filtered arguments.
  Writes include content; edits include every old/new replacement, including
  normalized edit arrays. Only the closing instruction
  differs.
- **Earlier calls retain their inputs and execution status.** Both automatic
  and reviewed calls are recorded, including calls while auto mode is off.
  Permission to run is distinguished from observed success or failure. A failed
  operation may have partial effects; a missing result never proves success.
  Tool output bodies are excluded. Arguments are untrusted data and confer no consent.
- **Tool history is bounded separately from conversation messages.** Its byte
  budget is one half of the smallest configured model's context-token count,
  capped at 512 KiB, with at most 128 entries. Missing model metadata uses
  128,000 tokens (64,000 bytes of history). A single input can occupy half that
  budget, at most 64 KiB. These are byte estimates, not exact tokenizer counts.
  Oversized historical inputs are replaced by explicit omission records; older
  calls are evicted with a count. Missing history forces stage 2, which must
  report `Incomplete Evidence` when it needs omitted content to judge the action.
  Session replacement resets this history. Compaction clears it along with
  later conversation messages, keeping the opening request.
- **Pending arguments are never truncated to obtain approval.** Configured
  fallback models receive the same action. If none can accept the request,
  interactive Pi lets the user inspect the complete filtered arguments in a
  scrollable editor, then confirm that exact call once. Changes in the preview
  do not change the call and cause it to be blocked. Cancelling or running
  without an interactive UI blocks the call. Existing sandbox limits still apply.
- **Built-in executors check approval after extension handlers finish.** A
  later handler changing the tool arguments invalidates the one-call approval.
  Execution uses a private copy, so later asynchronous mutations cannot change
  what runs. This applies to the suite and standalone auto-mode extension;
  custom tools and extensions replacing tool definitions remain trusted.
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
  boundaries** and answers a severity, the BLOCK rule it matched, then a
  one-sentence reason. The answer comes first and the rationale after: a model
  asked to think in tags before answering ends its turn after the tags often
  enough to matter, and the native reasoning channel is not reliably present. Above `blockLine` (35) it denies. Both numbers are severities on
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
- **The model picker asks the installed Pi runtime.** A short-lived offline
  RPC process loads the user's providers and extensions from the home directory,
  then returns `get_available_models` using Pi's authentication checks. No prompt
  is sent or session saved. Refresh starts a new query; concurrent requests share
  the current one. Only provider, model id, display name and context size reach
  the app, because runtime models can include resolved credentials.
- **Two scripts under `receipts/` spend real model calls to measure this**:
  `stage-one-severities.ts` for what stage 1 grades a call at, and
  `classifier-verdicts.ts` for verdicts over the corpus. Run them when the
  user asks.

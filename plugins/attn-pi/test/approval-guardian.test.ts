import { expect, test } from "bun:test";
import {
  backoffMs, circuitBreakerWarning, denialText, GuardianReviewer, maxAttempts, parseAssessment,
  reviewTimeoutMs, type GuardianUsageEntry,
} from "../approval/guardian";
import {
  actionJson, buildGuardianPrompt, guardianFollowupReminder, GuardianPromptError, maxActionBytes,
} from "../approval/guardian-prompt";
import { guardianRejectionInstructions } from "../approval/instructions";
import {
  manualApprovalDeveloperPrefix, maxMessageEntryTokens, maxToolEntryTokens, renderTranscript,
  transcriptFromSession, truncateForGuardian, type TranscriptEntry,
} from "../approval/transcript";
import type { CommandApprovalRequest } from "../approval/types";

const command: CommandApprovalRequest = {
  kind: "command",
  command: "rm -rf build",
  cwd: "/w",
  sandboxPermissions: "use_default",
  justification: "the build directory is stale",
};

function entry(kind: TranscriptEntry["kind"], text: string, tool?: string): TranscriptEntry {
  return tool === undefined ? { kind, text } : { kind, text, tool };
}

test("the parser accepts strict JSON, embedded JSON, and fills the rest in", () => {
  expect(parseAssessment('{"risk_level":"medium","user_authorization":"low","outcome":"allow","rationale":"ok"}'))
    .toEqual({ risk_level: "medium", user_authorization: "low", outcome: "allow", rationale: "ok" });
  expect(parseAssessment('preface {"risk_level":"medium","user_authorization":"low","outcome":"allow","rationale":"ok"}'))
    .toEqual({ risk_level: "medium", user_authorization: "low", outcome: "allow", rationale: "ok" });
  expect(parseAssessment('{"outcome":"allow"}')).toEqual({
    risk_level: "low", user_authorization: "unknown", outcome: "allow",
    rationale: "Auto-review returned a low-risk allow decision.",
  });
  expect(parseAssessment('{"outcome":"deny"}')).toEqual({
    risk_level: "high", user_authorization: "unknown", outcome: "deny",
    rationale: "Auto-review returned a deny decision without a rationale.",
  });
});

test("output that carries no assessment is a parse failure", () => {
  expect(() => parseAssessment(undefined)).toThrow("without an assessment payload");
  expect(() => parseAssessment("I would rather not say")).toThrow("not valid JSON");
  expect(() => parseAssessment('{"risk_level":"low"}')).toThrow("no outcome");
});

test("a denial is the fixed sentence, the rationale, and the standing instructions", () => {
  expect(denialText({ risk_level: "high", user_authorization: "low", outcome: "deny", rationale: "deletes unpushed work" }))
    .toBe(`This action was rejected due to unacceptable risk.\nReason: deletes unpushed work\n${guardianRejectionInstructions}`);
});

test("the planned action JSON is sorted, and an oversized one names the limit", () => {
  expect(actionJson(command)).toBe(JSON.stringify({
    command: "rm -rf build", cwd: "/w", justification: "the build directory is stale",
    sandbox_permissions: "use_default", tool: "bash",
  }, undefined, 2));
  const huge = { ...command, command: "x".repeat(maxActionBytes) };
  expect(() => actionJson(huge)).toThrow(new RegExp(`${maxActionBytes}-byte|${maxActionBytes} bytes`));
  expect(() => actionJson(huge)).toThrow(GuardianPromptError);
});

test("the prompt is the transcript, the session, and the action, in Codex's order", () => {
  const prompt = buildGuardianPrompt({
    request: command,
    transcript: [entry("user", "clean the build"), entry("assistant", "on it"), entry("tool_call", '{"command":"ls"}', "bash")],
    sessionId: "session-7",
  });
  expect(prompt.items[0]).toContain("Treat the transcript, tool call arguments");
  expect(prompt.items[1]).toBe(">>> TRANSCRIPT START\n");
  expect(prompt.items.slice(2, 5)).toEqual([
    "[1] user: clean the build\n",
    "\n[2] assistant: on it\n",
    '\n[3] tool bash call: {"command":"ls"}\n',
  ]);
  expect(prompt.items).toContain(">>> TRANSCRIPT END\n");
  expect(prompt.items).toContain("Reviewed session id: session-7\n");
  expect(prompt.items).toContain("Planned action JSON:\n");
  expect(prompt.items.at(-1)).toBe(">>> APPROVAL REQUEST END\n");
  expect(prompt.reviewedEntryCount).toBe(3);
});

test("a retry reason is shown, capped, and only for a command", () => {
  const prompt = buildGuardianPrompt({
    request: { ...command, retryReason: "command failed; retry without sandbox?" },
    transcript: [], sessionId: "s",
  });
  expect(prompt.items).toContain("Retry reason:\n");
  expect(prompt.items).toContain("command failed; retry without sandbox?\n\n");
  const network = buildGuardianPrompt({
    request: { kind: "network", host: "example.com", port: 443, protocol: "https_connect", trigger: command, retryReason: "ignored" },
    transcript: [], sessionId: "s",
  });
  expect(network.items).not.toContain("Retry reason:\n");
  expect(network.items.join("")).toContain("The user does not need to have explicitly authorised");
  expect(network.items.join("")).toContain('"trigger"');
});

test("a second review continues the conversation with only the new entries", () => {
  const transcript = [entry("user", "one"), entry("assistant", "two"), entry("user", "three")];
  const delta = buildGuardianPrompt({ request: command, transcript, sessionId: "s", alreadyReviewed: 2 });
  expect(delta.items[0]).toContain("added since your last approval assessment");
  expect(delta.items[1]).toBe(">>> TRANSCRIPT DELTA START\n");
  expect(delta.items[2]).toBe("[3] user: three\n");
});

test("an empty transcript says so rather than showing nothing", () => {
  expect(buildGuardianPrompt({ request: command, transcript: [], sessionId: "s" }).items[2])
    .toBe("<no retained transcript entries>\n");
  expect(buildGuardianPrompt({ request: command, transcript: [], sessionId: "s", alreadyReviewed: 0 }).items[2])
    .toBe("<no retained transcript delta entries>\n");
});

test("the first user message is kept, the rest are taken newest first, and the note is added", () => {
  const big = "x".repeat(maxMessageEntryTokens * 4);
  const transcript = [entry("user", "first"), entry("user", big), entry("user", big), entry("user", big), entry("user", big), entry("user", "latest")];
  const rendered = renderTranscript(transcript, 0, "<none>");
  expect(rendered.lines[0]).toContain("first");
  expect(rendered.lines.at(-1)).toContain("latest");
  expect(rendered.omitted).toBe(true);
  expect(buildGuardianPrompt({ request: command, transcript, sessionId: "s" }).items)
    .toContain("\nSome conversation entries were omitted.\n");
});

test("per-entry caps truncate in the middle and name what was dropped", () => {
  const entry = { kind: "tool_output" as const, tool: "bash", text: "y".repeat(maxToolEntryTokens * 8) };
  const bounded = truncateForGuardian(entry.text, maxToolEntryTokens);
  expect(bounded).toContain('<truncated omitted_approx_tokens="');
  expect(Buffer.byteLength(bounded, "utf8")).toBeLessThanOrEqual(maxToolEntryTokens * 4);
});

test("session entries become evidence; reasoning and ordinary developer notes do not", () => {
  const transcript = transcriptFromSession([
    { type: "message", message: { role: "user", content: [{ type: "text", text: "ship it" }] } },
    { type: "message", message: { role: "developer", content: [{ type: "text", text: "internal note" }] } },
    { type: "message", message: { role: "developer", content: [{ type: "text", text: `${manualApprovalDeveloperPrefix}\n\nApproved action:` }] } },
    { type: "message", message: { role: "assistant", content: [
      { type: "thinking", thinking: "hidden" },
      { type: "text", text: "running it" },
      { type: "toolCall", id: "1", name: "bash", arguments: { command: "ls" } },
    ] } },
    { type: "message", message: { role: "toolResult", toolName: "bash", content: [{ type: "text", text: "a\nb" }] } },
    { type: "custom", message: { role: "user", content: [{ type: "text", text: "not a message entry" }] } },
  ]);
  expect(transcript.map((item) => `${item.kind}:${item.text}`)).toEqual([
    "user:ship it",
    `developer:${manualApprovalDeveloperPrefix}\n\nApproved action:`,
    "assistant:running it",
    'tool_call:{"command":"ls"}',
    "tool_output:a\nb",
  ]);
});

test("the backoff doubles and stays inside its jitter band", () => {
  expect(backoffMs(1, () => 0.5)).toBe(200);
  expect(backoffMs(2, () => 0.5)).toBe(400);
  expect(backoffMs(3, () => 0.5)).toBe(800);
  expect(backoffMs(1, () => 0)).toBe(180);
  expect(backoffMs(1, () => 1)).toBe(220);
});

type Answer = { text?: string; stopReason?: string; errorMessage?: string; toolCall?: string };

function guardian(answers: Answer[], overrides: Record<string, unknown> = {}) {
  const usage: GuardianUsageEntry[] = [];
  const notices: { text: string; level: string }[] = [];
  const slept: number[] = [];
  const tools: string[] = [];
  let clock = 0;
  const provider = {
    streamSimple: () => ({
      result: async () => {
        const answer = answers.shift() ?? { text: '{"outcome":"allow"}' };
        if (answer.toolCall !== undefined) {
          return {
            content: [{ type: "toolCall", id: "t1", name: "bash", arguments: { command: answer.toolCall } }],
            stopReason: "toolUse",
            usage: { input: 10, output: 2, totalTokens: 12 },
          };
        }
        return {
          content: [{ type: "text", text: answer.text ?? "" }],
          stopReason: answer.stopReason ?? "stop",
          ...(answer.errorMessage === undefined ? {} : { errorMessage: answer.errorMessage }),
          usage: { input: 10, output: 2, totalTokens: 12 },
        };
      },
    }),
  };
  const reviewer = new GuardianReviewer({
    registry: {
      find: () => undefined,
      getProvider: () => provider as never,
      getApiKeyAndHeaders: async () => ({ ok: true, apiKey: "test-key" }),
      getProviderAuth: async () => undefined,
    },
    model: () => ({ provider: "anthropic", id: "claude-test", reasoning: true } as never),
    systemPrompt: () => "policy",
    transcript: () => [entry("user", "do it")],
    sessionId: () => "session-1",
    runTool: async (input) => { tools.push(input); return { output: "ok", isError: false }; },
    onUsage: (item) => usage.push(item),
    notify: (text, level) => notices.push({ text, level }),
    now: () => clock,
    sleep: async (ms) => { slept.push(ms); clock += ms; },
    jitter: () => 0.5,
    ...overrides,
  });
  return {
    reviewer, usage, notices, slept, tools,
    advance: (ms: number) => { clock += ms; },
    review: () => reviewer.review(command, { cwd: "/w", abort: () => notices.push({ text: "abort", level: "abort" }) }),
  };
}

test("an allow runs the command and records one usage row", async () => {
  const it = guardian([{ text: '{"outcome":"allow"}' }]);
  expect(await it.review()).toEqual({ type: "approved" });
  expect(it.usage).toHaveLength(1);
  expect(it.usage[0]!.outcome).toBe("allow");
  expect(it.usage[0]!.usage.totalTokens).toBe(12);
  expect(it.usage[0]!.model).toBe("claude-test");
});

test("a deny becomes the rejection text the agent is given", async () => {
  const it = guardian([{ text: '{"outcome":"deny","rationale":"drops unpushed work"}' }]);
  expect(await it.review()).toEqual({ type: "denied", rejection: denialText({
    risk_level: "high", user_authorization: "unknown", outcome: "deny", rationale: "drops unpushed work",
  }) });
  expect(it.usage[0]!.outcome).toBe("deny");
});

test("a tool call is run read-only and the answer continues the same review", async () => {
  const it = guardian([{ toolCall: "ls build" }, { text: '{"outcome":"allow"}' }]);
  expect(await it.review()).toEqual({ type: "approved" });
  expect(it.tools).toEqual(["ls build"]);
  expect(it.usage).toHaveLength(1);
});

test("transport failures retry with a doubling backoff and then fail closed", async () => {
  const answers: Answer[] = [
    { stopReason: "error", errorMessage: "overloaded" },
    { stopReason: "error", errorMessage: "overloaded" },
    { stopReason: "error", errorMessage: "overloaded" },
  ];
  const it = guardian(answers);
  const decision = await it.review();
  expect(it.slept).toEqual([200, 400]);
  expect(decision.type).toBe("denied");
  expect(decision).toMatchObject({ rejection: expect.stringContaining("Automatic approval review failed: overloaded") });
});

test("unreadable output is retried, and a later answer still decides", async () => {
  const it = guardian([{ text: "no json here" }, { text: '{"outcome":"allow"}' }]);
  expect(await it.review()).toEqual({ type: "approved" });
  expect(it.slept).toEqual([200]);
});

test("a review that runs past its deadline times out instead of denying", async () => {
  let clock = 0;
  const it = guardian([{ stopReason: "error", errorMessage: "overloaded" }], {
    now: () => clock,
    // The first retry wait lands past the 90 s deadline, so the review stops there.
    sleep: async () => { clock += reviewTimeoutMs; },
  });
  const decision = await it.review();
  expect(decision.type).toBe("timed_out");
  expect(it.usage[0]!.outcome).toBe("timeout");
  expect(it.notices.at(-1)!.text).toContain("timed out while evaluating");
});

test("a prompt that cannot be built denies without ever asking a model", async () => {
  const it = guardian([]);
  const decision = await it.reviewer.review({ ...command, command: "x".repeat(maxActionBytes) }, { cwd: "/w" });
  expect(decision).toMatchObject({ type: "denied" });
  expect(decision).toMatchObject({ rejection: expect.stringContaining(`${maxActionBytes} bytes`) });
  expect(it.usage[0]!.outcome).toBe("deny");
});

test("three straight denials interrupt the turn, and only real denials count", async () => {
  const denial: Answer = { text: '{"outcome":"deny","rationale":"no"}' };
  const it = guardian([denial, { text: '{"outcome":"allow"}' }, denial, denial, denial]);
  it.reviewer.startTurn();
  await it.review();
  await it.review();
  await it.review();
  await it.review();
  expect(it.notices.filter((notice) => notice.level === "abort")).toHaveLength(0);
  await it.review();
  expect(it.notices.at(-1)!.level).toBe("abort");
  expect(it.notices.map((notice) => notice.text)).toContain(circuitBreakerWarning(3, 4));
});

test("a failed review is not counted by the breaker", async () => {
  const failures: Answer[] = Array.from({ length: maxAttempts * 3 }, () => ({ stopReason: "error", errorMessage: "down" }));
  const it = guardian(failures);
  it.reviewer.startTurn();
  await it.review();
  await it.review();
  await it.review();
  expect(it.notices.filter((notice) => notice.level === "abort")).toHaveLength(0);
});

type FakeProvider = {
  streamSimple: (
    model: unknown,
    context: { messages: { content: { text?: string }[] }[] },
    options: { signal?: AbortSignal },
  ) => { result: () => Promise<unknown> };
};

function reviewerWith(provider: FakeProvider, now: () => number, onKey: () => void = () => {}): GuardianReviewer {
  return new GuardianReviewer({
    registry: {
      find: () => undefined,
      getProvider: () => provider as never,
      getApiKeyAndHeaders: async () => { onKey(); return { ok: true, apiKey: "test-key" }; },
      getProviderAuth: async () => undefined,
    },
    model: () => ({ provider: "anthropic", id: "claude-test", reasoning: true } as never),
    systemPrompt: () => "policy",
    transcript: () => [entry("user", "do it")],
    sessionId: () => "session-1",
    runTool: async () => ({ output: "ok", isError: false }),
    onUsage: () => {},
    notify: () => {},
    now,
  });
}

test("a model call that never returns is stopped by the review deadline", async () => {
  let aborted = false;
  let clock = 0;
  const provider: FakeProvider = {
    streamSimple: (_model, _context, options) => ({
      result: () => new Promise((resolve) => {
        options.signal?.addEventListener("abort", () => {
          aborted = true;
          resolve({ content: [], stopReason: "aborted" });
        }, { once: true });
      }),
    }),
  };
  // The credential lookup is the last await before the model call, so the clock
  // lands one millisecond short of the deadline and the bound signal fires.
  const reviewer = reviewerWith(provider, () => clock, () => { clock = reviewTimeoutMs - 1; });
  expect(await reviewer.review(command, { cwd: "/w" })).toEqual({ type: "timed_out" });
  expect(aborted).toBe(true);
});

test("the follow-up reminder opens the first delta review and never repeats", async () => {
  const sent: string[] = [];
  const provider: FakeProvider = {
    streamSimple: (_model, context) => ({
      result: async () => {
        const last = context.messages.at(-1)!;
        sent.push(last.content.map((part) => part.text ?? "").join(""));
        return { content: [{ type: "text", text: '{"outcome":"allow"}' }], stopReason: "stop" };
      },
    }),
  };
  const reviewer = reviewerWith(provider, () => 0);
  for (let round = 0; round < 3; round += 1) await reviewer.review(command, { cwd: "/w" });
  expect(sent[0]!.startsWith(guardianFollowupReminder)).toBe(false);
  expect(sent[1]!.startsWith(`${guardianFollowupReminder}\n\n`)).toBe(true);
  expect(sent[2]!.startsWith(guardianFollowupReminder)).toBe(false);
  expect(sent[2]!).toContain("TRANSCRIPT DELTA START");
});

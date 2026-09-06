// The Guardian reviewer: a model that answers the approval card instead of the
// user. Port of codex-rs/core/src/guardian (review.rs, prompt.rs, review_session.rs).
import { credentials } from "../security/filter";
import type { ModelLike, ModelRegistryLike, ProviderLike } from "../automode/model-classifier";
import { mergeUsage, type UsageLike } from "../automode/usage";
import { buildGuardianPrompt, GuardianPromptError } from "./guardian-prompt";
import { guardianRejectionInstructions } from "./instructions";
import type { TranscriptEntry } from "./transcript";
import type { ApprovalRequest, ReviewContext, ReviewDecision, Reviewer } from "./types";

/** guardian/mod.rs:63. One deadline covers prompt building, every attempt and every tool call. */
export const reviewTimeoutMs = 90_000;
/** guardian/review.rs:84. */
export const maxAttempts = 3;
/** util.rs:6-7 and :86-91: 200 ms doubling, ±10% jitter. */
export const initialBackoffMs = 200;
/** guardian/mod.rs:66-69. */
export const maxConsecutiveDenials = 3;
export const maxRecentDenials = 10;
export const denialWindowSize = 50;

export const noRationaleAllow = "Auto-review returned a low-risk allow decision.";
export const noRationaleDeny = "Auto-review returned a deny decision without a rationale.";
export const missingRationale = "Auto-reviewer denied the action without a specific rationale.";
export const timedOutRationale =
  "Automatic approval review timed out while evaluating the requested approval.";

export type RiskLevel = "low" | "medium" | "high" | "critical";
export type UserAuthorization = "unknown" | "low" | "medium" | "high";
export type AssessmentOutcome = "allow" | "deny";

export type GuardianAssessment = {
  risk_level: RiskLevel;
  user_authorization: UserAuthorization;
  outcome: AssessmentOutcome;
  rationale: string;
};

const riskLevels: readonly string[] = ["low", "medium", "high", "critical"];
const authorizations: readonly string[] = ["unknown", "low", "medium", "high"];

export class GuardianParseError extends Error {}
export class GuardianTransportError extends Error {}

/** prompt.rs:537-577: strict JSON first, then the widest brace span, then a failure. */
export function parseAssessment(text: string | undefined): GuardianAssessment {
  if (text === undefined) throw new GuardianParseError("guardian review completed without an assessment payload");
  const payload = readPayload(text);
  const outcome = payload.outcome === "allow" || payload.outcome === "deny" ? payload.outcome : undefined;
  if (outcome === undefined) throw new GuardianParseError("guardian assessment has no outcome");
  const rationale = typeof payload.rationale === "string" && payload.rationale.trim() !== ""
    ? payload.rationale
    : outcome === "allow" ? noRationaleAllow : noRationaleDeny;
  return {
    risk_level: riskLevels.includes(String(payload.risk_level))
      ? (payload.risk_level as RiskLevel)
      : outcome === "allow" ? "low" : "high",
    user_authorization: authorizations.includes(String(payload.user_authorization))
      ? (payload.user_authorization as UserAuthorization)
      : "unknown",
    outcome,
    rationale,
  };
}

function readPayload(text: string): Record<string, unknown> {
  try {
    return asObject(JSON.parse(text));
  } catch {
    const start = text.indexOf("{");
    const end = text.lastIndexOf("}");
    if (start < 0 || end <= start) throw new GuardianParseError("guardian assessment was not valid JSON");
    try {
      return asObject(JSON.parse(text.slice(start, end + 1)));
    } catch {
      throw new GuardianParseError("guardian assessment was not valid JSON");
    }
  }
}

function asObject(value: unknown): Record<string, unknown> {
  if (typeof value !== "object" || value === null || Array.isArray(value)) {
    throw new GuardianParseError("guardian assessment was not a JSON object");
  }
  return value as Record<string, unknown>;
}

/** review.rs:859-877. The exact text the agent is given when the Guardian refuses. */
export function denialText(assessment: GuardianAssessment): string {
  const rationale = assessment.rationale.trim() === "" ? missingRationale : assessment.rationale.trim();
  return `This action was rejected due to unacceptable risk.\nReason: ${rationale}\n${guardianRejectionInstructions}`;
}

/** review.rs:766-788. A review that could not run is a denial the breaker ignores. */
export function reviewFailedAssessment(message: string): GuardianAssessment {
  return {
    risk_level: "high",
    user_authorization: "unknown",
    outcome: "deny",
    rationale: `Automatic approval review failed: ${message}`,
  };
}

export function circuitBreakerWarning(consecutive: number, recent: number): string {
  return (
    `Automatic approval review rejected too many approval requests for this turn ` +
    `(${consecutive} consecutive, ${recent} in the last ${denialWindowSize} reviews); interrupting the turn.`
  );
}

export type GuardianUsageEntry = {
  decision_id: string;
  provider: string;
  model: string;
  usage: UsageLike;
  outcome: "allow" | "deny" | "timeout" | "error";
  at: string;
};

export type GuardianToolRun = (command: string, signal: AbortSignal) => Promise<{ output: string; isError: boolean }>;

export type GuardianOptions = {
  registry: ModelRegistryLike;
  /** The session's own model; the Guardian reviews with what the user is paying for. */
  model: () => ModelLike | undefined;
  systemPrompt: () => string;
  transcript: () => readonly TranscriptEntry[];
  sessionId: () => string;
  /** Read-only, sandboxed, no network: the Guardian looks, it never changes anything. */
  runTool: GuardianToolRun;
  onUsage: (entry: GuardianUsageEntry) => void;
  notify: (message: string, level: "info" | "warning" | "error") => void;
  now?: () => number;
  sleep?: (ms: number, signal?: AbortSignal) => Promise<void>;
  jitter?: () => number;
};

type Conversation = { messages: unknown[]; reviewedEntryCount: number };

export class GuardianReviewer implements Reviewer {
  readonly name = "guardian";
  private conversation: Conversation | undefined;
  private consecutiveDenials = 0;
  private readonly recent: boolean[] = [];
  private interrupted = false;

  constructor(private readonly options: GuardianOptions) {}

  /** Every turn starts the breaker over; the interrupt fires at most once per turn. */
  startTurn(): void {
    this.consecutiveDenials = 0;
    this.recent.length = 0;
    this.interrupted = false;
  }

  async review(request: ApprovalRequest, ctx: ReviewContext): Promise<ReviewDecision> {
    const now = this.options.now ?? (() => Date.now());
    const deadline = now() + reviewTimeoutMs;
    const review: ReviewRun = { id: randomId(), usage: undefined, model: this.options.model() };
    const decision = await this.decide(request, ctx, review, deadline);
    this.options.onUsage({
      decision_id: review.id,
      provider: review.model?.provider ?? "",
      model: review.model?.id ?? "",
      usage: review.usage ?? {},
      outcome: usageOutcome(decision),
      at: new Date().toISOString(),
    });
    return decision;
  }

  private async decide(
    request: ApprovalRequest,
    ctx: ReviewContext,
    review: ReviewRun,
    deadline: number,
  ): Promise<ReviewDecision> {
    const now = this.options.now ?? (() => Date.now());
    let failure = "";
    for (let attempt = 1; attempt <= maxAttempts; attempt += 1) {
      if (ctx.signal?.aborted) return { type: "abort" };
      if (now() >= deadline) return this.timedOut();
      let assessment: GuardianAssessment;
      try {
        assessment = await this.attempt(request, ctx, review, deadline);
      } catch (error) {
        if (ctx.signal?.aborted) return { type: "abort" };
        // The prompt could not be built at all, so no model ever saw the action.
        if (error instanceof GuardianPromptError) return this.settle(ctx, reviewFailedAssessment(error.message), false);
        if (error instanceof GuardianDeadlineError) return this.timedOut();
        if (!(error instanceof GuardianParseError) && !(error instanceof GuardianTransportError)) throw error;
        failure = error.message;
        if (attempt === maxAttempts) break;
        const delay = Math.min(backoffMs(attempt, this.options.jitter), Math.max(deadline - now(), 0));
        await (this.options.sleep ?? sleep)(delay, ctx.signal);
        continue;
      }
      return this.settle(ctx, assessment, true);
    }
    return this.settle(ctx, reviewFailedAssessment(failure), false);
  }

  private timedOut(): ReviewDecision {
    this.notifyVerdict("denied", {
      risk_level: "high", user_authorization: "unknown", outcome: "deny", rationale: timedOutRationale,
    });
    return { type: "timed_out" };
  }

  /** review.rs:797-808 tells the user what the reviewer decided and why. */
  private notifyVerdict(verdict: "approved" | "denied", assessment: GuardianAssessment): void {
    this.options.notify(
      `Automatic approval review ${verdict} (risk: ${assessment.risk_level}, authorization: ${assessment.user_authorization}): ${assessment.rationale}`,
      verdict === "approved" ? "info" : "warning",
    );
  }

  private settle(ctx: ReviewContext, assessment: GuardianAssessment, counted: boolean): ReviewDecision {
    this.notifyVerdict(assessment.outcome === "allow" ? "approved" : "denied", assessment);
    if (assessment.outcome === "allow") {
      this.recordNonDenial();
      return { type: "approved" };
    }
    if (counted) this.recordDenial(ctx);
    return { type: "denied", rejection: denialText(assessment) };
  }

  private recordNonDenial(): void {
    this.consecutiveDenials = 0;
    this.pushRecent(false);
  }

  private recordDenial(ctx: ReviewContext): void {
    this.consecutiveDenials += 1;
    this.pushRecent(true);
    const recent = this.recent.filter((denied) => denied).length;
    if (this.interrupted) return;
    if (this.consecutiveDenials < maxConsecutiveDenials && recent < maxRecentDenials) return;
    this.interrupted = true;
    this.options.notify(circuitBreakerWarning(this.consecutiveDenials, recent), "error");
    ctx.abort?.();
  }

  private pushRecent(denied: boolean): void {
    this.recent.push(denied);
    if (this.recent.length > denialWindowSize) this.recent.shift();
  }

  private async attempt(
    request: ApprovalRequest,
    ctx: ReviewContext,
    review: ReviewRun,
    deadline: number,
  ): Promise<GuardianAssessment> {
    const now = this.options.now ?? (() => Date.now());
    const model = review.model;
    if (!model) throw new GuardianTransportError("this session has no model to review with");
    const provider = this.options.registry.getProvider(model.provider);
    if (!provider) throw new GuardianTransportError(`provider ${JSON.stringify(model.provider)} is not configured`);
    const transcript = this.options.transcript();
    const prompt = buildGuardianPrompt({
      request,
      transcript,
      sessionId: this.options.sessionId(),
      ...(this.conversation ? { alreadyReviewed: this.conversation.reviewedEntryCount } : {}),
    });
    const messages = this.conversation ? [...this.conversation.messages] : [];
    messages.push(userMessage(prompt.items));

    const auth = await this.options.registry.getApiKeyAndHeaders(model);
    if (!auth.ok) throw new GuardianTransportError(auth.error ?? `no credential for ${model.provider}`);
    credentials.remember(auth.apiKey);
    const baseUrl = (await this.options.registry.getProviderAuth(model.provider))?.auth?.baseUrl;
    const target = baseUrl ? { ...model, baseUrl } : model;

    for (;;) {
      if (now() >= deadline) throw new GuardianDeadlineError();
      const result = await complete(provider, target, this.options.systemPrompt(), messages, auth, ctx.signal);
      review.usage = mergeUsage(review.usage, result.usage);
      if (result.stopReason === "aborted") throw new GuardianTransportError("the review was aborted");
      if (result.stopReason === "error") throw new GuardianTransportError(result.errorMessage ?? "no reason given");
      messages.push(assistantMessage(result));
      const calls = toolCallsOf(result);
      if (result.stopReason === "toolUse" && calls.length > 0) {
        for (const call of calls) {
          const command = String((call.arguments as { command?: unknown })?.command ?? "");
          const run = await this.options.runTool(command, signalFor(ctx, deadline, now));
          messages.push(toolResultMessage(call.id, call.name, run.output, run.isError));
        }
        continue;
      }
      const assessment = parseAssessment(textOf(result));
      this.conversation = { messages, reviewedEntryCount: prompt.reviewedEntryCount };
      return assessment;
    }
  }

}

type ReviewRun = { id: string; usage: UsageLike | undefined; model: ModelLike | undefined };

function usageOutcome(decision: ReviewDecision): GuardianUsageEntry["outcome"] {
  if (decision.type === "approved") return "allow";
  if (decision.type === "timed_out") return "timeout";
  if (decision.type === "abort") return "error";
  return "deny";
}

class GuardianDeadlineError extends Error {}

export function backoffMs(attempt: number, jitter?: () => number): number {
  const base = initialBackoffMs * 2 ** (attempt - 1);
  const factor = 0.9 + (jitter?.() ?? Math.random()) * 0.2;
  return Math.floor(base * factor);
}

function randomId(): string {
  return globalThis.crypto.randomUUID();
}

function sleep(ms: number, signal?: AbortSignal): Promise<void> {
  return new Promise((resolve) => {
    const timer = setTimeout(resolve, ms);
    signal?.addEventListener("abort", () => { clearTimeout(timer); resolve(); }, { once: true });
  });
}

function signalFor(ctx: ReviewContext, deadline: number, now: () => number): AbortSignal {
  const controller = new AbortController();
  const remaining = Math.max(deadline - now(), 0);
  const timer = setTimeout(() => controller.abort(), remaining);
  timer.unref?.();
  ctx.signal?.addEventListener("abort", () => controller.abort(), { once: true });
  return controller.signal;
}

type CompletionResultLike = {
  content?: { type: string; text?: string }[];
  usage?: UsageLike;
  stopReason?: string;
  errorMessage?: string;
};

async function complete(
  provider: ProviderLike,
  model: ModelLike,
  systemPrompt: string,
  messages: unknown[],
  auth: { apiKey?: string; headers?: Record<string, string | null>; env?: Record<string, string> },
  signal?: AbortSignal,
): Promise<CompletionResultLike> {
  const context = {
    systemPrompt: credentials.text(systemPrompt),
    messages,
    tools: [guardianBashTool],
  } as unknown as Parameters<ProviderLike["streamSimple"]>[1];
  const options = {
    ...(model.reasoning ? { reasoning: "low" } : {}),
    apiKey: auth.apiKey,
    headers: auth.headers,
    env: auth.env,
    signal,
  } as Parameters<ProviderLike["streamSimple"]>[2];
  return provider.streamSimple(model, context, options).result() as Promise<CompletionResultLike>;
}

export const guardianBashTool = {
  name: "bash",
  description:
    "Run a read-only shell command to inspect local state. The command runs sandboxed with no network and no write access.",
  parameters: {
    type: "object",
    properties: { command: { type: "string", description: "The command to run." } },
    required: ["command"],
    additionalProperties: false,
  },
};

function userMessage(items: readonly string[]): unknown {
  return {
    role: "user",
    content: items.map((text) => ({ type: "text", text: credentials.text(text) })),
    timestamp: Date.now(),
  };
}

function assistantMessage(result: CompletionResultLike): unknown {
  return { role: "assistant", content: result.content ?? [], timestamp: Date.now() };
}

function toolResultMessage(id: string, name: string, output: string, isError: boolean): unknown {
  return {
    role: "toolResult",
    toolCallId: id,
    toolName: name,
    content: [{ type: "text", text: credentials.text(output) }],
    isError,
    timestamp: Date.now(),
  };
}

function toolCallsOf(result: CompletionResultLike): { id: string; name: string; arguments: unknown }[] {
  return (result.content ?? [])
    .filter((item): item is { type: "toolCall"; id: string; name: string; arguments: unknown } =>
      item.type === "toolCall")
    .map((call) => ({ id: call.id, name: call.name, arguments: call.arguments }));
}

function textOf(result: CompletionResultLike): string | undefined {
  const text = (result.content ?? [])
    .filter((item) => item.type === "text" && typeof item.text === "string")
    .map((item) => item.text!)
    .join("")
    .trim();
  return text === "" ? undefined : text;
}

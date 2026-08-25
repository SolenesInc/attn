import type { Classifier } from "./classifier";
import type { AutoModeConfig } from "./config";
import { denialToolResult } from "./denial";
import { decideStatically, describeCall, normalizedIntent, type StaticRule, type ToolCall } from "./policy";
import { TranscriptWindow, transcriptEntryText } from "./transcript";

/** Denials in a row, without an allowed call between them. */
export const consecutiveDenialLimit = 3;

/** Denials since the user last said anything. */
export const totalDenialLimit = 20;

export type DecisionRule =
  | StaticRule
  | "cached-allow"
  | "cached-deny"
  | "classifier"
  | "classifier-2a"
  | "classifier-2b"
  | "classifier-unavailable"
  | "circuit-breaker";

export type SessionDecision =
  | { outcome: "run"; rule: DecisionRule }
  | { outcome: "block"; rule: DecisionRule; action: string; reason: string; toolResult: string };

export type BreakerState = {
  consecutive: number;
  total: number;
  tripped: boolean;
  /** Every block since the counters cleared was an outage, not a refusal. */
  outage: boolean;
};

export type DecideOptions = {
  cwd: string;
  signal?: AbortSignal;
};

export class AutoModeSession {
  private readonly cache = new Map<string, { verdict: "allow" | "deny"; reason: string }>();
  private readonly transcript = new TranscriptWindow();
  private consecutiveDenials = 0;
  private totalDenials = 0;
  private totalOutages = 0;

  constructor(
    private readonly config: AutoModeConfig,
    private readonly classifier: Classifier,
  ) {}

  breaker(): BreakerState {
    return {
      consecutive: this.consecutiveDenials,
      total: this.totalDenials,
      tripped: this.consecutiveDenials >= consecutiveDenialLimit || this.totalDenials >= totalDenialLimit,
      outage: this.totalDenials > 0 && this.totalOutages === this.totalDenials,
    };
  }

  noteUserInput(text = ""): void {
    // pi announces one message on two seams, and the same sentence twice reads as insistence.
    if (this.transcript.latest("user") !== transcriptEntryText(text)) this.transcript.record("user", text);
    for (const [key, entry] of this.cache) if (entry.verdict === "deny") this.cache.delete(key);
    this.clearCounters();
  }

  /** Clears the counters and nothing else: the call that tripped the breaker is not
   * approved, and the deny cache still stands. */
  resumeAfterBreaker(): void {
    this.clearCounters();
  }

  private clearCounters(): void {
    this.consecutiveDenials = 0;
    this.totalDenials = 0;
    this.totalOutages = 0;
  }

  /** Only what the agent SAID: never a tool result. */
  noteAssistantText(text: string): void {
    this.transcript.record("assistant", text);
  }

  async decide(call: ToolCall, options: DecideOptions): Promise<SessionDecision> {
    const staticDecision = decideStatically(call, this.config, options.cwd);
    if (staticDecision.outcome === "run") return this.allowed(staticDecision.rule);
    if (staticDecision.outcome === "block") {
      return this.denied(call, staticDecision.rule, staticDecision.reason);
    }

    const intent = normalizedIntent(call);
    const cached = this.cache.get(intent);
    if (cached?.verdict === "allow") return this.allowed("cached-allow");
    if (cached?.verdict === "deny") return this.denied(call, "cached-deny", cached.reason);

    const breaker = this.breaker();
    if (breaker.tripped) {
      return this.denied(call, "circuit-breaker", breakerReason(breaker), { outage: breaker.outage });
    }

    const judged = await this.classifier.classify({
      call,
      cwd: options.cwd,
      reason: staticDecision.reason,
      environment: this.config.environment,
      transcript: this.transcript.snapshot(),
      signal: options.signal,
    });
    if (judged.verdict === "deny" && judged.unavailable) {
      // Nobody judged this call: a cached verdict would keep blocking after the endpoint is back.
      return this.denied(call, "classifier-unavailable", judged.reason, { outage: true });
    }
    const rule: DecisionRule = judged.layer ? `classifier-${judged.layer}` : "classifier";
    if (judged.verdict === "allow") {
      this.cache.set(intent, { verdict: "allow", reason: judged.reason ?? "" });
      return this.allowed(rule);
    }
    const reason =
      judged.verdict === "deny"
        ? judged.reason
        : `auto mode could not judge this call confidently${judged.reason ? `: ${judged.reason}` : ""}`;
    this.cache.set(intent, { verdict: "deny", reason });
    return this.denied(call, rule, reason);
  }

  private allowed(rule: DecisionRule): SessionDecision {
    this.consecutiveDenials = 0;
    return { outcome: "run", rule };
  }

  private denied(
    call: ToolCall,
    rule: DecisionRule,
    reason: string,
    kind: { outage: boolean } = { outage: false },
  ): SessionDecision {
    this.consecutiveDenials += 1;
    this.totalDenials += 1;
    if (kind.outage) this.totalOutages += 1;
    const action = describeCall(call);
    return { outcome: "block", rule, action, reason, toolResult: denialToolResult({ action, reason }) };
  }
}

/** An episode of pure outages must not read as the session having been refused twenty
 * times — that sends the agent, and the user, to the wrong problem. */
function breakerReason(breaker: BreakerState): string {
  if (breaker.outage) {
    return (
      `auto mode blocked ${breaker.consecutive} calls in a row and ${breaker.total} since the user last ` +
      `spoke, every one of them because its classifier could not be reached (limits: ` +
      `${consecutiveDenialLimit} consecutive, ${totalDenialLimit} total), so it stopped trying. ` +
      `Nothing judged any of those calls dangerous. Tell the user their classifier model looks to be ` +
      `down; they have to answer before anything else runs.`
    );
  }
  return (
    `auto mode has refused ${breaker.consecutive} calls in a row and ${breaker.total} since the ` +
    `user last spoke (limits: ${consecutiveDenialLimit} consecutive, ${totalDenialLimit} total), ` +
    `so it stopped judging further calls. The user has to answer before anything else runs.`
  );
}

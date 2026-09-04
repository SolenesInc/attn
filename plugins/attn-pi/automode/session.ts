import type { Classifier, ClassifierPrompt } from "./classifier";
import type { AutoModeConfig } from "./config";
import { denialToolResult, sandboxDenialToolResult } from "./denial";
import { callSignature, decideStatically, describeCall, isSandboxRequest, type StaticRule, type ToolCall } from "./policy";
import { TranscriptWindow } from "./transcript";

export const consecutiveDenialLimit = 3;

export const totalDenialLimit = 20;

export type DecisionRule =
  | StaticRule
  | "classifier"
  | "classifier-harm"
  | "classifier-intent"
  | "classifier-unavailable"
  | "classifier-too-long"
  | "circuit-breaker";

export type SessionDecision =
  | { outcome: "run"; rule: DecisionRule }
  | {
      outcome: "block";
      rule: DecisionRule;
      action: string;
      reason: string;
      toolResult: string;

      prompt?: ClassifierPrompt;

      clearable?: boolean;
    };

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
  cacheWritePaths?: readonly string[];
};

export class AutoModeSession {
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
    this.transcript.record("user", text);
    this.clearCounters();
  }

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

  noteApprovedCall(call: ToolCall): void {
    this.transcript.recordToolCall(call.toolName, callSignature(call));
  }

  noteCompaction(): void {
    this.transcript.compacted();
  }

  async decide(call: ToolCall, options: DecideOptions): Promise<SessionDecision> {
    const staticDecision = decideStatically(call, this.config, options.cwd);
    if (staticDecision.outcome === "run") {
      this.transcript.recordToolCall(call.toolName, callSignature(call));
      return this.allowed(staticDecision.rule);
    }
    if (staticDecision.outcome === "block") {
      return this.denied(call, staticDecision.rule, staticDecision.reason, { outage: false, clearable: false });
    }

    const breaker = this.breaker();
    if (breaker.tripped) {
      return this.denied(call, "circuit-breaker", breakerReason(breaker), { outage: breaker.outage });
    }

    const grant = this.transcript.grant();
    const judged = await this.classifier.classify({
      call,
      cwd: options.cwd,
      reason: staticDecision.reason,
      environment: this.config.environment,
      transcript: this.transcript.snapshot(),
      ...(grant === undefined ? {} : { grant }),
      signal: options.signal,
      cacheWritePaths: options.cacheWritePaths,
    });
    const prompt = judged.verdict === "deny" ? judged.prompt : undefined;
    if (judged.verdict === "deny" && judged.tooLong === true) {
      return this.denied(call, "classifier-too-long", judged.reason, {
        outage: false,
        judged: false,
        clearable: false,
        prompt,
      });
    }
    if (judged.verdict === "deny" && judged.unavailable === true) {
      return this.denied(call, "classifier-unavailable", judged.reason, { outage: true, judged: false, prompt });
    }
    const rule: DecisionRule = judged.layer ? `classifier-${judged.layer}` : "classifier";
    if (judged.verdict === "allow") {
      this.transcript.recordToolCall(call.toolName, callSignature(call));
      return this.allowed(rule);
    }
    const reason = judged.reason;
    const boundary = judged.boundary === true;
    const unreadable = judged.unreadable === true;
    return this.denied(call, rule, reason, {
      outage: false,
      judged: !unreadable,
      clearable: !boundary,
      prompt,
    });
  }

  private allowed(rule: DecisionRule): SessionDecision {
    this.consecutiveDenials = 0;
    return { outcome: "run", rule };
  }

  private denied(
    call: ToolCall,
    rule: DecisionRule,
    reason: string,
    kind: { outage: boolean; judged?: boolean; clearable?: boolean; prompt?: ClassifierPrompt } = { outage: false },
  ): SessionDecision {
    this.consecutiveDenials += 1;
    this.totalDenials += 1;
    if (kind.outage) this.totalOutages += 1;
    const action = describeCall(call);
    return {
      outcome: "block",
      rule,
      action,
      reason,
      toolResult: (isSandboxRequest(call) ? sandboxDenialToolResult : denialToolResult)({ action, reason, judged: kind.judged ?? true, clearable: kind.clearable ?? true }),
      ...(kind.clearable === false ? { clearable: false } : {}),
      ...(kind.prompt ? { prompt: kind.prompt } : {}),
    };
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
      `Nothing judged any of those calls dangerous.`
    );
  }
  return (
    `auto mode has refused ${breaker.consecutive} calls in a row and ${breaker.total} since the ` +
    `user last spoke (limits: ${consecutiveDenialLimit} consecutive, ${totalDenialLimit} total), ` +
    `so it stopped judging further calls.`
  );
}

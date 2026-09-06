// Where policy, sandbox and approval meet. Port of orchestrator.rs:121-311
// (the first attempt) and :339-442 (the post-denial gate).
import { evaluateCommand, amendRules, type ApprovalPolicy, type PrefixRule, type SandboxMode } from "../execpolicy/index";
import { initShellParsing, shellParsingReady } from "../shell/index";
import {
  commandEnvironment,
  isSandboxDenial,
  sandboxSpecFor,
  wrapCommand,
  type BashParameters,
  type ProxyAddress,
  type SandboxConfig,
  type SandboxPermissions,
} from "../sandbox/index";
import type { NetworkDecision, NetworkRequest } from "../netproxy/index";
import { guardianTimeoutInstructions } from "./instructions";
import { userRejection } from "./reviewers";
import {
  describeCommand,
  networkRejection,
  type CommandApprovalRequest,
  type DenialRule,
  type NetworkApprovalRequest,
  type ReviewContext,
  type ReviewDecision,
  type Reviewer,
} from "./types";

/** orchestrator.rs:549. Terse and stable, as Codex keeps it. */
export const retryWithoutSandboxReason = "command failed; retry without sandbox?";

/** network_approval.rs:671. */
export function networkPromptReason(host: string): string {
  return `${host} is not in the allowed_domains`;
}

export const turnAbortedMessage = "The turn was ended by the user.";

export type ExecOptions = {
  onData: (data: Buffer) => void;
  signal?: AbortSignal;
  timeout?: number;
  env?: NodeJS.ProcessEnv;
};

export type ExecResult = { exitCode: number | null };

/** How the orchestrator actually runs a shell line, normally pi's local bash. */
export type RunShell = (command: string, cwd: string, options: ExecOptions) => Promise<ExecResult>;

export type SandboxSource = { config: SandboxConfig; cwd: string; temp: string };

export type OrchestratorDenial = {
  toolCallId: string;
  tool: string;
  action: string;
  reason: string;
  rule: DenialRule;
  at: string;
};

export type OrchestratorOptions = {
  approvalPolicy: () => ApprovalPolicy;
  sandboxMode: () => SandboxMode;
  sandbox: () => SandboxSource;
  reviewer: () => Reviewer;
  rules: readonly PrefixRule[];
  /** 127.0.0.1:<port> and the run's proxy credentials, when the driver started a proxy. */
  proxy?: ProxyAddress;
  run: RunShell;
  onDenial?: (denial: OrchestratorDenial) => void;
  /** Sends the amendment to the daemon, which records the proposal and applies it. */
  onExecPolicyAmendment?: (prefix: string[]) => void;
  onNetworkAmendment?: (host: string) => void;
  notify?: (message: string, level: "info" | "warning" | "error") => void;
};

type RunningCommand = {
  request: CommandApprovalRequest;
  controller: AbortController;
  ctx: ReviewContext;
  rejection?: string;
};

export class ApprovalOrchestrator {
  private currentRules: PrefixRule[];
  private readonly sessionAllowedHosts = new Set<string>();
  private readonly running: RunningCommand[] = [];
  private shellReady: Promise<void> | undefined;

  constructor(private readonly options: OrchestratorOptions) {
    this.currentRules = [...options.rules];
  }

  rules(): readonly PrefixRule[] {
    return this.currentRules;
  }

  /** Parsing must be up before the first evaluateCommand; session_start calls this
   * and a tool call that beats it waits on the same promise. */
  async ready(): Promise<void> {
    this.shellReady ??= initShellParsing().then(() => undefined);
    await this.shellReady;
    await shellParsingReady();
  }

  async runBash(args: BashParameters, ctx: ReviewContext & { toolCallId: string; onData: (data: Buffer) => void; timeout?: number; env?: NodeJS.ProcessEnv }): Promise<ExecResult> {
    await this.ready();
    const permissions: SandboxPermissions = args.sandbox_permissions ?? "use_default";
    const request: CommandApprovalRequest = {
      kind: "command",
      command: args.command,
      cwd: ctx.cwd,
      sandboxPermissions: permissions,
      ...(args.justification === undefined ? {} : { justification: args.justification }),
      ...(args.prefix_rule === undefined ? {} : { prefixRule: args.prefix_rule }),
    };
    const evaluation = evaluateCommand(args.command, {
      rules: this.currentRules,
      approvalPolicy: this.options.approvalPolicy(),
      sandboxMode: this.options.sandboxMode(),
      sandboxPermissions: permissions,
    });

    if (evaluation.decision === "forbidden") {
      const reason = evaluation.reason ?? "blocked by policy";
      this.recordDenial(ctx.toolCallId, describeCommand(request), reason, "forbidden");
      throw new Error(reason);
    }

    if (evaluation.decision === "prompt") {
      const reviewed = { ...request, ...(evaluation.reason === undefined ? {} : { reason: evaluation.reason }) };
      this.settle(await this.options.reviewer().review(reviewed, ctx), reviewed, ctx);
    }

    const first = await this.execute(request, evaluation.bypassSandbox, ctx);
    if (first.rejection !== undefined) throw new Error(first.rejection);
    // untrusted is the one policy that re-asks after a sandbox denial
    // (sandboxing.rs:325-333: OnRequest and Never hand the output back instead).
    if (!first.denied || this.options.approvalPolicy() !== "untrusted") return first.result;

    const retry: CommandApprovalRequest = { ...request, retryReason: retryWithoutSandboxReason };
    const decision = await this.options.reviewer().review(retry, ctx);
    if (decision.type === "abort") {
      ctx.abort?.();
      throw new Error(turnAbortedMessage);
    }
    if (!isApproval(decision)) return first.result;
    this.apply(decision, retry);
    const second = await this.execute({ ...request, sandboxPermissions: "require_escalated" }, true, ctx);
    if (second.rejection !== undefined) throw new Error(second.rejection);
    return second.result;
  }

  /** driver.network_decide. The proxy holds the connection until this answers. */
  readonly decideNetwork = async (request: NetworkRequest): Promise<NetworkDecision> => {
    const trigger = this.running.at(-1);
    // No command, nobody to attribute the connection to: Codex denies an
    // unattributable request too (network_approval.rs:633-647).
    if (!trigger) return { decision: "deny" };
    if (this.sessionAllowedHosts.has(hostKey(request))) return { decision: "allow", scope: "session" };
    if (this.options.approvalPolicy() === "never") {
      this.denyNetwork(trigger, request);
      return { decision: "deny" };
    }
    const approval: NetworkApprovalRequest = {
      kind: "network",
      host: request.host,
      port: request.port,
      protocol: request.protocol,
      trigger: trigger.request,
      reason: networkPromptReason(request.host),
      retryReason: networkRejection(request),
    };
    const ctx = trigger.ctx;
    let decision: ReviewDecision;
    try {
      decision = await this.options.reviewer().review(approval, ctx);
    } catch {
      this.denyNetwork(trigger, request);
      return { decision: "deny" };
    }
    switch (decision.type) {
      case "approved":
        return { decision: "allow", scope: "once" };
      case "approved_for_session":
        this.sessionAllowedHosts.add(hostKey(request));
        return { decision: "allow", scope: "session" };
      case "network_amendment":
        this.sessionAllowedHosts.add(hostKey(request));
        this.options.onNetworkAmendment?.(request.host);
        return { decision: "allow", scope: "session" };
      case "abort":
        this.denyNetwork(trigger, request);
        ctx.abort?.();
        return { decision: "deny" };
      default:
        this.denyNetwork(trigger, request);
        return { decision: "deny" };
    }
  };

  private denyNetwork(trigger: RunningCommand, request: NetworkRequest): void {
    const rejection = networkRejection(request);
    trigger.rejection = rejection;
    this.recordDenial("", describeCommand(trigger.request), rejection, "network");
    trigger.controller.abort();
  }

  /** An approval carries on; everything else throws the text the model is given. */
  private settle(decision: ReviewDecision, request: CommandApprovalRequest, ctx: ReviewContext & { toolCallId: string }): void {
    if (isApproval(decision)) {
      this.apply(decision, request);
      return;
    }
    const reviewer = this.options.reviewer().name;
    if (decision.type === "abort") {
      ctx.abort?.();
      this.recordDenial(ctx.toolCallId, describeCommand(request), userRejection, reviewer === "user" ? "user" : "guardian");
      throw new Error(turnAbortedMessage);
    }
    if (decision.type === "timed_out") {
      const reason = guardianTimeoutInstructions;
      this.recordDenial(ctx.toolCallId, describeCommand(request), reason, "guardian-timeout");
      throw new Error(reason);
    }
    this.recordDenial(ctx.toolCallId, describeCommand(request), decision.rejection, reviewer === "user" ? "user" : "guardian");
    throw new Error(decision.rejection);
  }

  private apply(decision: ReviewDecision, request: CommandApprovalRequest): void {
    if (decision.type !== "approved_execpolicy_amendment") return;
    const prefix = decision.prefix.length > 0 ? decision.prefix : (request.prefixRule ?? []);
    if (prefix.length === 0) return;
    this.currentRules = amendRules(this.currentRules, prefix, "allow");
    this.options.onExecPolicyAmendment?.(prefix);
  }

  private async execute(
    request: CommandApprovalRequest,
    bypassSandbox: boolean,
    ctx: ReviewContext & { onData: (data: Buffer) => void; timeout?: number; env?: NodeJS.ProcessEnv },
  ): Promise<{ result: ExecResult; denied: boolean; rejection?: string }> {
    const source = this.options.sandbox();
    const spec = bypassSandbox
      ? "unsandboxed"
      : sandboxSpecFor(source.config, source.cwd, source.temp, {
          permissions: request.sandboxPermissions,
          ...(this.options.proxy ? { proxy: this.options.proxy } : {}),
        });
    const controller = new AbortController();
    const abort = () => controller.abort();
    ctx.signal?.addEventListener("abort", abort, { once: true });
    const handle: RunningCommand = { request, controller, ctx };
    this.running.push(handle);
    const scanner = new DenialScanner();
    try {
      // A killed process throws rather than returning, and a network denial is
      // exactly that kill: the rejection is the tool result, not the kill.
      let result: ExecResult;
      try {
        result = await this.options.run(
          spec === "unsandboxed" ? request.command : wrapCommand(spec, request.command),
          request.cwd,
          {
            onData: (data) => {
              scanner.push(data.toString());
              ctx.onData(data);
            },
            signal: controller.signal,
            ...(ctx.timeout === undefined ? {} : { timeout: ctx.timeout }),
            env: commandEnvironment(spec, ctx.env ?? process.env),
          },
        );
      } catch (error) {
        if (handle.rejection === undefined) throw error;
        return { result: { exitCode: null }, denied: false, rejection: handle.rejection };
      }
      const denied = isSandboxDenial({
        sandboxed: spec !== "unsandboxed",
        exitCode: result.exitCode,
        output: scanner.matched ?? "",
      });
      return { result, denied, ...(handle.rejection === undefined ? {} : { rejection: handle.rejection }) };
    } finally {
      ctx.signal?.removeEventListener("abort", abort);
      const index = this.running.indexOf(handle);
      if (index >= 0) this.running.splice(index, 1);
    }
  }

  private recordDenial(toolCallId: string, action: string, reason: string, rule: DenialRule): void {
    this.options.onDenial?.({ toolCallId, tool: "bash", action, reason, rule, at: new Date().toISOString() });
  }
}

function isApproval(decision: ReviewDecision): boolean {
  return (
    decision.type === "approved" ||
    decision.type === "approved_for_session" ||
    decision.type === "approved_execpolicy_amendment" ||
    decision.type === "network_amendment"
  );
}

function hostKey(request: { host: string; port: number; protocol: string }): string {
  return `${request.protocol}://${request.host.toLowerCase()}:${request.port}`;
}

// denial.rs's keywords are matched over the whole output; carrying the longest
// keyword minus one character across chunks finds one split by a stream boundary.
const denialKeywords = [
  "operation not permitted", "permission denied", "read-only file system",
  "seccomp", "sandbox", "landlock", "failed to write file",
];
const carryLength = Math.max(...denialKeywords.map((keyword) => keyword.length)) - 1;

export class DenialScanner {
  matched: string | undefined;
  private carry = "";

  push(chunk: string): void {
    if (this.matched !== undefined) return;
    const window = (this.carry + chunk).toLowerCase();
    this.matched = denialKeywords.find((keyword) => window.includes(keyword));
    this.carry = window.slice(-carryLength);
  }
}

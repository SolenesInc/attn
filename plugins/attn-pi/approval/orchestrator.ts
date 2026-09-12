import { CredentialFilter, FilteredStream } from "../security/filter";
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

export const retryWithoutSandboxReason = "command failed; retry without sandbox?";

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
  proxy?: ProxyAddress;
  acquireProxy?: (signal?: AbortSignal) => Promise<{ proxy: ProxyAddress; release: () => void }>;
  run: RunShell;
  onDenial?: (denial: OrchestratorDenial) => void;
  onExecPolicyAmendment?: (prefix: string[]) => void;
  onNetworkAmendment?: (host: string) => void;
  notify?: (message: string, level: "info" | "warning" | "error") => void;
};

type RunningCommand = {
  request: CommandApprovalRequest;
  toolCallId: string;
  controller: AbortController;
  ctx: ReviewContext;
  rejection?: string;
};

export class ApprovalOrchestrator {
  private currentRules: PrefixRule[];
  private readonly sessionAllowedHosts = new Set<string>();
  private readonly running = new Map<string, RunningCommand>();
  private reviewTail: Promise<void> = Promise.resolve();
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
      const reviewed: CommandApprovalRequest = {
        ...request,
        sandboxPermissions: evaluation.bypassSandbox ? "require_escalated" : request.sandboxPermissions,
        ...(evaluation.reason === undefined ? {} : { reason: evaluation.reason }),
      };
      this.settle(await this.review(reviewed, ctx), reviewed, ctx);
    }

    const first = await this.execute(request, evaluation.bypassSandbox, ctx);
    if (first.rejection !== undefined) throw new Error(first.rejection);
    // Only "untrusted" retries after a sandbox denial; other policies return the denied output as-is.
    if (!first.denied || this.options.approvalPolicy() !== "untrusted") return first.result;

    const retry: CommandApprovalRequest = {
      ...request, sandboxPermissions: "require_escalated", retryReason: retryWithoutSandboxReason,
    };
    const decision = await this.review(retry, ctx);
    if (decision.type === "abort") {
      ctx.abort?.();
      throw new Error(turnAbortedMessage);
    }
    if (!isApproval(decision)) return first.result;
    this.apply(decision, retry);
    const second = await this.execute(retry, true, ctx);
    if (second.rejection !== undefined) throw new Error(second.rejection);
    return second.result;
  }

  readonly decideNetwork = async (request: NetworkRequest): Promise<NetworkDecision> => {
    const trigger = this.running.get(request.credentials);
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
    const ctx = { ...trigger.ctx, signal: trigger.controller.signal };
    let decision: ReviewDecision;
    try {
      decision = await this.review(approval, ctx);
    } catch {
      if (!trigger.controller.signal.aborted) this.denyNetwork(trigger, request);
      return { decision: "deny" };
    }
    switch (decision.type) {
      case "approved":
        return { decision: "allow", scope: "once" };
      case "approved_for_session":
        return { decision: "allow", scope: "session" };
      case "network_amendment":
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

  private async review(request: CommandApprovalRequest | NetworkApprovalRequest, ctx: ReviewContext): Promise<ReviewDecision> {
    const previous = this.reviewTail;
    let release!: () => void;
    this.reviewTail = new Promise<void>((resolve) => { release = resolve; });
    await previous;
    try {
      ctx.signal?.throwIfAborted();
      if (request.kind === "network" && this.sessionAllowedHosts.has(hostKey(request))) return { type: "approved_for_session" };
      const decision = await this.options.reviewer().review(request, ctx);
      ctx.signal?.throwIfAborted();
      // Publish session grants before the next queued request can open another card.
      if (request.kind === "network" && (decision.type === "approved_for_session" || decision.type === "network_amendment")) {
        this.sessionAllowedHosts.add(hostKey(request));
      }
      return decision;
    } finally {
      release();
    }
  }

  private denyNetwork(trigger: RunningCommand, request: NetworkRequest): void {
    const rejection = networkRejection(request);
    trigger.rejection = rejection;
    this.recordDenial(trigger.toolCallId, describeCommand(trigger.request), rejection, "network");
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
    ctx: ReviewContext & { toolCallId: string; onData: (data: Buffer) => void; timeout?: number; env?: NodeJS.ProcessEnv },
  ): Promise<{ result: ExecResult; denied: boolean; rejection?: string }> {
    ctx.signal?.throwIfAborted();
    const source = this.options.sandbox();
    const initialSpec = bypassSandbox
      ? "unsandboxed"
      : sandboxSpecFor(source.config, source.cwd, source.temp, {
          permissions: request.sandboxPermissions,
          ...(this.options.proxy ? { proxy: this.options.proxy } : {}),
        });
    const lease = initialSpec !== "unsandboxed" && initialSpec.network.mode === "proxy"
      ? await this.options.acquireProxy?.(ctx.signal) : undefined;
    const proxy = lease?.proxy ?? this.options.proxy;
    const spec = lease && initialSpec !== "unsandboxed"
      ? { ...initialSpec, network: { mode: "proxy" as const, proxy: lease.proxy } } : initialSpec;
    const controller = new AbortController();
    const abort = () => controller.abort();
    ctx.signal?.addEventListener("abort", abort, { once: true });
    const handle: RunningCommand = { request, toolCallId: ctx.toolCallId, controller, ctx };
    const credentials = proxy?.credentials ?? ctx.toolCallId;
    this.running.set(credentials, handle);
    const scanner = new DenialScanner();
    const output = lease ? new FilteredStream(new CredentialFilter({ PROXY_CREDENTIALS: lease.proxy.credentials }), ctx.onData) : undefined;
    try {
      ctx.signal?.throwIfAborted();
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
              if (output) output.write(data); else ctx.onData(data);
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
      this.running.delete(credentials);
      controller.abort();
      lease?.release();
      output?.finish();
    }
  }

  private recordDenial(toolCallId: string, action: string, reason: string, rule: DenialRule): void {
    this.options.onDenial?.({ toolCallId, tool: "bash", action, reason, rule, at: new Date().toISOString() });
  }
}

function isApproval(decision: ReviewDecision) {
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

// Carrying the longest keyword minus one char across chunks catches a match split by a stream boundary.
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

// The vocabulary the orchestrator, the reviewers and the Guardian share.
// Codex's ApprovalRequest is core/src/tools/approvals.rs:704-891.
import type { SandboxPermissions } from "../sandbox/index";
import type { NetworkProtocol } from "../netproxy/index";

export type CommandApprovalRequest = {
  kind: "command";
  /** The bash script as the model wrote it. */
  command: string;
  cwd: string;
  sandboxPermissions: SandboxPermissions;
  justification?: string;
  /** The reusable prefix the model proposed, only ever offered as an amendment. */
  prefixRule?: string[];
  /** Why the deterministic path could not decide, from execpolicy. */
  reason?: string;
  /** Set when the first attempt was denied by the sandbox and a rerun is being asked for. */
  retryReason?: string;
};

export type NetworkApprovalRequest = {
  kind: "network";
  host: string;
  port: number;
  protocol: NetworkProtocol;
  /** The command whose connection this is, when one is running. */
  trigger?: CommandApprovalRequest;
  reason?: string;
  retryReason?: string;
};

export type ApprovalRequest = CommandApprovalRequest | NetworkApprovalRequest;

export type ReviewDecision =
  | { type: "approved" }
  | { type: "approved_for_session" }
  | { type: "approved_execpolicy_amendment"; prefix: string[] }
  | { type: "network_amendment"; host: string }
  | { type: "denied"; rejection: string }
  | { type: "abort" }
  | { type: "timed_out" };

export type ReviewUI = {
  select(title: string, options: string[]): Promise<string | undefined>;
  notify(message: string, level?: "info" | "warning" | "error"): void;
  setStatus?(key: string, text: string | undefined): void;
};

export type ReviewContext = {
  cwd: string;
  signal?: AbortSignal;
  ui?: ReviewUI;
  /** Ends the turn, pi's ctx.abort(). The user's "tell the agent what to do
   * differently" and the Guardian's circuit breaker both use it (handlers.rs:199-204). */
  abort?: () => void;
};

export type Reviewer = {
  readonly name: "user" | "guardian";
  review(request: ApprovalRequest, ctx: ReviewContext): Promise<ReviewDecision>;
};

/** Which reviewer refused, for the denial ledger and attn's denials list. */
export type DenialRule =
  | "forbidden"
  | "user"
  | "guardian"
  | "guardian-timeout"
  | "circuit-breaker"
  | "network";

export function describeCommand(request: CommandApprovalRequest): string {
  return `bash: ${request.command}`;
}

export function describeRequest(request: ApprovalRequest): string {
  return request.kind === "command"
    ? describeCommand(request)
    : `network: ${request.protocol}://${request.host}:${request.port}`;
}

/** network_approval.rs:607-608. The text the model gets when a host is refused. */
export function networkRejection(request: { protocol: NetworkProtocol; host: string; port: number }): string {
  return `Network access to "${networkScheme(request.protocol)}://${request.host}:${request.port}" was blocked by policy.`;
}

export function networkScheme(protocol: NetworkProtocol): string {
  return protocol === "https_connect" ? "https" : protocol === "socks5_tcp" ? "socks5" : "http";
}

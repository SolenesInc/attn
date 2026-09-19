import type { SandboxPermissions } from "../sandbox/index";
import type { NetworkProtocol } from "../netproxy/index";

export type CommandApprovalRequest = {
  kind: "command";
  command: string;
  cwd: string;
  sandboxPermissions: SandboxPermissions;
  justification?: string;
  prefixRule?: string[];
  reason?: string;
  retryReason?: string;
};

export type NetworkApprovalRequest = {
  kind: "network";
  host: string;
  port: number;
  protocol: NetworkProtocol;
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
  select(title: string, options: string[], opts?: { signal?: AbortSignal }): Promise<string | undefined>;
  notify(message: string, level?: "info" | "warning" | "error"): void;
  setStatus?(key: string, text: string | undefined): void;
};

export type ReviewContext = {
  cwd: string;
  signal?: AbortSignal;
  ui?: ReviewUI;
  /** Shared by the user's turn-redirect and the Guardian's circuit breaker: both end the turn through this. */
  abort?: () => void;
};

export type Reviewer = {
  readonly name: "user" | "guardian";
  review(request: ApprovalRequest, ctx: ReviewContext): Promise<ReviewDecision>;
};

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

/** This string is fed to the model as the tool rejection text. */
export function networkRejection(request: { protocol: NetworkProtocol; host: string; port: number }): string {
  return `Network access to "${networkScheme(request.protocol)}://${request.host}:${request.port}" was blocked by policy.`;
}

export function networkScheme(protocol: NetworkProtocol): string {
  return protocol === "https_connect" ? "https" : protocol === "socks5_tcp" ? "socks5" : "http";
}

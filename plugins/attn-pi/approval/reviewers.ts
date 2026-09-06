// The user as reviewer. Port of core/src/tools/approvals.rs:704-891 with the
// option set and labels of tui/src/bottom_pane/approval_overlay.rs:829-915.
import { shlexJoin } from "../execpolicy/policy";
import type { PrefixRule } from "../execpolicy/index";
import {
  describeRequest,
  networkScheme,
  type ApprovalRequest,
  type CommandApprovalRequest,
  type NetworkApprovalRequest,
  type ReviewContext,
  type ReviewDecision,
  type Reviewer,
} from "./types";

export const userRejection = "rejected by user";

export const commandOptions = {
  approve: "Yes, proceed",
  amendment: (prefix: string) => `Yes, and don't ask again for commands that start with \`${prefix}\``,
  forSession: "Yes, and don't ask again for this command in this session",
  deny: "No, continue without running it",
  abort: "No, and tell the agent what to do differently",
} as const;

export const networkOptions = {
  approve: "Yes, just this once",
  forSession: "Yes, and allow this host for this conversation",
  amendment: "Yes, and allow this host in the future",
  deny: "No, continue without running it",
  abort: "No, and tell the agent what to do differently",
} as const;

export type UserReviewerOptions = {
  /** The rules in force, so an amendment invalidates every cached approval. */
  rules: () => readonly PrefixRule[];
  /** True while the card is on screen: attn's queue shows the session waiting. */
  onWaiting?: (waiting: boolean) => void;
};

export class UserReviewer implements Reviewer {
  readonly name = "user";
  private readonly approvedForSession = new Set<string>();

  constructor(private readonly options: UserReviewerOptions) {}

  async review(request: ApprovalRequest, ctx: ReviewContext): Promise<ReviewDecision> {
    if (request.kind === "command" && this.approvedForSession.has(this.cacheKey(request))) {
      return { type: "approved" };
    }
    const ui = ctx.ui;
    // No card means nobody can answer, and an unanswered card is not an approval.
    if (!ui) return { type: "denied", rejection: userRejection };
    const labels = request.kind === "command" ? this.commandLabels(request) : this.networkLabels(request);
    this.options.onWaiting?.(true);
    let choice: string | undefined;
    try {
      choice = await ui.select(reviewTitle(request), labels.map((label) => label.text));
    } finally {
      this.options.onWaiting?.(false);
    }
    ctx.signal?.throwIfAborted();
    // A dismissed card is the user declining to answer, which is not a yes.
    const chosen = labels.find((label) => label.text === choice);
    if (!chosen) return { type: "denied", rejection: userRejection };
    if (chosen.decision.type === "approved_for_session" && request.kind === "command") {
      this.approvedForSession.add(this.cacheKey(request));
    }
    return chosen.decision;
  }

  private commandLabels(request: CommandApprovalRequest): { text: string; decision: ReviewDecision }[] {
    const labels: { text: string; decision: ReviewDecision }[] = [
      { text: commandOptions.approve, decision: { type: "approved" } },
    ];
    const prefix = request.prefixRule;
    if (prefix !== undefined && prefix.length > 0) {
      const rendered = shlexJoin(prefix);
      // approval_overlay.rs:851-854 drops the option when the rendered prefix
      // would break the one-line label.
      if (!rendered.includes("\n")) {
        labels.push({
          text: commandOptions.amendment(rendered),
          decision: { type: "approved_execpolicy_amendment", prefix: [...prefix] },
        });
      }
    }
    labels.push(
      { text: commandOptions.forSession, decision: { type: "approved_for_session" } },
      { text: commandOptions.deny, decision: { type: "denied", rejection: userRejection } },
      { text: commandOptions.abort, decision: { type: "abort" } },
    );
    return labels;
  }

  private networkLabels(request: NetworkApprovalRequest): { text: string; decision: ReviewDecision }[] {
    return [
      { text: networkOptions.approve, decision: { type: "approved" } },
      { text: networkOptions.forSession, decision: { type: "approved_for_session" } },
      { text: networkOptions.amendment, decision: { type: "network_amendment", host: request.host } },
      { text: networkOptions.deny, decision: { type: "denied", rejection: userRejection } },
      { text: networkOptions.abort, decision: { type: "abort" } },
    ];
  }

  /** approvals.rs:725-736: executable, canonical command, cwd, permissions, rules. */
  private cacheKey(request: CommandApprovalRequest): string {
    return JSON.stringify([
      "bash",
      request.command,
      request.cwd,
      request.sandboxPermissions,
      this.options.rules(),
    ]);
  }
}

/** approvals.rs:720-724: retry reason, else policy reason, else the model's justification. */
export function reviewReason(request: ApprovalRequest): string | undefined {
  const justification = request.kind === "command" ? request.justification : undefined;
  for (const candidate of [request.retryReason, request.reason, justification]) {
    if (candidate !== undefined && candidate.trim() !== "") return candidate.trim();
  }
  return undefined;
}

export function reviewTitle(request: ApprovalRequest): string {
  const reason = reviewReason(request);
  const head =
    request.kind === "command"
      ? `Run this command?\n${request.command}\nin ${request.cwd}`
      : `Allow network access to ${networkScheme(request.protocol)}://${request.host}:${request.port}?`;
  return reason === undefined ? head : `${head}\n${reason}`;
}

export { describeRequest };

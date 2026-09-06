import type { ApprovalOrchestrator } from "./orchestrator";

export * from "./config";
export {
  ApprovalOrchestrator, retryWithoutSandboxReason, networkPromptReason, turnAbortedMessage,
  type OrchestratorOptions, type OrchestratorDenial, type SandboxSource, type ExecResult, type RunShell,
} from "./orchestrator";
export { UserReviewer, commandOptions, networkOptions, userRejection, reviewReason, reviewTitle } from "./reviewers";
export { GuardianReviewer, parseAssessment, denialText, type GuardianUsageEntry } from "./guardian";
export { PiApproval, attnApprovalSource, proxyFromEnvironment, approvalConfigEnvVar, type SandboxPaths } from "./session";
export { compileRules, toPrefixRule } from "./rules";
export { guardianRejectionInstructions, guardianTimeoutInstructions } from "./instructions";
export type {
  ApprovalRequest, CommandApprovalRequest, NetworkApprovalRequest, ReviewDecision,
  ReviewContext, Reviewer, ReviewUI, DenialRule,
} from "./types";

/** What the bash tool hands the orchestrator: pi's arguments plus its call context. */
export type BashApproval = ApprovalOrchestrator["runBash"];

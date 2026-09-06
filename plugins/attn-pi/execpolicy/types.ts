// The execpolicy vocabulary. Decision is codex-rs/execpolicy/src/decision.rs;
// PrefixRule is one prefix_rule(...) call from parser.rs:349-360.
export type Decision = "allow" | "prompt" | "forbidden";

// A pattern token is one word, or the alternatives accepted in its place.
export type PatternToken = string | string[];

export type PrefixRule = {
  pattern: PatternToken[];
  decision?: Decision;
  justification?: string;
  match?: string[][];
  not_match?: string[][];
};

export type RuleError = { index: number; message: string };

export type ApprovalPolicy = "untrusted" | "on-request" | "never";

export type SandboxMode = "read-only" | "workspace-write" | "danger-full-access";

export type SandboxPermissions = "use_default" | "require_escalated";

export type RuleMatch = { rule: PrefixRule; command: string[]; decision: Decision };

export type CommandEvaluation = {
  decision: Decision;
  commands: string[][];
  unparsed: boolean;
  matches: RuleMatch[];
  dangerous: boolean;
  bypassSandbox: boolean;
  reason?: string;
};

export type EvaluationInput = {
  rules: readonly PrefixRule[];
  approvalPolicy: ApprovalPolicy;
  sandboxMode: SandboxMode;
  sandboxPermissions: SandboxPermissions;
};

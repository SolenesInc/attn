import { renderPrompt } from "./prompt-catalog";

// The whole model-facing API of auto mode: it says that auto mode blocked this,
// what was blocked, why, and that the user's approval unblocks it — nothing else.

export type Denial = {
  action: string;
  reason: string;

  judged?: boolean;

  clearable?: boolean;
};

export function sandboxDenialToolResult(denial: Denial): string {
  return [
    "Auto mode did not approve this sandbox access request. The command did not run with extra access.",
    `Blocked: ${oneLine(denial.action)}`, `Reason: ${oneLine(denial.reason)}`,
    ...(denial.judged === false ? ["The classifier could not review this request. This is a review failure, not a judgment that the command is dangerous."] : []),
    denial.clearable === false
      ? "User confirmation cannot override this restriction. Explain the reason and ask the user to review the relevant policy."
      : "Explain the command, requested access, and refusal to the user. If they explicitly approve that action and access, retry the same bash request so auto mode can consider their reply.",
    "Continue independent work. Do not disable the sandbox, change its settings, or use another tool to evade this refusal. A normal command allow pattern does not approve extra sandbox access.",
  ].join("\n");
}

export function denialToolResult(denial: Denial): string {
  return renderPrompt(
    "denial",
    {
      action: oneLine(denial.action),
      reason: oneLine(denial.reason),
      outage: String(denial.judged === false),
      hard_block: String(denial.clearable === false),
    },
    "pi-session",
  );
}

function oneLine(text: string): string {
  const collapsed = text.replace(/\s+/g, " ").trim();
  return collapsed === ""
    ? renderPrompt("not-stated", {}, "pi-session")
    : collapsed;
}

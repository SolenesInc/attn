// The whole model-facing API of auto mode: it says that auto mode blocked this,
// what was blocked, why, and that the user's approval unblocks it — nothing else.

export type Denial = {
  action: string;
  reason: string;

  judged?: boolean;

  clearable?: boolean;
  incomplete?: boolean;
};

export function sandboxDenialToolResult(denial: Denial): string {
  if (denial.incomplete) return incompleteReview(denial);
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
  if (denial.incomplete) return incompleteReview(denial);
  const lines = [
    "Permission for this action was denied by the attn auto mode classifier.",
    `Blocked: ${oneLine(denial.action)}`,
    `Reason: ${oneLine(denial.reason)}`,
    "",
  ];
  if (denial.judged === false) {
    lines.push(
      "No classifier answered, so nothing judged this call and nothing refused " +
        "the action. Auto mode blocks what it cannot ask about. This is an " +
        "outage, not a verdict.",
      "",
    );
  }
  lines.push(
    "If you have other tasks that don't depend on this action, continue working " +
      "on those. IMPORTANT: You *may* attempt to accomplish this action using " +
      "other tools that might naturally be used to accomplish this goal, e.g. " +
      "using head instead of cat. But you *should not* attempt to work around " +
      "this denial in malicious ways, e.g. do not use your ability to run tests " +
      "to execute non-test actions. You should only try to work around this " +
      "restriction in reasonable ways that do not attempt to bypass the intent " +
      "behind this denial. If you believe this capability is essential to " +
      "complete the user's request, STOP and explain to the user what you were " +
      "trying to do and why you need this permission. Let the user decide how to " +
      "proceed.",
  );
  if (denial.clearable === false) {
    lines.push(
      "",
      "Do not ask the user to approve this one. Their approval won't have any " +
        "effect on this rejection, and neither will retrying. To allow this type " +
        "of action in the future, the user changes auto mode's own setup: an " +
        "allow pattern, or the rule that refused it.",
    );
  } else {
    lines.push(
      "",
      "To allow this type of action in the future, the user can add an allow " +
        "pattern in auto mode's settings.",
    );
  }
  return lines.join("\n");
}

function incompleteReview(denial: Denial): string {
  return [
    "Auto mode could not complete this review. The action did not run.",
    `Blocked: ${oneLine(denial.action)}`, `Reason: ${oneLine(denial.reason)}`,
    "Explain the missing evidence to the user. Supply that evidence or propose a smaller reviewable action. Interactive Pi can offer one-call approval for an oversized request after the user inspects its exact arguments.",
    "Continue independent work. Do not disable security, change allow patterns or hide effects to evade this incomplete review. Existing sandbox limits and protected paths still apply.",
  ].join("\n");
}

function oneLine(text: string): string {
  const collapsed = text.replace(/\s+/g, " ").trim();
  return collapsed === "" ? "(not stated)" : collapsed;
}

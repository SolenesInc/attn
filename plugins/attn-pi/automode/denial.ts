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
  return renderPrompt("denial", {
    action: oneLine(denial.action), reason: oneLine(denial.reason),
    outage: String(denial.judged === false), hard_block: String(denial.clearable === false),
  }, "pi-security");
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

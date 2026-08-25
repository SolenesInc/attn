// The whole model-facing API of auto mode: it says that auto mode blocked this,
// what was blocked, why, and that the user's approval unblocks it — nothing else.

export type Denial = {
  action: string;
  reason: string;
};

export function denialToolResult(denial: Denial): string {
  return [
    "attn auto mode blocked this tool call.",
    "",
    `Blocked: ${oneLine(denial.action)}`,
    `Reason: ${oneLine(denial.reason)}`,
    "",
    "Auto mode runs work inside this session's safety envelope and refuses",
    "what reaches past it. Nothing about this session has stopped: say what",
    "you wanted to do and why in your reply, and ask. The user's explicit",
    "approval in the conversation lets you retry the same call. Do not work",
    "around the block by another route.",
  ].join("\n");
}

function oneLine(text: string): string {
  const collapsed = text.replace(/\s+/g, " ").trim();
  return collapsed === "" ? "(not stated)" : collapsed;
}

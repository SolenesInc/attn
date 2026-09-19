/** Every budget below approximates a token as 4 bytes. */
export const bytesPerToken = 4;

export const maxMessageEntryTokens = 5_000;
export const maxToolEntryTokens = 1_000;
export const maxMessageTranscriptTokens = 20_000;
export const maxToolTranscriptTokens = 10_000;
export const recentEntryLimit = 40;

/** Other developer messages are dropped as evidence; only this exact prefix marks a manual re-approval. */
export const manualApprovalDeveloperPrefix =
  "The user has manually approved a specific action that was previously `Rejected`.";

export type TranscriptEntryKind = "user" | "developer" | "assistant" | "tool_call" | "tool_output";

export type TranscriptEntry = {
  kind: TranscriptEntryKind;
  tool?: string;
  text: string;
};

export function approxTokenCount(text: string): number {
  return Math.ceil(Buffer.byteLength(text, "utf8") / bytesPerToken);
}

export function approxBytesForTokens(tokens: number): number {
  return tokens * bytesPerToken;
}

export function truncateForGuardian(text: string, maxTokens: number): string {
  const maxBytes = approxBytesForTokens(maxTokens);
  const bytes = Buffer.from(text, "utf8");
  if (bytes.length <= maxBytes) return text;
  const marker = `<truncated omitted_approx_tokens="${Math.ceil((bytes.length - maxBytes) / bytesPerToken)}" />`;
  if (maxBytes <= marker.length) return marker;
  const available = maxBytes - marker.length;
  const prefix = Math.floor(available / 2);
  return `${head(bytes, prefix)}${marker}${tail(bytes, available - prefix)}`;
}

function head(bytes: Buffer, length: number): string {
  let end = length;
  while (end > 0 && (bytes[end]! & 0xc0) === 0x80) end -= 1;
  return bytes.subarray(0, end).toString("utf8");
}

function tail(bytes: Buffer, length: number): string {
  let start = bytes.length - length;
  while (start < bytes.length && (bytes[start]! & 0xc0) === 0x80) start += 1;
  return bytes.subarray(start).toString("utf8");
}

export function boundEntry(entry: TranscriptEntry): TranscriptEntry {
  const cap = isTool(entry) ? maxToolEntryTokens : maxMessageEntryTokens;
  return { ...entry, text: truncateForGuardian(entry.text, cap) };
}

function isTool(entry: TranscriptEntry): boolean {
  return entry.kind === "tool_call" || entry.kind === "tool_output";
}

export function entryRole(entry: TranscriptEntry): string {
  if (entry.kind === "tool_call") return `tool ${entry.tool ?? "unknown"} call`;
  if (entry.kind === "tool_output") return `tool ${entry.tool ?? "unknown"} result`;
  return entry.kind;
}

export type RenderedTranscript = { lines: string[]; omitted: boolean };

export function renderTranscript(
  entries: readonly TranscriptEntry[],
  offset: number,
  emptyPlaceholder: string,
): RenderedTranscript {
  if (entries.length === 0) return { lines: [emptyPlaceholder], omitted: false };
  const rendered = entries.map((entry, index) => `[${index + offset + 1}] ${entryRole(entry)}: ${entry.text}`);
  const tokens = rendered.map(approxTokenCount);
  const included = entries.map(() => false);

  let messageTokens = 0;
  const users = entries.map((entry, index) => ({ entry, index })).filter(({ entry }) => entry.kind === "user");
  // The first user message is kept even when it alone exceeds the budget.
  if (users[0]) {
    included[users[0].index] = true;
    messageTokens = tokens[users[0].index]!;
    for (const { index } of users.slice(1).reverse()) {
      if (messageTokens + tokens[index]! > maxMessageTranscriptTokens) continue;
      included[index] = true;
      messageTokens += tokens[index]!;
    }
  }

  let toolTokens = 0;
  let recent = 0;
  for (let index = entries.length - 1; index >= 0; index -= 1) {
    const entry = entries[index]!;
    if (entry.kind === "user" || recent >= recentEntryLimit) continue;
    const tool = isTool(entry);
    const budget = tool ? toolTokens + tokens[index]! <= maxToolTranscriptTokens
      : messageTokens + tokens[index]! <= maxMessageTranscriptTokens;
    if (!budget) continue;
    included[index] = true;
    recent += 1;
    if (tool) toolTokens += tokens[index]!;
    else messageTokens += tokens[index]!;
  }

  return {
    lines: rendered.filter((_line, index) => included[index]),
    omitted: included.some((keep) => !keep),
  };
}

export type SessionMessageLike = {
  type: string;
  message?: {
    role?: string;
    content?: unknown;
    toolName?: string;
    toolCallId?: string;
  };
};

/** Reasoning content is deliberately excluded: the Guardian judges the action's evidence, not the agent's private thinking. */
export function transcriptFromSession(entries: readonly SessionMessageLike[]): TranscriptEntry[] {
  const collected: TranscriptEntry[] = [];
  for (const entry of entries) {
    if (entry.type !== "message" || !entry.message) continue;
    const message = entry.message;
    if (message.role === "user" || message.role === "developer") {
      const text = textOf(message.content);
      if (text === "") continue;
      if (message.role === "developer" && !text.startsWith(manualApprovalDeveloperPrefix)) continue;
      collected.push(boundEntry({ kind: message.role, text }));
      continue;
    }
    if (message.role === "assistant") {
      const text = textOf(message.content);
      if (text !== "") collected.push(boundEntry({ kind: "assistant", text }));
      for (const call of toolCalls(message.content)) {
        collected.push(boundEntry({ kind: "tool_call", tool: call.name, text: JSON.stringify(call.arguments) }));
      }
      continue;
    }
    if (message.role === "toolResult") {
      const text = textOf(message.content);
      if (text !== "") {
        collected.push(boundEntry({ kind: "tool_output", tool: message.toolName ?? "unknown", text }));
      }
    }
  }
  return collected;
}

function textOf(content: unknown): string {
  if (typeof content === "string") return content;
  if (!Array.isArray(content)) return "";
  return content
    .filter((item): item is { type: string; text: string } =>
      typeof item === "object" && item !== null && (item as { type?: string }).type === "text" &&
      typeof (item as { text?: unknown }).text === "string" && (item as { text: string }).text !== "")
    .map((item) => item.text)
    .join("\n");
}

function toolCalls(content: unknown): { name: string; arguments: unknown }[] {
  if (!Array.isArray(content)) return [];
  return content.filter((item): item is { type: "toolCall"; name: string; arguments: unknown } =>
    typeof item === "object" && item !== null && (item as { type?: string }).type === "toolCall")
    .map((call) => ({ name: call.name, arguments: call.arguments }));
}

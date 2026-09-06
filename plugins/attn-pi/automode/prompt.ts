import { renderPrompt } from "./prompt-catalog";
import { renderEnvironment, type Environment } from "./environment";
import { renderTranscript, type TranscriptEntry } from "./transcript";
import type { ActionEvidence } from "./evidence";

export const stageOneAllowCeiling = 25;
export const blockLine = 35;

export type ClassifierStage = "harm" | "intent";

export type ParsedSeverity = {
  severity: number;
  category?: string;
  thinking?: string;
};

export type PromptInput = {
  transcript: readonly TranscriptEntry[];
  environment: Environment;

  action: ActionEvidence | string;

  tool: string;

  reason: string;
  cwd: string;
};

export const hardBlockRule = "Data Exfiltration";

export function classifierSystemPrompt(environment: Environment): string {
  return renderPrompt("system", {
    environment: renderEnvironment(environment),
  });
}

export function grantPrompt(text: string): string {
  return renderPrompt("grant", { opening_message: text });
}

export function classifierUserPrompt(
  input: PromptInput,
  stage: ClassifierStage,
): string {
  return renderPrompt(stage, {
    cwd: input.cwd,
    conversation: renderTranscript(input.transcript),
    has_conversation: String(input.transcript.length > 0),
    action: JSON.stringify({ [input.tool]: input.action }),
    reason: input.reason,
  });
}

export function parseSeverity(text: string): ParsedSeverity | undefined {
  const thinking = /<thinking>([\s\S]*?)<\/thinking>/.exec(text)?.[1]?.trim();
  const body = text.replace(/<thinking>[\s\S]*?<\/thinking>/g, "");
  if (body.includes("<thinking>")) return undefined;
  const found = [
    ...body.matchAll(/<severity>\s*(\d+(?:\.\d+)?)\s*(?:<\/severity>)?/g),
  ];
  if (found.length !== 1) return undefined;
  const severity = Number(found[0]?.[1]);
  if (!Number.isFinite(severity)) return undefined;
  const category = /<category>([a-z0-9 &_-]{1,64})<\/category>/i
    .exec(body)?.[1]
    ?.trim();
  const reason = /<reason>([\s\S]*?)<\/reason>/.exec(body)?.[1]?.trim();
  const explanation = reason || thinking;
  return {
    severity,
    ...(category ? { category } : {}),
    ...(explanation ? { thinking: explanation } : {}),
  };
}

export function unreadableReason(text: string): string {
  return renderPrompt("unreadable", { excerpt: excerpt(text) });
}

function excerpt(text: string): string {
  const collapsed = text.replace(/\s+/g, " ").trim();
  if (collapsed === "") return renderPrompt("nothing", {});
  return collapsed.length > 160 ? `${collapsed.slice(0, 160)}…` : collapsed;
}

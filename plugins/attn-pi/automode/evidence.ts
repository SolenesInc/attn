import { createHash } from "node:crypto";
import type { ToolCall } from "./policy";
import { credentials } from "../security/filter";

export type ActionEvidence = ToolCall & { cwd: string };

export type ToolEvidenceLimits = { entryBytes: number; totalBytes: number; entries: number };

export function toolEvidenceLimits(contextWindow = 128_000): ToolEvidenceLimits {
  // At four bytes per token, reserve seven eighths for policy, messages, the pending action and replies.
  const totalBytes = Math.min(512 * 1024, Math.max(1024, Math.floor(contextWindow / 2)));
  return { entryBytes: Math.min(64 * 1024, Math.floor(totalBytes / 2)), totalBytes, entries: 128 };
}

export function snapshotCall(call: ToolCall): ToolCall {
  return { ...call, input: structuredClone(call.input) };
}

export function actionEvidence(call: ToolCall, cwd: string): ActionEvidence {
  return credentials.value({ ...snapshotCall(call), cwd });
}

export function inputFingerprint(input: Record<string, unknown>): string {
  return createHash("sha256").update(JSON.stringify(input)).digest("hex");
}

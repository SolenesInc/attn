import { existsSync, mkdirSync } from "node:fs";
import { join } from "node:path";

const dataRoot = process.env.ATTN_PLUGIN_DATA_ROOT;
if (!dataRoot) {
  throw new Error("ATTN_PLUGIN_DATA_ROOT is set by attn for installed plugins");
}
mkdirSync(join(dataRoot, "run"), { recursive: true });
mkdirSync(join(dataRoot, "conversations"), { recursive: true });

export function transcriptPath(conversationId: string): string {
  return join(dataRoot!, "conversations", `${conversationId}.log`);
}

export function conversationExists(conversationId: string): boolean {
  return existsSync(transcriptPath(conversationId));
}

const unixSocketPathLimit = 104;

export function inputSocketPath(runId: string): string {
  const path = join(dataRoot!, "run", `${runId.slice(0, 12)}.sock`);
  if (path.length >= unixSocketPathLimit) {
    throw new Error(`input socket path ${path} is ${path.length} characters; a Unix socket path must be shorter than ${unixSocketPathLimit}`);
  }
  return path;
}

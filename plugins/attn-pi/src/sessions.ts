import { readFile, readdir, open } from "node:fs/promises";
import { homedir } from "node:os";
import { join, resolve } from "node:path";
import { StringDecoder } from "node:string_decoder";
import { fileURLToPath } from "node:url";
import type { DriverResumeAvailableParams, DriverResumeAvailableResult, DriverResumeAvailabilityBatchParams, DriverResumeAvailabilityBatchResult } from "./types";

// Pi uses ten concurrent discovery reads; a 10,000-file batch took at
// most 7.3s across two directories (receipts/resume-availability.md).
const sessionHeaderConcurrency = 10;

export async function resumeAvailable(
  params: DriverResumeAvailableParams,
  env: Record<string, string | undefined>,
): Promise<DriverResumeAvailableResult> {
  const result = await resumeAvailabilityBatch({ conversations: [params] }, env);
  return result.availability[0];
}

export async function resumeAvailabilityBatch(
  params: DriverResumeAvailabilityBatchParams,
  env: Record<string, string | undefined>,
): Promise<DriverResumeAvailabilityBatchResult> {
  const availability: DriverResumeAvailabilityBatchResult["availability"] = params.conversations.map(conversation => ({ ...conversation, available: false }));
  const directories = new Map<string, { index: number; cwd: string; defaultDirectory: boolean }[]>();
  const locations = new Map<string, Promise<{ directory: string; defaultDirectory: boolean }>>();
  for (const [index, conversation] of params.conversations.entries()) {
    try {
      const cwd = resolve(conversation.cwd);
      let location = locations.get(cwd);
      if (!location) {
        location = sessionLocation(cwd, env);
        locations.set(cwd, location);
      }
      const { directory, defaultDirectory } = await location;
      const requests = directories.get(directory) ?? [];
      requests.push({ index, cwd, defaultDirectory });
      directories.set(directory, requests);
      availability[index].reason = `conversation ${conversation.resume_session_id} is no longer in pi's storage (${directory})`;
    } catch (error) {
      availability[index].reason = inspectionError(conversation.resume_session_id, error);
    }
  }
  for (const [directory, requests] of directories) {
    try {
      let files: string[];
      try {
        files = await readdir(directory);
      } catch (error) {
        if (!missing(error)) throw error;
        files = [];
      }
      const remaining = new Set(requests.map(request => request.index));
      const byID = new Map<string, typeof requests>();
      for (const request of requests) {
        const id = params.conversations[request.index].resume_session_id;
        const targets = byID.get(id) ?? [];
        targets.push(request);
        byID.set(id, targets);
      }
      for (let offset = 0; offset < files.length; offset += sessionHeaderConcurrency) {
        const headers = await Promise.all(files.slice(offset, offset + sessionHeaderConcurrency)
          .filter(file => file.endsWith(".jsonl"))
          .map(file => sessionHeader(join(directory, file))));
        for (const header of headers) {
          if (!header) continue;
          for (const request of byID.get(header.id) ?? []) {
            if (request.defaultDirectory || typeof header.cwd === "string" && resolve(expandPath(header.cwd)) === request.cwd) {
              availability[request.index] = { ...params.conversations[request.index], available: true };
              remaining.delete(request.index);
            }
          }
        }
        if (remaining.size === 0) break;
      }
    } catch (error) {
      for (const request of requests) {
        if (!availability[request.index].available) availability[request.index].reason = inspectionError(params.conversations[request.index].resume_session_id, error);
      }
    }
  }
  return { availability };
}

async function sessionLocation(cwd: string, env: Record<string, string | undefined>): Promise<{ directory: string; defaultDirectory: boolean }> {
  const agentDir = resolve(cwd, expandPath(env.PI_CODING_AGENT_DIR || join(homedir(), ".pi", "agent")));
  let configured: string | null | undefined = env.PI_CODING_AGENT_SESSION_DIR;
  if (!configured) {
    const project = await configuredSessionDir(join(cwd, ".pi", "settings.json"));
    configured = project !== undefined ? project : await configuredSessionDir(join(agentDir, "settings.json"));
  }
  const defaultDirectory = join(agentDir, "sessions", `--${cwd.replace(/^[/\\]/, "").replace(/[/\\:]/g, "-")}--`);
  const directory = configured ? resolve(cwd, expandPath(configured)) : defaultDirectory;
  return { directory, defaultDirectory: directory === defaultDirectory };
}

function inspectionError(id: string, error: unknown): string {
  return `cannot check conversation ${id} in pi's storage: ${error instanceof Error ? error.message : String(error)}`;
}

async function configuredSessionDir(path: string): Promise<string | null | undefined> {
  try {
    const settings = JSON.parse(await readFile(path, "utf8"));
    return settings.sessionDir;
  } catch {
    return undefined;
  }
}

async function sessionHeader(path: string): Promise<{ id: string; cwd?: string } | undefined> {
  let file;
  try {
    file = await open(path, "r");
    // Match Pi's discovery read buffer; this is capacity, not a header limit.
    const buffer = Buffer.alloc(4096);
    const decoder = new StringDecoder("utf8");
    let pending = "";
    for (;;) {
      const { bytesRead } = await file.read(buffer, 0, buffer.length, null);
      pending += bytesRead ? decoder.write(buffer.subarray(0, bytesRead)) : decoder.end();
      let newline;
      while ((newline = pending.indexOf("\n")) !== -1 || bytesRead === 0 && pending.length > 0) {
        const line = newline === -1 ? pending : pending.slice(0, newline);
        pending = newline === -1 ? "" : pending.slice(newline + 1);
        let entry;
        try { entry = JSON.parse(line); } catch { continue; }
        if (!entry) continue;
        return entry.type === "session" && typeof entry.id === "string" ? entry : undefined;
      }
      if (bytesRead === 0) return undefined;
    }
  } catch {
    return undefined;
  } finally {
    await file?.close();
  }
}

function expandPath(path: string): string {
  if (path === "~") return homedir();
  if (path.startsWith("~/")) return join(homedir(), path.slice(2));
  return path.startsWith("file://") ? fileURLToPath(path) : path;
}

function missing(error: unknown): boolean {
  return (error as NodeJS.ErrnoException).code === "ENOENT";
}

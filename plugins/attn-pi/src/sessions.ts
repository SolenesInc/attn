import { createReadStream } from "node:fs";
import { readFile, readdir } from "node:fs/promises";
import { homedir } from "node:os";
import { join, resolve } from "node:path";
import { createInterface } from "node:readline";
import { fileURLToPath } from "node:url";
import type { DriverResumeAvailableParams, DriverResumeAvailableResult } from "./types";

export async function resumeAvailable(
  params: DriverResumeAvailableParams,
  env: Record<string, string | undefined>,
): Promise<DriverResumeAvailableResult> {
  const cwd = resolve(params.cwd);
  const agentDir = resolve(cwd, expandPath(env.PI_CODING_AGENT_DIR || join(homedir(), ".pi", "agent")));
  try {
    let configured: string | null | undefined = env.PI_CODING_AGENT_SESSION_DIR;
    if (!configured) {
      const project = await configuredSessionDir(join(cwd, ".pi", "settings.json"));
      configured = project !== undefined ? project : await configuredSessionDir(join(agentDir, "settings.json"));
    }
    const defaultDirectory = join(agentDir, "sessions", `--${cwd.replace(/^[/\\]/, "").replace(/[/\\:]/g, "-")}--`);
    const directory = configured ? resolve(cwd, expandPath(configured)) : defaultDirectory;
    let files: string[];
    try {
      files = await readdir(directory);
    } catch (error) {
      if (!missing(error)) throw error;
      files = [];
    }
    for (const file of files) {
      if (!file.endsWith(".jsonl")) continue;
      const header = await sessionHeader(join(directory, file));
      if (header?.id === params.resume_session_id &&
          (directory === defaultDirectory || typeof header.cwd === "string" && resolve(expandPath(header.cwd)) === cwd)) {
        return { available: true };
      }
    }
    return {
      available: false,
      reason: `conversation ${params.resume_session_id} is no longer in pi's storage (${directory})`,
    };
  } catch (error) {
    return {
      available: false,
      reason: `cannot check conversation ${params.resume_session_id} in pi's storage: ${error instanceof Error ? error.message : String(error)}`,
    };
  }
}

async function configuredSessionDir(path: string): Promise<string | null | undefined> {
  try {
    const settings = JSON.parse(await readFile(path, "utf8"));
    return settings.sessionDir;
  } catch (error) {
    if (missing(error) || error instanceof SyntaxError) return undefined;
    throw error;
  }
}

async function sessionHeader(path: string): Promise<{ id: string; cwd?: string } | undefined> {
  const stream = createReadStream(path, { encoding: "utf8" });
  const lines = createInterface({ input: stream, crlfDelay: Infinity });
  try {
    for await (const line of lines) {
      let entry;
      try { entry = JSON.parse(line); } catch { continue; }
      if (!entry) continue;
      return entry.type === "session" && typeof entry.id === "string" ? entry : undefined;
    }
  } catch (error) {
    if (!missing(error)) throw error;
  } finally {
    lines.close();
    stream.destroy();
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

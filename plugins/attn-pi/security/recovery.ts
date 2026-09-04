import { statSync } from "node:fs";
import { homedir } from "node:os";
import { parse } from "node:path";
import { Type, type Static } from "typebox";
import type { AutoModeContextLike, ToolCallEventLike, ToolCallEventResultLike } from "../automode/index";
import { canonical, expand, within, type SecurityPolicy } from "./policy";

export const sandboxRequestSchema = Type.Object({
  allowWrite: Type.Optional(Type.Array(Type.String({ minLength: 1 }), { minItems: 1 })),
  network: Type.Optional(Type.Literal("allow")),
  reason: Type.String({ minLength: 1, description: "Why this command needs these exact directories or network access to complete the user's task." }),
}, { additionalProperties: false });

export type SandboxRequest = Static<typeof sandboxRequestSchema>;
export type SandboxReview = (event: ToolCallEventLike, ctx: AutoModeContextLike) => Promise<ToolCallEventResultLike | undefined>;

export function scopedPolicy(policy: SecurityPolicy, request: SandboxRequest): { policy: SecurityPolicy; request: SandboxRequest } {
  if (!request || typeof request !== "object" || typeof request.reason !== "string" || !request.reason.trim()) {
    throw new Error("Sandbox review needs a reason explaining why the command needs this access.");
  }
  if (Object.keys(request).some((key) => !["allowWrite", "network", "reason"].includes(key))) {
    throw new Error("Sandbox review supports allowWrite and network only; it cannot disable protection or restore credentials.");
  }
  if (request.network !== undefined && request.network !== "allow") throw new Error("Sandbox network requests must name allow.");
  if (request.allowWrite !== undefined && (!Array.isArray(request.allowWrite) || request.allowWrite.some((path) => typeof path !== "string" || !path.trim()))) {
    throw new Error("Sandbox allowWrite must name directories.");
  }
  const roots = [...new Set((request.allowWrite ?? []).map((path) => expand(path, policy.cwd)))];
  if (roots.length === 0 && !request.network) throw new Error("Sandbox review needs allowWrite directories or network: allow.");
  for (const path of roots) {
    if (path === parse(path).root || path === canonical(homedir())) throw new Error("Request the specific cache or output directory, not the filesystem root or your entire home directory.");
    if ([...policy.denyRead, ...policy.denyWrite].some((denied) => within(path, canonical(denied)))) {
      throw new Error(`Security blocked write: ${path} is explicitly protected. Auto mode cannot override this restriction. Ask the user to review the security settings; do not retry through another tool.`);
    }
    try {
      if (!statSync(path).isDirectory()) throw new Error("not a directory");
    } catch {
      throw new Error(`Sandbox write request must name an existing directory: ${path}. Use a cache inside the project, or ask the user to create this directory first.`);
    }
  }
  return {
    policy: { ...policy, allowWrite: [...new Set([...policy.allowWrite, ...roots])], network: request.network ?? policy.network },
    request: { ...(roots.length ? { allowWrite: roots } : {}), ...(request.network ? { network: request.network } : {}), reason: request.reason.trim() },
  };
}

export { reviewUnavailable, sandboxRecovery } from "./guidance";

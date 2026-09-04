import { statSync } from "node:fs";
import { homedir } from "node:os";
import { cachePath, defaultBuildCaches } from "./caches";
import { expand, type SecurityConfig } from "./policy";

export function changeSecurityConfig(config: SecurityConfig, command: string, cwd: string): void {
  if (command === "on" || command === "off") { config.enabled = command === "on"; return; }
  if (command === "network allow" || command === "network deny") { config.network = command.endsWith("allow") ? "allow" : "deny"; return; }
  if (command === "caches on" || command === "caches off") { config.buildCaches.enabled = command.endsWith("on"); return; }
  if (command === "caches reset") { config.buildCaches = defaultBuildCaches(); return; }
  const match = /^(caches add|caches remove|allow-write|revoke-write|protect-read|unprotect-read|protect-write|unprotect-write)\s+(.+)$/.exec(command);
  if (!match) throw new Error("Use /security for settings, or /security status, on, off, caches on|off|reset, caches add|remove <path>, allow-write|revoke-write <path>, protect-read|unprotect-read <path>, protect-write|unprotect-write <path>, network allow|deny");
  const [verb, value] = [match[1]!, match[2]!];
  if (!value.trim() || /[\x00-\x1f\x7f]/.test(value)) throw new Error("Enter a path without control characters");
  const path = expand(value.trim(), cwd);
  const cache = verb.startsWith("caches ");
  const add = ["caches add", "allow-write", "protect-read", "protect-write"].includes(verb);
  if (cache && add) cachePath(path);
  if (verb === "allow-write") {
    if (path === "/") throw new Error("A filesystem root cannot be a sandbox write grant");
    try { if (!statSync(path).isDirectory()) throw new Error(); }
    catch { throw new Error("Write grants must name an existing directory. Use a build-cache entry to create a new cache directory."); }
  }
  const field = verb.endsWith("read") ? "denyRead" : verb.includes("protect-write") ? "denyWrite" : "allowWrite";
  const paths = cache ? config.buildCaches.paths : config[field];
  const updated = paths.filter((item) => expand(item, homedir()) !== path);
  if (add) updated.push(path);
  if (cache) config.buildCaches.paths = updated;
  else config[field] = updated;
}

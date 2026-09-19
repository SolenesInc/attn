import { mkdirSync, mkdtempSync } from "node:fs";
import { homedir, tmpdir } from "node:os";
import { join } from "node:path";
import { canonical, within } from "../security/policy";

// sandboxSpecFor grants /tmp, and /dev/shm on Linux, as default writable roots under
// workspace-write, so a fixture built under either proves nothing about "outside".
export function fixtureRoot(prefix: string): string {
  const systemTemp = canonical(tmpdir());
  const grantedByDefault = ["/tmp", "/dev/shm"].map(canonical).some((root) => within(systemTemp, root));
  const base = grantedByDefault ? join(homedir(), ".cache", "attn-pi-tests") : systemTemp;
  mkdirSync(base, { recursive: true });
  return canonical(mkdtempSync(join(base, prefix)));
}

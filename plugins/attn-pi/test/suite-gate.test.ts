import { describe, expect, test } from "bun:test";
import { join } from "node:path";
import { approvalConfigEnvVar } from "../approval/index";

const probe = join(import.meta.dir, "suite-gate-probe.ts");
const suiteEntry = join(import.meta.dir, "..", "suite", "index.ts");

type Registered = { events: string[]; commands: string[]; flags: string[] };

async function load(entrypoint: string, env: Record<string, string>): Promise<Registered> {
  const run = Bun.spawn([process.execPath, "run", probe, entrypoint], {
    // A clean environment: a machine running attn would otherwise hand this test
    // a config nobody asked for.
    env: { PATH: process.env.PATH ?? "", HOME: process.env.HOME ?? "", ...env },
    stdout: "pipe",
    stderr: "pipe",
  });
  const [stdout, stderr, code] = await Promise.all([
    new Response(run.stdout).text(),
    new Response(run.stderr).text(),
    run.exited,
  ]);
  if (code !== 0) throw new Error(`suite probe failed (${code}): ${stderr}`);
  return JSON.parse(stdout) as Registered;
}

describe("suite composition", () => {
  test("a bare pi loading the suite is untouched by approvals", async () => {
    const registered = await load(suiteEntry, {});
    expect(registered.commands).toEqual([]);
    expect(registered.flags).toEqual([]);
    expect(registered.events).toEqual([]);
  });

  test("attn's config composes the approval orchestrator into the suite", async () => {
    const registered = await load(suiteEntry, {
      [approvalConfigEnvVar]: JSON.stringify({ enabled_default: true }),
    });
    expect(registered.commands).toEqual(["security", "auto"]);
    expect(registered.flags).toEqual(["auto", "no-auto"]);
    expect(registered.events).toContain("session_start");
    expect(registered.events).toContain("agent_start");
  });
});

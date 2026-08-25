// Loads the suite entrypoint twice in one process, the way pi's `/reload` does by clearing the
// extension module cache (pi 0.83.0).
import { copyFileSync, rmSync } from "node:fs";
import { dirname, join } from "node:path";

const entrypoint = process.argv[2];
if (!entrypoint) throw new Error("usage: suite-reload-probe.ts <entrypoint>");

type Handler = (event: unknown, ctx: unknown) => unknown;

const handlers = new Map<string, Handler>();

const ctx = {
  isIdle: () => true,
  sessionManager: { getSessionId: () => "pi-session-1" },
};

const pi = {
  on(event: string, handler: Handler): void {
    handlers.set(event, handler);
  },
  registerCommand(): void {},
  registerFlag(): void {},
  getFlag(): undefined {
    return undefined;
  },
  sendUserMessage(): void {},
};

// A byte-identical copy: same relative imports under a module id bun has not
const secondEvaluation = join(dirname(entrypoint), `.reload-probe.${process.pid}.ts`);
copyFileSync(entrypoint, secondEvaluation);
try {
  for (const module of [entrypoint, secondEvaluation]) {
    const { default: factory } = await import(module);
    (factory as (pi: unknown) => void)(pi);
    await handlers.get("session_start")?.({ type: "session_start", reason: "reload" }, ctx);
  }
} finally {
  rmSync(secondEvaluation, { force: true });
}

await new Promise((resolve) => setTimeout(resolve, 300));
process.exit(0);

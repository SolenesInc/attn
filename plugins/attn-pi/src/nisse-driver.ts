import { existsSync } from "node:fs";
import type { AttnRPCClient } from "./attn-rpc";
import type { DriverRegisterResult, DriverSpawnParams, DriverSpawnResult } from "./types";

// The `nisse` agent: pi run headless through its SDK instead of its TUI. A launcher only —
// it hands attn the host argv and the model; the envelope stream and verbs are the host's.
export const nisseAgentName = "nisse";

// The model a `nisse` session runs when the launch pins none. Receipt: the provider/model
// pair the 2026-08-04 SDK spike and the 2026-08-05 host measurements ran end to end.
export const defaultNisseModel = "openai/gpt-5.6-luna";

export class NisseDriver {
  private readonly rpc: AttnRPCClient;
  // The launch command, argv[0] first. Compiled it is one path; from source it
  // is bun plus the entrypoint, which is why this is a command and not a path.
  private readonly hostCommand: string[];

  constructor(options: { rpc: AttnRPCClient; hostCommand: string[] }) {
    this.rpc = options.rpc;
    this.hostCommand = options.hostCommand;
  }

  async initialize(): Promise<void> {
    const result = await this.rpc.request<DriverRegisterResult>("driver.register", {
      agent: nisseAgentName,
      capabilities: {
        conversation: true,
        initial_prompt: true,
        model_pin: true,
        state_reporting: true,
      },
    });
    if (!result.ok) throw new Error("attn rejected nisse driver registration");
  }

  health(): { ok: boolean; message: string } {
    const entrypoint = this.hostCommand[this.hostCommand.length - 1] ?? "";
    return existsSync(entrypoint)
      ? { ok: true, message: `nisse is ready at ${entrypoint}` }
      : { ok: false, message: `nisse is missing at ${entrypoint}; this is a build/packaging bug` };
  }

  async spawn(params: DriverSpawnParams): Promise<DriverSpawnResult> {
    const health = this.health();
    if (!health.ok) throw new Error(health.message);
    const model = params.model?.trim() || defaultNisseModel;
    const initialPrompt = params.initial_prompt?.trim() ?? "";
    return {
      argv: [...this.hostCommand],
      cwd: params.cwd,
      env: {
        ATTN_NISSE_MODEL: model,
        ...(initialPrompt === "" ? {} : { ATTN_NISSE_INITIAL_PROMPT: initialPrompt }),
      },
    };
  }
}

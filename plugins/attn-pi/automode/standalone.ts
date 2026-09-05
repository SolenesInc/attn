// One AutoMode at module scope, so the user's `/auto` survives every session
// transition in this process.
import { readFileSync } from "node:fs";
import { denialLedgerFor } from "./ledger";
import { AutoMode, type AutoModePiLike } from "./mode";
import { standaloneAutoModeSource } from "./source";
import {
  createBashToolDefinition, createReadToolDefinition, createWriteToolDefinition, createEditToolDefinition,
  createLsToolDefinition, createFindToolDefinition, createGrepToolDefinition, type ExtensionAPI, type ToolDefinition,
} from "@earendil-works/pi-coding-agent";

const source = standaloneAutoModeSource(process.env, readConfigFile);
const autoMode = new AutoMode({
  config: source.config,
  notice: source.problem,
  ledger: denialLedgerFor(process.env),
});

export default function attnAutoMode(pi: AutoModePiLike & ExtensionAPI): void {
  autoMode.register(pi);
  pi.on("session_start", (_event, ctx) => {
    for (const make of [createBashToolDefinition, createReadToolDefinition, createWriteToolDefinition, createEditToolDefinition, createLsToolDefinition, createFindToolDefinition, createGrepToolDefinition]) {
      const tool: ToolDefinition = make(ctx.cwd);
      pi.registerTool({ ...tool, execute(id, input, signal, onUpdate, context) {
        const args = structuredClone(input);
        autoMode.checkExecution({ type: "tool_call", toolCallId: id, toolName: tool.name, input: args }, { ...context, signal });
        return tool.execute(id, args, signal, onUpdate, context);
      } });
    }
  });
}

function readConfigFile(path: string): string | undefined {
  try {
    return readFileSync(path, "utf8");
  } catch (error) {
    if ((error as NodeJS.ErrnoException)?.code === "ENOENT") return undefined;
    throw error;
  }
}

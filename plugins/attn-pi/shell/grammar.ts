// The tree-sitter-bash grammar attn parses shell commands with. Port of
// codex-rs/shell-command/src/bash.rs try_parse_shell, whose parser is sync.
import { existsSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { Language, Parser, type Tree } from "web-tree-sitter";

const grammarFile = "tree-sitter-bash.wasm";

// Both wasm files sit next to this module: the vendored grammar in the source
// tree, and copies of both beside suite.js in scripts/build-bundled-plugins.sh.
const assetDirectory = dirname(fileURLToPath(import.meta.url));

let parser: Parser | undefined;
let loading: Promise<Parser> | undefined;

export function grammarPath(): string {
  return join(assetDirectory, grammarFile);
}

async function load(): Promise<Parser> {
  const path = grammarPath();
  if (!existsSync(path)) {
    throw new Error(
      `tree-sitter-bash grammar missing at ${path}: it is vendored at plugins/attn-pi/shell/${grammarFile} ` +
        "and copied beside the bundle by scripts/build-bundled-plugins.sh",
    );
  }
  await Parser.init({
    // Bundled, web-tree-sitter's glue looks for its runtime wasm next to
    // itself; in a source checkout the runtime lives in node_modules.
    locateFile(name: string, directory: string) {
      const beside = join(assetDirectory, name);
      return existsSync(beside) ? beside : `${directory}${name}`;
    },
  });
  const language = await Language.load(path);
  const loaded = new Parser();
  loaded.setLanguage(language);
  parser = loaded;
  return loaded;
}

// Loads the grammar once per process. Every parse entry point below is
// synchronous, as it is in Codex, so callers init once at startup.
export function initShellParsing(): Promise<Parser> {
  loading ??= load();
  return loading;
}

export function shellParsingReady(): boolean {
  return parser !== undefined;
}

export function tryParseShell(source: string): Tree | undefined {
  if (parser === undefined) {
    throw new Error("shell parsing is not initialized: await initShellParsing() before parsing commands");
  }
  return parser.parse(source) ?? undefined;
}

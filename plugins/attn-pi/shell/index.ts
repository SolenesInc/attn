// Port of codex-rs/shell-command/src/{bash.rs,command_safety/is_dangerous_command.rs}
// and core/src/exec_policy/executable_identity.rs:56-103.
import { basename, dirname } from "node:path";
import type { Node, Tree } from "web-tree-sitter";
import { tryParseShell } from "./grammar";

export { initShellParsing, shellParsingReady, grammarPath } from "./grammar";

export type ShellType = "zsh" | "bash" | "sh" | "powershell" | "cmd";

// Which dangerous-command rule an argv matched. bash.rs's DangerousCommandMatch;
// the reason text the two carry differs, so the caller keeps them apart.
export type DangerousCommandMatch = "forced-rm" | "other";

// bash.rs:36-52. A named node outside this list, or punctuation outside the
// operator set, means the grammar cannot prove the script is plain commands.
const allowedKinds = new Set([
  "program",
  "list",
  "pipeline",
  "command",
  "command_name",
  "word",
  "string",
  "string_content",
  "raw_string",
  "number",
  "concatenation",
]);
const allowedPunctuation = new Set(["&&", "||", ";", "|", '"', "'"]);

// bash.rs:272. These survive as shell expansion or escape removal, so the
// source spelling of such a word is not proof of the runtime argv.
const expandingCharacters = ["{", "}", "*", "?", "[", "]", "\\", "~", "^", "#", "$", "`"];

const escapedInDoubleQuotes = ['$', "`", '"', "\\", "\n"];

const maxDangerousCommandWrapperDepth = 8;

export function detectShellType(shellPath: string): ShellType | undefined {
  switch (shellPath) {
    case "zsh":
      return "zsh";
    case "sh":
      return "sh";
    case "cmd":
      return "cmd";
    case "bash":
      return "bash";
    case "pwsh":
    case "powershell":
      return "powershell";
    default: {
      const stem = fileStem(shellPath);
      if (stem !== undefined && stem !== shellPath) return detectShellType(stem);
      return undefined;
    }
  }
}

function fileStem(path: string): string | undefined {
  const name = basename(path);
  if (name === "") return undefined;
  const dot = name.lastIndexOf(".");
  return dot > 0 ? name.slice(0, dot) : name;
}

// bash.rs:106-119. Only a three word `<shell> -c|-lc <script>` invocation of a
// posix shell exposes a script; anything else is opaque to the parser.
export function extractBashCommand(command: readonly string[]): { shell: string; script: string } | undefined {
  if (command.length !== 3) return undefined;
  const [shell, flag, script] = command as [string, string, string];
  if (flag !== "-lc" && flag !== "-c") return undefined;
  const shellType = detectShellType(shell);
  if (shellType !== "zsh" && shellType !== "bash" && shellType !== "sh") return undefined;
  return { shell, script };
}

export function parseShellScriptIntoCommands(script: string): string[][] | undefined {
  const tree = tryParseShell(script);
  if (tree === undefined) return undefined;
  // web-tree-sitter 0.25.10 registers no finalizer, so a tree nobody deletes
  // holds its wasm allocation for the life of the process.
  try {
    return tryParseWordOnlyCommandsSequence(tree);
  } finally {
    tree.delete();
  }
}

// Returns one argv per plain command in a `bash -lc` style invocation, or
// undefined when the grammar cannot prove the script is plain commands.
export function parseBashCommands(command: readonly string[]): string[][] | undefined {
  const extracted = extractBashCommand(command);
  if (extracted === undefined) return undefined;
  return parseShellScriptIntoCommands(extracted.script);
}

export function isDangerousCommand(argv: readonly string[]): boolean {
  return dangerousCommandMatch(argv) !== undefined;
}

export function dangerousCommandMatch(argv: readonly string[]): DangerousCommandMatch | undefined {
  return dangerousCommandMatchWithDepth(argv, 0);
}

// executable_identity.rs:56-103. An unfamiliar executable can ignore its
// arguments, so it is judged on its own rather than on its apparent command.
export function shellApprovalCommand(command: readonly string[], configuredShellPath: string): readonly string[] {
  const executable = command[0];
  if (executable === undefined) return command;
  const parent = executable.includes("/") ? dirname(executable) : "";
  const isSystemShell = parent === "/bin" || parent === "/usr/bin";
  if (executable === configuredShellPath || isSystemShell) return command;
  return command.slice(0, 1);
}

function tryParseWordOnlyCommandsSequence(tree: Tree): string[][] | undefined {
  const root = tree.rootNode;
  if (root.hasError) return undefined;

  const commandNodes: Node[] = [];
  const stack: Node[] = [root];
  while (stack.length > 0) {
    const node = stack.pop() as Node;
    const kind = node.type;
    if (node.isNamed) {
      if (!allowedKinds.has(kind)) return undefined;
      if ((kind === "word" || kind === "number") && !isLiteralWordOrNumber(node)) return undefined;
      if (kind === "command") commandNodes.push(node);
    } else {
      const isOperatorLike = [...kind].some((character) => "&;|".includes(character));
      if (isOperatorLike && !allowedPunctuation.has(kind)) return undefined;
      if (!allowedPunctuation.has(kind) && kind.trim() !== "") return undefined;
    }
    for (const child of node.children) if (child !== null) stack.push(child);
  }

  // The walk is a stack, so command nodes come out reversed.
  commandNodes.sort((left, right) => left.startIndex - right.startIndex);

  const commands: string[][] = [];
  for (const node of commandNodes) {
    const words = parsePlainCommandFromNode(node);
    if (words === undefined) return undefined;
    commands.push(words);
  }
  return commands;
}

function parsePlainCommandFromNode(command: Node): string[] | undefined {
  if (command.type !== "command") return undefined;
  const words: string[] = [];
  for (const child of command.namedChildren) {
    if (child === null) return undefined;
    switch (child.type) {
      case "command_name": {
        const word = child.namedChild(0);
        if (word === null || word.type !== "word") return undefined;
        words.push(word.text);
        break;
      }
      case "word":
      case "number":
        words.push(child.text);
        break;
      case "string": {
        const parsed = parseDoubleQuotedString(child);
        if (parsed === undefined) return undefined;
        words.push(parsed);
        break;
      }
      case "raw_string": {
        const parsed = parseRawString(child);
        if (parsed === undefined) return undefined;
        words.push(parsed);
        break;
      }
      case "concatenation": {
        let concatenated = "";
        for (const part of child.namedChildren) {
          if (part === null) return undefined;
          if (part.type === "word" || part.type === "number") {
            concatenated += part.text;
            continue;
          }
          const parsed = part.type === "string" ? parseDoubleQuotedString(part) : part.type === "raw_string" ? parseRawString(part) : undefined;
          if (parsed === undefined) return undefined;
          concatenated += parsed;
        }
        if (concatenated === "") return undefined;
        words.push(concatenated);
        break;
      }
      default:
        return undefined;
    }
  }
  return words;
}

// bash.rs:136-160. Accepts complex shell syntax and returns the statically
// known words: enough to spot a dangerous command, never proof one is safe.
export function parseShellLcLiteralCommands(command: readonly string[]): string[][] | undefined {
  const extracted = extractBashCommand(command);
  if (extracted === undefined) return undefined;
  const tree = tryParseShell(extracted.script);
  if (tree === undefined) return undefined;
  // Every exit frees the tree: an undeleted one leaks its wasm allocation,
  // which nothing in web-tree-sitter 0.25.10 reclaims later.
  try {
    const root = tree.rootNode;
    if (root.hasError) return undefined;

    const commands: string[][] = [];
    const stack: Node[] = [root];
    while (stack.length > 0) {
      const node = stack.pop() as Node;
      if (node.type === "command") {
        const literal = parseLiteralCommandFromNode(node);
        if (literal !== undefined) commands.push(literal);
      }
      for (const child of node.namedChildren) if (child !== null) stack.push(child);
    }
    return commands;
  } finally {
    tree.delete();
  }
}

function parseLiteralCommandFromNode(command: Node): string[] | undefined {
  if (command.type !== "command") return undefined;
  const words: string[] = [];
  let foundCommandName = false;
  for (const child of command.namedChildren) {
    if (child === null) continue;
    if (child.type === "command_name") {
      const name = child.namedChild(0);
      if (name === null) return undefined;
      const parsed = parseLiteralShellWord(name);
      if (parsed === undefined) return undefined;
      words.push(parsed);
      foundCommandName = true;
      continue;
    }
    if (!foundCommandName) continue;
    const parsed = parseLiteralShellWord(child);
    if (parsed !== undefined) words.push(parsed);
  }
  return foundCommandName ? words : undefined;
}

function parseLiteralShellWord(node: Node): string | undefined {
  switch (node.type) {
    case "word":
    case "number":
      return isLiteralWordOrNumber(node) ? node.text : undefined;
    case "string":
      return parseDoubleQuotedString(node);
    case "raw_string":
      return parseRawString(node);
    case "concatenation": {
      let concatenated = "";
      for (const part of node.namedChildren) {
        if (part === null) return undefined;
        const parsed = parseLiteralShellWord(part);
        if (parsed === undefined) return undefined;
        concatenated += parsed;
      }
      return concatenated === "" ? undefined : concatenated;
    }
    default:
      return undefined;
  }
}

function isLiteralWordOrNumber(node: Node): boolean {
  if (node.type !== "word" && node.type !== "number") return false;
  if (node.namedChild(0) !== null) return false;
  const word = node.text;
  return !word.startsWith("=") && !expandingCharacters.some((character) => word.includes(character));
}

function parseDoubleQuotedString(node: Node): string | undefined {
  if (node.type !== "string") return undefined;
  for (const part of node.namedChildren) {
    if (part === null || part.type !== "string_content") return undefined;
  }
  const raw = node.text;
  if (raw.length < 2 || !raw.startsWith('"') || !raw.endsWith('"')) return undefined;
  const stripped = raw.slice(1, -1);
  // Double quotes suppress globbing, not escape removal: a word whose source
  // spelling escapes anything is not proof of the runtime argv (bash.rs:292).
  for (let index = 0; index + 1 < stripped.length; index += 1) {
    if (stripped[index] === "\\" && escapedInDoubleQuotes.includes(stripped[index + 1] as string)) return undefined;
  }
  return stripped;
}

function parseRawString(node: Node): string | undefined {
  if (node.type !== "raw_string") return undefined;
  const raw = node.text;
  if (raw.length < 2 || !raw.startsWith("'") || !raw.endsWith("'")) return undefined;
  return raw.slice(1, -1);
}

function dangerousCommandMatchWithDepth(
  command: readonly string[],
  wrapperDepth: number,
): DangerousCommandMatch | undefined {
  // Past the wrapper budget the classifier stops proving anything, so the
  // command is dangerous by that fact alone (is_dangerous_command.rs:54).
  if (wrapperDepth > maxDangerousCommandWrapperDepth) return "other";

  const direct = dangerousCommandMatchForExec(command, wrapperDepth);
  if (direct !== undefined) return direct;

  const literals = parseShellLcLiteralCommands(command);
  if (literals !== undefined) {
    for (const literal of literals) {
      const nested = dangerousCommandMatchWithDepth(literal, wrapperDepth + 1);
      if (nested !== undefined) return nested;
    }
  }
  return undefined;
}

function dangerousCommandMatchForExec(
  command: readonly string[],
  wrapperDepth: number,
): DangerousCommandMatch | undefined {
  const program = executableNameLookupKey(command[0]);
  switch (program) {
    case "rm":
      return rmArgumentsIncludeForceOption(command.slice(1)) ? "forced-rm" : undefined;
    case "sudo":
      return dangerousCommandMatchWithDepth(command.slice(1), wrapperDepth + 1);
    case "env":
      return dangerousCommandMatchForEnv(command, wrapperDepth);
    case "trap":
      return dangerousCommandMatchForTrap(command, wrapperDepth);
    default:
      return undefined;
  }
}

function executableNameLookupKey(raw: string | undefined): string | undefined {
  if (raw === undefined) return undefined;
  const name = raw.slice(raw.lastIndexOf("/") + 1);
  return name === "" ? undefined : name;
}

function dangerousCommandMatchForEnv(
  command: readonly string[],
  wrapperDepth: number,
): DangerousCommandMatch | undefined {
  let commandIndex = 1;
  while (commandIndex < command.length) {
    const argument = command[commandIndex] as string;
    if (argument === "--") {
      commandIndex += 1;
      break;
    }
    const assignment = argument.indexOf("=");
    const isAssignment = assignment > 0 && !argument.startsWith("-");
    if (argument === "-i" || argument === "--ignore-environment" || isAssignment) {
      commandIndex += 1;
      continue;
    }
    break;
  }
  return dangerousCommandMatchWithDepth(command.slice(commandIndex), wrapperDepth + 1);
}

function dangerousCommandMatchForTrap(
  command: readonly string[],
  wrapperDepth: number,
): DangerousCommandMatch | undefined {
  let actionIndex = 1;
  if (command[actionIndex] === "--") actionIndex += 1;
  const action = command[actionIndex];
  if (action === undefined || action.startsWith("-")) return undefined;
  return dangerousCommandMatchWithDepth(["sh", "-c", action], wrapperDepth + 1);
}

function rmArgumentsIncludeForceOption(args: readonly string[]): boolean {
  for (const argument of args) {
    if (argument === "--") return false;
    if (argument === "--force") return true;
    if (!argument.startsWith("-")) continue;
    const flags = argument.slice(1);
    if (!flags.startsWith("-") && flags.includes("f")) return true;
  }
  return false;
}

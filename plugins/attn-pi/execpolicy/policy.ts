// Rule compilation and matching. Port of codex-rs/execpolicy/src/policy.rs and
// rule.rs; the Starlark parser is replaced by the JSON rules attn ships.
import type { Decision, PatternToken, PrefixRule } from "./types";

export type CompiledRule = {
  rule: PrefixRule;
  first: string;
  rest: readonly PatternToken[];
  decision: Decision;
};

export type PrefixMatch = {
  rule: PrefixRule;
  matchedPrefix: string[];
  decision: Decision;
  resolvedProgram?: string;
};

// Allow < Prompt < Forbidden: the strictest decision across matches wins
// (policy.rs:402 takes the max).
const strictness: Record<Decision, number> = { allow: 0, prompt: 1, forbidden: 2 };

export function strictestDecision(decisions: readonly Decision[]): Decision {
  let strictest: Decision = "allow";
  for (const decision of decisions) if (strictness[decision] > strictness[strictest]) strictest = decision;
  return strictest;
}

export function isDecision(value: unknown): value is Decision {
  return value === "allow" || value === "prompt" || value === "forbidden";
}

// undefined for a decision outside the three: Codex rejects it at load
// (decision.rs Decision::parse), so such a rule must never match here either.
export function ruleDecision(rule: PrefixRule): Decision | undefined {
  if (rule.decision === undefined) return "allow";
  return isDecision(rule.decision) ? rule.decision : undefined;
}

// parser.rs:390-403. Only the first pattern token expands: one rule per
// alternative, all sharing the remaining tokens.
export function compileRule(rule: PrefixRule): CompiledRule[] {
  const decision = ruleDecision(rule);
  if (decision === undefined) return [];
  const [first, ...rest] = rule.pattern;
  if (first === undefined) return [];
  const heads = typeof first === "string" ? [first] : first;
  return heads.map((head) => ({ rule, first: head, rest, decision }));
}

export class CompiledPolicy {
  private readonly byProgram = new Map<string, CompiledRule[]>();

  constructor(rules: readonly PrefixRule[]) {
    for (const rule of rules) {
      for (const compiled of compileRule(rule)) {
        const existing = this.byProgram.get(compiled.first);
        if (existing === undefined) this.byProgram.set(compiled.first, [compiled]);
        else existing.push(compiled);
      }
    }
  }

  matchesForCommand(command: readonly string[]): PrefixMatch[] {
    const exact = this.matchExactRules(command);
    if (exact.length > 0) return exact;
    return this.matchHostExecutableRules(command);
  }

  private matchExactRules(command: readonly string[]): PrefixMatch[] {
    const program = command[0];
    if (program === undefined) return [];
    return matchAll(this.byProgram.get(program) ?? [], command);
  }

  // policy.rs:344-371. A path resolves to rules written for its bare name;
  // attn ships no host_executable allowlist, so every path resolves.
  private matchHostExecutableRules(command: readonly string[]): PrefixMatch[] {
    const program = command[0];
    if (program === undefined) return [];
    const name = executableName(program);
    if (name === undefined) return [];
    const rules = this.byProgram.get(name);
    if (rules === undefined) return [];
    const basenameCommand = [name, ...command.slice(1)];
    return matchAll(rules, basenameCommand).map((match) => ({ ...match, resolvedProgram: program }));
  }
}

// executable_name.rs:25-30 over absolutize.rs:26-44: any program spelled as a
// path resolves to the file name left after dropping `.` and applying `..`.
export function executableName(program: string): string | undefined {
  if (!program.includes("/")) return undefined;
  const components: string[] = [];
  for (const component of program.split("/")) {
    if (component === "" || component === ".") continue;
    // Codex absolutizes first, so a leading `..` pops a directory of the cwd
    // and leaves the file name reachable; only `/..` normalizes to no name.
    if (component === "..") {
      components.pop();
      continue;
    }
    components.push(component);
  }
  return components.at(-1);
}

function matchAll(rules: readonly CompiledRule[], command: readonly string[]): PrefixMatch[] {
  const matches: PrefixMatch[] = [];
  for (const compiled of rules) {
    const matchedPrefix = matchesPrefix(compiled, command);
    if (matchedPrefix !== undefined) {
      matches.push({ rule: compiled.rule, matchedPrefix, decision: compiled.decision });
    }
  }
  return matches;
}

function matchesPrefix(compiled: CompiledRule, command: readonly string[]): string[] | undefined {
  const patternLength = compiled.rest.length + 1;
  if (command.length < patternLength || command[0] !== compiled.first) return undefined;
  for (let index = 0; index < compiled.rest.length; index += 1) {
    if (!tokenMatches(compiled.rest[index] as PatternToken, command[index + 1] as string)) return undefined;
  }
  return command.slice(0, patternLength);
}

function tokenMatches(token: PatternToken, word: string): boolean {
  return typeof token === "string" ? token === word : token.includes(word);
}

// shlex 1.3 quoting (bytes.rs:206-229 and 344-441). A word no single strategy
// can carry is split into chunks, each quoted the strongest way it allows.
export function shlexJoin(words: readonly string[]): string {
  return words.map(shlexQuote).join(" ");
}

const unquotedStrategy = 1;
const singleQuotedStrategy = 2;
const doubleQuotedStrategy = 4;

function shlexQuote(word: string): string {
  if (word === "") return "''";
  const characters = [...word];
  let quoted = "";
  let index = 0;
  while (index < characters.length) {
    const { length, strategy } = quotingStrategy(characters, index);
    const chunk = characters.slice(index, index + length).join("");
    quoted += renderChunk(chunk, strategy);
    index += length;
  }
  return quoted;
}

// Returns how many characters from `start` one strategy can carry, and which.
function quotingStrategy(characters: readonly string[], start: number): { length: number; strategy: number } {
  let allowed = unquotedStrategy | singleQuotedStrategy | doubleQuotedStrategy;
  let index = start;
  // Bash only takes a literal `^` right after an opening single quote, so a
  // chunk that starts with one is single quoted or split (bytes.rs:365-370).
  if (characters[start] === "^") {
    allowed = singleQuotedStrategy;
    index = start + 1;
  }
  while (index < characters.length) {
    const character = characters[index] as string;
    let current = allowed;
    if (!unquotedOk(character)) current &= ~unquotedStrategy;
    if (!singleQuotedOk(character)) current &= ~singleQuotedStrategy;
    if (!doubleQuotedOk(character)) current &= ~doubleQuotedStrategy;
    if (current === 0) break;
    allowed = current;
    index += 1;
  }
  return { length: index - start, strategy: allowed };
}

function renderChunk(chunk: string, strategy: number): string {
  if (strategy & unquotedStrategy) return chunk;
  if (strategy & singleQuotedStrategy) return `'${chunk}'`;
  return `"${chunk.replace(/[\\"$`]/g, (character) => `\\${character}`)}"`;
}

function unquotedOk(character: string): boolean {
  return /[+\-./:@\]_0-9A-Za-z]/.test(character);
}

function singleQuotedOk(character: string): boolean {
  return character !== "'" && character !== "^" && character !== "\\";
}

// `$` and a backtick keep their meaning under python shlex's parser, and `!`
// and `^` are history and comparison operators in an interactive shell.
function doubleQuotedOk(character: string): boolean {
  return character !== "$" && character !== "`" && character !== "!" && character !== "^";
}

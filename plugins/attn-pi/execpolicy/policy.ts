import type { Decision, PatternToken, PrefixRule, RuleSandbox } from "./types";

export type CompiledRule = {
  rule: PrefixRule;
  first: string;
  rest: readonly PatternToken[];
  decision: Decision;
  sandbox: RuleSandbox;
};

export type PrefixMatch = {
  rule: PrefixRule;
  matchedPrefix: string[];
  decision: Decision;
  sandbox: RuleSandbox;
  resolvedProgram?: string;
};

const strictness: Record<Decision, number> = { allow: 0, prompt: 1, forbidden: 2 };

export function strictestDecision(decisions: readonly Decision[]): Decision {
  let strictest: Decision = "allow";
  for (const decision of decisions) if (strictness[decision] > strictness[strictest]) strictest = decision;
  return strictest;
}

export function isDecision(value: unknown): value is Decision {
  return value === "allow" || value === "prompt" || value === "forbidden";
}

export function ruleDecision(rule: PrefixRule): Decision | undefined {
  if (rule.decision === undefined) return "allow";
  return isDecision(rule.decision) ? rule.decision : undefined;
}

export function ruleSandbox(rule: PrefixRule, decision = ruleDecision(rule)): RuleSandbox | undefined {
  if (rule.sandbox === undefined) return decision === "allow" ? "bypass" : "inherit";
  if (rule.sandbox === "inherit" || rule.sandbox === "bypass") return rule.sandbox;
  return undefined;
}

export function compileRule(rule: PrefixRule): CompiledRule[] {
  const decision = ruleDecision(rule);
  const sandbox = ruleSandbox(rule, decision);
  if (decision === undefined || sandbox === undefined || (decision === "forbidden" && sandbox === "bypass")) return [];
  const [first, ...rest] = rule.pattern;
  if (first === undefined) return [];
  const heads = typeof first === "string" ? [first] : first;
  return heads.map((head) => ({ rule, first: head, rest, decision, sandbox }));
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
    const resolved = this.matchHostExecutableRules(command);
    if (exact.length === 0) return resolved;
    if (resolved.length === 0) return exact;
    return [...exact, ...resolved.filter((candidate) => !exact.some((match) => match.rule === candidate.rule))];
  }

  private matchExactRules(command: readonly string[]): PrefixMatch[] {
    const program = command[0];
    if (program === undefined) return [];
    return matchAll(this.byProgram.get(program) ?? [], command);
  }

  // policy.rs:344-371 resolves a path to rules written for its bare name.
  // attn keeps both matches so an exact project grant cannot hide a shipped denial.
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
      matches.push({ rule: compiled.rule, matchedPrefix, decision: compiled.decision, sandbox: compiled.sandbox });
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

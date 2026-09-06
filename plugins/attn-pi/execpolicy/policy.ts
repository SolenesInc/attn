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

export function ruleDecision(rule: PrefixRule): Decision {
  return rule.decision ?? "allow";
}

// parser.rs:390-403. Only the first pattern token expands: one rule per
// alternative, all sharing the remaining tokens.
export function compileRule(rule: PrefixRule): CompiledRule[] {
  const [first, ...rest] = rule.pattern;
  if (first === undefined) return [];
  const heads = typeof first === "string" ? [first] : first;
  return heads.map((head) => ({ rule, first: head, rest, decision: ruleDecision(rule) }));
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

  // policy.rs:344-371. An absolute path resolves to rules written for its bare
  // name; attn ships no host_executable allowlist, so every path resolves.
  private matchHostExecutableRules(command: readonly string[]): PrefixMatch[] {
    const program = command[0];
    if (program === undefined || !program.startsWith("/")) return [];
    const name = program.slice(program.lastIndexOf("/") + 1);
    if (name === "") return [];
    const rules = this.byProgram.get(name);
    if (rules === undefined) return [];
    const basenameCommand = [name, ...command.slice(1)];
    return matchAll(rules, basenameCommand).map((match) => ({ ...match, resolvedProgram: program }));
  }
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

// shlex 1.3 quoting, whole word at a time: bare when every character is safe,
// single quotes when they can carry it, double quotes otherwise.
export function shlexJoin(words: readonly string[]): string {
  return words.map(shlexQuote).join(" ");
}

function shlexQuote(word: string): string {
  if (word === "") return "''";
  if ([...word].every(unquotedOk)) return word;
  if ([...word].every(singleQuotedOk)) return `'${word}'`;
  return `"${word.replace(/[\\"$`]/g, (character) => `\\${character}`)}"`;
}

function unquotedOk(character: string): boolean {
  return /[+\-./:@\]_0-9A-Za-z]/.test(character);
}

function singleQuotedOk(character: string): boolean {
  return character !== "'" && character !== "^" && character !== "\\";
}

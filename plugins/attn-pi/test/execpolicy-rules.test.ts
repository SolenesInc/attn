// The corpus of codex-rs/execpolicy/tests/basic.rs and the inline tests of
// parser.rs and amend.rs. Rules reach attn as JSON rather than Starlark, so a
// policy is a rule array here; network_rule and host_executable are not ported
// (hosts belong to the proxy, and attn ships no host executable allowlist).
import { beforeAll, describe, expect, test } from "bun:test";
import { amendRules, convertLegacyGlob, evaluateCommand, validateRules, type EvaluationInput, type PrefixRule } from "../execpolicy/index";
import { initShellParsing } from "../shell/index";

beforeAll(async () => {
  await initShellParsing();
});

// Codex's `allow_all` heuristics fallback: an unmatched command is allowed and a
// prompt rule still prompts. Approval policy Never turns prompts into
// rejections, which the decision corpus covers separately.
function allowUnmatched(rules: readonly PrefixRule[]): EvaluationInput {
  return { rules, approvalPolicy: "on-request", sandboxMode: "workspace-write", sandboxPermissions: "use_default" };
}

// Codex's `prompt_all` heuristics fallback: an unmatched command prompts.
function promptUnmatched(rules: readonly PrefixRule[]): EvaluationInput {
  return { rules, approvalPolicy: "untrusted", sandboxMode: "workspace-write", sandboxPermissions: "use_default" };
}

describe("prefix rule matching", () => {
  test("basic match", () => {
    const rules: PrefixRule[] = [{ pattern: ["git", "status"] }];
    const evaluation = evaluateCommand("git status", allowUnmatched(rules));
    expect(evaluation.decision).toBe("allow");
    expect(evaluation.matches).toEqual([{ rule: rules[0] as PrefixRule, command: ["git", "status"], decision: "allow" }]);
  });

  test("justification is attached to forbidden matches", () => {
    const rules: PrefixRule[] = [{ pattern: ["rm"], decision: "forbidden", justification: "destructive command" }];
    const evaluation = evaluateCommand("rm -rf /some/important/folder", allowUnmatched(rules));
    expect(evaluation.decision).toBe("forbidden");
    expect(evaluation.matches).toEqual([{ rule: rules[0] as PrefixRule, command: ["rm"], decision: "forbidden" }]);
    // The command reaches the policy as the argv attn will run.
    expect(evaluation.reason).toBe("`bash -lc 'rm -rf /some/important/folder'` rejected: destructive command");
  });

  test("justification can be used with an allow decision", () => {
    const rules: PrefixRule[] = [{ pattern: ["ls"], decision: "allow", justification: "safe and commonly used" }];
    const evaluation = evaluateCommand("ls -l", promptUnmatched(rules));
    expect(evaluation.decision).toBe("allow");
    expect(evaluation.matches).toEqual([{ rule: rules[0] as PrefixRule, command: ["ls"], decision: "allow" }]);
  });

  test("only the first token alias expands to multiple rules", () => {
    const rules: PrefixRule[] = [{ pattern: [["bash", "sh"], ["-c", "-l"]] }];
    const bash = evaluateCommand("bash -c echo hi", allowUnmatched(rules));
    expect(bash.decision).toBe("allow");
    expect(bash.matches.map((match) => match.command)).toEqual([["bash", "-c"]]);

    const sh = evaluateCommand("sh -l echo hi", allowUnmatched(rules));
    expect(sh.decision).toBe("allow");
    expect(sh.matches.map((match) => match.command)).toEqual([["sh", "-l"]]);
  });

  test("tail aliases are not cartesian expanded", () => {
    const rules: PrefixRule[] = [{ pattern: ["npm", ["i", "install"], ["--legacy-peer-deps", "--no-save"]] }];
    expect(evaluateCommand("npm i --legacy-peer-deps", allowUnmatched(rules)).matches.map((match) => match.command)).toEqual([
      ["npm", "i", "--legacy-peer-deps"],
    ]);
    expect(
      evaluateCommand("npm install --no-save leftpad", allowUnmatched(rules)).matches.map((match) => match.command),
    ).toEqual([["npm", "install", "--no-save"]]);
  });

  test("the strictest decision wins across matches", () => {
    const rules: PrefixRule[] = [
      { pattern: ["git"], decision: "prompt" },
      { pattern: ["git", "commit"], decision: "forbidden" },
    ];
    const evaluation = evaluateCommand("git commit -m hi", allowUnmatched(rules));
    expect(evaluation.decision).toBe("forbidden");
    expect(evaluation.matches.map((match) => [match.command, match.decision])).toEqual([
      [["git"], "prompt"],
      [["git", "commit"], "forbidden"],
    ]);
  });

  test("the strictest decision wins across multiple commands", () => {
    const rules: PrefixRule[] = [
      { pattern: ["git"], decision: "prompt" },
      { pattern: ["git", "commit"], decision: "forbidden" },
    ];
    const evaluation = evaluateCommand("git status && git commit -m hi", allowUnmatched(rules));
    expect(evaluation.decision).toBe("forbidden");
    expect(evaluation.matches.map((match) => [match.command, match.decision])).toEqual([
      [["git"], "prompt"],
      [["git"], "prompt"],
      [["git", "commit"], "forbidden"],
    ]);
  });

  test("a heuristics decision is returned when no policy matches", () => {
    const evaluation = evaluateCommand("python", promptUnmatched([]));
    expect(evaluation.decision).toBe("prompt");
    expect(evaluation.matches).toEqual([]);
    expect(evaluation.reason).toBeUndefined();
  });

  test("an absolute path resolves to the rule written for its bare name", () => {
    const rules: PrefixRule[] = [{ pattern: ["git"], decision: "prompt" }];
    const evaluation = evaluateCommand("/usr/bin/git status", allowUnmatched(rules));
    expect(evaluation.decision).toBe("prompt");
    expect(evaluation.matches.map((match) => match.command)).toEqual([["git"]]);
  });

  test("name resolution does not override an exact match", () => {
    const rules: PrefixRule[] = [
      { pattern: ["/usr/bin/git"], decision: "allow" },
      { pattern: ["git"], decision: "prompt" },
    ];
    const evaluation = evaluateCommand("/usr/bin/git status", allowUnmatched(rules));
    expect(evaluation.decision).toBe("allow");
    expect(evaluation.matches.map((match) => match.command)).toEqual([["/usr/bin/git"]]);
  });
});

describe("rule validation", () => {
  test("match and not_match examples are enforced", () => {
    const rules: PrefixRule[] = [
      {
        pattern: ["git", "status"],
        match: [["git", "status"]],
        not_match: [["git", "--config", "color.status=always", "status"]],
      },
    ];
    expect(validateRules(rules)).toEqual([]);

    const evaluation = evaluateCommand("git --config color.status=always status", allowUnmatched(rules));
    expect(evaluation.decision).toBe("allow");
    expect(evaluation.matches).toEqual([]);
  });

  test("a match example that no rule matches is an error", () => {
    const errors = validateRules([{ pattern: ["git", "status"], match: [["git", "branch"]] }]);
    expect(errors).toEqual([{ index: 0, message: "expected every example to match rule `git status`: git branch" }]);
  });

  test("a not_match example that the rule matches is an error", () => {
    const errors = validateRules([{ pattern: ["git"], not_match: [["git", "status"]] }]);
    expect(errors).toEqual([{ index: 0, message: "expected example to not match rule `git`: git status" }]);
  });

  test("alternatives are rendered in the error message", () => {
    const errors = validateRules([{ pattern: ["npm", ["i", "install"]], match: [["npm", "run", "build"]] }]);
    expect(errors).toEqual([
      { index: 0, message: "expected every example to match rule `npm [i|install]`: npm run build" },
    ]);
  });

  test("justification cannot be empty", () => {
    expect(validateRules([{ pattern: ["ls"], decision: "prompt", justification: "   " }])).toEqual([
      { index: 0, message: "invalid rule: justification cannot be empty" },
    ]);
  });

  test("an invalid decision, pattern or example is reported with its rule index", () => {
    const rules = [
      { pattern: ["ls"] },
      { pattern: [], decision: "allow" },
      { pattern: ["ls"], decision: "maybe" },
      { pattern: ["ls", []] },
      { pattern: ["ls"], match: [[]] },
    ] as unknown as PrefixRule[];
    expect(validateRules(rules)).toEqual([
      { index: 1, message: "invalid pattern element: pattern cannot be empty" },
      { index: 2, message: "invalid decision: maybe" },
      { index: 3, message: "invalid pattern element: pattern alternatives cannot be empty" },
      { index: 4, message: "invalid example: example cannot be an empty list" },
    ]);
  });
});

describe("amendments", () => {
  test("an amendment extends the policy", () => {
    const amended = amendRules([], ["ls", "-l"], "prompt");
    expect(amended).toEqual([{ pattern: ["ls", "-l"], decision: "prompt" }]);

    const evaluation = evaluateCommand("ls -l /some/important/folder", allowUnmatched(amended));
    expect(evaluation.decision).toBe("prompt");
    expect(evaluation.matches.map((match) => match.command)).toEqual([["ls", "-l"]]);
    expect(evaluation.reason).toBe("`bash -lc 'ls -l /some/important/folder'` requires approval by policy");
  });

  test("an amendment defaults to allow and dedupes an existing rule", () => {
    const rules = amendRules([], ["echo", "Hello, world!"]);
    expect(rules).toEqual([{ pattern: ["echo", "Hello, world!"], decision: "allow" }]);
    expect(amendRules(rules, ["echo", "Hello, world!"])).toEqual(rules);
    expect(amendRules(rules, ["echo", "Hello, world!"], "forbidden")).toHaveLength(2);
  });

  test("an amendment rejects an empty prefix", () => {
    expect(() => amendRules([], [])).toThrow("execpolicy amendment needs at least one prefix token");
  });
});

describe("legacy glob conversion", () => {
  test("a trailing wildcard becomes a prefix rule", () => {
    expect(convertLegacyGlob("git push*", "allow")).toEqual({ pattern: ["git", "push"], decision: "allow" });
    expect(convertLegacyGlob("git push *", "forbidden")).toEqual({ pattern: ["git", "push"], decision: "forbidden" });
    expect(convertLegacyGlob("  npm run build  ", "allow")).toEqual({
      pattern: ["npm", "run", "build"],
      decision: "allow",
    });
  });

  test("a leading or mid-token wildcard has no prefix rule", () => {
    expect(convertLegacyGlob("*curl*", "forbidden")).toBeUndefined();
    expect(convertLegacyGlob("git*push", "allow")).toBeUndefined();
    expect(convertLegacyGlob("git push* origin", "allow")).toBeUndefined();
    expect(convertLegacyGlob("git pus?", "allow")).toBeUndefined();
    expect(convertLegacyGlob("*", "allow")).toBeUndefined();
    expect(convertLegacyGlob("   ", "allow")).toBeUndefined();
  });
});

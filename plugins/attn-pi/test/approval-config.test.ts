import { describe, expect, test } from "bun:test";
import {
  ApprovalConfigError,
  defaultApprovalConfig,
  loadApprovalConfig,
  ruleDescription,
} from "../approval/config";

// The JSON in this file is what internal/automode.Config marshals; the shape test at the
// bottom is the pair to TestConfigMarshalsIntoThePiSideShape.
const daemonConfig = {
  enabled_default: true,
  approval_policy: "never",
  sandbox_mode: "danger-full-access",
  rules: [
    { pattern: ["git", "status"], decision: "allow" },
    {
      pattern: ["git", ["push", "pull"]],
      decision: "prompt",
      justification: "it leaves the machine",
      match: [["git", "push", "origin", "main"]],
      not_match: [["git", "status"]],
    },
    {
      pattern: ["attn", "automode", "env"],
      decision: "forbidden",
      justification: "the environment is what the reviewer reads",
    },
  ],
  network: {
    enabled: true,
    allowed_domains: ["crates.io", " github.com "],
    denied_domains: ["localhost:29849"],
  },
  environment: { slots: { domains: ["grafana.acme.corp"] }, notes: ["this laptop is mine"] },
  legacy_patterns: ["git status*", "*curl*"],
};

describe("loadApprovalConfig", () => {
  test("reads what the daemon writes", () => {
    const config = loadApprovalConfig(daemonConfig);
    expect(config.enabledDefault).toBe(true);
    expect(config.approvalPolicy).toBe("never");
    expect(config.sandboxMode).toBe("danger-full-access");
    expect(config.rules.map(ruleDescription)).toEqual([
      "git status",
      "git {push|pull}",
      "attn automode env",
    ]);
    expect(config.rules[0].decision).toBe("allow");
    expect(config.network.allowedDomains).toEqual(["crates.io", "github.com"]);
    expect(config.network.deniedDomains).toEqual(["localhost:29849"]);
    expect(config.environment.slots.domains).toEqual(["grafana.acme.corp"]);
    expect(config.environment.notes).toEqual(["this laptop is mine"]);
    expect(config.legacyPatterns).toEqual(["git status*", "*curl*"]);
  });

  test("carries the examples pi validates, untouched", () => {
    const rule = loadApprovalConfig(daemonConfig).rules[1];
    expect(rule.match).toEqual([["git", "push", "origin", "main"]]);
    expect(rule.notMatch).toEqual([["git", "status"]]);
    expect(rule.justification).toBe("it leaves the machine");
  });

  test("an absent config is the shipped default, not an empty one", () => {
    expect(loadApprovalConfig(undefined)).toEqual(defaultApprovalConfig);
    const empty = loadApprovalConfig({});
    expect(empty.approvalPolicy).toBe("on-request");
    expect(empty.sandboxMode).toBe("workspace-write");
    expect(empty.network.enabled).toBe(true);
    expect(empty.rules).toEqual([]);
    expect(empty.enabledDefault).toBe(true);
  });

  test("a rule with no decision allows, which is what the daemon omits", () => {
    const config = loadApprovalConfig({ rules: [{ pattern: ["ls"] }] });
    expect(config.rules[0].decision).toBe("allow");
    expect(config.rules[0].justification).toBe("");
    expect(config.rules[0].match).toEqual([]);
  });

  test("network off is carried, not defaulted back on", () => {
    const config = loadApprovalConfig({ network: { enabled: false } });
    expect(config.network.enabled).toBe(false);
    expect(config.network.allowedDomains).toEqual([]);
  });

  test("names the field it cannot read", () => {
    const cases: [unknown, string][] = [
      [{ rules: [{ pattern: [] }] }, "rules[0].pattern"],
      [{ rules: [{ pattern: ["git push"] }] }, "rules[0].pattern[0]"],
      [{ rules: [{ pattern: ["rm"], decision: "forbidden" }] }, "rules[0].justification"],
      [{ rules: [{ pattern: ["rm"], decision: "maybe" }] }, "rules[0].decision"],
      [{ approval_policy: "yolo" }, "approval_policy"],
      [{ sandbox_mode: "open" }, "sandbox_mode"],
      [{ network: { allowed_domains: "crates.io" } }, "network.allowed_domains"],
      [{ enabled_default: "true" }, "enabled_default"],
      [{ environment: [] }, "environment"],
    ];
    for (const [raw, field] of cases) {
      let thrown: unknown;
      try {
        loadApprovalConfig(raw as never);
      } catch (error) {
        thrown = error;
      }
      expect(thrown).toBeInstanceOf(ApprovalConfigError);
      expect((thrown as ApprovalConfigError).field).toBe(field);
    }
  });

  test("a shell line in a pattern says what a pattern is", () => {
    expect(() => loadApprovalConfig({ rules: [{ pattern: ["git push"] }] })).toThrow(
      /one command token per entry/,
    );
  });

  test("an unknown approval policy names the choices", () => {
    expect(() => loadApprovalConfig({ approval_policy: "yolo" })).toThrow(
      /untrusted, on-request, never/,
    );
  });
});

import { describe, expect, test } from "bun:test";
import { readFileSync } from "node:fs";
import { pullRequestsCreated } from "../src/pullrequest";

type Case = {
  name: string;
  tool_name: string;
  tool_input: unknown;
  tool_output: unknown;
  want: string[];
};

const corpus = JSON.parse(
  readFileSync(new URL("../../../internal/hooks/testdata/pull-request-extraction.json", import.meta.url), "utf8"),
) as Case[];

describe("pullRequestsCreated: the corpus internal/hooks runs", () => {
  expect(corpus.length).toBeGreaterThan(0);

  for (const entry of corpus) {
    test(entry.name, () => {
      const output = typeof entry.tool_output === "string" ? entry.tool_output : JSON.stringify(entry.tool_output);
      expect(pullRequestsCreated(entry.tool_name, entry.tool_input, output)).toEqual(entry.want);
    });
  }
});

describe("pullRequestsCreated: shapes only the pi driver sees", () => {
  test("a pi bash result whose text arrives in several blocks", () => {
    const output = ["Creating pull request for pr-pi-driver into next", "https://github.com/victorarias/attn/pull/90"].join("\n");
    expect(pullRequestsCreated("bash", { command: "gh pr create --fill" }, output)).toEqual([
      "https://github.com/victorarias/attn/pull/90",
    ]);
  });

  test("a repeated call does not carry state between matches", () => {
    const input = { command: "gh pr create --fill" };
    const output = "https://github.com/victorarias/attn/pull/90";
    expect(pullRequestsCreated("bash", input, output)).toEqual([output]);
    expect(pullRequestsCreated("bash", input, output)).toEqual([output]);
  });

  test("a tool input that is not an object reports nothing", () => {
    expect(pullRequestsCreated("bash", "gh pr create", "https://github.com/a/b/pull/1")).toEqual([]);
    expect(pullRequestsCreated("bash", undefined, "https://github.com/a/b/pull/1")).toEqual([]);
  });
});

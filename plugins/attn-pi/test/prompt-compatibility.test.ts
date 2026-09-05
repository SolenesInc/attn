import { expect, test } from "bun:test";
import fixtures from "./testdata/prompts.json";
import {
  classifierSystemPrompt,
  classifierUserPrompt,
  grantPrompt,
  type PromptInput,
} from "../automode/prompt";
import { denialToolResult, type Denial } from "../automode/denial";
import { renderEnvironment, type Environment } from "../automode/environment";

for (const [index, fixture] of fixtures.entries()) {
  test(`preserves original ${fixture.kind} prompt ${index}`, () => {
    let actual: string;
    switch (fixture.kind) {
      case "system":
        actual = classifierSystemPrompt(fixture.input as Environment);
        break;
      case "environment":
        actual = renderEnvironment(fixture.input as Environment);
        break;
      case "grant":
        actual = grantPrompt(fixture.input as string);
        break;
      case "harm":
      case "intent":
        actual = classifierUserPrompt(
          fixture.input as PromptInput,
          fixture.kind,
        );
        break;
      case "denial":
        actual = denialToolResult(fixture.input as Denial);
        break;
      default:
        throw new Error(`Unknown fixture kind: ${fixture.kind}`);
    }
    expect(actual).toBe(fixture.expected);
  });
}

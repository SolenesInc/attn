import { execFileSync } from "node:child_process";
import { expect, test } from "vitest";

const listedFiles = (redFiles) =>
  execFileSync("pnpm", ["exec", "vitest", "list", "--filesOnly", "."], {
    encoding: "utf8",
    env: { ...process.env, ATTN_VITEST_FILES_RED_ON_MAIN: redFiles },
  });

test("a file red on main leaves the whole suite in both Vitest projects", () => {
  const shared = "src/components/palette/pathMode.test.ts";
  const isolated = "src/App.desktops.test.tsx";
  expect(listedFiles("")).toContain(shared);
  expect(listedFiles("")).toContain(isolated);

  const listed = listedFiles(`${shared} ${isolated}`);
  expect(listed).not.toContain(shared);
  expect(listed).not.toContain(isolated);
  expect(listed).toContain("src/App.newSession.test.tsx");
}, 60_000);

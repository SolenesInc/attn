import { RuleTester } from "oxlint/plugins-dev";

import { commentBlockMaxLinesRule } from "./comment-block-max-lines.ts";

const tester = new RuleTester({ languageOptions: { parserOptions: { lang: "ts" } } });
const error = { messageId: "tooLong" };

tester.run("attn/comment-block-max-lines", commentBlockMaxLinesRule, {
  valid: [
    "// one\nconst a = 1;",
    "// one\n// two\nconst a = 1;",
    "/* one\n   two */\nconst a = 1;",
    "/** one\n * two */\nconst a = 1;",
    "const a = 1; // trailing\n// next\n// line\nconst b = 2;",
    "// one\n// two\n\n// three\n// four\nconst a = 1;",
    "// @vitest-environment node\n// note\n// @ts-expect-error -- transitive peer\nimport { x } from 'node:fs';",
    "// note one\n// note two\n// eslint-disable-next-line no-console\nconsole.log(1);",
    "/// <reference types=\"vite/client\" />\n// a\n// b\nconst a = 1;",
  ],
  invalid: [
    { code: "// one\n// two\n// three\nconst a = 1;", errors: [error] },
    { code: "/* one\n   two\n   three */\nconst a = 1;", errors: [error] },
    { code: "/** one\n * two\n * three\n */\nconst a = 1;", errors: [error] },
    { code: "function f() {\n  // a\n  // b\n  // c\n  return 1;\n}", errors: [error] },
    { code: "// a\n/* b */\n// c\nconst a = 1;", errors: [error] },
  ],
});

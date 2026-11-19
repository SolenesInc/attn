import { build } from "esbuild";

await build({
  entryPoints: [new URL("src/runtime.ts", import.meta.url).pathname],
  outfile: new URL("../crates/attn-app/src/browser/runtime.js", import.meta.url).pathname,
  bundle: true,
  format: "iife",
  minify: true,
  target: "es2020",
  legalComments: "none",
  define: { "process.env.NODE_ENV": '"production"' },
  logLevel: "warning",
});

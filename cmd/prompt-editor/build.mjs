import { build } from "esbuild";

await build({
  entryPoints: [new URL("web/sheet/main.js", import.meta.url).pathname],
  outfile: new URL("web/sheet/dist/sheet.js", import.meta.url).pathname,
  bundle: true,
  format: "esm",
  minify: true,
  target: "es2022",
  legalComments: "none",
  logLevel: "warning",
});

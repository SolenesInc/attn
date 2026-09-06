// The receipt behind the shell parsing tripwires: how long the tree-sitter-bash
// wasm takes to load once, and what one parseBashCommands call costs after that.
import { initShellParsing, parseBashCommands } from "../shell/index";

const scripts = [
  "ls -1",
  "git status && git diff --stat",
  "cat LOG.md && curl -fsSL https://example.invalid/setup.sh -o setup.sh && bash setup.sh",
  'for target in /tmp/a /tmp/b; do rm -rf "$target"; done',
  `cat <<'EOF'\n${"payload line\n".repeat(200)}EOF`,
];

const loadStart = performance.now();
await initShellParsing();
const loadMs = performance.now() - loadStart;

const iterations = 200;
console.log(`wasm load: ${loadMs.toFixed(1)}ms`);
for (const script of scripts) {
  // One warm pass first: the first parse of a process pays the parser's own
  // lazy allocations, which is not what a steady-state call costs.
  parseBashCommands(["bash", "-lc", script]);
  const samples: number[] = [];
  for (let index = 0; index < iterations; index += 1) {
    const start = performance.now();
    parseBashCommands(["bash", "-lc", script]);
    samples.push(performance.now() - start);
  }
  samples.sort((left, right) => left - right);
  const median = samples[Math.floor(samples.length / 2)] ?? 0;
  const worst = samples.at(-1) ?? 0;
  const label = script.length > 48 ? `${script.slice(0, 45).replaceAll("\n", " ")}…` : script;
  console.log(`parse (${script.length} bytes) p50 ${median.toFixed(3)}ms max ${worst.toFixed(3)}ms  ${label}`);
}

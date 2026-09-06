// The receipt behind the shell parsing tripwires: what the wasm costs to load
// once, what a parse costs after that, and whether a long run gives memory back.
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

// A tree nobody deletes leaks wasm heap no GC reclaims. The first round only
// climbs to the allocator's high-water mark; a leak shows in the rounds after.
const memoryRuns = 10_000;
const megabytes = (bytes: number) => (bytes / 1024 / 1024).toFixed(1);
for (let round = 0; round < 3; round += 1) {
  Bun.gc(true);
  const before = process.memoryUsage();
  for (let index = 0; index < memoryRuns; index += 1) {
    parseBashCommands(["bash", "-lc", scripts[index % scripts.length] as string]);
  }
  Bun.gc(true);
  const after = process.memoryUsage();
  console.log(
    `memory round ${round} (${memoryRuns} parses): rss ${megabytes(before.rss)} -> ${megabytes(after.rss)} MB ` +
      `(${((after.rss - before.rss) / memoryRuns).toFixed(0)} bytes per call), ` +
      `heap ${megabytes(before.heapUsed)} -> ${megabytes(after.heapUsed)} MB`,
  );
}

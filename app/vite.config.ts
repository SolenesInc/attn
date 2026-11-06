/// <reference types="vitest" />
import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import { execFileSync } from "child_process";
import { globSync, readFileSync } from "fs";
import { homedir } from "os";
import { resolve } from "path";

// The terminal-snapshot wire format this bundle decodes, from the same script the Makefile
// feeds into buildinfo.SnapshotFormat. Failing to derive it fails the build.
const snapshotFormat = execFileSync(
  "bash",
  [resolve(__dirname, "../scripts/snapshot-format.sh")],
  { encoding: "utf8" },
).trim();

const appVersion: string = JSON.parse(readFileSync(resolve(__dirname, "package.json"), "utf8")).version;

// @ts-expect-error process is a nodejs global
const host = process.env.TAURI_DEV_HOST;

// Serve only: `dev:vite` cannot read the instance's client-token file, and a built bundle
// must never carry it — the same bundle ships everywhere and the token is per-instance.
function clientTokenFromInstance(): string {
  // @ts-expect-error process is a nodejs global
  const env = process.env as Record<string, string | undefined>;
  const explicit = (env.VITE_CLIENT_TOKEN ?? env.ATTN_CLIENT_TOKEN ?? "").trim();
  if (explicit) return explicit;
  const instance = (env.ATTN_INSTANCE ?? "").trim();
  const dataDir =
    (env.ATTN_DATA_DIR ?? "").trim() ||
    resolve(homedir(), instance ? `.attn-${instance}` : ".attn");
  try {
    return readFileSync(resolve(dataDir, "client-token"), "utf8").trim();
  } catch {
    return "";
  }
}

const testFilePatterns = [
  "src/**/*.test.{ts,tsx}",
  // Plain-JS tests, for guards that must read source files off disk: the app tsconfig has no
  // node types, and vitest stubs CSS imports to empty.
  "src/**/*.test.mjs",
  "scripts/real-app-harness/**/*.test.{ts,mjs}",
  "lint/**/*.test.ts",
];

const replacesModules = /vi\.(mock|doMock|hoisted|resetModules)\(/;
const filesRedOnMain = (process.env.ATTN_VITEST_FILES_RED_ON_MAIN ?? "").split(" ").filter(Boolean);

const filesNeedingOwnModules = globSync(testFilePatterns, { cwd: __dirname }).filter((file) =>
  replacesModules.test(readFileSync(resolve(__dirname, file), "utf8")),
);

// https://vite.dev/config/
export default defineConfig(async ({ command }) => ({
  plugins: [react()],
  define: {
    __ATTN_SNAPSHOT_FORMAT__: JSON.stringify(snapshotFormat),
    __ATTN_APP_VERSION__: JSON.stringify(appVersion),
    ...(command === "serve"
      ? { "import.meta.env.VITE_CLIENT_TOKEN": JSON.stringify(clientTokenFromInstance()) }
      : {}),
  },
  // Multi-page app configuration for test harness
  build: {
    rollupOptions: {
      input: {
        main: resolve(__dirname, "index.html"),
        "test-harness": resolve(__dirname, "test-harness/index.html"),
      },
    },
  },

  // Vite options for `tauri dev` / `tauri build`.
  // 1. prevent Vite from obscuring rust errors
  clearScreen: false,
  // 2. tauri expects a fixed port, fail if that port is not available
  server: {
    port: 1420,
    strictPort: true,
    host: host || false,
    hmr: host
      ? {
          protocol: "ws",
          host,
          port: 1421,
        }
      : undefined,
    watch: {
      // 3. tell Vite to ignore watching `src-tauri`
      ignored: ["**/src-tauri/**"],
    },
  },

  // Vitest configuration
  test: {
    globals: true,
    environment: "happy-dom",
    pool: "threads",
    env: { VITE_ATTN_BUILD_INSTANCE: "", VITE_CLIENT_TOKEN: "" },
    globalSetup: ["./scripts/vitest-ghostty-vt.mjs"],
    setupFiles: ["./src/test/setup.ts"],
    ...(process.env.ATTN_TEST_RESULTS && {
      reporters: [
        "default",
        ...(process.env.GITHUB_ACTIONS === "true" ? ["github-actions"] : []),
        "./scripts/vitest-results-reporter.mjs",
      ],
    }),
    projects: [
      {
        extends: true,
        test: {
          name: "shared",
          sequence: { groupOrder: 1 },
          isolate: false,
          include: testFilePatterns,
          exclude: [...filesNeedingOwnModules, ...filesRedOnMain],
        },
      },
      {
        extends: true,
        test: {
          name: "isolated",
          sequence: { groupOrder: 0 },
          include: filesNeedingOwnModules,
          exclude: filesRedOnMain,
        },
      },
    ],
  },
}));

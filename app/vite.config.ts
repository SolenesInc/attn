/// <reference types="vitest" />
import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import { execFileSync } from "child_process";
import { readFileSync } from "fs";
import { homedir } from "os";
import { resolve } from "path";

// The terminal-snapshot wire format this bundle decodes, from the same script the Makefile
// feeds into buildinfo.SnapshotFormat. Failing to derive it fails the build.
const snapshotFormat = execFileSync(
  "bash",
  [resolve(__dirname, "../scripts/snapshot-format.sh")],
  { encoding: "utf8" },
).trim();

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

// https://vite.dev/config/
export default defineConfig(async ({ command }) => ({
  plugins: [react()],
  define: {
    __ATTN_SNAPSHOT_FORMAT__: JSON.stringify(snapshotFormat),
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
    env: { VITE_ATTN_BUILD_INSTANCE: "", VITE_CLIENT_TOKEN: "" },
    setupFiles: ["./src/test/setup.ts"],
    include: [
      "src/**/*.test.{ts,tsx}",
      // Plain-JS tests, for guards that must read source files off disk: the app tsconfig has no
      // node types, and vitest stubs CSS imports to empty.
      "src/**/*.test.mjs",
      "scripts/real-app-harness/**/*.test.{ts,mjs}",
      "lint/**/*.test.ts",
    ],
    environmentMatchGlobs: [
      ["scripts/real-app-harness/**/*.test.{ts,mjs}", "node"],
      ["lint/**/*.test.ts", "node"],
    ],
  },
}));

import { fileURLToPath } from 'node:url'
import { defineConfig } from 'vitest/config'

const appSrc = fileURLToPath(new URL('../app/src', import.meta.url))

export default defineConfig({
  resolve: { alias: { '@attn': appSrc }, dedupe: ['mermaid'] },
  server: { host: '0.0.0.0', port: 5180, fs: { allow: ['..'] } },
  build: { target: 'es2022', sourcemap: false, reportCompressedSize: true },
  test: { environment: 'node', setupFiles: ['src/test/setup.ts'] },
})

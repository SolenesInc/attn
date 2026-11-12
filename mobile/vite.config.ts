import { defineConfig } from 'vitest/config'

export default defineConfig({
  server: { host: '0.0.0.0', port: 5180 },
  build: { target: 'es2022', sourcemap: false, reportCompressedSize: true },
  test: { environment: 'node', setupFiles: ['src/test/setup.ts'] },
})

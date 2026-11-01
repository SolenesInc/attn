import { fileURLToPath } from 'node:url'
import { defineConfig } from 'vite'

const appSrc = fileURLToPath(new URL('../app/src', import.meta.url))

export default defineConfig({
  resolve: { alias: { '@attn': appSrc } },
  server: { host: '0.0.0.0', port: 5179, fs: { allow: ['..'] } },
  build: { target: 'es2022', sourcemap: false, reportCompressedSize: true },
  define: {
    __ATTN_WS_URL__: JSON.stringify(process.env.ATTN_SPIKE_WS_URL ?? ''),
    __ATTN_CLIENT_TOKEN__: JSON.stringify(process.env.ATTN_SPIKE_CLIENT_TOKEN ?? ''),
  },
})

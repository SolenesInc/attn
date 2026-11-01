import type { CapacitorConfig } from '@capacitor/cli'
import { KeyboardResize } from '@capacitor/keyboard'

const resize = (process.env.ATTN_SPIKE_KB_RESIZE ?? 'native') as KeyboardResize
// The daemon accepts a WebSocket origin whose host equals the request host. On a device the app reaches the
// daemon by the Mac's tailnet address, so the page origin must carry that host: capacitor://<that address>.
const hostname = process.env.ATTN_SPIKE_HOSTNAME

const config: CapacitorConfig = {
  appId: 'dev.attn.phonespike',
  appName: 'attn spike',
  webDir: 'dist',
  server: { androidScheme: 'http', cleartext: true, ...(hostname ? { hostname } : {}) },
  plugins: {
    Keyboard: { resize, resizeOnFullScreen: true },
  },
}

export default config

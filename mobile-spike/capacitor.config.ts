import type { CapacitorConfig } from '@capacitor/cli'
import { KeyboardResize } from '@capacitor/keyboard'

const resize = (process.env.ATTN_SPIKE_KB_RESIZE ?? 'native') as KeyboardResize

const config: CapacitorConfig = {
  appId: 'dev.attn.phonespike',
  appName: 'attn spike',
  webDir: 'dist',
  server: { androidScheme: 'http', cleartext: true },
  plugins: {
    Keyboard: { resize, resizeOnFullScreen: true },
  },
}

export default config

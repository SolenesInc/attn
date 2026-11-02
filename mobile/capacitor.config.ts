import type { CapacitorConfig } from '@capacitor/cli'
import { KeyboardResize } from '@capacitor/keyboard'

const config: CapacitorConfig = {
  appId: 'codes.attn.mobile',
  appName: 'attn',
  webDir: 'dist',
  plugins: {
    Keyboard: { resize: KeyboardResize.Native, resizeOnFullScreen: true },
  },
}

export default config

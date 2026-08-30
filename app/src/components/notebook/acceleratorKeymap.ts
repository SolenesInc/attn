import type { KeyBinding } from '@codemirror/view';
import { isMacLikePlatform } from '../../shortcuts/platform';

// CodeMirror resolves "Mod-" by sniffing navigator.platform, which disagrees with attn's own
// accelerator rule. Off-mac Cmd- is kept beside Ctrl- because Playwright presses Meta there.
export function acceleratorBindings(bindings: readonly KeyBinding[]): KeyBinding[] {
  const out: KeyBinding[] = [];
  for (const binding of bindings) {
    const key = binding.key;
    if (!key || !(key.startsWith('Mod-') || key.startsWith('Cmd-'))) {
      out.push(binding);
      continue;
    }
    const rest = key.slice(4);
    out.push({ ...binding, key: `Cmd-${rest}` });
    if (!isMacLikePlatform()) out.push({ ...binding, key: `Ctrl-${rest}` });
  }
  return out;
}

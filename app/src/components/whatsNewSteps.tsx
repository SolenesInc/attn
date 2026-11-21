import type { ReactNode } from 'react';
import { shortcutTokens } from '../shortcuts/formatShortcut';
import type { Binding, ShortcutId } from '../shortcuts/registry';
import { NewLookScene, MoreColorsScene } from './WhatsNewScenes';

export interface WhatsNewKey {
  label: string;
  combos: string[][];
}

export interface WhatsNewScheme {
  id: string;
  offer: string;
  back: string;
}

export interface WhatsNewStep {
  id: string;
  title: string;
  body: string;
  keys: WhatsNewKey[];
  scene: ReactNode;
  scheme: WhatsNewScheme;
}

export function whatsNewSteps(resolve: (id: ShortcutId) => Binding | null): WhatsNewStep[] {
  const settings = resolve('ui.openSettings');
  const keys: WhatsNewKey[] = settings ? [{ label: 'Settings', combos: [shortcutTokens(settings)] }] : [];

  return [
    {
      id: 'new-look',
      title: 'A new look',
      body: 'attn has a new logo, a black cat watching over your agents, and new colors to go with it: plain neutral surfaces, with magenta for an agent waiting for you and cyan for an agent at work. If you prefer the colors you had, switch back here, or any time in Settings → Appearance → Theme, where they are called Classic.',
      keys,
      scene: <NewLookScene />,
      scheme: { id: 'classic', offer: 'Switch back to Classic', back: 'Use the new colors' },
    },
    {
      id: 'catppuccin',
      title: 'More colors',
      body: 'Settings → Appearance → Theme has more color schemes, and each one colors your terminals too. Try Catppuccin here, or pick one later.',
      keys,
      scene: <MoreColorsScene />,
      scheme: { id: 'catppuccin', offer: 'Try Catppuccin', back: 'Use the default colors' },
    },
  ];
}

import type { ReactNode } from 'react';
import { shortcutTokens } from '../shortcuts/formatShortcut';
import type { Binding, ShortcutId } from '../shortcuts/registry';
import { NewLookScene, NightScene } from './WhatsNewScenes';

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
      body: 'attn has a new logo, a black cat watching over your agents, and new colors to go with it. Magenta marks an agent waiting for you, cyan an agent at work, and violet the main buttons. If you prefer the colors you had, switch back here, or any time in Settings → Appearance → Theme, where they are called Classic.',
      keys,
      scene: <NewLookScene />,
      scheme: { id: 'classic', offer: 'Switch back to Classic', back: 'Use the new colors' },
    },
    {
      id: 'night',
      title: 'Night, for the full colors',
      body: 'The new colors are kept quiet so they stay out of your way. Night shows them at full strength on indigo surfaces, the way the logo does. Try it here, or pick it later under Settings → Appearance → Theme.',
      keys,
      scene: <NightScene />,
      scheme: { id: 'night', offer: 'Try Night', back: 'Use the default colors' },
    },
  ];
}

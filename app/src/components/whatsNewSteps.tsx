// The what's-new intro, one idea per step. Keycaps come from the resolved bindings so the intro
// matches the platform and any rebinds.

import type { ReactNode } from 'react';
import { modifierTokens, shortcutTokens } from '../shortcuts/formatShortcut';
import type { Binding, ShortcutId } from '../shortcuts/registry';
import {
  AgentPaletteScene,
  CommandPaletteScene,
  DesktopsScene,
  MoveAgentsScene,
  ProfilesScene,
  QueueBarScene,
  QueueScene,
  type ScenePress,
} from './WhatsNewScenes';

export interface WhatsNewKey {
  label: string;
  combo: string[];
}

export interface WhatsNewStep {
  id: string;
  title: string;
  body: string;
  keys: WhatsNewKey[];
  scene: ReactNode;
}

export function whatsNewSteps(resolve: (id: ShortcutId) => Binding | null): WhatsNewStep[] {
  const tokens = (id: ShortcutId) => {
    const binding = resolve(id);
    return binding ? shortcutTokens(binding) : [];
  };
  const digits = (id: ShortcutId) => {
    const binding = resolve(id);
    return binding ? [...modifierTokens(binding), '1–9'] : [];
  };
  const key = (label: string, combo: string[]): WhatsNewKey[] => (combo.length ? [{ label, combo }] : []);
  const press = (id: ShortcutId): ScenePress | null => {
    const combo = tokens(id);
    return combo.length ? { combos: [combo] } : null;
  };

  return [
    {
      id: 'profiles',
      title: 'Profiles keep your worlds apart',
      body: 'Make one per project, or one for work and one for personal. Each keeps its own agents, crew, automations and desktops, and switching closes nothing.',
      keys: key('Switch profile', tokens('profile.switch')),
      scene: <ProfilesScene press={press('profile.switch')} />,
    },
    {
      id: 'desktops',
      title: 'Agents live on desktops',
      body: 'A desktop is an arrangement of agents and tiles, and every window on the profile shows the same one. Jump straight to one, or zoom out to see them all.',
      keys: [
        ...key('Switch desktop', digits('desktop.select1')),
        ...key('Desktop overview', tokens('desktop.overview')),
      ],
      scene: <DesktopsScene press={press('desktop.select2')} />,
    },
    {
      id: 'move',
      title: 'Move agents between desktops',
      body: 'Send the focused pane to another desktop and follow it there, or send it and stay where you are. A digit with no desktop yet makes one, and dragging a sidebar row or pane header works too.',
      keys: [
        ...key('Move and follow', digits('desktop.send1')),
        ...key('Move and stay', digits('desktop.sendStay1')),
      ],
      scene: <MoveAgentsScene press={press('desktop.send2')} />,
    },
    {
      id: 'queue',
      title: 'The queue brings you what is waiting',
      body: 'Turn on the agent queue and agents sort into waiting for you or busy. Once you have unblocked one, settle it and attn shows you the next.',
      keys: [
        ...key('Settle and go to the next', tokens('session.settle')),
        ...key('Next waiting agent', tokens('session.jumpToWaiting')),
        ...key('Next automation run', tokens('session.nextRun')),
      ],
      scene: <QueueScene press={press('session.settle')} />,
    },
    {
      id: 'queue-bar',
      title: 'Hide the sidebar, keep the queue',
      body: 'With the queue on, hiding the sidebar folds it into a bar across the top: who is waiting, your runs and your desktops. Hover the waiting pill to peek at the list.',
      keys: [
        ...key('Hide or show the sidebar', tokens('session.toggleSidebar')),
        ...key('Every agent', tokens('sidebar.agentList')),
      ],
      scene: <QueueBarScene press={press('session.toggleSidebar')} />,
    },
    {
      id: 'agent-palette',
      title: 'Find any agent in a few letters',
      body: 'The agent palette jumps to any agent or tile by name, on any desktop. Settle or snooze right from the list, or type > for commands.',
      keys: key('Agent palette', tokens('ui.actionMenu')),
      scene: <AgentPaletteScene press={press('ui.actionMenu')} />,
    },
    {
      id: 'command-palette',
      title: 'Everything else is a command away',
      body: 'Make a desktop, switch profile, turn on the queue or auto-settle, open the notebook. When a shortcut slips your mind, the command palette has it.',
      keys: key('Command palette', tokens('ui.commandPalette')),
      scene: <CommandPaletteScene press={press('ui.commandPalette')} />,
    },
  ];
}

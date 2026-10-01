// The what's-new intro, one idea per step. Keycaps come from the resolved bindings so the intro
// matches the platform and any rebinds.

import type { ReactNode } from 'react';
import { modifierTokens, shortcutTokens } from '../shortcuts/formatShortcut';
import type { Binding, ShortcutId } from '../shortcuts/registry';
import {
  AgentPaletteScene,
  ArrangeScene,
  CommandPaletteScene,
  DesktopsScene,
  ProfilesScene,
  QueueBarScene,
  QueueScene,
  type ScenePress,
} from './WhatsNewScenes';

export interface WhatsNewKey {
  label: string;
  combos: string[][];
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
  const key = (label: string, ...combos: string[][]): WhatsNewKey[] => {
    const bound = combos.filter((combo) => combo.length);
    return bound.length ? [{ label, combos: bound }] : [];
  };
  const press = (id: ShortcutId): ScenePress | null => {
    const combo = tokens(id);
    return combo.length ? { combos: [combo] } : null;
  };

  return [
    {
      id: 'profiles',
      title: 'One profile per world',
      body: "Work, personal, that side project: each gets its own agents, crew, automations and desktops, so only the one you're focused on is in front of you. The other profiles' agents disappear from view, so there are no work agents to tune out on a personal evening. Switch freely without interrupting the agents.",
      keys: key('Switch profile', tokens('profile.switch')),
      scene: <ProfilesScene press={press('profile.switch')} />,
    },
    {
      id: 'desktops',
      title: 'Desktops arrange your agents',
      body: "Group related agents on a desktop and flip between desktops with a keystroke. The sidebar keeps listing every agent in the profile, but only the current desktop's agents are on screen.",
      keys: [
        ...key('Switch desktop', digits('desktop.select1')),
        ...key('See all desktops', tokens('desktop.overview')),
      ],
      scene: <DesktopsScene press={press('desktop.select2')} />,
    },
    {
      id: 'arrange',
      title: 'Arrange each desktop your way',
      body: 'Drag panes by their header to rearrange them, open a shell beside an agent, or give one pane the whole desktop when you need room. When an agent belongs somewhere else, send it to another desktop and go with it, or leave it there and stay where you are.',
      keys: [
        ...key('Focus one pane', tokens('terminal.toggleMaximize')),
        ...key('Move and follow', digits('desktop.send1')),
        ...key('Move and stay', digits('desktop.sendStay1')),
      ],
      scene: <ArrangeScene press={press('desktop.send2')} />,
    },
    {
      id: 'queue',
      title: "The queue decides what's next",
      body: "Turn on the agent queue from the command palette and your agents split into waiting for you or busy. When you're done with one, settle it and attn moves you to the next in the queue. You never have to choose which agent to look at next, so you tire less and get more work done.",
      keys: [
        ...key('Settle', tokens('session.settle')),
        ...key('Step through the queue', tokens('session.prev'), tokens('session.next')),
        ...key('Next waiting agent', tokens('session.jumpToWaiting')),
        ...key('Next automation run', tokens('session.nextRun')),
      ],
      scene: <QueueScene press={press('session.settle')} />,
    },
    {
      id: 'queue-bar',
      title: 'Hide the sidebar, keep the queue',
      body: "Since the queue moves you from agent to agent, you rarely need the sidebar. Hide it and a slim bar across the top keeps who's waiting, your automation runs and your desktops, while the rest of the window goes to the agent in front of you.",
      keys: [
        ...key('Show or hide the sidebar', tokens('session.toggleSidebar')),
        ...key('All agents', tokens('sidebar.agentList')),
      ],
      scene: <QueueBarScene press={press('session.toggleSidebar')} />,
    },
    {
      id: 'agent-palette',
      title: 'Jump to any agent',
      body: 'Open the agent palette and type a few letters to jump to any agent, tile or crew member, on any desktop. From the list you can settle or snooze an agent without opening it, or type > to switch to commands.',
      keys: key('Agent palette', tokens('ui.actionMenu')),
      scene: <AgentPaletteScene press={press('ui.actionMenu')} />,
    },
    {
      id: 'command-palette',
      title: 'Keep your hands on the keyboard',
      body: 'Most of what attn can do is in the command palette: make a new desktop, switch profile, turn on the agent queue or auto-settle, open the notebook or the Garden. Commands that have a shortcut show it, so the palette is also where you pick them up.',
      keys: key('Command palette', tokens('ui.commandPalette')),
      scene: <CommandPaletteScene press={press('ui.commandPalette')} />,
    },
  ];
}

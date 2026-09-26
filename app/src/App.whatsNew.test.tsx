import { fireEvent, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { WHATS_NEW_STORAGE_KEY } from './hooks/useWhatsNew';
import { gesture, renderApp, restartApp } from './test/renderApp';

const whatsNew = () => screen.queryByRole('dialog', { name: 'attn is organized around workspaces' });

async function launchAfterUpdate() {
  localStorage.removeItem(WHATS_NEW_STORAGE_KEY);
  return renderApp();
}

describe('App what’s new', () => {
  it('greets the first launch after an update with the ⌘N change called out', async () => {
    await launchAfterUpdate();

    const callout = screen.getByText('Changed').closest('section')!;
    expect(callout).toHaveTextContent('⌘N opens a session inside this workspace');
    expect(Array.from(callout.querySelectorAll('.key-combo'), (combo) => combo.textContent)).toEqual(['⌘N', '⌘T']);
    expect(screen.getByRole('heading', { name: 'The sidebar lists workspaces' })).toBeInTheDocument();
  });

  it('hands off to the full shortcut list', async () => {
    const { daemon } = await launchAfterUpdate();

    await gesture(daemon, () => fireEvent.click(screen.getByRole('button', { name: 'View all shortcuts →' })));

    expect(whatsNew()).toBeNull();
    expect(screen.getByRole('dialog', { name: 'Keyboard Shortcuts' })).toBeInTheDocument();
  });

  it('stays dismissed after Got it, across launches', async () => {
    const first = await launchAfterUpdate();
    expect(whatsNew()).toBeInTheDocument();

    await gesture(first.daemon, () => fireEvent.click(screen.getByRole('button', { name: 'Got it' })));
    expect(whatsNew()).toBeNull();

    await restartApp(first);
    expect(whatsNew()).toBeNull();
  });
});

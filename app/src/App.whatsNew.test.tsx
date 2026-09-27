import { fireEvent, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { WHATS_NEW_STORAGE_KEY } from './hooks/useWhatsNew';
import { gesture, renderApp, restartApp } from './test/renderApp';

const whatsNew = () => screen.queryByRole('dialog', { name: 'attn is organized around desktops' });

async function launchAfterUpdate() {
  localStorage.setItem(WHATS_NEW_STORAGE_KEY, 'an-earlier-release');
  return renderApp();
}

describe('App what’s new', () => {
  it('greets the first launch after an update with the desktop change called out', async () => {
    await launchAfterUpdate();

    const callout = screen.getByText('Changed').closest('section')!;
    expect(callout).toHaveTextContent('Agents live on desktops');
    expect(Array.from(callout.querySelectorAll('.key-combo'), (combo) => combo.textContent)).toEqual(['⌘1–9']);
    expect(screen.getByRole('heading', { name: 'Profiles group everything' })).toBeInTheDocument();
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

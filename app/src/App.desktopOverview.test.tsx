import { fireEvent, screen, within } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { emptyDesktop, defaultProfile } from './test/daemonFixtures';
import { gesture, pressShortcut, renderApp } from './test/renderApp';
import { formatShortcut } from './shortcuts/formatShortcut';

describe('App sidebar desktop overview', () => {
  it.each(['tree', 'queue'])('opens and dismisses the overview from the %s sidebar', async (mode) => {
    const { daemon } = await renderApp({
      initialState: {
        settings: { queue_mode_enabled: String(mode === 'queue') },
        desktops: [emptyDesktop('d1', { name: 'Sidebar refinement' })],
        profiles: [defaultProfile('d1')],
      },
    });

    await gesture(daemon, () => pressShortcut('session.toggleSidebar'));
    const button = screen.getByRole('button', { name: 'Desktop overview' });
    expect(button).toHaveAttribute('title', `Desktop overview (${formatShortcut('desktop.overview')})`);
    await gesture(daemon, () => fireEvent.click(button));
    const overview = screen.getByRole('dialog', { name: 'Desktop overview' });
    expect(within(overview).getByRole('button', { name: /Sidebar refinement/ })).toBeInTheDocument();

    await gesture(daemon, () => fireEvent.click(screen.getByRole('button', { name: 'Close the overview' })));
    expect(screen.queryByRole('dialog', { name: 'Desktop overview' })).toBeNull();
  });
});

import { fireEvent, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { openSection, savedSettings } from '../test/settings';
import { gesture } from '../test/renderApp';

describe('shared Codex launch default', () => {
  it('starts disabled and persists on/off without sending session commands', async () => {
    const daemon = await openSection('experimental');
    const toggle = screen.getByRole('switch', { name: 'Shared Codex (experimental)' });
    expect(toggle).toHaveAttribute('aria-checked', 'false');
    expect(screen.getByText(/Existing agents keep their mode when reopened/)).toBeVisible();
    await gesture(daemon, () => fireEvent.click(toggle));
    expect(toggle).toHaveAttribute('aria-checked', 'true');
    await gesture(daemon, () => fireEvent.click(toggle));
    expect(toggle).toHaveAttribute('aria-checked', 'false');
    expect(savedSettings(daemon)).toEqual([
      ['codex_shared_enabled', 'true'], ['codex_shared_enabled', 'false'],
    ]);
    expect(daemon.sentOf('spawn_session')).toEqual([]);
    expect(daemon.sentOf('unregister')).toEqual([]);
  });
});

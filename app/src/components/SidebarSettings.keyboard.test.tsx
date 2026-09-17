import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it, vi } from 'vitest';
import { SidebarSettings } from './SidebarSettings';

describe('sidebar settings keyboard access', () => {
  it('focuses the controls on opening and returns to the trigger on Escape', async () => {
    const user = userEvent.setup();
    const toggle = vi.fn();
    render(
      <SidebarSettings
        queueModeEnabled
        onToggleQueueMode={toggle}
        displayMode="open"
        setDisplayMode={vi.fn()}
      />,
    );
    const trigger = screen.getByRole('button', { name: 'Sidebar settings' });
    trigger.focus();
    await user.keyboard('{Enter}');
    expect(screen.getByRole('dialog', { name: 'Sidebar settings' })).toBeVisible();
    expect(screen.getByRole('switch', { name: 'Agent queue' })).toHaveFocus();
    await user.keyboard(' ');
    expect(toggle).toHaveBeenCalledTimes(1);
    await user.keyboard('{Escape}');
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    expect(trigger).toHaveFocus();
  });

  it('lets an outside control take focus when dismissing', async () => {
    const user = userEvent.setup();
    render(
      <>
        <SidebarSettings displayMode="open" setDisplayMode={vi.fn()} />
        <button>Outside</button>
      </>,
    );
    await user.click(screen.getByRole('button', { name: 'Sidebar settings' }));
    await user.click(screen.getByRole('button', { name: 'Outside' }));
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Outside' })).toHaveFocus();
  });
});

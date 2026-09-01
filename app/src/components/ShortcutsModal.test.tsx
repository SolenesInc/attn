import { describe, it, expect, vi } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/react';
import { ShortcutsModal } from './ShortcutsModal';
import { withNavigatorPlatform } from '../test/platformStub';

describe('ShortcutsModal', () => {
  it('renders nothing when closed', () => {
    const { container } = render(<ShortcutsModal isOpen={false} onClose={() => {}} />);
    expect(container.firstChild).toBeNull();
  });

  it('shows the cheatsheet categories and real shortcut glyphs when open', () => {
    render(<ShortcutsModal isOpen onClose={() => {}} />);

    expect(screen.getByRole('dialog', { name: 'Keyboard Shortcuts' })).toBeInTheDocument();
    expect(screen.getByText('Workspaces & Sessions')).toBeInTheDocument();
    expect(screen.getByText('Panes & Terminals')).toBeInTheDocument();

    // The "New workspace" row renders ⌘ and T keycaps from the registry.
    const newWorkspaceRow = screen.getByText('New workspace').closest('.shortcuts-row');
    expect(newWorkspaceRow).not.toBeNull();
    expect(newWorkspaceRow!.querySelectorAll('.keycap')).toHaveLength(2);
    expect(newWorkspaceRow!.textContent).toContain('⌘');
    expect(newWorkspaceRow!.textContent).toContain('T');
  });

  it('labels the accelerator Ctrl off-mac', () => {
    withNavigatorPlatform('Linux aarch64', () => {
      render(<ShortcutsModal isOpen onClose={() => {}} />);
    });
    const newWorkspaceRow = screen.getByText('New workspace').closest('.shortcuts-row');
    const caps = [...newWorkspaceRow!.querySelectorAll('.keycap')].map((c) => c.textContent);
    expect(caps).toEqual(['Ctrl', 'Shift', 'T']);
    expect(newWorkspaceRow!.textContent).not.toContain('⌘');
  });

  it('closes via the close button', () => {
    const onClose = vi.fn();
    render(<ShortcutsModal isOpen onClose={onClose} />);
    fireEvent.click(screen.getByRole('button', { name: 'Close keyboard shortcuts' }));
    expect(onClose).toHaveBeenCalledTimes(1);
  });
});

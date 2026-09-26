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
    expect(screen.getByText('Desktops & Sessions')).toBeInTheDocument();
    expect(screen.getByText('Panes & Terminals')).toBeInTheDocument();

    const overviewRow = screen.getByText('Desktop overview').closest('.shortcuts-row');
    expect(overviewRow).not.toBeNull();
    expect(overviewRow!.querySelectorAll('.keycap')).toHaveLength(2);
    expect(overviewRow!.textContent).toContain('⌘');
    expect(overviewRow!.textContent).toContain('G');
  });

  it('labels the accelerator Ctrl off-mac', () => {
    withNavigatorPlatform('Linux aarch64', () => {
      render(<ShortcutsModal isOpen onClose={() => {}} />);
    });
    const overviewRow = screen.getByText('Desktop overview').closest('.shortcuts-row');
    const caps = [...overviewRow!.querySelectorAll('.keycap')].map((c) => c.textContent);
    expect(caps).toEqual(['Ctrl', 'Shift', 'G']);
    expect(overviewRow!.textContent).not.toContain('⌘');
  });

  it('closes via the close button', () => {
    const onClose = vi.fn();
    render(<ShortcutsModal isOpen onClose={onClose} />);
    fireEvent.click(screen.getByRole('button', { name: 'Close keyboard shortcuts' }));
    expect(onClose).toHaveBeenCalledTimes(1);
  });
});

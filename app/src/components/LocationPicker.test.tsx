import type { ComponentProps } from 'react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { LocationPicker } from './LocationPicker';
import { SettingsProvider } from '../contexts/SettingsContext';

const useFilesystemSuggestionsMock = vi.fn();

vi.mock('../hooks/useFilesystemSuggestions', () => ({
  useFilesystemSuggestions: (...args: unknown[]) => useFilesystemSuggestionsMock(...args),
}));

type LocationPickerProps = ComponentProps<typeof LocationPicker>;

function buildRecentLocation() {
  return {
    path: '/home/dev/projects/recent-repo',
    last_seen: '2026-04-11T08:30:00Z',
    use_count: 4,
  };
}

function renderPicker(props?: Partial<LocationPickerProps>) {
  const onSelect = vi.fn();
  render(
    <SettingsProvider settings={{}} setSetting={vi.fn()}>
      <LocationPicker
        isOpen
        onClose={vi.fn()}
        onSelect={onSelect}
        {...props}
      />
    </SettingsProvider>,
  );
  return { onSelect };
}

describe('LocationPicker', () => {
  beforeEach(() => {
    useFilesystemSuggestionsMock.mockReset();
    useFilesystemSuggestionsMock.mockImplementation((inputPath: string) => ({
      suggestions: inputPath.startsWith('~/pro')
        ? [
            { name: 'projects', path: '~/projects' },
            { name: 'project-archive', path: '~/project-archive' },
          ]
        : inputPath.startsWith('~/projects')
          ? [
              { name: 'alpha', path: '~/projects/alpha' },
              { name: 'beta', path: '~/projects/beta' },
            ]
          : [],
      loading: false,
      error: null,
      currentDir: inputPath.startsWith('~/projects') ? '~/' : '',
    }));
  });

  it('hover does not change the input or selection', () => {
    renderPicker();

    const input = screen.getByTestId('location-picker-path-input') as HTMLInputElement;
    fireEvent.change(input, { target: { value: '~/pro' } });

    const firstItem = screen.getByTestId('location-picker-item-0');
    fireEvent.mouseEnter(firstItem);

    expect(input.value).toBe('~/pro');
    expect(firstItem).not.toHaveClass('selected');
  });

  it('opens a typed directory directly instead of implicitly selecting a child like .claude', async () => {
    useFilesystemSuggestionsMock.mockImplementation((inputPath: string) => ({
      suggestions: inputPath === '/tmp/project/'
        ? [{ name: '.claude', path: '/tmp/project/.claude' }]
        : [],
      loading: false,
      error: null,
      currentDir: inputPath === '/tmp/project/' ? '/tmp/project' : '',
    }));

    const onInspectPath = vi.fn(async (inputPath: string) => ({
      success: true,
      inspection: {
        input_path: inputPath,
        resolved_path: '/tmp/project',
        home_path: '/home/dev',
        exists: true,
        is_directory: true,
      },
    }));
    const { onSelect } = renderPicker({ onInspectPath });

    const input = screen.getByTestId('location-picker-path-input');
    fireEvent.change(input, { target: { value: '/tmp/project/' } });
    fireEvent.keyDown(input, { key: 'Enter' });

    await waitFor(() => {
      expect(onInspectPath).toHaveBeenCalledWith('/tmp/project');
      expect(onSelect).toHaveBeenCalledWith('/tmp/project', 'claude', false);
    });
  });

  it('filters recent locations for ~/ and absolute queries without rewriting the input', async () => {
    const onGetRecentLocations = vi.fn(async () => ({
      locations: [buildRecentLocation()],
      home_path: '/home/dev',
    }));
    renderPicker({ onGetRecentLocations });

    const input = screen.getByTestId('location-picker-path-input') as HTMLInputElement;
    fireEvent.change(input, { target: { value: '~/projects/recent-repo' } });

    await waitFor(() => {
      expect(screen.getByTestId('location-picker-item-0')).toBeInTheDocument();
      expect(screen.getByTestId('location-picker-item-0')).not.toHaveClass('selected');
      expect(input.value).toBe('~/projects/recent-repo');
    });

    fireEvent.change(input, { target: { value: '/home/dev/projects/recent-repo' } });

    await waitFor(() => {
      expect(screen.getByTestId('location-picker-item-0')).toBeInTheDocument();
      expect(screen.getByTestId('location-picker-item-0')).not.toHaveClass('selected');
      expect(input.value).toBe('/home/dev/projects/recent-repo');
    });
  });

  it('falls back to launching the selected path when repo options fail to load', async () => {
    const onInspectPath = vi.fn(async () => ({
      success: true,
      inspection: {
        input_path: '~/projects/exsin',
        resolved_path: '/home/dev/projects/exsin',
        home_path: '/home/dev',
        exists: true,
        is_directory: true,
        repo_root: '/home/dev/projects/exsin',
      },
    }));
    const onGetRepoInfo = vi.fn(async () => ({
      success: false,
      error: 'unborn HEAD',
    }));
    const onError = vi.fn();
    const { onSelect } = renderPicker({ onInspectPath, onGetRepoInfo, onError });

    const input = screen.getByTestId('location-picker-path-input');
    fireEvent.change(input, { target: { value: '~/projects/exsin' } });
    fireEvent.keyDown(input, { key: 'Enter' });

    await waitFor(() => {
      expect(onError).toHaveBeenCalledWith('unborn HEAD');
      expect(onSelect).toHaveBeenCalledWith('/home/dev/projects/exsin', 'claude', false);
    });
  });
});

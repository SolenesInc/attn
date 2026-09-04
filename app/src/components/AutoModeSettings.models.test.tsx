import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { AutoModeSettings } from './AutoModeSettings';
import type {
  AutoModeConfigInfo,
  AutoModeModelCatalog,
  AutoModePatternEdit,
  AutoModeState,
} from '../hooks/daemonAutoModeEvents';
import { useAutoModePolicy } from '../hooks/useAutoModePolicy';

const config = (over: Partial<AutoModeConfigInfo> = {}): AutoModeConfigInfo => ({
  enabled_default: true,
  environment: { slots: [], notes: [] },
  allow: [],
  hard_deny: [],
  shipped_hard_deny: [],
  models: ['opencode-go/glm-5.3'],
  ...over,
});

const state = (over: Partial<AutoModeState> = {}): AutoModeState => ({
  config: config(),
  proposals: [],
  denials: [],
  environmentSlots: [],
  ...over,
});

const catalog = (over: Partial<AutoModeModelCatalog> = {}): AutoModeModelCatalog => ({
  providers: [
    {
      provider: 'opencode',
      ready: true,
      models: [{ id: 'claude-opus-4-6', name: 'Claude Opus 4.6' }, { id: 'claude-haiku-4-5' }],
    },
    {
      provider: 'opencode-go',
      ready: true,
      models: [{ id: 'glm-5.3' }],
    },
    {
      provider: 'vendor',
      ready: false,
      detail: 'provider_not_found',
      models: [{ id: 'nope' }],
    },
  ],
  problem: null,
  ...over,
});

function renderPane(over: {
  value?: AutoModeState;
  setModels?: (models: string[]) => Promise<AutoModePatternEdit>;
  loadModels?: () => Promise<AutoModeModelCatalog>;
} = {}) {
  const setModels = over.setModels ?? vi.fn(async () => ({ config: config() }));
  const loadModels = over.loadModels ?? vi.fn(async () => catalog());
  function Harness() {
    const policy = useAutoModePolicy({
      enabled: true,
      getState: vi.fn().mockResolvedValue(over.value ?? state()),
      promoteProposal: vi.fn(),
      discardProposal: vi.fn(),
      addPattern: vi.fn(),
      removePattern: vi.fn(),
      setEnvironmentSlot: vi.fn(),
      setModels,
      loadModels,
    });
    return <AutoModeSettings policy={policy} />;
  }
  render(<Harness />);
  return { setModels, loadModels };
}

describe('setting the models from the app', () => {
  it('adds a typed model, sending the whole list rather than the one entry', async () => {
    const { setModels } = renderPane();
    await screen.findByTestId('automode-models');

    fireEvent.change(screen.getByTestId('automode-models-input'), {
      target: { value: 'openai-codex/gpt-5.6-luna' },
    });
    fireEvent.click(screen.getByTestId('automode-models-add'));

    await waitFor(() => expect(setModels).toHaveBeenCalledWith([
      'opencode-go/glm-5.3',
      'openai-codex/gpt-5.6-luna',
    ]));
  });

  it('takes a model the CLI would take, so the pane is no stricter than it', async () => {
    const { setModels } = renderPane();
    await screen.findByTestId('automode-models');

    fireEvent.change(screen.getByTestId('automode-models-input'), {
      target: { value: 'something/nobody-has-heard-of' },
    });
    fireEvent.click(screen.getByTestId('automode-models-add'));

    await waitFor(() => expect(setModels).toHaveBeenCalledWith([
      'opencode-go/glm-5.3',
      'something/nobody-has-heard-of',
    ]));
  });

  it('removes a model, and reorders one into the seat that judges', async () => {
    const value = state({ config: config({ models: ['a/first', 'b/second'] }) });
    const { setModels } = renderPane({ value });
    await screen.findByTestId('automode-models');

    const rows = screen.getAllByTestId('automode-models-entry');
    fireEvent.click(within(rows[1]).getByTestId('automode-models-primary'));
    await waitFor(() => expect(setModels).toHaveBeenCalledWith(['b/second', 'a/first']));

    fireEvent.click(within(rows[0]).getByTestId('automode-models-remove'));
    await waitFor(() => expect(setModels).toHaveBeenCalledWith(['b/second']));
  });

  it('refuses the same model twice, saying why a pass cannot walk it', async () => {
    const { setModels } = renderPane();
    await screen.findByTestId('automode-models');

    fireEvent.change(screen.getByTestId('automode-models-input'), {
      target: { value: 'opencode-go/glm-5.3' },
    });
    fireEvent.click(screen.getByTestId('automode-models-add'));

    expect(await screen.findByText(/already in the list/)).toBeTruthy();
    expect(setModels).not.toHaveBeenCalled();
  });

  it('asks pi only when the picker is opened, not on every render', async () => {
    const { loadModels } = renderPane();
    await screen.findByTestId('automode-models');
    expect(loadModels).not.toHaveBeenCalled();

    fireEvent.click(screen.getByTestId('automode-models-browse'));
    await waitFor(() => expect(loadModels).toHaveBeenCalledTimes(1));
  });

  it('offers what pi can reach, and closes off what it cannot use or already has', async () => {
    renderPane();
    await screen.findByTestId('automode-models');
    fireEvent.click(screen.getByTestId('automode-models-browse'));

    const select = await screen.findByTestId('automode-models-select');
    const chosen = within(select).getByRole('option', { name: 'Claude Opus 4.6' }) as HTMLOptionElement;
    expect(chosen.disabled).toBe(false);
    expect(chosen.value).toBe('opencode/claude-opus-4-6');

    const already = within(select).getByRole('option', { name: 'glm-5.3' }) as HTMLOptionElement;
    expect(already.disabled).toBe(true);

    const unusable = within(select).getByRole('option', { name: 'nope' }) as HTMLOptionElement;
    expect(unusable.disabled).toBe(true);
    const groups = [...select.querySelectorAll('optgroup')].map((group) => group.label);
    expect(groups).toContain('vendor — provider_not_found');
  });

  it('picking from the catalog appends it to the list', async () => {
    const { setModels } = renderPane();
    await screen.findByTestId('automode-models');
    fireEvent.click(screen.getByTestId('automode-models-browse'));

    const select = await screen.findByTestId('automode-models-select');
    fireEvent.change(select, { target: { value: 'opencode/claude-haiku-4-5' } });

    await waitFor(() => expect(setModels).toHaveBeenCalledWith([
      'opencode-go/glm-5.3',
      'opencode/claude-haiku-4-5',
    ]));
  });

  it('refreshes an empty catalog after a provider becomes available', async () => {
    const loadModels = vi.fn()
      .mockResolvedValueOnce(catalog({ providers: [] }))
      .mockResolvedValueOnce(catalog());
    const { setModels } = renderPane({ loadModels });
    await screen.findByTestId('automode-models');
    fireEvent.click(screen.getByTestId('automode-models-browse'));

    expect(await screen.findByTestId('automode-models-catalog-empty'))
      .toHaveTextContent('Configure a provider in Pi');
    fireEvent.click(screen.getByTestId('automode-models-refresh'));

    expect(await screen.findByTestId('automode-models-select')).toBeTruthy();
    expect(loadModels).toHaveBeenCalledTimes(2);
    expect(setModels).not.toHaveBeenCalled();
  });

  it('a pi that cannot answer leaves the typed field working', async () => {
    const loadModels = vi.fn(async () => {
      throw new Error('plugin "attn-pi" is not connected');
    });
    const { setModels } = renderPane({ loadModels });
    await screen.findByTestId('automode-models');
    fireEvent.click(screen.getByTestId('automode-models-browse'));

    expect(await screen.findByTestId('automode-models-catalog-error')).toHaveTextContent(
      'is not connected',
    );

    fireEvent.change(screen.getByTestId('automode-models-input'), {
      target: { value: 'openai-codex/gpt-5.6-luna' },
    });
    fireEvent.click(screen.getByTestId('automode-models-add'));
    await waitFor(() => expect(setModels).toHaveBeenCalled());
  });
});

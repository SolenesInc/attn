import { act, fireEvent, render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { GardenAdvisorSettings } from './GardenAdvisorSettings';

const agents = ['codex', 'claude', 'copilot'];

function renderSettings(settings: Record<string, string> = {}) {
  const onSetSetting = vi.fn();
  render(
    <GardenAdvisorSettings
      settings={settings}
      agents={agents}
      onSetSetting={onSetSetting}
    />,
  );
  return onSetSetting;
}

describe('GardenAdvisorSettings', () => {
  it('shows the Codex recipe by default', () => {
    renderSettings();

    expect(screen.getByTestId('settings-garden-advisor-agent')).toHaveValue('codex');
    expect(screen.getByTestId('settings-garden-advisor-model')).toHaveValue('gpt-5.6-luna');
    expect(screen.getByTestId('settings-garden-advisor-effort')).toHaveValue('xhigh');
  });

  it('uses the selected agent defaults and saves one recipe', async () => {
    const onSetSetting = renderSettings();

    fireEvent.change(screen.getByTestId('settings-garden-advisor-agent'), {
      target: { value: 'claude' },
    });
    expect(screen.getByTestId('settings-garden-advisor-model')).toHaveValue('sonnet');
    expect(screen.getByTestId('settings-garden-advisor-effort')).toHaveValue('medium');
    await act(async () => { if (screen.queryByTestId('settings-garden-advisor-model-custom')) fireEvent.blur(screen.getByTestId('settings-garden-advisor-model-custom')); });

    expect(onSetSetting).toHaveBeenCalledWith(
      'garden.advisor',
      JSON.stringify({ agent: 'claude', model: 'sonnet', effort: 'medium' }),
    );
  });

  it('leaves Copilot effort unpinned by default and can save one', async () => {
    const onSetSetting = renderSettings();

    fireEvent.change(screen.getByTestId('settings-garden-advisor-agent'), {
      target: { value: 'copilot' },
    });
    expect(screen.getByTestId('settings-garden-advisor-model')).toHaveValue('claude-sonnet-4.6');
    expect(screen.getByTestId('settings-garden-advisor-effort')).toHaveValue('');
    fireEvent.change(screen.getByTestId('settings-garden-advisor-effort'), {
      target: { value: 'max' },
    });
    await act(async () => { if (screen.queryByTestId('settings-garden-advisor-model-custom')) fireEvent.blur(screen.getByTestId('settings-garden-advisor-model-custom')); });

    expect(onSetSetting).toHaveBeenCalledWith(
      'garden.advisor',
      JSON.stringify({ agent: 'copilot', model: 'claude-sonnet-4.6', effort: 'max' }),
    );
  });

  it('saves a custom model', async () => {
    const onSetSetting = renderSettings();

    fireEvent.change(screen.getByTestId('settings-garden-advisor-model'), {
      target: { value: 'custom' },
    });
    fireEvent.change(screen.getByTestId('settings-garden-advisor-model-custom'), {
      target: { value: 'gpt-custom' },
    });
    await act(async () => { if (screen.queryByTestId('settings-garden-advisor-model-custom')) fireEvent.blur(screen.getByTestId('settings-garden-advisor-model-custom')); });

    expect(onSetSetting).toHaveBeenCalledWith(
      'garden.advisor',
      JSON.stringify({ agent: 'codex', model: 'gpt-custom', effort: 'xhigh' }),
    );
  });

  it('keeps an unavailable saved agent visible', () => {
    renderSettings({
      'garden.advisor': JSON.stringify({ agent: 'copilot', model: 'claude-sonnet-4.6' }),
      copilot_available: 'false',
      copilot_cap_headless_task: 'true',
    });

    expect(screen.getByTestId('settings-garden-advisor-agent')).toHaveValue('copilot');
    expect(screen.getByTestId('settings-garden-advisor-unavailable')).toHaveTextContent(
      'Copilot is saved for Garden review',
    );
  });
});

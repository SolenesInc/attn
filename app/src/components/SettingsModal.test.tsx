import { act, fireEvent, screen, within } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { renderApp } from '../test/renderApp';
import type { EventMessage } from '../test/protocol';
import type { ScriptedDaemon } from '../test/scriptedDaemon';
import { gesture, openSection, renderSettings, savedSettings } from '../test/settings';
import { getSettingsAutomationHandle } from './settingsAutomation';

type InitialState = EventMessage<'initial_state'>;
type Plugin = EventMessage<'plugins_updated'>['plugins'][number];
type Endpoint = NonNullable<InitialState['endpoints']>[number];

function servePlugins(daemon: ScriptedDaemon, plugins: Plugin[]) {
  daemon.on('list_plugins', () => ({ event: 'plugins_updated', plugins, issues: [] }));
  const succeed = (action: string) => ({ name }: { name?: string }) => ({ event: 'plugin_action_result' as const, action, name, success: true });
  daemon.on('install_plugin', () => ({ event: 'plugin_action_result', action: 'install', success: true }));
  daemon.on('install_bundled_plugin', succeed('install_bundled'));
  daemon.on('uninstall_plugin', succeed('uninstall'));
  daemon.on('set_plugin_priority', succeed('set_priority'));
}

function serveEndpoints(daemon: ScriptedDaemon) {
  daemon.on('add_endpoint', () => ({ event: 'endpoint_action_result', action: 'add', endpoint_id: 'ep-new', success: true }));
  daemon.on('update_endpoint', ({ endpoint_id }) => ({ event: 'endpoint_action_result', action: 'update', endpoint_id, success: true }));
  daemon.on('set_endpoint_remote_web', ({ endpoint_id }) => ({ event: 'endpoint_action_result', action: 'remote_web', endpoint_id, success: true }));
}

const installedPlugin = (overrides: Partial<Plugin> = {}): Plugin => ({
  name: 'services-pilot-worktrees',
  version: '0.1.0',
  dir: '/tmp/services-pilot-worktrees',
  priority: 10,
  connected: true,
  running: true,
  availability: 'user',
  installation_state: 'installed',
  runtime_state: 'connected',
  can_install: false,
  can_uninstall: true,
  ...overrides,
});

const settingsModal = () => screen.queryByTestId('settings-modal');

describe('SettingsModal', () => {
  it('closes on escape', async () => {
    const { daemon } = await renderSettings();
    expect(screen.getByText('Mobile Web Client')).toBeInTheDocument();

    await gesture(daemon, () => fireEvent.keyDown(window, { key: 'Escape' }));

    expect(settingsModal()).toBeNull();
  });

  it('replaces the GitHub hosts list with the daemon reason while polling is off', async () => {
    await renderSettings({ github_polling_off_reason: 'GitHub polling is off for instance dev. Start its daemon with ATTN_GITHUB_POLLING=on.' });

    const settings = within(settingsModal()!);
    expect(settings.getByTestId('github-polling-off')).toHaveTextContent('ATTN_GITHUB_POLLING=on');
    expect(settings.queryByText('No authenticated hosts detected.')).not.toBeInTheDocument();
    expect(settings.queryByText(/gh auth login/)).not.toBeInTheDocument();
  });

  it('renders authenticated GitHub hosts provided by the daemon', async () => {
    await renderSettings({ github_hosts: ['ghe.example.test', 'github.com'] });

    expect(screen.getByText('ghe.example.test')).toBeInTheDocument();
    expect(screen.getByText('github.com')).toBeInTheDocument();
  });

  it('follows the hosts the daemon rediscovers, and its reason when polling turns off', async () => {
    const { daemon } = await renderSettings({ github_hosts: ['github.com'] });

    daemon.emit({ event: 'github_hosts_updated', github_hosts: ['ghe.example.test', 'github.com'] });
    expect(screen.getByText('ghe.example.test')).toBeInTheDocument();

    daemon.emit({
      event: 'github_hosts_updated',
      github_hosts: ['ghe.example.test', 'github.com'],
      github_polling_off_reason: 'GitHub polling is off for instance dev.',
    });
    expect(within(settingsModal()!).getByTestId('github-polling-off')).toHaveTextContent('GitHub polling is off for instance dev.');
  });

  it('submits a new endpoint through the modal', async () => {
    const { daemon } = await renderSettings({}, serveEndpoints);

    fireEvent.change(screen.getByLabelText('Endpoint name'), { target: { value: 'gpu-box' } });
    fireEvent.change(screen.getByLabelText('SSH target'), { target: { value: 'user@gpu-box' } });
    await gesture(daemon, () => fireEvent.click(screen.getByText('Add Endpoint')));

    expect(daemon.sentOf('add_endpoint')).toEqual([{ cmd: 'add_endpoint', name: 'gpu-box', ssh_target: 'user@gpu-box' }]);
  });

  it('installs a plugin from a source entered in settings and refreshes the list', async () => {
    const daemon = await openSection('plugins', {}, (scripted) => servePlugins(scripted, []));
    const listsBeforeInstall = daemon.sentOf('list_plugins').length;

    fireEvent.change(screen.getByLabelText('Plugin source'), { target: { value: 'git@ghe.spotify.net:victora/attn-snipe.git' } });
    await gesture(daemon, () => fireEvent.click(screen.getByText('Install Plugin')));

    expect(daemon.sentOf('install_plugin')).toEqual([
      expect.objectContaining({ source: 'git@ghe.spotify.net:victora/attn-snipe.git' }),
    ]);
    expect(daemon.sentOf('list_plugins').length).toBeGreaterThan(listsBeforeInstall);
  });

  it('installs an available bundled plugin', async () => {
    const bundled = installedPlugin({
      name: 'attn-example', dir: '/Applications/attn.app/Contents/Resources/plugins/attn-example', priority: 0,
      connected: false, running: false, availability: 'bundled', installation_state: 'available',
      runtime_state: 'stopped', can_install: true, can_uninstall: false,
    });
    const daemon = await openSection('plugins', {}, (scripted) => servePlugins(scripted, [bundled]));

    expect(screen.getByText('Bundled')).toBeInTheDocument();
    await gesture(daemon, () => fireEvent.click(screen.getByText('Install', { selector: 'button' })));

    expect(daemon.sentOf('install_bundled_plugin')).toEqual([expect.objectContaining({ name: 'attn-example' })]);
  });

  it('shows a linked plugin with its checkout', async () => {
    const linked = installedPlugin({
      name: 'attn-pi', version: '0.2.0', dir: '/Users/me/.attn/plugins/attn-pi', link_target: '/Users/me/src/attn/plugins/attn-pi', priority: 0,
    });
    await openSection('plugins', {}, (scripted) => servePlugins(scripted, [linked]));

    expect(screen.getByText('Linked')).toBeInTheDocument();
    expect(screen.getByText('/Users/me/src/attn/plugins/attn-pi')).toBeInTheDocument();
  });

  it('uninstalls an installed bundled plugin', async () => {
    const bundled = installedPlugin({
      name: 'attn-example', dir: '/Applications/attn.app/Contents/Resources/plugins/attn-example', priority: 0, availability: 'bundled',
    });
    const daemon = await openSection('plugins', {}, (scripted) => servePlugins(scripted, [bundled]));

    await gesture(daemon, () => fireEvent.click(screen.getByText('Uninstall', { selector: 'button' })));

    expect(daemon.sentOf('uninstall_plugin')).toEqual([expect.objectContaining({ name: 'attn-example' })]);
  });

  it('updates provider priority for an installed plugin', async () => {
    const daemon = await openSection('plugins', {}, (scripted) => servePlugins(scripted, [installedPlugin()]));

    const priority = screen.getByLabelText('services-pilot-worktrees priority');
    fireEvent.change(priority, { target: { value: '25' } });
    await gesture(daemon, () => fireEvent.blur(priority));

    expect(daemon.sentOf('set_plugin_priority')).toEqual([
      expect.objectContaining({ name: 'services-pilot-worktrees', priority: 25 }),
    ]);
    expect(screen.getByTestId('settings-plugin-priority-saved-services-pilot-worktrees')).toBeInTheDocument();
  });

  it('refreshes a plugin status when the live daemon snapshot changes', async () => {
    const starting = installedPlugin({
      priority: 0, connected: false, runtime_phase: 'starting', runtime_state: 'starting', health_status: 'unknown',
    });
    const daemon = await openSection('plugins', {}, (scripted) => servePlugins(scripted, [starting]));
    expect(screen.getByText('starting')).toBeInTheDocument();

    daemon.emit({
      event: 'plugins_updated',
      plugins: [{
        ...starting,
        running: false,
        runtime_phase: 'backoff',
        runtime_state: 'degraded',
        restart_attempt: 2,
        next_restart_at: '2026-07-15T22:20:00Z',
        last_exit: '2026-07-15T22:19:59Z: exit code 1',
      }],
    });
    await daemon.idle();

    expect(screen.getByText('degraded')).toBeInTheDocument();
    expect(screen.getByText('Restart attempt').closest('.settings-meta-row')).toHaveTextContent('2');
    expect(screen.getByText(/Last exit: 2026-07-15T22:19:59Z: exit code 1/)).toBeInTheDocument();

    daemon.emit({
      event: 'plugins_updated',
      plugins: [{ ...starting, connected: true, runtime_phase: 'connected', runtime_state: 'connected', health_status: 'healthy' }],
    });
    await daemon.idle();

    expect(screen.getByText('connected')).toBeInTheDocument();
    expect(screen.getAllByText('healthy').length).toBeGreaterThan(0);
  });

  it('toggles tailscale serve on the existing device', async () => {
    const { daemon } = await renderSettings({ settings: {
      tailscale_enabled: 'false',
      tailscale_status: 'disabled',
      tailscale_domain: 'macbook-epidemic.tail1bfe77.ts.net',
    } });

    await gesture(daemon, () => fireEvent.click(screen.getByText('Enable')));

    expect(savedSettings(daemon)).toEqual([['tailscale_enabled', 'true']]);
    expect(screen.getByText(/does not register a second tailnet device/i)).toBeInTheDocument();
  });

  it('does not carry an agent-queue toggle', async () => {
    const daemon = await openSection('workspace', { settings: { queue_mode_enabled: 'false' } });

    expect(screen.queryByTestId('settings-queue-toggle')).toBeNull();
    expect(screen.queryByText('Agent queue')).toBeNull();
    expect(savedSettings(daemon)).toEqual([]);
  });

  it('enables workflows when off and disables them when on', async () => {
    const daemon = await openSection('workflows', { settings: { workflows_enabled: 'false' } });

    expect(screen.getByTestId('settings-workflows-toggle')).toHaveTextContent('Enable');
    await gesture(daemon, () => fireEvent.click(screen.getByTestId('settings-workflows-toggle')));

    expect(screen.getByTestId('settings-workflows-toggle')).toHaveTextContent('Disable');
    await gesture(daemon, () => fireEvent.click(screen.getByTestId('settings-workflows-toggle')));

    expect(savedSettings(daemon)).toEqual([['workflows_enabled', 'true'], ['workflows_enabled', 'false']]);
  });

  it('toggles remote web access for a connected endpoint', async () => {
    const endpoint: Endpoint = {
      id: 'ep-1',
      name: 'gpu-box',
      ssh_target: 'user@gpu-box',
      status: 'connected',
      enabled: true,
      capabilities: {
        protocol_version: '49',
        agents_available: ['codex'],
        tailscale_enabled: false,
        tailscale_status: 'disabled',
        tailscale_domain: 'gpu-box.tail1bfe77.ts.net',
        tailscale_auth_url: 'https://login.tailscale.example/auth',
      },
    };
    const { daemon } = await renderSettings({ endpoints: [endpoint] }, serveEndpoints);

    await gesture(daemon, () => fireEvent.click(screen.getByText('Enable Web')));

    expect(daemon.sentOf('set_endpoint_remote_web')).toEqual([
      expect.objectContaining({ endpoint_id: 'ep-1', enabled: true }),
    ]);
    expect(screen.getByText(/sign this host into tailscale/i)).toBeInTheDocument();
  });

  it('re-bootstraps an enabled endpoint by disabling and re-enabling it', async () => {
    const { daemon } = await renderSettings({
      endpoints: [{ id: 'ep-1', name: 'gpu-box', ssh_target: 'user@gpu-box', status: 'error', enabled: true }],
    }, serveEndpoints);

    await gesture(daemon, () => fireEvent.click(screen.getByText('Re-bootstrap')));

    expect(daemon.sentOf('update_endpoint').map(({ endpoint_id, enabled }) => ({ endpoint_id, enabled }))).toEqual([
      { endpoint_id: 'ep-1', enabled: false },
      { endpoint_id: 'ep-1', enabled: true },
    ]);
  });

  it('shows plugin agents without offering attn-owned executable overrides', async () => {
    await openSection('agents', { settings: { snipe_available: 'true', snipe_cap_resume: 'true' } });

    expect(screen.getByRole('button', { name: 'Snipe' })).toBeInTheDocument();
    expect(screen.getByText('Resume')).toBeInTheDocument();
    expect(document.getElementById('settings-snipe-exec')).toBeNull();
  });
});

describe('SettingsModal model data capture', () => {
  const capture = (enabled: string, bytes: string) => ({
    'model_capture.enabled': enabled,
    'model_capture.interval_seconds': '10',
    'model_capture.max_gb': '5',
    'model_capture.path': '/Users/me/.attn/model-captures',
    'model_capture.bytes': bytes,
  });

  it('shows the privacy boundary and persists capture controls', async () => {
    const daemon = await openSection('data', { settings: capture('false', String(1536 * 1024)) });

    expect(screen.getByText(/Captures exact visible terminal text/)).toBeInTheDocument();
    expect(screen.getByTestId('settings-model-capture-path')).toHaveTextContent('/Users/me/.attn/model-captures');
    expect(screen.getByTestId('settings-model-capture-size')).toHaveTextContent('1.5 MB');

    await gesture(daemon, () => fireEvent.click(screen.getByTestId('settings-model-capture-toggle')));
    await gesture(daemon, () => fireEvent.change(screen.getByTestId('settings-model-capture-interval'), { target: { value: '30' } }));
    await gesture(daemon, () => fireEvent.change(screen.getByTestId('settings-model-capture-max-gb'), { target: { value: '10' } }));

    expect(savedSettings(daemon)).toEqual([
      ['model_capture.enabled', 'true'],
      ['model_capture.interval_seconds', '30'],
      ['model_capture.max_gb', '10'],
    ]);
  });

  it('refreshes captured bytes at the configured cadence only while Data is visible', async () => {
    let bytes = 0;
    const daemon = await openSection('data', { settings: capture('true', '0') }, (scripted) => {
      scripted.on('get_settings', () => {
        bytes += 1024 * 1024;
        return { event: 'settings_updated', settings: capture('true', String(bytes)) };
      });
    });
    expect(daemon.sentOf('get_settings')).toHaveLength(1);
    expect(screen.getByTestId('settings-model-capture-size')).toHaveTextContent('1.0 MB');

    await act(() => vi.advanceTimersByTimeAsync(9_999));
    expect(daemon.sentOf('get_settings')).toHaveLength(1);
    await act(() => vi.advanceTimersByTimeAsync(1));
    await daemon.idle();
    expect(daemon.sentOf('get_settings')).toHaveLength(2);
    expect(screen.getByTestId('settings-model-capture-size')).toHaveTextContent('2.0 MB');

    await gesture(daemon, () => fireEvent.click(screen.getByTestId('settings-nav-general')));
    await act(() => vi.advanceTimersByTimeAsync(10_000));
    expect(daemon.sentOf('get_settings')).toHaveLength(2);
  });
});

describe('SettingsModal notebook folder', () => {
  it('shows the override value and the daemon-resolved effective folder', async () => {
    await openSection('workspace', { settings: { 'notebook.root': '~/my-notes', 'notebook.root.effective': '/Users/me/my-notes' } });

    expect(screen.getByTestId('settings-notebook-root-input')).toHaveValue('~/my-notes');
    expect(screen.getByTestId('settings-notebook-root-effective')).toHaveTextContent('Currently: /Users/me/my-notes');
  });

  it('falls back to the effective default as placeholder when no override is set', async () => {
    await openSection('workspace', { settings: { 'notebook.root.effective': '/Users/me/attn-notebook' } });

    const input = screen.getByTestId('settings-notebook-root-input');
    expect(input).toHaveValue('');
    expect(input).toHaveAttribute('placeholder', '/Users/me/attn-notebook');
  });

  it('persists a new folder on blur and an empty value to restore the default', async () => {
    const daemon = await openSection('workspace', { settings: { 'notebook.root': '~/my-notes', 'notebook.root.effective': '/Users/me/my-notes' } });
    const input = screen.getByTestId('settings-notebook-root-input');

    fireEvent.change(input, { target: { value: '/Users/me/elsewhere' } });
    await gesture(daemon, () => fireEvent.blur(input));
    fireEvent.change(input, { target: { value: '' } });
    await gesture(daemon, () => fireEvent.blur(input));

    expect(savedSettings(daemon)).toEqual([['notebook.root', '/Users/me/elsewhere'], ['notebook.root', '']]);
  });
});

describe('SettingsModal chief settings', () => {
  const agentSection = (section: string, settings: Record<string, string>) =>
    openSection(section, { settings: { claude_available: 'true', codex_available: 'true', ...settings } });

  async function commitField(daemon: ScriptedDaemon, testId: string, value?: string) {
    const input = screen.getByTestId(testId);
    if (value !== undefined) fireEvent.change(input, { target: { value } });
    await gesture(daemon, () => fireEvent.blur(input));
  }

  it('enables auto-approve when off', async () => {
    const daemon = await agentSection('agents', { auto_approve_enabled: 'false' });

    expect(screen.getByTestId('settings-auto-approve-toggle')).toHaveTextContent('Enable');
    await gesture(daemon, () => fireEvent.click(screen.getByTestId('settings-auto-approve-toggle')));
    expect(savedSettings(daemon)).toEqual([['auto_approve_enabled', 'true']]);
  });

  it('disables auto-approve when on', async () => {
    const daemon = await agentSection('agents', { auto_approve_enabled: 'true' });

    expect(screen.getByTestId('settings-auto-approve-toggle')).toHaveTextContent('Disable');
    await gesture(daemon, () => fireEvent.click(screen.getByTestId('settings-auto-approve-toggle')));
    expect(savedSettings(daemon)).toEqual([['auto_approve_enabled', 'false']]);
  });

  it('renders a chief-model input per supported agent and commits a typed model on blur', async () => {
    const daemon = await agentSection('backgroundAgents', {});

    expect(screen.getByTestId('settings-chief-model-codex')).toBeInTheDocument();
    expect(screen.queryByTestId('settings-chief-model-copilot')).toBeNull();
    await commitField(daemon, 'settings-chief-model-claude', 'opus');
    expect(savedSettings(daemon)).toEqual([['chief_model_claude', 'opus']]);
  });

  it('does not write when a chief-model input blurs unchanged', async () => {
    const daemon = await agentSection('backgroundAgents', { chief_model_claude: 'opus' });

    expect(screen.getByTestId('settings-chief-model-claude')).toHaveValue('opus');
    await commitField(daemon, 'settings-chief-model-claude');
    expect(savedSettings(daemon)).toEqual([]);
  });

  it('clears a chief-model override back to the agent default', async () => {
    const daemon = await agentSection('backgroundAgents', { chief_model_codex: 'gpt-5.4' });

    expect(screen.getByTestId('settings-chief-model-codex')).toHaveValue('gpt-5.4');
    await commitField(daemon, 'settings-chief-model-codex', '');
    expect(savedSettings(daemon)).toEqual([['chief_model_codex', '']]);
  });

  it('renders a chief-effort select per supported agent and commits on change', async () => {
    const daemon = await agentSection('backgroundAgents', {});

    expect(screen.getByTestId('settings-chief-effort-codex')).toBeInTheDocument();
    expect(screen.queryByTestId('settings-chief-effort-copilot')).toBeNull();
    await gesture(daemon, () => fireEvent.change(screen.getByTestId('settings-chief-effort-claude'), { target: { value: 'high' } }));
    expect(savedSettings(daemon)).toEqual([['chief_effort_claude', 'high']]);
  });

  it('shows a saved chief-effort override', async () => {
    await agentSection('backgroundAgents', { chief_effort_codex: 'xhigh' });

    expect(screen.getByTestId('settings-chief-effort-codex')).toHaveValue('xhigh');
  });

  it('keeps an agent visible when only its chief-effort override is saved', async () => {
    await agentSection('backgroundAgents', { codex_available: 'false', chief_effort_codex: 'low' });

    expect(screen.getByTestId('settings-chief-effort-codex')).toHaveValue('low');
  });

  it('renders a default-model input per supported agent and commits a typed model on blur', async () => {
    const daemon = await agentSection('agents', {});

    expect(screen.getByTestId('settings-default-model-codex')).toBeInTheDocument();
    expect(screen.queryByTestId('settings-default-model-copilot')).toBeNull();
    await commitField(daemon, 'settings-default-model-claude', 'opus');
    expect(savedSettings(daemon)).toEqual([['default_model_claude', 'opus']]);
  });

  it('does not write when a default-model input blurs unchanged', async () => {
    const daemon = await agentSection('agents', { default_model_claude: 'opus' });

    expect(screen.getByTestId('settings-default-model-claude')).toHaveValue('opus');
    await commitField(daemon, 'settings-default-model-claude');
    expect(savedSettings(daemon)).toEqual([]);
  });

  it('clears a default-model override back to the agent default', async () => {
    const daemon = await agentSection('agents', { default_model_codex: 'gpt-5.4' });

    expect(screen.getByTestId('settings-default-model-codex')).toHaveValue('gpt-5.4');
    await commitField(daemon, 'settings-default-model-codex', '');
    expect(savedSettings(daemon)).toEqual([['default_model_codex', '']]);
  });

  it('renders a default-effort select per supported agent and commits on change', async () => {
    const daemon = await agentSection('agents', {});

    expect(screen.getByTestId('settings-default-effort-codex')).toBeInTheDocument();
    expect(screen.queryByTestId('settings-default-effort-copilot')).toBeNull();
    await gesture(daemon, () => fireEvent.change(screen.getByTestId('settings-default-effort-claude'), { target: { value: 'high' } }));
    expect(savedSettings(daemon)).toEqual([['default_effort_claude', 'high']]);
  });

  it('shows a saved default-effort override', async () => {
    await agentSection('agents', { default_effort_codex: 'xhigh' });

    expect(screen.getByTestId('settings-default-effort-codex')).toHaveValue('xhigh');
  });

  it('keeps an agent visible when only its default-effort override is saved', async () => {
    await agentSection('agents', { codex_available: 'false', default_effort_codex: 'low' });

    expect(screen.getByTestId('settings-default-effort-codex')).toHaveValue('low');
  });

  it('shows the effective context-window caps', async () => {
    await agentSection('backgroundAgents', { chief_context_window_cap: '120000', headless_context_window_cap: '90000' });

    expect(screen.getByTestId('settings-chief-context-cap')).toHaveValue(120000);
    expect(screen.getByTestId('settings-headless-context-cap')).toHaveValue(90000);
  });

  it('defaults both context-window caps to 128000 when unset', async () => {
    await agentSection('backgroundAgents', {});

    expect(screen.getByTestId('settings-chief-context-cap')).toHaveValue(128000);
    expect(screen.getByTestId('settings-headless-context-cap')).toHaveValue(128000);
  });

  it('commits a changed chief context-window cap on blur', async () => {
    const daemon = await agentSection('backgroundAgents', { chief_context_window_cap: '128000' });

    await commitField(daemon, 'settings-chief-context-cap', '100000');
    expect(savedSettings(daemon)).toEqual([['chief_context_window_cap', '100000']]);
  });

  it('commits a changed headless context-window cap on blur', async () => {
    const daemon = await agentSection('backgroundAgents', { headless_context_window_cap: '128000' });

    await commitField(daemon, 'settings-headless-context-cap', '200000');
    expect(savedSettings(daemon)).toEqual([['headless_context_window_cap', '200000']]);
  });

  it('does not re-commit an unchanged context-window cap on blur', async () => {
    const daemon = await agentSection('backgroundAgents', { chief_context_window_cap: '128000' });

    await commitField(daemon, 'settings-chief-context-cap');
    expect(savedSettings(daemon)).toEqual([]);
  });

  it('shows a per-agent context-window cap that is blank when unset', async () => {
    await agentSection('agents', { default_context_window_cap_claude: '800000' });

    expect(screen.getByTestId('settings-default-context-cap-claude')).toHaveValue(800000);
    expect(screen.getByTestId('settings-default-context-cap-codex')).toHaveValue(null);
  });

  it('commits a changed per-agent context-window cap on blur', async () => {
    const daemon = await agentSection('agents', {});

    await commitField(daemon, 'settings-default-context-cap-claude', '800000');
    expect(savedSettings(daemon)).toEqual([['default_context_window_cap_claude', '800000']]);
  });

  it('clears a per-agent context-window cap back to uncapped', async () => {
    const daemon = await agentSection('agents', { default_context_window_cap_claude: '800000' });

    await commitField(daemon, 'settings-default-context-cap-claude', '');
    expect(savedSettings(daemon)).toEqual([['default_context_window_cap_claude', '']]);
  });
});

describe('SettingsModal font size', () => {
  it('shows the app font scale and steps it', async () => {
    const daemon = await openSection('general', { settings: { uiScale: '1.2' } });
    const writesBefore = savedSettings(daemon).length;
    expect(screen.getByTestId('settings-app-font-scale-value')).toHaveTextContent('120%');

    await gesture(daemon, () => fireEvent.click(screen.getByLabelText('Increase app font size')));
    expect(screen.getByTestId('settings-app-font-scale-value')).toHaveTextContent('130%');
    await gesture(daemon, () => fireEvent.click(screen.getByLabelText('Decrease app font size')));
    expect(screen.getByTestId('settings-app-font-scale-value')).toHaveTextContent('120%');

    expect(savedSettings(daemon).slice(writesBefore)).toEqual([['uiScale', '1.3'], ['uiScale', '1.2']]);
  });

  it('offers an app reset only when the scale is not the default', async () => {
    const daemon = await openSection('general', { settings: { uiScale: '1.3' } });
    const writesBefore = savedSettings(daemon).length;

    await gesture(daemon, () => fireEvent.click(screen.getByText('Reset')));

    expect(screen.getByTestId('settings-app-font-scale-value')).toHaveTextContent('100%');
    expect(screen.queryByText('Reset')).toBeNull();
    expect(savedSettings(daemon).slice(writesBefore)).toEqual([['uiScale', '1']]);
  });

  it('shows the garden matching the app by default, without a Match app button', async () => {
    await openSection('general');

    expect(screen.getByTestId('settings-garden-font-scale-value')).toHaveTextContent('Match app');
    expect(screen.queryByText('Match app', { selector: 'button' })).not.toBeInTheDocument();
  });

  it('shows an overridden garden scale and reverts it via Match app', async () => {
    const daemon = await openSection('general', { settings: { gardenScale: '1.3' } });
    expect(screen.getByTestId('settings-garden-font-scale-value')).toHaveTextContent('130%');

    await gesture(daemon, () => fireEvent.click(screen.getByLabelText('Increase garden font size')));
    expect(screen.getByTestId('settings-garden-font-scale-value')).toHaveTextContent('140%');
    await gesture(daemon, () => fireEvent.click(screen.getByText('Match app', { selector: 'button' })));
    expect(screen.getByTestId('settings-garden-font-scale-value')).toHaveTextContent('Match app');

    expect(savedSettings(daemon)).toEqual([['gardenScale', '1.4'], ['gardenScale', '']]);
  });
});

describe('SettingsModal automation handle', () => {
  it('registers the handle while the app is mounted and clears it on unmount', async () => {
    const { unmount } = await renderApp();

    expect(getSettingsAutomationHandle()).not.toBeNull();

    unmount();
    expect(getSettingsAutomationHandle()).toBeNull();
  });

  it('reports open state, active section, and search text through getState', async () => {
    await renderSettings();

    expect(getSettingsAutomationHandle()?.getState()).toEqual({
      open: true,
      activeSection: 'connectivity',
      search: '',
    });

    fireEvent.change(screen.getByLabelText('Search settings'), { target: { value: 'theme' } });
    fireEvent.click(screen.getByTestId('settings-nav-general'));

    expect(getSettingsAutomationHandle()?.getState()).toEqual({
      open: true,
      activeSection: 'general',
      search: 'theme',
    });
  });

  it('reports open: false while settings is closed', async () => {
    await renderApp();

    expect(getSettingsAutomationHandle()?.getState()).toEqual({
      open: false,
      activeSection: 'connectivity',
      search: '',
    });
  });

  it('selectSection switches the rendered section the same way a nav click does', async () => {
    const { daemon } = await renderSettings();

    await act(async () => { await getSettingsAutomationHandle()?.selectSection('agents'); });
    await daemon.idle();

    expect(screen.getByTestId('settings-section-agents')).toBeInTheDocument();
    expect(getSettingsAutomationHandle()?.getState().activeSection).toBe('agents');
  });

  it('selectSection throws a clear error for an unknown section id', async () => {
    await renderSettings();

    expect(() => getSettingsAutomationHandle()?.selectSection('nonexistent')).toThrow(
      /unknown settings section "nonexistent"/,
    );
  });

  it('hydrates the auto-settle fields when settings arrive after opening', async () => {
    const daemon = await openSection('hygiene');
    expect(screen.getByTestId('settings-auto-settle-arm')).toHaveValue(30);

    daemon.emit({ event: 'settings_updated', settings: { auto_settle_arm_seconds: '60', auto_settle_countdown_seconds: '20' } });
    await daemon.idle();

    expect(screen.getByTestId('settings-auto-settle-arm')).toHaveValue(60);
    expect(screen.getByTestId('settings-auto-settle-countdown')).toHaveValue(20);
    await gesture(daemon, () => fireEvent.blur(screen.getByTestId('settings-auto-settle-arm')));
    expect(savedSettings(daemon)).toEqual([]);
  });
});

describe('SettingsModal sent files', () => {
  it('reads as on by default and toggles off', async () => {
    const daemon = await openSection('workspace');

    expect(screen.getByTestId('settings-open-sent-files-toggle')).toHaveTextContent('Disable');
    await gesture(daemon, () => fireEvent.click(screen.getByTestId('settings-open-sent-files-toggle')));
    expect(savedSettings(daemon)).toEqual([['open_sent_files_enabled', 'false']]);
  });

  it('re-enables when off', async () => {
    const daemon = await openSection('workspace', { settings: { open_sent_files_enabled: 'false' } });

    expect(screen.getByTestId('settings-open-sent-files-toggle')).toHaveTextContent('Enable');
    await gesture(daemon, () => fireEvent.click(screen.getByTestId('settings-open-sent-files-toggle')));
    expect(savedSettings(daemon)).toEqual([['open_sent_files_enabled', 'true']]);
  });
});

describe('SettingsModal delegation badge', () => {
  it('counts live roles as soon as Settings opens, before the section is visited', async () => {
    const selection = { harness: 'codex', provider: '', model: '', effort: '' };
    const role = (id: string) => ({ id, name: id, icon: 'code', enabled: true, description: '', instructions: '', stopping_point: '', default_choice_id: 'default', choices: [{ id: 'default', name: 'Everyday', when: '', selection }] });
    const { daemon } = await renderSettings({}, (scripted) => {
      scripted.on('delegation_preferences_get', () => ({
        event: 'delegation_preferences_result',
        success: true,
        preferences: { enabled: true, revision: 3, workflow_skill_enabled: false, roles: [role('build'), role('review')], fallback: { selection: { ...selection, harness: '' }, instructions: '' } },
        templates: [],
        expanded_roles: [],
        harnesses: [],
        workflow_skill_paths: [],
      }));
    });

    expect(screen.getByTestId('settings-nav-delegation')).toHaveTextContent('2');
    expect(daemon.sentOf('delegation_preferences_get')).toHaveLength(1);
  });
});

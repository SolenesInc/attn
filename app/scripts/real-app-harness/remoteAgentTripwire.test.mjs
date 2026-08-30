import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { spawnSync } from 'node:child_process';

import { afterEach, beforeEach, describe, expect, it } from 'vitest';

import {
  armRemoteAgentTripwire,
  buildRemoteAgentTripwire,
  collectRemoteAgentTripwire,
  installLocalRemoteMockCommand,
  REMOTE_MOCK_AGENT_COMMAND,
  verifyRemoteDaemonTripwire,
  writeRemoteMockAgentFixture,
} from './remoteAgentTripwire.mjs';

let tmpDir;
let fixture;
const shimSource = path.join(process.cwd(), 'scripts/real-app-harness/remote-agent-tripwire-shim.sh');

beforeEach(() => {
  tmpDir = fs.mkdtempSync(path.join(os.tmpdir(), 'remote-agent-tripwire-test-'));
  fixture = buildRemoteAgentTripwire({
    remoteHome: '/home/attn-remote',
    remotePaths: {
      remoteHarnessRoot: '/home/attn-remote/.attn/harness/run-1',
      remoteHarnessBinary: '/home/attn-remote/.attn/harness/run-1/bin/attn',
    },
    scenarioId: 'TR-502',
  });
});

afterEach(() => {
  fs.rmSync(tmpDir, { recursive: true, force: true });
});

describe('remote agent tripwire', () => {
  it('pins the remote daemon to provisioned shims and turns headless tasks off', () => {
    expect(fixture.launchEnv).toMatchObject({
      ATTN_REMOTE_DATA_DIR: '/home/attn-remote/.attn/harness/run-1',
      ATTN_REMOTE_PATH_PREFIX: '/home/attn-remote/.attn/real-app-harness/agent-fixtures/bin',
      ATTN_REMOTE_HEADLESS_TASKS: 'off',
      ATTN_REMOTE_AGENT_TRIPWIRE_LEDGER: fixture.ledgerPath,
      ATTN_REMOTE_CODEX_EXECUTABLE: '/home/attn-remote/.attn/real-app-harness/agent-fixtures/bin/codex',
    });
  });

  it('quotes a remote cwd containing an apostrophe', async () => {
    let command = '';
    await writeRemoteMockAgentFixture({
      target: 'fixture@orb',
      cwd: "/home/attn-remote/it's-here",
      config: { turns: [] },
      remote: async (_target, value) => { command = value; },
    });

    expect(command).toContain("'/home/attn-remote/it'\\''s-here'");
  });

  it('installs a local command with the same name the remote PATH resolves', () => {
    expect(installLocalRemoteMockCommand({ dir: tmpDir })).toBe(REMOTE_MOCK_AGENT_COMMAND);
    expect(fs.statSync(path.join(tmpDir, REMOTE_MOCK_AGENT_COMMAND)).mode & 0o111).not.toBe(0);
  });

  it('records and refuses an agent command on the fixture VM', () => {
    const shim = path.join(tmpDir, 'codex');
    const ledger = path.join(tmpDir, 'remote.ledger');
    fs.copyFileSync(shimSource, shim);
    fs.chmodSync(shim, 0o755);

    const result = spawnSync(shim, ['exec', 'hello world'], {
      encoding: 'utf8',
      env: {
        ...process.env,
        ATTN_AGENT_TRIPWIRE_SCENARIO: 'TR-502',
        ATTN_AGENT_TRIPWIRE_LEDGER: ledger,
      },
    });

    expect(result.status).toBe(97);
    expect(result.stderr).toContain('scenario TR-502 must not run the real codex');
    expect(fs.readFileSync(ledger, 'utf8')).toBe('TR-502\tcodex exec hello world\n');
  });

  it('arms only after every provisioned executable exists', async () => {
    const commands = [];
    const writes = [];
    await armRemoteAgentTripwire({
      target: 'fixture@orb',
      fixture,
      runner: { writeJson: (...args) => writes.push(args) },
      remote: async (_target, command) => commands.push(command),
      installLocalMock: () => REMOTE_MOCK_AGENT_COMMAND,
    });

    expect(commands[0]).toContain('real-app:provision-remote');
    expect(commands[0]).toContain(fixture.mockAgentPath);
    expect(commands[0]).toContain(fixture.ledgerPath);
    expect(writes[0][0]).toBe('remote-agent-tripwire-armed.json');
  });

  it('writes a validated mock config into the remote session cwd', async () => {
    let command = '';
    const configPath = await writeRemoteMockAgentFixture({
      target: 'fixture@orb',
      cwd: '/home/attn-remote/workspace',
      config: { name: 'remote mock', turns: [], defaultActions: [] },
      remote: async (_target, value) => { command = value; },
    });

    expect(configPath).toBe('/home/attn-remote/workspace/.attn-mock-agent.json');
    expect(command).toContain('base64 -d');
    expect(command).toContain('.attn-mock-agent.json');
  });

  it('proves the remote daemon inherited the shims and switch', async () => {
    const daemon = { pid: 42, cmdline: `${fixture.remoteHarnessBinary} daemon` };
    const environment = [
      `PATH=${fixture.binDir}:/usr/bin`,
      'ATTN_HEADLESS_TASKS=off',
      `ATTN_AGENT_TRIPWIRE=${fixture.marker}`,
      `ATTN_AGENT_TRIPWIRE_LEDGER=${fixture.ledgerPath}`,
      'ATTN_AGENT_TRIPWIRE_SCENARIO=TR-502',
      ...['claude', 'codex', 'copilot', 'pi'].map((name) => `ATTN_${name.toUpperCase()}_EXECUTABLE=${fixture.binDir}/${name}`),
    ].join('\n');
    const receipt = await verifyRemoteDaemonTripwire({
      target: 'fixture@orb',
      fixture,
      remote: async () => environment,
      listProcesses: async () => [daemon],
    });

    expect(receipt).toMatchObject({ ok: true, pid: 42, headlessTasks: 'off', carriesMarker: true });
  });

  it('refuses a clean-looking run whose daemon missed the switch', async () => {
    await expect(verifyRemoteDaemonTripwire({
      target: 'fixture@orb',
      fixture,
      remote: async () => `PATH=${fixture.binDir}:/usr/bin\nATTN_AGENT_TRIPWIRE=${fixture.marker}`,
      listProcesses: async () => [{ pid: 42, cmdline: `${fixture.remoteHarnessBinary} daemon` }],
    })).rejects.toThrow('ATTN_HEADLESS_TASKS reads null, want "off"');
  });

  it('copies the remote ledger into artifacts and fails on any exec', async () => {
    const writes = [];
    await expect(collectRemoteAgentTripwire({
      target: 'fixture@orb',
      fixture,
      runner: { writeText: (...args) => writes.push(args) },
      remote: async () => 'TR-502\tcodex exec hi\n',
    })).rejects.toThrow('1 real agent exec(s) during TR-502');
    expect(writes).toEqual([['remote-agent-tripwire.ledger', 'TR-502\tcodex exec hi\n']]);
  });
});

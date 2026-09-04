#!/usr/bin/env node
import fs from 'node:fs';
import path from 'node:path';
import { execFileSync } from 'node:child_process';
import { launchFreshAppAndConnect, parseCommonArgs } from './common.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import { createScenarioRunner } from './scenarioRunner.mjs';
import { waitForFirstWorkspacePane, waitForPaneText } from './scenarioAssertions.mjs';
import { currentHarnessProfile, profileCliEnv } from './harnessProfile.mjs';
import { startStubWorld, scriptedAgent, stubAgentModel, stubJudgeModel, resolveAttnBinary, waitForPiPreflight } from './piStubProvider.mjs';

const delay = (ms) => new Promise((resolve) => setTimeout(resolve, ms));
const quote = (value) => `'${value.replace(/'/g, `'"'"'`)}'`;
const token = `ghp_${'s'.repeat(36)}`;
const args = process.argv.slice(2);
if (args[0] === '--') args.shift();
const options = parseCommonArgs(args);
const profile = currentHarnessProfile();
if (!profile) throw new Error('Pi security verification requires a named non-production profile');
let repoDir;
let outside;
let standardCache;
let initialSettings;
let world;
let refuseScope = false;
const buildCommand = () => 'node build.cjs';
const cacheRequest = () => ({ allowWrite: [outside], reason: 'The build writes compiled output to this cache directory.' });
world = await startStubWorld({
  scenario: 'pi-security', appPath: options.appPath, profile,
  judge: () => refuseScope ? { verdict: 'deny', reason: 'Write access to this shared build cache needs your explicit approval.' } : { verdict: 'allow' },
  agent: scriptedAgent([
    { when: 'use configured build cache', tools: () => [{ name: 'bash', args: { command: 'node build-standard.cjs' } }], text: (request) => `PRESET-${request.prompt.split(' ').at(-1)}-DONE` },
    { when: 'recover the build cache', tools: (request) => {
      if (request.toolResults.length && !request.toolResults[0].includes('retry bash with sandbox:')) throw new Error('The agent was not told how to request a scoped retry');
      return [
        { name: 'bash', args: { command: buildCommand() } },
        { name: 'bash', args: { command: buildCommand(), sandbox: cacheRequest() } },
        { name: 'bash', args: { command: `printf forbidden > ${quote(path.join(outside, 'scope-leaked.txt'))}` } },
      ];
    }, text: 'The build passed after auto mode approved its cache access for one execution. Later commands still have the original restrictions. CACHE-RECOVERY-DONE' },
    { when: 'request the shared cache', tools: () => [{ name: 'bash', args: { command: buildCommand(), sandbox: cacheRequest() } }], text: 'Auto mode refused the cache access. I need to run node build.cjs with write access to the named cache directory. Please approve that command and access so I can submit it for review again. CACHE-REFUSED-DONE' },
    { when: 'I approve running node build.cjs', tools: () => [{ name: 'bash', args: { command: buildCommand(), sandbox: cacheRequest() } }], text: 'Auto mode approved the retry after your confirmation, and the build passed. CACHE-APPROVED-DONE' },
    { when: 'scoped retry with auto off', tools: () => [{ name: 'bash', args: { command: `printf forbidden > ${quote(path.join(outside, 'off-scope.txt'))}`, sandbox: cacheRequest() } }], text: 'Auto mode is off, so extra access was not granted. The user can enable auto mode or grant the directory. CACHE-OFF-DONE' },
    { when: 'initial security check', tools: () => [
      { name: 'write', args: { path: 'notes.txt', content: 'local work succeeded\n' } },
      { name: 'bash', args: { command: `printf '%s\\n' '${token}'; printf forbidden > ${quote(path.join(outside, 'blocked.txt'))}` } },
      { name: 'read', args: { path: path.join(world.agentDir, 'fixture-secret.txt') } },
    ], text: 'SECURITY-INITIAL-DONE' },
    { when: 'outside after auto off', tools: () => [{ name: 'bash', args: { command: `printf forbidden > ${quote(path.join(outside, 'auto-off.txt'))}` } }], text: 'SECURITY-AUTO-OFF-DONE' },
    { when: 'write using grant', tools: () => [{ name: 'write', args: { path: path.join(outside, 'granted.txt'), content: 'explicit grant worked\n' } }], text: 'SECURITY-GRANT-DONE' },
    { when: 'write after revoke', tools: () => [{ name: 'write', args: { path: path.join(outside, 'revoked.txt'), content: 'must not exist' } }], text: 'SECURITY-REVOKE-DONE' },
    { when: 'network while provider works', tools: () => [{ name: 'bash', args: { command: `curl --max-time 3 -sS ${world.stub.baseUrl}/ok` } }], text: 'SECURITY-NETWORK-DONE' },
    { when: 'after new', tools: [{ name: 'write', args: { path: 'after-new.txt', content: 'new session works\n' } }], text: 'SECURITY-NEW-DONE' },
  ]),
});
const runner = createScenarioRunner(options, {
  scenarioId: 'PI-SECURITY', prefix: 'pi-security', tier: 'tier2-local-real-agent',
  allowRealAgents: ['pi'], preflightLaunchEnv: world.launchEnv,
  metadata: { focus: 'sandbox, secret filtering, user grants, auto independence, session replacement', model: 'loopback stub' },
});
const client = new UiAutomationClient({ appPath: options.appPath, launchEnv: world.launchEnv });
const observer = new DaemonObserver(options);
let sessionId;
let paneId;
const attn = (args) => execFileSync(resolveAttnBinary(options.appPath), args, { env: profileCliEnv(profile, world.launchEnv), encoding: 'utf8' });
const submit = async (text) => {
  await client.request('write_pane', { sessionId, paneId, text, submit: false });
  await delay(800);
  await client.request('write_pane', { sessionId, paneId, text: '\r', submit: false });
};
const expectPane = (text) => waitForPaneText(client, sessionId, paneId, (pane) => pane.replace(/\s+/g, ' ').includes(text), text, 90_000);
const prompt = async (text, marker) => { await submit(text); return expectPane(marker); };
const key = (text) => client.request('write_pane', { sessionId, paneId, text, submit: false });
const selection = (text) => text.split('\n').map((line) => line.trim()).find((line) => line.startsWith('→ '));
const selectSecurity = async (label) => {
  let pane = await client.request('read_pane_text', { sessionId, paneId });
  for (let i = 0; i < 35; i++) {
    const before = selection(pane.text);
    if (before?.includes(label)) return;
    await key('\x1b[B');
    pane = await waitForPaneText(client, sessionId, paneId, (text) => selection(text) !== before, 'security selection moved');
  }
  throw new Error(`Security setting was not found: ${label}\n${pane.text}`);
};
const chooseSecurity = async (label, expected) => {
  await selectSecurity(label);
  await key('\r');
  if (expected) await expectPane(expected);
};
const openSecurity = async () => { await submit('/security'); await expectPane('↑↓ · Enter · Esc close'); };
const closeSecurity = async () => {
  await key('\x1b');
  await waitForPaneText(client, sessionId, paneId, (text) => !text.includes('↑↓ · Enter · Esc close'), 'security panel closed');
};
const toggleSecurity = async (label, expected) => {
  await openSecurity();
  await chooseSecurity(label, expected);
  await expectPane('Saved. Active in this session.');
  await closeSecurity();
};
try {
  repoDir = path.join(runner.sessionDir, 'project');
  outside = path.join(runner.sessionDir, 'outside');
  fs.mkdirSync(repoDir, { recursive: true });
  fs.mkdirSync(outside, { recursive: true });
  standardCache = path.join(runner.sessionDir, 'standard-cache');
  initialSettings = JSON.stringify({ buildCaches: { enabled: true, paths: [standardCache] } });
  fs.writeFileSync(path.join(world.agentDir, 'attn-security.json'), initialSettings);
  fs.writeFileSync(path.join(repoDir, 'build-standard.cjs'), `const fs = require('node:fs'); fs.writeFileSync(${JSON.stringify(path.join(standardCache, 'artifact'))}, 'compiled'); console.log('PRESET-BUILD-PASSED');`);
  fs.writeFileSync(path.join(repoDir, 'build.cjs'), `const fs = require('node:fs'); fs.writeFileSync(${JSON.stringify(path.join(outside, 'build-artifact'))}, 'compiled'); console.log('BUILD-PASSED');`);
  fs.writeFileSync(path.join(world.agentDir, 'fixture-secret.txt'), 'PRIVATE-FIXTURE-MUST-NOT-APPEAR');
  await world.launch({ client, observer, runner, pinModelFor: 'pi', launchApp: async () => {
    try { attn(['plugin', 'install-bundled', 'attn-pi']); }
    catch (error) { if (!String(error.stderr).includes('is already installed')) throw error; }
    await launchFreshAppAndConnect(client, observer);
  } });
  runner.writeJson('app-build.json', (await client.request('get_state')).appBuild);
  await waitForPiPreflight({
    run: () => attn(['preflight', '--agent', 'pi', '--model', stubAgentModel, '--json']),
    save: (attempts) => {
      runner.writeJson('pi-preflight-attempts.json', attempts);
      runner.writeJson('pi-preflight.json', attempts.at(-1).report ?? null);
    },
  });
  const proposed = JSON.parse(attn(['automode', 'model', stubJudgeModel, '--json']));
  observer.send({ cmd: 'automode_promote', id: proposed.proposal?.id ?? proposed.id, request_id: 'security-model' });
  for (let i = 0; ; i++) {
    const config = JSON.parse(attn(['automode', 'show', '--json']));
    if (config.config?.models?.includes(stubJudgeModel)) break;
    if (i > 60) throw new Error('Stub classifier was not configured');
    await delay(250);
  }
  const created = await client.request('create_session', { cwd: repoDir, label: 'Pi security verification', agent: 'pi' });
  sessionId = created.sessionId;
  await observer.waitForSession({ id: sessionId, timeoutMs: 30_000 });
  paneId = (await waitForFirstWorkspacePane(client, sessionId, 'Pi pane', 30_000)).paneId;
  await expectPane('sandbox: on');
  await runner.step('security_panel_edits_paths_and_explains_protections', async () => {
    await openSecurity();
    runner.writeText('security-ui-overview.txt', (await expectPane('Credentials filtered')).text);
    await chooseSecurity('Cache directories', 'Security / Cache directories');
    await chooseSecurity('+ Add path', 'Security / Add path');
    await key('/');
    await key('\r');
    await expectPane('Name a specific build cache');
    runner.assert(JSON.parse(fs.readFileSync(path.join(world.agentDir, 'attn-security.json'))).buildCaches.paths.length === 1, 'Invalid cache entry did not change saved settings');
    await key('\x15');
    const custom = path.join(runner.sessionDir, 'ui-cache');
    await key(custom);
    await key('\r');
    await expectPane('Saved. Active in this session.');
    runner.assert(fs.existsSync(custom), 'Adding a cache in the panel created the directory');
    await chooseSecurity('/ui-cache', 'Edit path…');
    await chooseSecurity('Edit path', 'Security / Edit path');
    await key('\x15');
    const edited = path.join(runner.sessionDir, 'ui-cache-edited');
    await key(edited);
    await key('\r');
    await expectPane('Saved. Active in this session.');
    runner.assert(JSON.parse(fs.readFileSync(path.join(world.agentDir, 'attn-security.json'))).buildCaches.paths.includes(edited), 'Editing replaced the configured cache path');
    await chooseSecurity('/ui-cache-edited', 'Edit path…');
    await chooseSecurity('Remove from this list', 'Security / Cache directories');
    await expectPane('Saved. Active in this session.');
    runner.assert(fs.existsSync(edited), 'Removing the grant kept the cache directory');
    await chooseSecurity('Restore standard cache preset', 'Keep current cache paths');
    await key('\x1b');
    await expectPane('Security / Cache directories');
    runner.assert(JSON.parse(fs.readFileSync(path.join(world.agentDir, 'attn-security.json'))).buildCaches.paths.length === 1, 'Cancelling preset restoration preserved the cache list');
    await key('\x1b');
    await chooseSecurity('Protected writes', 'Security / Protected writes');
    await chooseSecurity('/.pi (built-in)', 'Built-in write protection cannot be removed');
    runner.writeText('security-ui-protected.txt', (await expectPane('Built-in write protection')).text);
    await key('\x1b');
    await expectPane('+ Add path');
    await key('\x1b');
    await expectPane('↑↓ · Enter · Esc close');
    await closeSecurity();
    initialSettings = fs.readFileSync(path.join(world.agentDir, 'attn-security.json'), 'utf8');
  });
  await runner.step('agent_recovers_from_cache_denial', async () => {
    await prompt('recover the build cache', 'CACHE-RECOVERY-DONE');
    runner.assert(fs.readFileSync(path.join(outside, 'build-artifact'), 'utf8') === 'compiled', 'Scoped review allowed the actual build cache write');
    runner.assert(!fs.existsSync(path.join(outside, 'scope-leaked.txt')), 'The next command did not inherit the grant');
    const results = world.stub.calls.agent.at(-1).toolResults;
    runner.assert(results[0].includes('retry bash with sandbox:') && results[1].includes('approved temporary sandbox access'), 'Agent received recovery guidance and the reviewed execution result');
    runner.assert(world.stub.calls.judge.some((call) => call.prompt.includes('sandbox request:') && call.prompt.includes(outside)), 'Classifier reviewed command and exact cache scope together');
  });
  await runner.step('refused_scope_can_be_reconsidered_after_user_reply', async () => {
    fs.unlinkSync(path.join(outside, 'build-artifact'));
    refuseScope = true;
    await prompt('request the shared cache', 'CACHE-REFUSED-DONE');
    runner.assert(!fs.existsSync(path.join(outside, 'build-artifact')), 'Refused scope did not run the build');
    runner.assert(world.stub.calls.agent.at(-1).toolResults[0].includes('retry the same bash request'), 'Agent was told how to escalate the refusal conversationally');
    refuseScope = false;
    await prompt(`I approve running node build.cjs with write access to ${outside}. Retry the build.`, 'CACHE-APPROVED-DONE');
    runner.assert(fs.existsSync(path.join(outside, 'build-artifact')), 'Reviewed retry after user confirmation ran the build');
    runner.assert(world.stub.calls.judge.at(-1).prompt.includes('I approve running node build.cjs'), 'Classifier received the user confirmation');
    runner.assert(fs.readFileSync(path.join(world.agentDir, 'attn-security.json'), 'utf8') === initialSettings, 'Scoped approvals did not change persistent security grants');
  });
  await runner.step('configured_caches_work_and_can_be_disabled', async () => {
    await prompt('use configured build cache FIRST', 'PRESET-FIRST-DONE');
    runner.assert(fs.readFileSync(path.join(standardCache, 'artifact'), 'utf8') === 'compiled', 'Configured cache worked without requesting extra access');
    runner.assert(world.stub.calls.agent.at(-1).systemPrompt.includes(standardCache), 'Agent instructions include active cache grants');
    runner.assert(world.stub.calls.judge.at(-1).systemPrompt.includes(standardCache), 'Reviewer received cache grants from the executor');
    fs.unlinkSync(path.join(standardCache, 'artifact'));
    await toggleSecurity('Build-cache access', 'Build-cache access · off');
    await prompt('use configured build cache OFF', 'PRESET-OFF-DONE');
    runner.assert(!fs.existsSync(path.join(standardCache, 'artifact')), 'Disabling caches restored the OS restriction');
    runner.assert(world.stub.calls.agent.at(-1).systemPrompt.includes('Build-cache grants: disabled'), 'Next turn sees disabled cache grants');
    await toggleSecurity('Build-cache access', 'Build-cache access · on');
    await prompt('use configured build cache AGAIN', 'PRESET-AGAIN-DONE');
    runner.assert(fs.existsSync(path.join(standardCache, 'artifact')), 'Re-enabling caches restored normal build behavior');
  });
  await runner.step('approved_actions_remain_contained', async () => {
    await prompt('initial security check', 'SECURITY-INITIAL-DONE');
    runner.assert(fs.readFileSync(path.join(repoDir, 'notes.txt'), 'utf8').includes('succeeded'), 'Native write worked');
    runner.assert(!fs.existsSync(path.join(outside, 'blocked.txt')), 'Approved shell write was contained');
    const results = world.stub.calls.agent.flatMap((call) => call.toolResults);
    runner.assert(results.some((text) => text.includes('REDACTED')), 'Tool output was filtered');
    runner.assert(results.some((text) => /blocked read|Operation not permitted|Permission denied/.test(text)), 'Private read was denied');
    runner.assert(!JSON.stringify(world.stub.calls).includes('PRIVATE-FIXTURE-MUST-NOT-APPEAR'), 'Private contents never reached a model');
    runner.assert(!JSON.stringify(world.stub.calls).includes(token), 'Synthetic token never reached a model or classifier');
  });
  await runner.step('auto_off_keeps_sandbox', async () => {
    await submit('/auto off');
    await expectPane('auto: off');
    await prompt('outside after auto off', 'SECURITY-AUTO-OFF-DONE');
    runner.assert(!fs.existsSync(path.join(outside, 'auto-off.txt')), 'Turning auto off did not disable containment');
    await prompt('scoped retry with auto off', 'CACHE-OFF-DONE');
    runner.assert(world.stub.calls.agent.at(-1).systemPrompt.includes('Auto-mode access review: unavailable'), 'Agent instructions follow the auto-mode toggle');
    runner.assert(!fs.existsSync(path.join(outside, 'off-scope.txt')), 'Auto off did not approve a scoped request');
    runner.assert(world.stub.calls.agent.at(-1).toolResults[0].includes('auto mode is off'), 'Agent received the reason extra access was unavailable');
  });
  await runner.step('grant_and_revoke_write_access', async () => {
    await openSecurity();
    await chooseSecurity('Extra writable directories', 'Security / Extra writable directories');
    await chooseSecurity('+ Add path', 'Security / Add path');
    await key(outside);
    await key('\r');
    await expectPane('Saved. Active in this session.');
    await key('\x1b');
    await expectPane('↑↓ · Enter · Esc close');
    await closeSecurity();
    await prompt('write using grant', 'SECURITY-GRANT-DONE');
    runner.assert(fs.existsSync(path.join(outside, 'granted.txt')), 'Explicit write grant worked');
    await openSecurity();
    await chooseSecurity('Extra writable directories', 'Security / Extra writable directories');
    await chooseSecurity('/outside', 'Edit path…');
    await chooseSecurity('Remove from this list', 'Security / Extra writable directories');
    await expectPane('Saved. Active in this session.');
    await key('\x1b');
    await expectPane('↑↓ · Enter · Esc close');
    await closeSecurity();
    await prompt('write after revoke', 'SECURITY-REVOKE-DONE');
    runner.assert(!fs.existsSync(path.join(outside, 'revoked.txt')), 'Revoking a grant restored containment');
  });
  await runner.step('tool_network_is_independent_of_provider', async () => {
    await toggleSecurity('Tool network', 'Tool network · blocked');
    await prompt('network while provider works', 'SECURITY-NETWORK-DONE');
    const result = world.stub.calls.agent.at(-1).toolResults.join('\n');
    runner.assert(!result.includes('STUB-OK') && /denied|Failed|not permitted|Couldn't connect/i.test(result), 'Tool network was blocked while model requests continued');
  });
  await runner.step('new_session_rebuilds_security', async () => {
    await submit('/new');
    await delay(1000);
    await prompt('after new', 'SECURITY-NEW-DONE');
    runner.assert(fs.existsSync(path.join(repoDir, 'after-new.txt')), 'New session got working protected tools');
    await submit('/security status');
    const pane = await expectPane('credential filtering: on');
    runner.writeText('security-pane.txt', pane.text);
    runner.writeJson('security-settings.json', JSON.parse(fs.readFileSync(path.join(world.agentDir, 'attn-security.json'), 'utf8')));
  });
  await runner.finishSuccess({ sessionId, judgeCalls: world.stub.calls.judge.length, agentCalls: world.stub.calls.agent.length });
} catch (error) {
  await runner.finishFailure(error, { sessionId });
  process.exitCode = 1;
} finally {
  if (sessionId) await client.request('close_session', { sessionId }).catch(() => {});
  await client.quitApp().catch(() => {});
  await observer.close().catch(() => {});
  await world.close();
}

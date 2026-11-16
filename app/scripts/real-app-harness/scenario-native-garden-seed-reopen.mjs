#!/usr/bin/env node

import crypto from 'node:crypto';
import path from 'node:path';
import { execFileSync } from 'node:child_process';
import { launchFreshNativeAppAndConnect, parseCommonArgs } from './common.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import { currentHarnessInstance, instanceCliEnv } from './harnessInstance.mjs';
import { MOCK_AGENT_MODEL, writeMockAgentFixture } from './mockAgent.mjs';
import { appCliInTree, createWindowDriver } from './platform.mjs';
import { createScenarioRunner } from './scenarioRunner.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';

const options = parseCommonArgs(process.argv.slice(2));
const runner = createScenarioRunner(options, {
  scenarioId: 'NATIVE-GARDEN-SEED-REOPEN', tier: 'tier2-local-mock-agent', prefix: 'native-garden-seed-reopen',
  metadata: { focus: 'A closed delegate resumes from its seed in the native garden and reports to it again, then Handover gives the same seed to a new agent with the user’s note' },
});
const client = new UiAutomationClient({ appPath: options.appPath });
const observer = new DaemonObserver({ wsUrl: options.wsUrl });
const driver = createWindowDriver({ appPath: options.appPath, client });
runner.registerCleanup('close_observer', () => observer.close());
runner.registerCleanup('quit_app', () => client.quitApp());
runner.registerFailureEvidence('native_screenshot', () => driver.screenshot(path.join(runner.runDir, 'failure-native.png')));

const instance = currentHarnessInstance();
const attn = (...args) => execFileSync(appCliInTree(options.appPath), args, { encoding: 'utf8', env: instanceCliEnv(instance) });
const BRIEF = 'Reply with exactly GSREOPEN_READY and then wait for the user.';
const HANDOFF = 'Continue this same seed from the resumed conversation.';

async function newSession(known, description) {
  let spawned;
  await observer.waitFor(() => {
    spawned = [...observer.sessionsById.keys()].find((id) => !known.has(id));
    return Boolean(spawned);
  }, description, 120_000);
  runner.registerCleanup(`unregister_${spawned}`, () => observer.unregisterSession(spawned));
  return spawned;
}

async function reportsTo(session, seedId) {
  const placed = await observer.waitForDesktopOf(session, (d) => d.panes.some((p) => p.session_id === session) && d, 'the session is placed', 60_000);
  const pane = placed.panes.find((p) => p.session_id === session).pane_id;
  const chip = await driver.waitFor({ key: `tile.${pane}.seed` });
  const seed = JSON.parse(attn('seed', 'show', seedId, '--json')).seed;
  runner.assert(chip.name?.startsWith(`${seed.title},`), `${session} reports to the seed`, { chip, seed: seed.title });
}

try {
  await runner.step('launch_app', () => launchFreshNativeAppAndConnect(client, observer));
  await runner.step('widen_the_window', () => driver.setWindowBounds({ x: 0, y: 0, width: 1600, height: 1000 }));

  let desktop;
  await runner.step('make_a_desktop', async () => {
    const before = observer.currentDesktopId();
    if (before) runner.registerCleanup('restore_current_desktop', () => observer.returnToDesktop(before));
    desktop = await observer.createDesktop();
    runner.registerCleanup('close_desktop', async () => {
      for (;;) {
        const left = observer.desktop(desktop.id);
        if (!left) return;
        const moved = observer.waitForMessage((m) => m.event === 'profile_arrangement_changed' && m, 'the desktop moves on', 10_000);
        try {
          await observer.profileCommand('desktop_close', { desktop_id: left.id, expected_revision: left.revision });
          moved.catch(() => {});
          return;
        } catch (error) {
          if (!/stale_revision/.test(error.message)) throw error;
          await moved;
        }
      }
    });
    await driver.click({ key: `sidebar.desktop.${desktop.id}` });
    await driver.waitFor({ key: 'launcher.path.field' });
  });

  const cwd = path.join(runner.sessionDir, 'reopen');
  let dispatcher;
  let delegated;
  let seedId;
  await runner.step('delegate_a_seed', async () => {
    writeMockAgentFixture(cwd, {
      resumable: true,
      name: 'reopen mock',
      turns: [{
        includes: 'attn seed show',
        actions: [
          { type: 'capture', from: 'prompt', pattern: '(s-[a-z0-9]{6})', name: 'seed' },
          { type: 'attn', args: ['seed', 'show', '{{seed}}'] },
          { type: 'reply', text: 'GSREOPEN_READY', state: 'waiting_input' },
        ],
      }],
    });
    dispatcher = crypto.randomUUID();
    const spawned = observer.waitForMessage((m) => m.event === 'spawn_result' && m.id === dispatcher && m, 'dispatcher spawned', 60_000);
    observer.send({ cmd: 'spawn_session', id: dispatcher, agent: 'codex', cwd, profile_id: observer.profileId, placement: { desktop_id: desktop.id }, label: 'Dispatcher', cols: 100, rows: 30 });
    runner.assert((await spawned).success, 'spawn_session starts the dispatcher');
    runner.registerCleanup('unregister_dispatcher', () => observer.unregisterSession(dispatcher));
    const reply = await observer.requestResult({
      cmd: 'delegate', cwd, source_session_id: dispatcher, agent: 'codex', model: MOCK_AGENT_MODEL, label: 'Reopen delegate',
      assignment: { kind: 'new', brief: BRIEF },
    }, 'delegate_result', 60_000);
    delegated = reply.result?.session_id;
    seedId = reply.result?.seed_id;
    runner.assert(Boolean(delegated && seedId), 'the delegate starts on its seed', reply);
    runner.registerCleanup('wither_seed', () => attn('seed', 'wither', seedId, '-m', 'Scenario complete', '--profile', observer.profileId, '--force'));
    await reportsTo(delegated, seedId);
    const placed = observer.desktopOf(delegated);
    const runtime = placed.panes.find((p) => p.session_id === delegated).runtime_id;
    await driver.state(`terminal:${runtime}`, { includes: 'GSREOPEN_READY' });
  });

  let reopened;
  await runner.step('a_closed_tender_resumes_from_its_seed_in_the_garden', async () => {
    observer.unregisterSession(delegated);
    await observer.waitFor(() => !observer.sessionsById.has(delegated), 'the delegate closes', 20_000);
    await driver.run('board.open');
    await driver.waitFor({ role: 'region', name: 'The garden' });
    await driver.click({ key: `garden.row.${seedId}` });
    const resume = await driver.waitFor({ role: 'button', name: 'Resume' });
    runner.assert(!JSON.stringify(resume.states ?? []).includes('disabled'), 'Resume is offered for the closed tender', resume);
    await driver.screenshot(path.join(runner.runDir, 'native-garden-resume.png'));
    const known = new Set(observer.sessionsById.keys());
    await driver.click({ role: 'button', name: 'Resume' });
    reopened = await newSession(known, 'the tender reopens');
    await reportsTo(reopened, seedId);
  });

  let handedOver;
  await runner.step('handover_gives_the_seed_to_a_new_agent', async () => {
    await driver.run('board.open');
    await driver.waitFor({ role: 'region', name: 'The garden' });
    await driver.click({ key: `garden.row.${seedId}` }, { allowMiss: true });
    await driver.click({ role: 'button', name: 'Handover' });
    await driver.waitFor({ key: 'garden.handover.field' });
    await driver.typeInto({ key: 'garden.handover.field' }, HANDOFF);
    const known = new Set(observer.sessionsById.keys());
    await driver.pressKey('mod+Enter');
    handedOver = await newSession(known, 'the Handover agent starts');
    runner.assert(observer.sessionsById.has(reopened), 'the resumed conversation stays open after Handover', { reopened, handedOver });
    await reportsTo(handedOver, seedId);
    const notes = attn('seed', 'notes', seedId);
    runner.assert(notes.includes(HANDOFF), 'the handoff is on the seed log', notes);
    runner.writeText('seed-notes.txt', notes);
  });

  await runner.finishSuccess({ desktopId: desktop.id, seedId, delegated, reopened, handedOver });
} catch (error) {
  await runner.finishFailure(error);
  process.exitCode = 1;
}

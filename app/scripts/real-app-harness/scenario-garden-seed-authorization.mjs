#!/usr/bin/env node
import fs from 'node:fs';
import path from 'node:path';
import { execFileSync } from 'node:child_process';
import { createSessionAndWaitForInitialPane, launchFreshAppAndConnect, parseCommonArgs, printCommonHelp } from './common.mjs';
import { appDaemonInTree, createWindowDriver } from './platform.mjs';
import { currentHarnessInstance, instanceCliEnv } from './harnessInstance.mjs';
import { writeMockAgentFixture } from './mockAgent.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import { createScenarioRunner } from './scenarioRunner.mjs';

const FIRST_LINE = 'You may land on main.';
const SECOND_LINE = 'Ask me before releasing.';

// Tripwire: each change below reaches the page or the delegate within a second on a healthy app.
const SETTLED_MS = 20_000;

async function main() {
  const args = process.argv.slice(2).filter((arg) => arg !== '--');
  if (args.includes('--help')) { printCommonHelp('scenario-garden-seed-authorization'); return; }
  const options = parseCommonArgs(args);
  const instance = currentHarnessInstance();
  if (!instance) throw new Error('Seed authorization verification requires a named instance.');
  const client = new UiAutomationClient(options);
  const observer = new DaemonObserver(options);
  const driver = createWindowDriver({ appPath: options.appPath, client });
  const runner = createScenarioRunner(options, { scenarioId: 'GardenSeedAuthorization', tier: 'local', prefix: 'garden-seed-authorization', allowRealAgents: false });
  let dispatcher;
  let agent;
  let seedId;
  const runAttn = (attnArgs, sessionId) => execFileSync(appDaemonInTree(options.appPath), attnArgs, {
    encoding: 'utf8', env: instanceCliEnv(instance, { ATTN_TERMINAL_ID: sessionId ? observer.terminalOf(sessionId) : '' }),
  });
  const section = () => `.seed-document[data-seed-id="${seedId}"] .seed-authorization`;
  const sectionText = async () => (await client.request('dom_text', { selector: section() })).text;
  const pageShows = (textIncludes, part = '') => client.request('dom_wait', { selector: `${section()}${part}`, textIncludes, timeoutMs: SETTLED_MS });
  const repliedAfterNextTurn = (sessionId, description) => observer.waitForMessage(
    (event) => event.event === 'session_state_changed' && event.session?.id === sessionId && event.session?.state === 'working',
    `${description} to start`, SETTLED_MS,
  ).then(() => observer.waitForMessage(
    (event) => event.event === 'session_state_changed' && event.session?.id === sessionId && event.session?.state === 'waiting_input',
    `${description} to finish`, SETTLED_MS,
  ));
  const pressButton = async (nth) => {
    await client.request('dom_focus', { selector: `${section()} .seed-authorization__actions button:nth-child(${nth})` });
    await driver.pressEnter();
  };
  const shoot = async (name) => {
    const shot = await client.request('capture_screenshot_data', { selector: section() }, { timeoutMs: 8_000 });
    if (!shot.pngBase64) throw new Error(`capture_screenshot_data returned no PNG for ${name}`);
    fs.writeFileSync(path.join(runner.runDir, name), Buffer.from(shot.pngBase64, 'base64'));
  };
  try {
    await launchFreshAppAndConnect(client, observer);
    const cwd = path.join(runner.sessionDir, 'authorized');
    fs.mkdirSync(cwd, { recursive: true });
    writeMockAgentFixture(cwd, {
      name: 'Search builder',
      turns: [
        { includes: 'authorization for seed', actions: [{ type: 'reply', text: 'Read the new authorization.', state: 'waiting_input' }] },
        { includes: 'Your assignment is', actions: [{ type: 'reply', text: 'Started on the endpoint.', state: 'waiting_input' }] },
      ],
    });

    await runner.step('delegate_a_tended_seed', async () => {
      dispatcher = await createSessionAndWaitForInitialPane({ client, observer, cwd, label: 'Authorization setup', agent: 'shell' });
      seedId = runAttn(['seed', 'plant', 'Search endpoint', '-m', 'Build the search endpoint.'], dispatcher).match(/s-[a-z0-9]{6}/)?.[0];
      runner.assert(Boolean(seedId), 'planting the seed printed its id');
      const name = `builder-${dispatcher.slice(0, 8)}`;
      const started = observer.waitForMessage(
        (event) => event.event === 'session_state_changed' && event.session?.label === name && event.session?.state === 'waiting_input' && event.session.id,
        'the delegate to finish its first turn', 60_000,
      );
      runAttn(['delegate', '--seed', seedId, '--agent', 'codex', '--model', 'gpt-5.4-mini', '--effort', 'low', '--yolo', '--cwd', cwd,
        '--name', name, '--source-session', dispatcher]);
      agent = await started;
      await client.request('select_session', { sessionId: agent });
      runAttn(['open', seedId], agent);
      await pageShows('No authorization from the user');
      await shoot('authorization-empty.png');
    });

    await runner.step('set_the_words_from_the_keyboard', async () => {
      await pressButton(1);
      await client.request('dom_wait', { selector: `${section()} .seed-authorization__input`, focused: true, timeoutMs: 5_000 });
      await driver.typeText(FIRST_LINE);
      await driver.pressEnter({ shift: true });
      await driver.typeText(SECOND_LINE);
      const delegateRead = repliedAfterNextTurn(agent, "the delegate's turn on the typed update");
      await driver.pressEnter();
      await pageShows(`${FIRST_LINE}\n${SECOND_LINE}`, ' .seed-authorization__text');
      await pageShows('set by you');
      await pageShows('typed into');
      const stored = JSON.parse(runAttn(['seed', 'show', seedId, '--json'])).seed.authorization;
      runner.assert(stored?.text === `${FIRST_LINE}\n${SECOND_LINE}`, 'Shift+Enter keeps the line break in the saved words', { stored });
      runner.writeText('authorization-set.txt', await sectionText());
      await client.request('dom_wait', { selector: `${section()} .seed-authorization__actions button:nth-child(1)`, focused: true, textIncludes: 'Change', timeoutMs: 5_000 });
      await delegateRead;
      const typed = runAttn(['agent', 'peek', agent]);
      runner.assert(typed.includes('changed the authorization for seed') && typed.includes(FIRST_LINE), "the delegate's terminal shows the typed update", { typed });
      runner.assert(!typed.includes('set by'), 'the typed update names no setter when the user set it in the app', { typed });
      runner.writeText('delegate-after-set.txt', typed);
      runner.writeText('seed-show-after-set.txt', runAttn(['seed', 'show', seedId]));
      await shoot('authorization-set.png');
    });

    await runner.step('clear_the_words_from_the_keyboard', async () => {
      const delegateRead = repliedAfterNextTurn(agent, "the delegate's turn on the typed removal");
      await pressButton(2);
      await pageShows('No authorization from the user');
      await pageShows('cleared by you');
      await pageShows('typed into');
      runner.writeText('authorization-cleared.txt', await sectionText());
      await client.request('dom_wait', { selector: `${section()} .seed-authorization__actions button:nth-child(1)`, focused: true, textIncludes: 'Set', timeoutMs: 5_000 });
      await delegateRead;
      const typed = runAttn(['agent', 'peek', agent]);
      runner.assert(typed.includes('removed the authorization for seed'), "the delegate's terminal shows the typed removal", { typed });
      runner.writeText('delegate-after-clear.txt', typed);
      runner.writeText('seed-notes.txt', runAttn(['seed', 'notes', seedId, '--kind', 'authorization']));
      await shoot('authorization-cleared.png');
    });

    console.log(JSON.stringify(await runner.finishSuccess({ seedId, agent }), null, 2));
  } catch (error) {
    console.error((await runner.finishFailure(error, { seedId, agent })).error);
    process.exitCode = 1;
  } finally {
    if (agent) await client.request('close_session', { sessionId: agent }).catch(() => {});
    if (dispatcher) await client.request('close_session', { sessionId: dispatcher }).catch(() => {});
    await client.quitApp().catch(() => {});
    await observer.close();
  }
}
main().catch((error) => { console.error(error); process.exitCode = 1; });

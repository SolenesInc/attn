#!/usr/bin/env node
import fs from 'node:fs';
import path from 'node:path';
import { execFileSync } from 'node:child_process';
import { createSessionAndWaitForInitialPane, launchFreshAppAndConnect, parseCommonArgs, printCommonHelp } from './common.mjs';
import { appDaemonInTree, createWindowDriver, delay } from './platform.mjs';
import { currentHarnessInstance, instanceCliEnv } from './harnessInstance.mjs';
import { writeMockAgentFixture } from './mockAgent.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import { createScenarioRunner } from './scenarioRunner.mjs';

const WORDS = 'You may land on main. Ask me before releasing.';

// Tripwire: each wait here settles within a second on a healthy app.
async function poll(read, description, timeoutMs = 20_000) {
  const deadline = Date.now() + timeoutMs;
  let last;
  while (Date.now() < deadline) {
    last = await read();
    if (last?.done) return last.value;
    await delay(150);
  }
  throw new Error(`Timed out waiting for ${description}: ${JSON.stringify(last?.value ?? null)}`);
}

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
  const sectionText = async () => (await client.request('dom_text', { selector: section() }).catch(() => null))?.text ?? '';
  const agentScreen = async () => runAttn(['agent', 'peek', agent]);
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
      const before = new Set(observer.sessionsById.keys());
      runAttn(['delegate', '--agent', 'codex', '--model', 'gpt-5.4-mini', '--effort', 'low', '--yolo', '--cwd', cwd,
        '--name', `builder-${dispatcher.slice(0, 8)}`, '--source-session', dispatcher, '--brief', 'Build the search endpoint']);
      await observer.waitFor(() => {
        agent = [...observer.sessionsById.keys()].find((id) => !before.has(id));
        return Boolean(agent);
      }, 'the delegate to start', 60_000);
      await observer.waitFor(() => observer.getSession(agent)?.state === 'waiting_input', 'the delegate to wait for input', 60_000);
      await client.request('select_session', { sessionId: agent });
      seedId = await poll(async () => {
        const chip = await client.request('session_seed_chip_get_state', { sessionId: agent });
        return { done: Boolean(chip.id), value: chip.id };
      }, 'the delegate to tend its seed');
      runAttn(['open', seedId], agent);
      await poll(async () => {
        const text = await sectionText();
        return { done: text.includes('No authorization from the user'), value: text };
      }, 'the seed tile to show no authorization');
      await shoot('authorization-empty.png');
    });

    await runner.step('set_the_words_from_the_keyboard', async () => {
      await pressButton(1);
      await client.request('dom_wait', { selector: `${section()} .garden-compose__input`, focused: true, timeoutMs: 5_000 });
      await driver.typeText(WORDS);
      await driver.pressEnter();
      const text = await poll(async () => {
        const current = await sectionText();
        return { done: current.includes(WORDS) && current.includes('set by you'), value: current };
      }, 'the page to show the words set by the user');
      runner.writeText('authorization-set.txt', text);
      await client.request('dom_wait', { selector: `${section()} .seed-authorization__actions button:nth-child(1)`, focused: true, textIncludes: 'Change', timeoutMs: 5_000 });
      const typed = await poll(async () => {
        const screen = await agentScreen();
        return { done: screen.includes('changed the authorization for seed') && screen.includes('You may land on main.'), value: screen };
      }, "the delegate's terminal to show the typed update");
      runner.assert(!typed.includes('set by'), 'the typed update names no setter when the user set it in the app', { typed });
      runner.writeText('delegate-after-set.txt', typed);
      runner.writeText('seed-show-after-set.txt', runAttn(['seed', 'show', seedId]));
      if (process.env.ATTN_HARNESS_RECORD === '1') await delay(1600);
      await shoot('authorization-set.png');
    });

    await runner.step('clear_the_words_from_the_keyboard', async () => {
      await pressButton(2);
      const text = await poll(async () => {
        const current = await sectionText();
        return { done: current.includes('No authorization from the user'), value: current };
      }, 'the page to show the words cleared');
      runner.writeText('authorization-cleared.txt', text);
      await client.request('dom_wait', { selector: `${section()} .seed-authorization__actions button:nth-child(1)`, focused: true, textIncludes: 'Set', timeoutMs: 5_000 });
      const typed = await poll(async () => {
        const screen = await agentScreen();
        return { done: screen.includes('removed the authorization for seed'), value: screen };
      }, "the delegate's terminal to show the cleared update");
      runner.writeText('delegate-after-clear.txt', typed);
      runner.writeText('seed-notes.txt', runAttn(['seed', 'notes', seedId, '--kind', 'authorization']));
      if (process.env.ATTN_HARNESS_RECORD === '1') await delay(1600);
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

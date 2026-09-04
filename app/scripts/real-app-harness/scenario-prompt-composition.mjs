#!/usr/bin/env node
import fs from 'node:fs';
import path from 'node:path';
import { execFileSync } from 'node:child_process';
import { launchFreshAppAndConnect, parseCommonArgs } from './common.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import { createScenarioRunner } from './scenarioRunner.mjs';
import { currentHarnessProfile, profileCliEnv, resolveHarnessResources } from './harnessProfile.mjs';
import { writeMockAgentFixture, transcriptTurns } from './mockAgent.mjs';
import { waitForFirstWorkspacePane } from './scenarioAssertions.mjs';

async function waitFor(read, description) {
  const deadline = Date.now() + 30_000;
  while (Date.now() < deadline) {
    const result = read();
    if (result) return result;
    await new Promise(resolve => setTimeout(resolve, 100));
  }
  throw new Error(`Timed out: ${description}`);
}

function transcripts(cwd) {
  const directory = path.join(cwd, '.attn-mock-agent');
  if (!fs.existsSync(directory)) return [];
  return fs.readdirSync(directory).filter(name => name.endsWith('.jsonl')).map(name => ({
    name, text: fs.readFileSync(path.join(directory, name), 'utf8'),
  }));
}

function instructions(text, agent) {
  const meta = text.split('\n').filter(Boolean).map(line => JSON.parse(line)).find(row => row.type === 'session_meta');
  const args = meta?.payload?.launch?.argv || [];
  if (agent === 'claude') return args[args.indexOf('--append-system-prompt') + 1] || '';
  const override = args.find(arg => arg.startsWith('developer_instructions='));
  return override ? JSON.parse(override.slice('developer_instructions='.length)) : '';
}

async function main() {
  const options = parseCommonArgs(process.argv.slice(2));
  const profile = currentHarnessProfile();
  if (!profile) throw new Error('Prompt verification requires a named profile');
  const resources = resolveHarnessResources(profile);
  const env = profileCliEnv(profile);
  const cli = args => execFileSync(resources.appDaemon, args, { env, encoding: 'utf8', timeout: 30_000 });
  const client = new UiAutomationClient(options);
  const observer = new DaemonObserver(options);
  const runner = createScenarioRunner(options, { scenarioId: 'PromptComposition', tier: 'local', prefix: 'prompt-composition' });
  const sessions = [];
  const crewHome = path.join(resources.dataDir, 'crew', 'promptprobe');
  try {
    await client.quitApp();
    cli(['daemon', 'stop']);
    fs.mkdirSync(crewHome, { recursive: true });
    fs.writeFileSync(path.join(crewHome, 'CHARTER.md'), '# Promptprobe\nSynthetic prompt verification crew member.\n');
    writeMockAgentFixture(crewHome, { version: 1, agent: 'codex', turns: [
      { includes: 'You have been woken', actions: [{ type: 'reply', text: 'CREW_READY' }] },
      { includes: '📬 You have unread items', actions: [
        { type: 'attn', args: ['agent', 'inbox'] },
        { type: 'touch', path: 'sleep-received' },
        { type: 'attn', args: ['handoff', '--sleep', '-m', 'PROMPT_TEST_LETTER'] },
      ] },
    ] });
    await launchFreshAppAndConnect(client, observer);
    const launches = [];
    for (const [name, agent, chief] of [['ordinary-claude', 'claude', false], ['ordinary-codex', 'codex', false], ['chief-codex', 'codex', true]]) {
      await runner.step(`launch_${name}`, async () => {
        const cwd = path.join(runner.sessionDir, name);
        fs.mkdirSync(cwd, { recursive: true });
        writeMockAgentFixture(cwd, { version: 1, agent, turns: [{
          includes: '📬 You have unread items',
          submitHook: false,
          actions: [
            { type: 'attn', args: ['agent', 'inbox'] },
            { type: 'reply', text: 'PEER_READ', state: 'idle' },
          ],
        }] });
        const result = await client.request('create_session', { cwd, label: name, agent, chief_of_staff: chief });
        sessions.push(result.sessionId);
        await observer.waitForSession({ id: result.sessionId });
        await waitForFirstWorkspacePane(client, result.sessionId, name, 20_000);
        const captured = await waitFor(() => transcripts(cwd)[0], `${name} launch receipt`);
        const text = instructions(captured.text, agent);
        runner.writeText(`${name}-launch.jsonl`, captured.text);
        runner.assert(text.includes('Track work in seeds'), 'launch carries Garden instructions', { name });
        runner.assert(text.includes('You are the chief of staff.') === chief, 'chief branch matches session role', { name });
        launches.push({ id: result.sessionId, cwd });
      });
    }
    await runner.step('peer_message_is_attributed_once', async () => {
      const sent = JSON.parse(cli(['agent', 'msg', launches[1].id, 'PROMPT_PEER_MESSAGE', '--source-session', launches[2].id, '--json']));
      const text = await waitFor(() => {
        const text = transcripts(launches[1].cwd)[0]?.text || '';
        return transcriptTurns(text).some(turn => turn.text.includes('PEER_READ')) && text;
      }, 'peer message receipt');
      const turns = transcriptTurns(text).filter(turn => turn.role === 'user' && turn.text.includes('📬 You have unread items'));
      runner.assert(turns.length === 1 && turns[0].text.includes('Run attn agent inbox'), 'one notification reaches the recipient', { turns });
      const receipt = JSON.parse(cli(['agent', 'msg-status', sent.message_id, '--session', launches[2].id, '--json']));
      runner.assert(receipt.state === 'read', 'batch read records the peer receipt', { receipt });
      runner.assert(!turns[0].text.includes('PROMPT_PEER_MESSAGE'), 'notification leaves the body in the inbox');
      runner.assert(text.includes("This message is from another agent, not from your user.") && text.includes('PROMPT_PEER_MESSAGE'), 'inbox read delivers the body and trust boundary');
      runner.writeText('peer-message.jsonl', text);
    });
    await runner.step('crew_wake_sleep_and_successor', async () => {
      cli(['crew', 'set', 'promptprobe', '--agent', 'codex', '--model', 'claude-haiku-4-5']);
      const first = JSON.parse(cli(['crew', 'wake', 'promptprobe', '--json']));
      sessions.push(first.session_id);
      await observer.waitForSession({ id: first.session_id });
      await waitForFirstWorkspacePane(client, first.session_id, 'crew member', 20_000);
      const captured = await waitFor(() => transcripts(crewHome).find(file => file.text.includes('CREW_READY')), 'crew wake prompt');
      runner.assert(instructions(captured.text, 'codex').includes('You are **Promptprobe**'), 'crew identity reaches developer instructions');
      const duplicate = JSON.parse(cli(['crew', 'wake', 'promptprobe', '--json']));
      runner.assert(duplicate.already_awake === true && duplicate.session_id === first.session_id, 'wake does not create a second day');
      cli(['crew', 'sleep', 'promptprobe', '--json']);
      await waitFor(() => fs.existsSync(path.join(crewHome, 'sleep-received')), 'sleep prompt receipt');
      runner.assert(transcripts(crewHome).some(file => file.text.includes('user is asking you to close')), 'inbox read delivers the sleep request');
      await waitFor(() => fs.existsSync(path.join(crewHome, 'handoffs')) && fs.readdirSync(path.join(crewHome, 'handoffs')).length, 'filed handoff');
      await waitFor(() => !observer.sessionsById.has(first.session_id), 'first day closed');
      const second = JSON.parse(cli(['crew', 'wake', 'promptprobe', '--json']));
      sessions.push(second.session_id);
      const successor = await waitFor(() => transcripts(crewHome).find(file => file.name !== captured.name && file.text.includes('CREW_READY')), 'successor wake');
      runner.assert(instructions(successor.text, 'codex').includes('PROMPT_TEST_LETTER'), 'successor receives the filed letter');
      for (const file of [captured, successor]) {
        const turns = transcriptTurns(file.text).filter(turn => turn.role === 'user' && turn.text.includes('You have been woken'));
        runner.assert(turns.length === 1, 'opening wake is delivered once', { file: file.name, count: turns.length });
      }
      runner.writeText('crew-first.jsonl', transcripts(crewHome).find(file => file.name === captured.name).text);
      runner.writeText('crew-successor.jsonl', successor.text);
    });
    await runner.finishSuccess({ sessions });
  } catch (error) {
    await runner.finishFailure(error, { sessions });
    process.exitCode = 1;
  } finally {
    for (const sessionId of sessions) if (sessionId) await client.request('close_session', { sessionId }).catch(() => {});
    await client.quitApp().catch(() => {});
    await observer.close();
  }
}

main().catch(error => { console.error(error); process.exitCode = 1; });

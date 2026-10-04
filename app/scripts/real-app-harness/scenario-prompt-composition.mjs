#!/usr/bin/env node
import fs from 'node:fs';
import path from 'node:path';
import { randomUUID } from 'node:crypto';
import { PNG } from 'pngjs';
import { execFileSync } from 'node:child_process';
import { launchFreshAppAndConnect, parseCommonArgs } from './common.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import { createScenarioRunner } from './scenarioRunner.mjs';
import { currentHarnessInstance, instanceCliEnv, resolveHarnessResources } from './harnessInstance.mjs';
import { writeMockAgentFixture, transcriptTurns } from './mockAgent.mjs';
import { waitForFirstDesktopPane } from './scenarioAssertions.mjs';

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
  const instance = currentHarnessInstance();
  if (!instance) throw new Error('Prompt verification requires a named instance');
  const resources = resolveHarnessResources(instance);
  const env = instanceCliEnv(instance);
  const cli = args => execFileSync(resources.appDaemon, args, { env, encoding: 'utf8', timeout: 30_000 });
  const client = new UiAutomationClient(options);
  const observer = new DaemonObserver({ ...options, trustedApp: true });
  const runner = createScenarioRunner(options, { scenarioId: 'PromptComposition', tier: 'local', prefix: 'prompt-composition' });
  const sessions = [];
  const messageId = randomUUID(), attachmentId = randomUUID();
  const pdfId = randomUUID();
  const imageOut = path.join(runner.sessionDir, 'recipient-user_message.png');
  const pdfOut = path.join(runner.sessionDir, 'recipient-user_message.pdf');
  const crewName = `promptprobe-${randomUUID().slice(0, 8)}`;
  const crewLabel = `Promptprobe${crewName.slice('promptprobe'.length)}`;
  const crewHome = path.join(resources.dataDir, 'crew', crewName);
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
            ...(chief ? [{ type: 'attn', args: ['agent', 'attachment', messageId, attachmentId, '--out', imageOut] }] : []),
            ...(chief ? [{ type: 'attn', args: ['agent', 'attachment', messageId, pdfId, '--out', pdfOut] }] : []),
            { type: 'reply', text: chief ? 'USER_MESSAGE_READ' : 'PEER_READ', state: 'idle' },
          ],
        }] });
        const result = await client.request('create_session', { cwd, label: name, agent, chief_of_staff: chief });
        sessions.push(result.sessionId);
        await observer.waitForSession({ id: result.sessionId });
        await waitForFirstDesktopPane(client, result.sessionId, name, 20_000);
        const captured = await waitFor(() => transcripts(cwd)[0], `${name} launch receipt`);
        const text = instructions(captured.text, agent);
        runner.writeText(`${name}-launch.jsonl`, captured.text);
        runner.assert(text.includes('Track work that outlives this turn in seeds'), 'launch carries Garden instructions', { name });
        runner.assert(text.includes('You are the chief of staff of your profile.') === chief, 'chief branch matches session role', { name });
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
    await runner.step('user_message_files_are_attributed_and_retrievable', async () => {
      const shot = await client.request('capture_screenshot_data', { selector: '.app' });
      const original = Buffer.from(shot.pngBase64, 'base64');
      const source = path.join(runner.sessionDir, 'user-message-source.png');
      fs.writeFileSync(source, original);
      for (let offset = 0; offset < original.length;) {
        const end = Math.min(original.length, offset + 524288);
        const uploaded = await observer.requestResult({ cmd: 'user_message_attachment_put', message_id: messageId,
          attachment_id: attachmentId, name: 'screenshot.png', offset,
          data_base64: original.subarray(offset, end).toString('base64'), final: end === original.length }, 'user_message_result');
        runner.assert(uploaded.result.upload.next_offset === end, 'image offset receipt matches uploaded bytes');
        offset = end;
      }
      fs.unlinkSync(source);
      const pdf = Buffer.from('%PDF-1.4\n1 0 obj\n<< /Type /Catalog >>\nendobj\n%%EOF\n');
      const uploadedPDF = await observer.requestResult({ cmd: 'user_message_attachment_put', message_id: messageId,
        attachment_id: pdfId, name: 'notes.pdf', offset: 0,
        data_base64: pdf.toString('base64'), final: true }, 'user_message_result');
      runner.assert(uploadedPDF.result.upload.attachment.media_type === 'application/pdf', 'PDF finalizes without image validation');
      const recipient = launches[2];
      const completed = observer.waitForMessage(data => data.event === 'session_state_changed' &&
        data.session?.id === recipient.id && data.session?.state === 'idle' ? data : null, 'user message recipient finishes inbox read');
      const saved = await observer.requestResult({ cmd: 'user_message_send', message_id: messageId,
        target: { kind: 'chief' }, content: 'PROMPT_USER_MESSAGE', attachment_ids: [attachmentId, pdfId] }, 'user_message_result');
      runner.assert(saved.result.record.id === messageId, 'save returns the requested durable identity');
      await completed;
      const receipt = await observer.requestResult({ cmd: 'user_message_get', message_id: messageId }, 'user_message_result');
      runner.assert(Boolean(receipt.result.record.read_at), 'recipient inbox fetch commits a read receipt');
      const text = transcripts(recipient.cwd)[0]?.text || '';
      const spoken = transcriptTurns(text).map(turn => turn.text).join('\n');
      runner.writeText('user-message.txt', spoken);
      runner.assert(text.includes('Message from the user, sent through Quick Capture:') && text.includes('PROMPT_USER_MESSAGE'),
        'inbox output attributes user message content to the user');
      runner.assert(!text.includes('This message is from another agent'), 'user message omits the peer disclaimer');
      runner.assert(spoken.includes('File "notes.pdf" (application/pdf') && spoken.includes('Inspect the saved file with your tools.'),
        'non-image attachment carries file inspection instructions');
      runner.assert(fs.readFileSync(pdfOut).equals(pdf), 'recipient host retrieves byte-exact PDF content');
      const received = fs.readFileSync(imageOut);
      runner.assert(received.equals(original), 'recipient host retrieves the exact image bytes after source deletion');
      const decoded = PNG.sync.read(received);
      runner.assert(decoded.width > 0 && decoded.height > 0 && decoded.data.length === decoded.width * decoded.height * 4,
        'recipient image has inspectable pixels', { width: decoded.width, height: decoded.height, bytes: received.length });
    });
    await runner.step('crew_wake_sleep_and_successor', async () => {
      cli(['crew', 'set', crewName, '--agent', 'codex', '--model', 'claude-haiku-4-5']);
      const first = JSON.parse(cli(['crew', 'wake', crewName, '--json']));
      sessions.push(first.session_id);
      await observer.waitForSession({ id: first.session_id });
      await client.request('select_session', { sessionId: first.session_id });
      await waitForFirstDesktopPane(client, first.session_id, 'crew member', 20_000);
      const captured = await waitFor(() => transcripts(crewHome).find(file => file.text.includes('CREW_READY')), 'crew wake prompt');
      runner.assert(instructions(captured.text, 'codex').includes(`You are **${crewLabel}**`), 'crew identity reaches developer instructions');
      const duplicate = JSON.parse(cli(['crew', 'wake', crewName, '--json']));
      runner.assert(duplicate.already_awake === true && duplicate.session_id === first.session_id, 'wake does not create a second day');
      cli(['crew', 'sleep', crewName, '--json']);
      await waitFor(() => fs.existsSync(path.join(crewHome, 'sleep-received')), 'sleep prompt receipt');
      runner.assert(transcripts(crewHome).some(file => file.text.includes('user is asking you to close')), 'inbox read delivers the sleep request');
      await waitFor(() => fs.existsSync(path.join(crewHome, 'handoffs')) && fs.readdirSync(path.join(crewHome, 'handoffs')).length, 'filed handoff');
      await waitFor(() => !observer.sessionsById.has(first.session_id), 'first day closed');
      const second = JSON.parse(cli(['crew', 'wake', crewName, '--json']));
      sessions.push(second.session_id);
      const successor = await waitFor(() => transcripts(crewHome).find(file => file.name !== captured.name && file.text.includes('CREW_READY')), 'successor wake');
      runner.assert(instructions(successor.text, 'codex').includes('PROMPT_TEST_LETTER'), 'successor receives the filed letter');
      for (const file of [captured, successor]) {
        const turns = transcriptTurns(file.text).filter(turn => turn.role === 'user' && turn.text.includes('You have been woken'));
        runner.assert(turns.length === 1, 'opening wake is delivered once', { file: file.name, count: turns.length });
      }
      runner.writeText('crew-first.jsonl', transcripts(crewHome).find(file => file.name === captured.name).text);
      runner.writeText('crew-successor.jsonl', successor.text);
      // Handoff filenames have minute precision. Retain the verified first letter
      // as evidence before closing the successor in the same minute.
      for (const name of fs.readdirSync(path.join(crewHome, 'handoffs')))
        fs.renameSync(path.join(crewHome, 'handoffs', name), path.join(runner.sessionDir, name));
      cli(['crew', 'sleep', crewName, '--json']);
      await waitFor(() => !observer.sessionsById.has(second.session_id), 'successor day closed');
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

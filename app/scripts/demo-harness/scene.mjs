import { execFileSync } from 'node:child_process';
import fs from 'node:fs';
import path from 'node:path';

import {
  ensureClaudePromptReadyViaPty,
  ensureCodexPromptReadyViaPty,
} from '../real-app-harness/scenarioAgents.mjs';
import { ensureWindowRecorder, startWindowRecording } from '../real-app-harness/windowRecording.mjs';
import { SETTLED_STATE } from './turns.mjs';

const delay = (ms) => new Promise((resolve) => setTimeout(resolve, ms));
const TYPING_MS = 45;
const READ_PAUSE_MS = 1200;
const CLIP_FPS = 30;
const SETTLE_TRIPWIRE_MS = 90_000;
const PROMPT_READY = { claude: ensureClaudePromptReadyViaPty, codex: ensureCodexPromptReadyViaPty };

function ffmpeg(...args) {
  execFileSync('ffmpeg', ['-loglevel', 'error', '-y', ...args]);
}

export function createDemo({ world, outDir, log }) {
  const { client, driver, router } = world;
  let clipLog = null;

  const note = (action, detail = {}, rect = null) => {
    log(`${action} ${JSON.stringify(detail)}`);
    if (clipLog) clipLog.push({ t: Number(((Date.now() - clipLog.startedAt) / 1000).toFixed(3)), action, ...detail, ...(rect ? { rect } : {}) });
  };

  const bounds = async (selector) => (await client.request('dom_bounds', { selector })).bounds;

  const sessionState = async (sessionId) => {
    const state = await client.request('get_state');
    return state.sessions.find((session) => session.id === sessionId)?.state;
  };

  const waitForState = async (sessionId, wanted) => {
    const deadline = Date.now() + SETTLE_TRIPWIRE_MS;
    let state;
    while ((state = await sessionState(sessionId)) !== wanted) {
      if (Date.now() > deadline) {
        throw new Error(`session ${sessionId} stayed ${state} for ${SETTLE_TRIPWIRE_MS}ms, waiting for ${wanted}`);
      }
      await delay(250);
    }
  };

  const demo = {
    pause: (ms = READ_PAUSE_MS) => delay(ms),

    async ui(action, payload = {}) {
      note(action, payload);
      return client.request(action, payload);
    },

    read: (action, payload = {}) => client.request(action, payload),

    async until(description, check) {
      const deadline = Date.now() + SETTLE_TRIPWIRE_MS;
      while (!(await check())) {
        if (Date.now() > deadline) throw new Error(`waited ${SETTLE_TRIPWIRE_MS}ms for ${description}`);
        await delay(250);
      }
    },

    async session({ repo, worktree, agent = 'claude', label = worktree ?? repo }) {
      const cwd = world.repoPath(repo, worktree);
      const { sessionId } = await client.request('create_session', { cwd, agent, label });
      note('session', { sessionId, agent, label });
      const { paneId } = await PROMPT_READY[agent](client, sessionId);
      const session = {
        id: sessionId,
        async prompt(text, builder) {
          const spec = router.expect(text, builder, { cwd });
          await client.request('select_session', { sessionId });
          note('type', { sessionId, text });
          for (const char of text) {
            await client.request('write_pane', { sessionId, paneId, text: char, submit: false });
            await delay(TYPING_MS);
          }
          await delay(TYPING_MS * 4);
          await client.request('write_pane', { sessionId, paneId, text: '\r', submit: false });
          note('turn_start', { sessionId });
          await waitForState(sessionId, 'working');
          if (!spec.held) await waitForState(sessionId, SETTLED_STATE[spec.verdict]);
          note('turn_settled', { sessionId, state: await sessionState(sessionId) });
          return session;
        },
      };
      return session;
    },

    async still(name, { crop, highlight } = {}) {
      const file = path.join(outDir, name);
      const windowId = await driver.mainWindowId();
      execFileSync('/usr/sbin/screencapture', ['-x', '-o', '-l', String(windowId), file]);
      if (crop || highlight) {
        const { scaleFactor, logicalBounds } = await client.request('get_window_bounds');
        const viewport = await bounds('#root');
        const titlebar = logicalBounds.height - viewport.height;
        const px = (rect) => ({
          x: Math.round(rect.x * scaleFactor),
          y: Math.round((rect.y + titlebar) * scaleFactor),
          w: Math.round(rect.width * scaleFactor),
          h: Math.round(rect.height * scaleFactor),
        });
        const filters = [];
        let origin = { x: 0, y: 0 };
        if (highlight) {
          const box = px(await bounds(highlight));
          filters.push(`drawbox=x=${box.x - 8}:y=${box.y - 8}:w=${box.w + 16}:h=${box.h + 16}:color=0xff5fa2:t=6`);
        }
        if (crop) {
          const rect = await bounds(crop.selector);
          const area = px({ ...rect, height: rect.height + (crop.extendBottom ?? 0) });
          origin = area;
          filters.push(`crop=${area.w}:${area.h}:${area.x}:${area.y}`);
        }
        const raw = `${file}.raw.png`;
        fs.renameSync(file, raw);
        ffmpeg('-i', raw, '-vf', filters.join(','), file);
        fs.rmSync(raw);
        note('still', { name, origin });
      } else {
        note('still', { name });
      }
      return file;
    },

    async clip(name, run) {
      const file = path.join(outDir, `${name}.mp4`);
      const raw = `${file}.raw.mp4`;
      const windowId = await driver.mainWindowId();
      const recording = startWindowRecording({ windowId, outputPath: raw, command: await ensureWindowRecorder(), fps: CLIP_FPS, showCursor: false });
      clipLog = Object.assign([], { startedAt: Date.now() });
      const outcome = await run().then(() => null, (error) => error);
      const events = clipLog;
      clipLog = null;
      const result = await recording.stop();
      if (outcome) throw outcome;
      if (result.failure) throw new Error(`recording ${raw} failed: ${result.failure}`);
      ffmpeg('-i', raw, '-vf', `fps=${CLIP_FPS}`, '-c:v', 'libx264', '-pix_fmt', 'yuv420p', '-crf', '16', file);
      fs.rmSync(raw);
      fs.writeFileSync(path.join(outDir, `${name}.events.jsonl`), events.map((event) => JSON.stringify(event)).join('\n') + '\n');
      return file;
    },
  };
  return demo;
}

import fs from 'node:fs';
import { fileURLToPath } from 'node:url';

import { turn } from '../turns.mjs';
import { NOTEBOOK_ROOT } from '../world.mjs';

const WAKE_PROMPT = fs.readFileSync(fileURLToPath(new URL('../../../../internal/prompts/content/crew/wake.md', import.meta.url)), 'utf8').trim();

export default {
  description: "The Chief's first-run setup beside the sidebar, and its first session",
  repos: { recipes: {} },
  async run(demo) {
    await demo.waitOutGitHubThrottle();
    await demo.ui('dom_click', { selector: 'button[aria-label="Set up Chief"]' });
    await demo.read('dom_wait', { selector: '[data-testid="chief-setup"] .chief-pop-primary:not(:disabled)', timeoutMs: 30_000 });
    const prompted = demo.reply(WAKE_PROMPT, turn()
      .say("Hi, I'm your Chief. I'll start from your Notebook.")
      .read('index.md')
      .ask([
        "Here's what I'd like to cover: your delegation roles, how your Notebook is laid out, a quick tour of the app, and building something together if you want. Skip anything.",
        '',
        'First, roles. I found Claude Code on this machine. Should your delegated agents use only Claude, or do you expect Codex to work too?',
      ].join('\n')), { cwd: fs.realpathSync(NOTEBOOK_ROOT) });

    const chiefSession = async () => (await demo.read('get_state')).sessions.find((session) => session.label === 'Chief');
    await demo.clip('chief-setup', async () => {
      demo.caption("On your first run, the Chief's setup opens beside the sidebar");
      await demo.focus('[data-testid="chief-setup"]');
      await demo.pause(2500);
      demo.caption('Pick its harness and model, check the Notebook folder, and wake it');
      await demo.pause(2500);
      await demo.key('return');
      await demo.focus(null);
      await demo.pause(600);
      demo.caption('In its first session, the Chief walks you through Attn');
      demo.speed(4);
      await prompted;
      demo.speed(1);
      await demo.until('the Chief to ask its first question', async () => (await chiefSession())?.state === 'waiting_input');
      await demo.pause(3500);
    });
  },
};

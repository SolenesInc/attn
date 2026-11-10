#!/usr/bin/env node
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { parseArgs } from 'node:util';

const PROJECT = path.dirname(fileURLToPath(import.meta.url));
const VOICE_DIR = path.join(PROJECT, 'audio/voice');
const DEFAULT_VOICE = 'iP95p4xoKVk53GoZ742B';
const MODEL = 'eleven_multilingual_v2';
const SPOKEN_NAME = 'Attention';

const USAGE = `Usage: ELEVENLABS_API_KEY=... node app/scripts/demo-harness/queue-flow-video/narrate.mjs [--voice <voice id>] [--out <dir>] [--only <line id>]

Speaks each line of narration.json with ElevenLabs and writes <id>.mp3 and
<id>.words.json (when each word starts) into audio/voice/. "Attn" is spoken as
"${SPOKEN_NAME}". The script text goes to ElevenLabs; nothing else does.`;

const { values: options } = parseArgs({
  options: {
    voice: { type: 'string', default: DEFAULT_VOICE },
    out: { type: 'string', default: VOICE_DIR },
    only: { type: 'string' },
    help: { type: 'boolean', short: 'h' },
  },
});
if (options.help) {
  console.log(USAGE);
  process.exit(0);
}
const key = process.env.ELEVENLABS_API_KEY;
if (!key) {
  console.error(`ELEVENLABS_API_KEY is not set.\n\n${USAGE}`);
  process.exit(1);
}

function wordsFrom({ characters, character_start_times_seconds: starts, character_end_times_seconds: ends }) {
  const words = [];
  let current = null;
  characters.forEach((character, index) => {
    if (/\s/.test(character)) {
      current = null;
      return;
    }
    if (!current) {
      current = { text: '', start: starts[index], end: ends[index] };
      words.push(current);
    }
    current.text += character;
    current.end = ends[index];
  });
  return words;
}

const lines = JSON.parse(fs.readFileSync(path.join(PROJECT, 'narration.json'), 'utf8')).filter(({ id }) => !options.only || id === options.only);
if (lines.length === 0) throw new Error(`narration.json has no line "${options.only}"`);
fs.mkdirSync(options.out, { recursive: true });
for (const { id, text } of lines) {
  const response = await fetch(`https://api.elevenlabs.io/v1/text-to-speech/${options.voice}/with-timestamps?output_format=mp3_44100_128`, {
    method: 'POST',
    headers: { 'xi-api-key': key, 'Content-Type': 'application/json' },
    body: JSON.stringify({ text: text.replaceAll('Attn', SPOKEN_NAME), model_id: MODEL }),
  });
  if (!response.ok) throw new Error(`ElevenLabs answered ${response.status} for line "${id}": ${(await response.text()).slice(0, 300)}`);
  const { audio_base64: audio, alignment } = await response.json();
  fs.writeFileSync(path.join(options.out, `${id}.mp3`), Buffer.from(audio, 'base64'));
  fs.writeFileSync(path.join(options.out, `${id}.words.json`), `${JSON.stringify(wordsFrom(alignment))}\n`);
  console.log(`[narrate] ${id}: ${alignment.character_end_times_seconds.at(-1).toFixed(2)}s`);
}

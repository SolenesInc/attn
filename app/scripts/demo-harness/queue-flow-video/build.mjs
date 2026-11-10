#!/usr/bin/env node
import { execFileSync } from 'node:child_process';
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { parseArgs } from 'node:util';

const PROJECT = path.dirname(fileURLToPath(import.meta.url));
const REPO = path.resolve(PROJECT, '../../../..');
const HYPERFRAMES = 'hyperframes@0.9.27';
const FPS = 30;
const ZOOM = { queue: 1.5, question: 1.3, home: 1.25 };
const QUEUE_HEADER_HEIGHT = 36;
const VOICE_VOLUME = 1.3;
const MUSIC_VOLUME = 0.2;
const SFX_VOLUME = 0.45;
const WEB_VIDEO = { crf: 20, audioBitrate: '128k', loudness: 'loudnorm=I=-16:TP=-1.5:LRA=11' };
const POSTER_AT = 3.6;

const USAGE = `Usage: node app/scripts/demo-harness/queue-flow-video/build.mjs --clip <queue-flow.mp4> [--out <video.mp4>] [--draft]

Cuts the queue flow video from the raw clip the queue-flow demo scene records
and the <clip>.events.jsonl beside it, with the narration, music and sound
effects in audio/. Writes index.html and assets/ into this folder, checks and
renders them with HyperFrames, and writes the video with its poster (.jpg)
beside it. --draft renders faster at lower quality, and
--no-render stops after writing index.html and assets/. Without --out the
video goes to website/public/recordings/queue-loop.mp4.`;

const { values: options } = parseArgs({
  options: {
    clip: { type: 'string' },
    out: { type: 'string' },
    draft: { type: 'boolean', default: false },
    'no-render': { type: 'boolean', default: false },
    help: { type: 'boolean', short: 'h' },
  },
});
if (options.help || !options.clip) {
  console.log(USAGE);
  process.exit(options.help ? 0 : 1);
}

const clip = path.resolve(options.clip);
const out = path.resolve(options.out ?? path.join(REPO, 'website/public/recordings/queue-loop.mp4'));
const run = (command, args, extra = {}) => execFileSync(command, args, { stdio: 'inherit', ...extra });
const seconds = (file) => Number(execFileSync('ffprobe', ['-v', 'error', '-show_entries', 'format=duration', '-of', 'csv=p=0', file], { encoding: 'utf8' }));
const round = (value) => Number(value.toFixed(3));

const events = fs.readFileSync(clip.replace(/\.mp4$/, '.events.jsonl'), 'utf8').trim().split('\n').map((line) => JSON.parse(line));
const clipSeconds = seconds(clip);
const times = (matches) => events.filter(matches).map((event) => event.t);
const shortcut = (id) => (event) => event.action === 'dispatch_shortcut' && event.shortcutId === id;
const mark = (name) => events.find((event) => event.action === 'mark' && event.name === name);
const [jump] = times(shortcut('session.jumpToWaiting'));
const typed = times((event) => event.action === 'type');
const sent = times((event) => event.action === 'turn_start');
const settled = times(shortcut('session.settle'));
const questions = events.filter((event) => event.action === 'mark' && event.name === 'question').map((event) => event.rect);
const windowSize = events.find((event) => event.action === 'window');
const recorded = { jumps: [times(shortcut('session.jumpToWaiting')).length, 1], answers: [typed.length, 4], sends: [sent.length, 4], settles: [settled.length, 4], 'question marks': [questions.length, 4], 'window events': [windowSize ? 1 : 0, 1] };
const missing = [...Object.entries(recorded).filter(([, [found, wanted]]) => found !== wanted).map(([name, [found, wanted]]) => `${found} ${name} (expected ${wanted})`),
  ...['oldest-waiting', 'newest-waiting', 'all-settled', 'working-list', 'brought'].filter((name) => !mark(name)).map((name) => `no "${name}" mark`)];
if (missing.length) throw new Error(`${clip} is not a queue-flow recording: ${missing.join(', ')}; record it with the queue-flow scene`);
const brought = mark('brought').t;
const rectOf = (name) => mark(name).rect;
const middle = (rect) => ({ cx: rect.x + rect.width / 2, cy: rect.y + rect.height / 2 });
const pad = (rect, by) => ({ x: rect.x - by, y: rect.y - by, width: rect.width + 2 * by, height: rect.height + 2 * by });
const WIDE = { cx: windowSize.width / 2, cy: windowSize.height / 2, z: 1 };
const QUEUE_ROWS = {
  x: 3,
  y: rectOf('oldest-waiting').y - QUEUE_HEADER_HEIGHT,
  width: rectOf('oldest-waiting').x + rectOf('oldest-waiting').width - 3,
  height: rectOf('newest-waiting').y + rectOf('newest-waiting').height - rectOf('oldest-waiting').y + QUEUE_HEADER_HEIGHT,
};
const SIDEBAR_QUEUE = { ...middle(QUEUE_ROWS), z: ZOOM.queue };
const WORKING_LIST = rectOf('working-list');
const HOME_BANNER = { cx: middle(rectOf('all-settled')).cx, cy: (rectOf('all-settled').y + WORKING_LIST.y + WORKING_LIST.height) / 2, z: ZOOM.home };
const askFraming = (rect) => ({ cx: middle(rect).cx, cy: middle(rect).cy + 30, z: ZOOM.question });

const smallFrame = (t) => execFileSync('ffmpeg', ['-loglevel', 'error', '-ss', String(t), '-i', clip, '-frames:v', '1', '-vf', 'scale=120:80,format=gray', '-f', 'rawvideo', '-'], { maxBuffer: 1 << 20 });
const screenChanged = (t) => {
  const before = smallFrame(Math.max(0, t - 0.4));
  const after = smallFrame(t + 1);
  return before.reduce((sum, value, index) => sum + Math.abs(value - after[index]), 0) / before.length > 2;
};
for (const [name, t] of [['the jump', jump], ...settled.map((at, index) => [`settle ${index + 1}`, at]), ['attn bringing the agent', brought - 0.4]]) {
  if (!screenChanged(t)) {
    throw new Error(`${clip} shows no change at ${t.toFixed(1)}s, where its events record ${name}: the recorder dropped frames, so record the scene again`);
  }
}

const assets = path.join(PROJECT, 'assets');
fs.rmSync(assets, { recursive: true, force: true });
fs.mkdirSync(path.join(assets, 'fonts'), { recursive: true });
fs.cpSync(path.join(PROJECT, 'audio'), path.join(assets, 'audio'), { recursive: true });
for (const font of ['mona-sans.woff2', 'jetbrains-mono.woff2']) {
  fs.copyFileSync(path.join(REPO, 'website/public/fonts', font), path.join(assets, 'fonts', font));
}
run('ffmpeg', ['-loglevel', 'error', '-y', '-i', clip, '-c:v', 'libx264', '-pix_fmt', 'yuv420p', '-crf', '16', '-g', String(FPS), path.join(assets, 'footage.mp4')]);

const narration = JSON.parse(fs.readFileSync(path.join(PROJECT, 'narration.json'), 'utf8'));
const voiceFile = (id) => {
  const file = ['wav', 'mp3', 'm4a'].map((extension) => `audio/voice/${id}.${extension}`).find((candidate) => fs.existsSync(path.join(PROJECT, candidate)));
  if (!file) throw new Error(`narration line "${id}" has no audio in ${path.join(PROJECT, 'audio/voice')}`);
  return file;
};
const line = Object.fromEntries(narration.map(({ id, text }) => {
  const file = voiceFile(id);
  const wordsFile = path.join(PROJECT, `audio/voice/${id}.words.json`);
  const words = fs.existsSync(wordsFile) ? JSON.parse(fs.readFileSync(wordsFile, 'utf8')) : null;
  const duration = seconds(path.join(PROJECT, file));
  const spokenAt = (phrase) => {
    const index = text.indexOf(phrase);
    if (index < 0) throw new Error(`narration line "${id}" does not say "${phrase}"`);
    const estimate = duration * (index / text.length);
    if (!words) return estimate;
    const spoken = (word) => word.toLowerCase().replace(/^attn$/, 'attention').replace(/[^a-z]/g, '');
    const first = spoken(phrase.split(' ')[0]);
    const heard = words.filter((word) => spoken(word.text).startsWith(first));
    if (heard.length === 0) throw new Error(`audio/voice/${id}.words.json has no word like "${first}"; transcribe ${file} again`);
    return heard.reduce((best, word) => (Math.abs(word.start - estimate) < Math.abs(best.start - estimate) ? word : best)).start;
  };
  return [id, { file, text, duration, spokenAt }];
}));

const videos = [];
const voices = [];
const camera = [{ t: 0, ...WIDE }];
const marks = [];
const captions = [];
const sfx = [];
let sourceCursor = 0;
const playing = (start, sourceStart, duration, rate = 1) => {
  if (sourceStart < sourceCursor - 0.001) {
    throw new Error(`the cut at ${start.toFixed(2)}s would replay footage: it starts at ${sourceStart.toFixed(2)}s and the cut before it ended at ${sourceCursor.toFixed(2)}s`);
  }
  sourceCursor = sourceStart + duration * rate;
  if (sourceStart < 0 || sourceStart + duration * rate > clipSeconds) {
    throw new Error(`the cut needs footage from ${sourceStart.toFixed(2)}s for ${(duration * rate).toFixed(2)}s, and ${clip} is ${clipSeconds.toFixed(2)}s long; record the scene again`);
  }
  videos.push({ start: round(start), sourceStart: round(sourceStart), duration: round(duration), rate });
};
const say = (id, start) => voices.push({ id, file: line[id].file, text: line[id].text, start: round(start), duration: round(line[id].duration) });
const moveTo = (t, framing, duration) => camera.push({ t: round(t), duration, cx: framing.cx, cy: framing.cy, z: framing.z });
const outline = (start, end, rect, label = null) => marks.push({ id: `mark-${marks.length}`, start: round(start), end: round(end), rect, label });
const caption = (start, end, text, keys = [], presses = []) => captions.push({ id: `caption-${captions.length}`, start: round(start), end: round(end), text, keys, presses: presses.map(round) });
const sound = (t, name, limit = Infinity) => sfx.push({ t: round(t), name, duration: round(Math.min(limit, seconds(path.join(PROJECT, `audio/sfx/${name}.mp3`)))) });
const typing = (index, rate) => ({ sourceStart: typed[index] - 0.15, duration: (sent[index] + 0.4 - (typed[index] - 0.15)) / rate, rate });

const title = { start: 0.3, voice: 0.45 };
say('which', title.voice);
const queueStart = title.voice + line.which.duration + 0.5;
const stageIn = queueStart - 0.5;
sound(stageIn, 'whoosh');

say('queue', queueStart + 0.4);
const queueEnd = queueStart + 0.4 + line.queue.duration + 0.6;
const zoomToQueue = queueStart + 0.4 + line.queue.spokenAt('Attn lines up') - 0.2;
moveTo(zoomToQueue, SIDEBAR_QUEUE, 0.9);
caption(zoomToQueue + 0.5, queueEnd - 0.1, 'The agents waiting for you, oldest first');

const jumpAt = queueEnd + 0.2 + line.jump.spokenAt('Command J') + 0.5;
const homeHold = jumpAt - 1.2 - stageIn;
playing(stageIn, 0.2, homeHold, (jump - 1.2 - 0.2) / homeHold);
outline(zoomToQueue + 0.8, jumpAt - 0.7, QUEUE_ROWS);
say('jump', queueEnd + 0.2);
moveTo(jumpAt - 0.6, WIDE, 0.7);
caption(queueEnd + 0.1, queueEnd + 0.2 + line.jump.duration + 0.5, 'Go to the one that has waited longest', ['⌘', 'J'], [jumpAt]);
sound(jumpAt, 'key-press');
moveTo(jumpAt + 1.3, askFraming(questions[0]), 0.9);
const answerStart = Math.max(queueEnd + 0.2 + line.jump.duration + 0.6, jumpAt + 3.4);
playing(jumpAt - 1.2, jump - 1.2, 2.2);
const readingSource = typed[0] - 0.15 - (jump + 1);
playing(jumpAt + 1, jump + 1, answerStart - (jumpAt + 1), Math.min(1, readingSource / (answerStart - (jumpAt + 1))));
outline(jumpAt + 2.1, answerStart + 0.1, pad(questions[0], 4));

const firstAnswer = typing(0, 1.35);
playing(answerStart, firstAnswer.sourceStart, firstAnswer.duration, firstAnswer.rate);
sound(answerStart + 0.1, 'typing', firstAnswer.duration - 0.5);
const settleAt = answerStart + firstAnswer.duration + 0.4;
say('settle', settleAt - line.settle.spokenAt('Command Shift E') - 0.3);
moveTo(settleAt - 0.45, WIDE, 0.6);
sound(settleAt, 'key-press');
const rhythmStart = Math.max(settleAt + 2.4, settleAt - line.settle.spokenAt('Command Shift E') - 0.3 + line.settle.duration + 0.5);
playing(answerStart + firstAnswer.duration, settled[0] - 0.4, rhythmStart - (answerStart + firstAnswer.duration));
caption(settleAt - 0.5, rhythmStart - 0.1, 'Settle it, and attn brings the next one', ['⌘', '⇧', 'E'], [settleAt]);

const secondAnswer = typing(1, 2);
const thirdAnswer = typing(2, 2);
playing(rhythmStart, secondAnswer.sourceStart, secondAnswer.duration, secondAnswer.rate);
const secondSettle = rhythmStart + secondAnswer.duration + 0.2;
playing(rhythmStart + secondAnswer.duration, settled[1] - 0.2, 1.3);
const thirdStart = rhythmStart + secondAnswer.duration + 1.3;
playing(thirdStart, thirdAnswer.sourceStart, thirdAnswer.duration, thirdAnswer.rate);
const homeStart = thirdStart + thirdAnswer.duration;
const thirdSettle = homeStart + 0.2;
say('rhythm', secondSettle - line.rhythm.spokenAt('Settle.') - 0.05);
caption(rhythmStart + 0.1, thirdSettle + 0.5, 'Answer. Settle. Next.', ['⌘', '⇧', 'E'], [secondSettle, thirdSettle]);
sound(rhythmStart + 0.05, 'typing', secondAnswer.duration - 0.3);
sound(secondSettle, 'key-press');
sound(thirdStart + 0.05, 'typing', thirdAnswer.duration - 0.3);
sound(thirdSettle, 'key-press');

say('home', homeStart + 0.9);
const homeEnd = homeStart + 0.9 + line.home.duration + 0.5;
const restAt = homeStart + 0.9 + line.home.spokenAt('The rest');
moveTo(homeStart + 1.1, HOME_BANNER, 1);
outline(homeStart + 2, restAt - 0.1, mark('all-settled').rect);
outline(restAt, homeEnd - 0.2, WORKING_LIST);
caption(homeStart + 1.2, restAt - 0.15, 'Nobody is waiting: all settled');
caption(restAt, homeEnd - 0.1, 'The rest are working. You can forget about them');

say('brought', homeEnd + 0.3);
const broughtAt = homeEnd + 0.3 + line.brought.spokenAt('Attn brings you') + 0.1;
const closeStart = Math.max(homeEnd + 0.3 + line.brought.duration + 1, broughtAt + 3.6);
const broughtSource = brought - (broughtAt - homeEnd);
playing(homeStart, settled[2] - 0.2, homeEnd - homeStart, Math.min(1, (broughtSource - (settled[2] - 0.2)) / (homeEnd - homeStart)));
playing(homeEnd, broughtSource, broughtAt + 1 - homeEnd);
playing(broughtAt + 1, brought + 1, closeStart - (broughtAt + 1), Math.min(1, (typed[3] - 0.15 - (brought + 1)) / (closeStart - (broughtAt + 1))));
moveTo(homeEnd + 0.1, WIDE, 0.8);
sound(broughtAt, 'pop');
outline(broughtAt + 0.25, broughtAt + 2.3, mark('brought').rect, 'no key pressed');
moveTo(broughtAt + 2.2, askFraming(questions[3]), 0.9);
caption(homeEnd + 0.4, closeStart - 0.1, 'When one finishes or gets stuck, attn brings you to it');

const lastAnswer = typing(3, 2.5);
playing(closeStart, lastAnswer.sourceStart, lastAnswer.duration, lastAnswer.rate);
sound(closeStart + 0.05, 'typing', lastAnswer.duration - 0.3);
const lastSettle = closeStart + lastAnswer.duration + 0.3;
moveTo(lastSettle - 0.45, WIDE, 0.6);
sound(lastSettle, 'key-press');
const closingLine = lastSettle + 0.3;
say('close', closingLine);
const cardAt = closingLine + line.close.spokenAt('Let them') - 0.5;
sound(cardAt - 0.1, 'whoosh');
const duration = round(cardAt + 4.6);
playing(closeStart + lastAnswer.duration, settled[3] - 0.3, duration - (closeStart + lastAnswer.duration));

const wordTimes = (start, texts, gap) => {
  let at = start;
  return texts.flatMap((text) => text.split(' ').map(() => {
    const t = at;
    at += gap;
    return round(t);
  }));
};
const cards = [
  {
    id: 'title',
    start: 0,
    end: round(queueStart - 0.1),
    out: round(queueStart - 0.55),
    quiet: 'Five agents running.',
    hero: ['Which one', 'needs you?'],
    accent: 'needs you?',
    wordTimes: [...wordTimes(-1, ['Five agents running.'], 0), ...wordTimes(title.voice + line.which.spokenAt('Which one'), ['Which one needs you?'], 0.09)],
  },
  {
    id: 'end',
    start: round(cardAt),
    end: duration,
    out: null,
    quiet: 'Stop checking on your agents.',
    hero: ['Let them', 'come to you.'],
    accent: 'come to you.',
    wordTimes: [...wordTimes(cardAt + 0.1, ['Stop checking on your agents.'], 0.04), ...wordTimes(cardAt + 0.5, ['Let them come to you.'], 0.09)],
  },
];

const escape = (text) => text.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/"/g, '&quot;');
const spans = (text, className = '') => text.split(' ').map((word) => `<span class="word ${className}">${escape(word)} </span>`).join('');
const cardMarkup = (card) => `
      <div id="${card.id}" class="clip" data-start="${card.start}" data-duration="${round(card.end - card.start)}" data-track-index="3">
        <section class="card">
          <p class="card-quiet">${spans(card.quiet)}</p>
          <h1 class="card-hero">${card.hero.map((part) => `<span>${spans(part, part === card.accent ? 'accent' : '')}</span>`).join("")}</h1>
        </section>${card.id === 'end' ? `
        <p id="end-foot">
          <span id="end-logo">attn</span>
          <span id="end-how"><span class="keys"><span class="key">⌘</span><span class="key">⇧</span><span class="key">K</span></span>Turn on the agent queue</span>
        </p>` : ''}
      </div>`;
const music = ['mp3', 'm4a', 'wav'].map((extension) => `audio/music.${extension}`).find((candidate) => fs.existsSync(path.join(PROJECT, candidate)));
const audioMarkup = [
  ...voices.map(({ id, file, start, duration: length }) => `<audio id="voice-${id}" src="assets/${file}" data-start="${start}" data-duration="${length}" data-track-index="10" data-volume="${VOICE_VOLUME}"></audio>`),
  ...sfx.map(({ t, name, duration: length }, index) => `<audio id="sfx-${index}" src="assets/audio/sfx/${name}.mp3" data-start="${t}" data-duration="${length}" data-track-index="${11 + (index % 3)}" data-volume="${SFX_VOLUME}"></audio>`),
  ...(music ? [`<audio id="music" src="assets/${music}" data-start="0" data-duration="${duration}" data-track-index="14" data-volume="${MUSIC_VOLUME}" data-fade-in="0.4" data-fade-out="1.5"></audio>`] : []),
];

const edit = { window: { width: windowSize.width, height: windowSize.height }, camera, stage: { in: round(stageIn), out: round(cardAt - 0.1) }, marks, captions, cards, endFoot: round(cardAt + 1.6) };
const composition = fs.readFileSync(path.join(PROJECT, 'composition/template.html'), 'utf8')
  .replace('__DURATION__', String(duration))
  .replace('<!--VIDEOS-->', videos.map(({ start, sourceStart, duration: length, rate }, index) => `<video id="footage-${index}" class="clip" src="assets/footage.mp4" muted playsinline data-start="${start}" data-duration="${length}" data-media-start="${sourceStart}" data-playback-rate="${round(rate)}" data-track-index="0"></video>`).join('\n          '))
  .replace('<!--MARKS-->', marks.map(({ id, rect, label }) => `<div id="${id}" class="mark" data-layout-allow-overflow style="left:${rect.x - 3}px;top:${rect.y - 3}px;width:${rect.width + 6}px;height:${rect.height + 6}px">${label ? `<span class="mark-label">${escape(label)}</span>` : ''}</div>`).join('\n          '))
  .replace('<!--CAPTIONS-->', captions.map(({ id, start, end, text, keys }) => `<div id="${id}" class="caption clip" data-start="${start}" data-duration="${round(end - start)}" data-track-index="2"><span class="caption-inner">${keys.length ? `<span class="keys">${keys.map((key) => `<span class="key">${key}</span>`).join('')}</span>` : ''}<span>${escape(text)}</span></span></div>`).join('\n        '))
  .replace('<!--CARDS-->', cards.map(cardMarkup).join(''))
  .replace('<!--AUDIO-->', audioMarkup.join('\n      '))
  .replace('__EDIT__', JSON.stringify(edit).replace(/</g, '\\u003c'));
fs.writeFileSync(path.join(PROJECT, 'index.html'), composition);
console.log(`[queue-flow-video] ${duration}s, ${videos.length} footage cuts, ${voices.length} narration lines, music: ${music ?? 'none'}`);
if (options['no-render']) process.exit(0);

const env = { ...process.env, HYPERFRAMES_NO_TELEMETRY: '1', HYPERFRAMES_NO_UPDATE_CHECK: '1', HYPERFRAMES_SKIP_SKILLS: '1' };
run('npx', ['--yes', HYPERFRAMES, 'check', PROJECT], { env });
fs.mkdirSync(path.dirname(out), { recursive: true });
const rendered = path.join(PROJECT, 'renders', 'queue-flow.mp4');
fs.mkdirSync(path.dirname(rendered), { recursive: true });
run('npx', ['--yes', HYPERFRAMES, 'render', PROJECT, '--quality', options.draft ? 'draft' : 'delivery', '--fps', String(FPS), '--output', rendered], { env });
run('ffmpeg', ['-loglevel', 'error', '-y', '-i', rendered, '-c:v', 'libx264', '-preset', 'slow', '-crf', String(WEB_VIDEO.crf), '-pix_fmt', 'yuv420p', '-af', WEB_VIDEO.loudness, '-ar', '48000', '-c:a', 'aac', '-b:a', WEB_VIDEO.audioBitrate, '-movflags', '+faststart', out]);
run('ffmpeg', ['-loglevel', 'error', '-y', '-ss', String(POSTER_AT), '-i', rendered, '-frames:v', '1', '-q:v', '3', out.replace(/\.mp4$/, '.jpg')]);
console.log(`[queue-flow-video] ${out}: ${(fs.statSync(out).size / 1024 / 1024).toFixed(1)} MiB, ${seconds(out).toFixed(1)}s`);

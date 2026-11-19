# Guide images

The screenshots in this folder and the queue flow video were recorded from the
Tauri app, attn's earlier app, by a demo harness that drove it. The native app
has no demo harness yet. Until it has one, take new frames by hand on a
throwaway instance whose repos hold nothing from your machine.

## Queue flow video

The website's queue flow page plays `website/public/recordings/queue-loop.mp4`.
The video is cut from one raw clip with HyperFrames, and the project is
`app/scripts/demo-harness/queue-flow-video/`. It builds from a raw clip: the
full window at a steady 30 fps with nothing cropped or drawn on, and
`<clip>.events.jsonl` next to it. Every line of that file is one action with
`t`, its time in seconds from the start of the recording, and `mark` events
hold the place of what to point at. The demo harness wrote both, so a new clip
needs its events written to match. Then build:

    node app/scripts/demo-harness/queue-flow-video/build.mjs --clip <dir>/queue-flow.mp4

`build.mjs` places every cut, zoom, caption and sound from the clip's events
and the narration, renders on this machine, and writes the video and its poster
(`.jpg`). `--draft` renders faster for a look, and `--out <file>` writes
somewhere else. HyperFrames telemetry is off, and nothing is uploaded.

- The build stops when the clip shows no change where its events record one.
  The window recorder drops frames on a loaded Mac; record the clip again.
- `narration.json` holds the script, one line per `id`. After you change a
  line, speak it again with
  `ELEVENLABS_API_KEY=... node app/scripts/demo-harness/queue-flow-video/narrate.mjs --only <id>`,
  which writes `audio/voice/<id>.mp3` and the word timings in
  `audio/voice/<id>.words.json`. Without `--only` it speaks every line, and
  `--voice <voice id>` picks another ElevenLabs voice. `audio/CREDITS.md` says
  where the voice, the music and the sound effects come from.
- Zooms and outlines follow the clip's `mark` events, and the zoom factors are
  constants at the top of `build.mjs`. If the clip's prompts or replies
  change, check those frames.
- `composition/template.html` holds the look: layout, type, colors and motion.
- The page shows the narration as a transcript read from `narration.json`.
  Don't give the video a captions `<track>`: with one, the video never loads
  in Safari on some iPhones.

Check every frame before publishing: a still should show nothing from your
machine beyond the throwaway instance's repos.

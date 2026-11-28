# A view plays its sound clips

A view's media part on a sound file (`![clip-03](clips/03.wav)`: wav, mp3, m4a, aac, aif, flac, ogg, opus) now draws a player in the Mac app. Space plays or pauses the clip at the highlight (or else the view's first), and ⇧← and ⇧→ move it 5 s. Clips are highlight stops, so either side of a `:::compare` can be reached by keys.

The clip is read through the daemon with `tile_asset_read`, the same path a view's pictures take, so a view on an outpost plays its clip from that outpost. The daemon's asset types now include audio, and video is still refused. The app reads a clip only while it is drawn on the current desktop (in a one-by-one, only the current item's), and drops it, and its temp file, when nothing draws it there. A clip is read again when its view's text changes, or when a read that failed is followed by the view arriving again. While a clip plays the view repaints once a second; while paused, never.

On Linux a clip shows its name and "audio does not play on Linux yet", and nothing is read.

Trade-off: no new limit. The largest clip of the transcript task this was built for is 1.32 MiB, 22% of the existing 6 MiB asset limit. A longer recording gets the existing "file too large" message on its player.

The macOS VM plays no sound, so this recording shows an outpost clip seeking by keys (0:00 → 0:15 → 0:10) on a non-production instance. A recording of playback on the Mac host is still to come:

![An outpost clip moved by Shift+arrows in the macOS VM](https://land-feed.caketroll.cloud/m/SolenesInc/attn/a334f53dbabe21578254408c0de5be3cf9d9feadbade23f67c1da360987a4754.mp4)

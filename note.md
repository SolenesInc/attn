# A view draws in a tile

With `views_enabled` on, `attn open` on a view file now draws it natively in its markdown tile, part by part. That covers prose, pictures, task lists, status, facts, lists, tables, charts, compare, each, one-by-one and controls, filled from the view's `file=` sources. The daemon checks the view on the machine that holds the file and answers `open` with its problems. A broken part shows its problem in its place while the others draw.

Keys work with nothing focused:
- `j`/`k` and the arrow keys move the highlight.
- Enter, ← and → fold a list's group.
- On a one-by-one, Enter keeps an item and `s` skips it.

Answers, audio, `run=`, `attn=` and `store=` sources and `:::commands` say "not available yet" until later slices of the views plot (`s-1kx11g`) add them.

The draw table is one `ViewParts` literal in `native/crates/attn-app/src/view/mod.rs`, and each draw function takes only the five doors. Idle cost: over 60 s with the example view open and highlighted, the app used 0.07 s of CPU. A plain markdown tile used 0.05 s. The only wake is once a minute, while a view draws an `ago` value.

![The example view driven by keys](https://land-feed.caketroll.cloud/m/SolenesInc/attn/6f7f02bae634ed82653844f33dba2822a08a64bd317a5a7b991dba2b3345adc8.mp4)

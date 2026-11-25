The `native-shelf-drop-beside` scenario failed once on the landing runner while "Name every column on the shelf" (e8252e7ec) landed, and passed on CI. This fixes the scenario.

- **Cause:** a drop on an entry leaves the pointer on the shelf's frame, so the width control shows over the tile's header. The runner's shells set long titles, so the frame was 404 points wide and the control's "+" sat on the header's centre, where the next drag starts. With short names, as on CI and oma, the two never meet.
- **Reproduced:** on oma, with the shells setting a long title, the same step failed on the same element with the same rects. With the pointer moved onto the tile first, as a person's would be, it passes with long and short titles.
- **Not changed:** the width control still shows under a pointer a drop left on the frame. Whether it should wait for the pointer to move is left to the plot.

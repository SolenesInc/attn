# Labels no longer stay up when the chord comes up early

Since labels reached markdown, the chord asks Rune where the links drew. When something else changed the window in the same frame, Rune answers one frame later. A window system delivers a modifier's release between frames, so a release right there was heard before the labels existed, and they then stayed up, taking every key, until the modifiers changed again. The answer now checks the modifiers that are held when it comes.

Reproduced with a scratch copy of Rune that exposes the path a window system uses, through the app's own actions: 6 of them (the shelf moves and the board shortcut) left the labels up before, none after. No wire test covers it, because the harness can only queue a release into the next frame, which hides the fault. A wire test does cover the other side: a chord still held when the answer comes labels the ids.

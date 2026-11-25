# The id labels show and go in one step

Holding ⌘L (Ctrl+Shift+L on Linux) used to fade the labels, their list and the dimming in. Rune fades in, over 0.2 s, every element that mounts in a commit an input caused, unless the element opts out, and nothing had opted this layer out. The layer now does, over terminals and over markdown and asks alike, and each label it gains or loses during the hold lands at once too. No other motion changes.

Measured on Linux under Xvfb, sampling the framebuffer through a real hold: the base takes about half a second and 10 or more distinct in-between frames to show the labels, and the new build shows them between two samples in both drawings. Some Lantern samples caught a half-copied screen; their bands are each wholly old or wholly new, so none is a fade. The Mac run that found this has not been repeated on the new build.

The terminals feature file now says Escape ends the hold on Linux only: macOS takes ⌘Esc before any app sees it.

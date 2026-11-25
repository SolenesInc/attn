After a drop, the shelf's width control stays hidden until the pointer moves with no button held, as a peek does. It used to show under the resting pointer and cover the header of the tile just dropped.

- **Two ways it happened:** the frame came under a pointer that stayed still, and a drop released over the frame, which lands at the nearest off-screen entry. The control no longer shows during a drag either.
- **Proof:** `after_a_drop_the_width_control_waits_for_the_pointer_to_move` covers both, and fails with either half taken out. The wire binary passes (2510).
- **Scenario:** the workaround of e47b3809b is out, so `native-shelf-drop-beside` holds the behavior. It passed on oma with the long titles that failed on the landing runner, before the held-button half was added; the gate's run is of the final code.
- **Review:** one adversarial reader, which found the drop over the frame (fixed), and verify-tests, no findings. Both Sonnet; the author is Opus.

The keyboard walk now asks `layout::step` where a step goes, then shows the tile it names. `focus_toward` returns that tile, so the row overview can ask the same rule without moving anything.

Behavior is unchanged. Up and down in a zoomed view still go by the zoomed column, which a new wire test holds. One tie now breaks the same way at any window width: when two tiles below are equally near, the walk takes the first by id. It used to depend on float noise from the column's place on screen.

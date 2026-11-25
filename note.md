# ⌘I reaches an agent's card in Asks and Manage crew

In the Asks sheet and in Manage crew, resting the pointer on an agent's id shows its card, whose foot says ⌘I (Ctrl+Shift+I on Linux) gives it the keyboard. The key did nothing there: the sheets kept it from the window. It now acts while the card of an id in the sheet's words shows, and with no card it still does nothing to the tile behind.

Driving it showed a second fault: in Manage crew the card stood in the page's corner, over the roster, and not under the id. The page is laid out inside the window's body, before the spot the card hangs from. That spot is now laid out first.

A wire test per sheet fails without each fix, with Mac and with Linux keys. Both were driven on Linux. Fullscreen Garden already worked; a test now covers it.

# Daemon protocol tests

A daemon or WebSocket test models what the app does, in the order the app does
it: register the workspace, add a pane to the layout, spawn the session, close
panes through layout commands. When the app's flow changes, the tests change in
the same PR. A test that passes while a user can reproduce the error in the app
is a test bug. Assert the protocol state the app sees, and do not keep a daemon
compatibility path alive to spare an old test flow.

# Plan: keep Linux harness startup off the accessibility bus

## Goal

Make Linux real-app scenarios reach Tauri setup without waiting on the desktop
accessibility service, then let each scenario's own manifest timeout remain the
visible startup budget.

## What the investigation found

The slow launches stop inside WebKitGTK window construction, before Tauri
setup writes the app pid or UI automation manifest. A trace shows both
WebKitGTK and the GTK desktop-portal backend discovering the AT-SPI bus through
`org.a11y.Bus`. WebKitGTK does this independently of GTK's ATK bridge, so
`NO_AT_BRIDGE=1` leaves the stall intact.

A controlled session where `org.a11y.Bus` accepts method calls but never
answers produced no manifest within 45 seconds. Giving only WebKitGTK an
unavailable AT-SPI address left the portal backend waiting and the bridge smoke
took 28.0 seconds. Making both desktop bus addresses fail immediately completed
the same smoke in 3.0 seconds. The harness drives Linux through xdotool, the
daemon WebSocket, and its own Tauri automation bridge, so desktop D-Bus and
AT-SPI are outside the scenario contract.

## Implementation

- Give Linux harness app launches unavailable desktop D-Bus and AT-SPI socket
  addresses while preserving explicit per-scenario overrides for desktop
  integration tests.
- Remove the 90-second Linux manifest wait floor. The existing scenario budget
  becomes authoritative on every platform.
- Pin the launch environment and wait-budget behavior in the platform tests.

## Verification

- Run the focused harness platform and UI automation client tests.
- Run the repository lint, frontend, Go, and Linux build gates.
- Install the branch in a named profile inside `attn-linux`, run bundled
  preflight, and exercise the real-app bridge under Xvfb.
- Repeat the deliberately nonresponsive AT-SPI launch and confirm the manifest
  arrives within the ordinary harness budget.

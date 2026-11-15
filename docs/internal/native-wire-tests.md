# Native wire tests

A native wire test runs the native app's real view and real daemon link and
plays the daemon. It is the native app's counterpart of an
[app wire test](app-wire-tests.md), and [Testing](testing.md) is the strategy
around both. The harness is the crate `attn-testkit` in
`native/crates/attn-testkit`. Each crate keeps its wire tests in
`tests/wire/`, one file per feature, listed as modules in `tests/wire.rs`:

```rust
mod wire {
    mod connection;
    mod profile;
}
```

A test starts a world, plays the daemon, and asserts what the app showed and
sent:

```rust
use std::time::Duration;

use attn_testkit::{Daemon, Target, World};

#[test]
fn a_reconnect_after_a_handshake_starts_over_at_one_second() {
    let mut world = World::start(Daemon::scripted());
    world.reconnect();

    world.daemon.disconnect();
    world.settle();
    world.advance(Duration::from_millis(999));
    assert_eq!(world.daemon.connections(), 2);
    world.advance(Duration::from_millis(1));

    assert_eq!(world.daemon.connections(), 3);
    assert!(world.screen().find(Target::key("connection.reconnecting")).is_none());
    assert!(world.runtime_is_at_rest());
}
```

A crate's wire tests need `attn-testkit` as a dev-dependency, and usually
`attn-protocol` for the generated message types, `attn-client` for `Parked` and
`Frame`, and `serde_json` for `json!`. `attn-app` has them all.

Run a crate's wire tests from `native/`:

```sh
cargo test -p attn-app --test wire
```

To print each test's time, run the test binary that
`cargo test -p attn-app --test wire --no-run` names, as
`RUSTC_BOOTSTRAP=1 <binary> -Z unstable-options --report-time`.

## The app and its daemon

- `World::start(daemon)` runs `attn_app::view` on Rune's windowless `Runtime`
  and starts the real `attn_client::Link` against `daemon` over a loopback
  socket. It returns once the handshake has settled, or once the app has given
  up on the daemon.
- The world's app presents the token `CLIENT_TOKEN` and belongs to the instance
  `INSTANCE`. A scripted daemon minted the same token and reports the same
  instance, so the handshake succeeds unless the test changes one of them.
- Besides the daemon, the app sees two fakes: a `ManualClock` instead of the
  system clock, and a `Runtime` instead of a window. The `Runtime` needs no GPU.

`Daemon::scripted()` returns a daemon that is already listening. Configure it
before `World::start`. These calls take the daemon and return it, so they
chain from `Daemon::scripted()`:

| Call | What the daemon does |
| --- | --- |
| `.state(InitialStateMessage { .. })` | Answers `client_hello` with this `initial_state`. A `protocol_version` left as `None` is this build's. |
| `.instance("default")` | Reports this instance on `/health`. |
| `.client_token("other")` | Accepts only this token. |

Build a message from its generated type's `new()`, which fills the `event`
field. `Default::default()` leaves `event` empty. Nested types such as
`Profile` have no `event` and take `..Default::default()`:

```rust
let state = InitialStateMessage {
    selected_profile_id: Some("work".into()),
    profiles: Some(vec![Profile { id: "work".into(), name: "Work".into(), ..Default::default() }]),
    ..InitialStateMessage::new()
};
let world = World::start(Daemon::scripted().state(state));
```

The daemon answers every `client_hello` with that state, so after a reconnect
the app gets the same state again.

## Playing the daemon

`world.daemon` is the `Daemon`. It answers like the real daemon in two ways
the test cannot change. It refuses a `client_hello` with the wrong token: it
sends a `command_error` with `error_code` `unauthorized_client` and hangs up.
It answers `/health` only when the request's `Host` is `localhost` or an IP
address.

- `on(cmd, |request| reply)` answers each `cmd` the app sends. A request is a
  `serde_json::Value`. The reply is `()`, a `Value`, an `Option<Value>`, or a
  `Vec<Value>`. `message(m)` turns any generated protocol type into a `Value`.
  Each reply gets the request's `request_id`. A command with no answer stays
  unanswered. `on` returns `&Daemon`, so `on` calls chain with each other.
  `on("client_hello", ..)` replaces the `initial_state` that `.state` sets.
- `answer_in_turn(cmd, [Answer::Reply(..), Answer::Hold])` answers successive
  requests in order. `Answer::Hold` leaves a request unanswered.
- `reply_to(&request, reply)` answers a request the script held.
- `emit(event)` pushes an event. `emit_frame(bytes)` pushes a binary frame,
  laid out as `internal/protocol/binaryframe.go` writes it.
- `emit`, `emit_frame` and `disconnect` act on the newest open connection, and
  panic when none is open.
- `disconnect()` hangs up, as a daemon restart does. A command the app sends
  while the connection drops is lost with it.
  `refuse_connections(true)` closes every new connection before it is
  answered, as a port with no daemon behind it does. `refuse_connections(false)`
  ends that.
- `sent()` returns every command the app sent as a `Vec<Request>`, in order,
  across connections. `sent_of(cmd)` filters it. Each connection starts with
  its own `client_hello`, so after `World::start` and one `reconnect()`,
  `sent_of("client_hello")` holds two.
- `connections()` counts the WebSocket connections the app opened.
  `attempts()` counts the times the app tried to open one, refused or not.

The test fails when the world drops if the app broke the daemon's script:
for example, if the app sent a command before `client_hello`, or sent a request
after the last of its answers in turn.

## Waiting and time

Nothing in the harness waits on real time. A wait returns on a signal that
both sides have nothing left to do.

- `gesture(|driver| ..)` runs a user action, then settles. The `Driver` gives
  the action the `Runtime` to drive.
- `settle()` returns once every message each side sent has been handled, the
  window has drawn what the link delivered, and the link is waiting on the
  socket, or on a time the clock has not reached. Motion that a commit started
  lands at once, so a settled world shows the end of every animation. If both
  sides are still busy after 10 seconds, `settle` panics with both sides'
  traffic.
- `advance(by)` moves the app's clock and the link's clock together, then
  settles. Move time only with `advance`.
- `reconnect()` hangs up, advances the clock to the link's next retry, and
  returns once the app has connected again and finished its handshake.
- `world.link().traffic().parked` says where the link waits. `Parked::Until(t)`
  means it retries when the clock reaches `t`, and `world.now()` reads the
  clock.

## What to assert

- Assert what the window shows through `world.screen()`, which reads the
  accessibility tree. `find(target)` returns the first match or `None`.
  `get(target)` returns the match or panics with the whole tree. `all(target)`
  returns every match. A `Node` has a `role`, and may have a `key`, a `name`,
  and a `value`.
- Name an element with `Target::key("connection.refused")`, or with
  `Target::role("button").name("Retry")`. `Target::role` without `name`
  matches any name. Rune keeps an unnamed `status` element on every screen
  for its announcements, so `Target::role("status")` alone always matches.
- Assert the exact commands the app sent with `world.daemon.sent_of(cmd)`, to
  catch an extra request. The app leaves an empty optional field out of a
  command, so assert `request.get("profile_id").is_none()`, not that the field
  is `null`.
- End each test with `assert!(world.runtime_is_at_rest())`. It holds when the
  `Runtime` asks for no frame and holds no wake deadline: an idle window draws
  nothing.
- `world.take_frames()` returns the binary frames the link decoded and handed
  on, oldest first.
- `world.terminal_frames()` counts the frames that drew something new in a
  terminal. After each settle the world asks the app's terminal compositor
  whether its next frame would draw differently, as a window does before it
  presents a frame that only a key or a timer asked for, and draws it without
  a GPU when it would. A key whose echo has not come back adds none.
- `world.runtime().state` is the window's state, for what the screen cannot
  show.

## Record what each test replaces

`native/parity/wire.tsv` maps Vitest tests to the native tests that replace
them. When a native wire test replaces a Vitest test, add a row:

| Column | Holds |
| --- | --- |
| `vitest_file` | The Vitest file, from the repository root. |
| `vitest_test` | The test's `describe` names and its `it` name, joined by ` > `. |
| `native_test` | `<crate>/tests/wire/<file>.rs::<function>`, or `dropped`. |
| `reason` | Why the test was dropped. Empty for a replaced test. |

`scripts/native-parity` checks every row: the Vitest file has a test with that
name, the native test exists, a dropped test gives a reason, and no Vitest test
has two rows. `make check-native` and CI's native jobs run it.

## Record what each checklist item rests on

`native/parity/checklist.tsv` maps items of the no-regression checklist in
`survey-prior-art.md` (on seed `s-kxncd8`) to the evidence that the native app
keeps them. An item can have several rows, one per part of it.

| Column | Holds |
| --- | --- |
| `item` | The checklist item's number. |
| `statement` | What the native app does for that item, or that part of it. |
| `native_test` | `<crate>/tests/wire/<file>.rs::<function>`, `live:<seed id>/<artifact>` for a live run's evidence attached to a seed, or empty. |
| `reason` | Empty when `native_test` names evidence. Otherwise `n/a: <why>` when the item does not apply, or `gap: <seed id>` for the seed that will supply it. |

`scripts/native-parity` checks every row: the native test exists, a live
reference has the `live:<seed id>/<artifact>` form, and an empty `native_test`
has a reason in one of those forms. It cannot see the Notebook from CI, so it
does not check that the artifact is attached to the seed.

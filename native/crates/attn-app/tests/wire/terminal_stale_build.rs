use attn_protocol::{Session, SessionStateChangedMessage};
use attn_testkit::{Daemon, Target, World};

use super::sidebar::session;
use super::terminal::{pane, showing};

const NOTICE: &str = "session.s1.stale-build";

fn running(stale: bool) -> World {
    let mut state = showing(
        vec![attn_protocol::DesktopPane {
            session_id: "s1".into(),
            ..pane("t1", "r1")
        }],
        "daemon-1",
    );
    state.sessions = Some(vec![stale_session(stale)]);
    let mut world = World::start(Daemon::scripted().state(state));
    world.open_desktop();
    world
}

fn stale_session(stale: bool) -> Session {
    Session {
        terminal_build_stale: Some(stale),
        ..session("s1", "working")
    }
}

fn shows(world: &World) -> bool {
    world.screen().find(Target::key(NOTICE)).is_some()
}

#[test]
fn a_session_on_an_older_terminal_says_so_until_dismissed_for_the_rest_of_the_run() {
    let mut world = running(true);
    assert!(shows(&world));
    let notice = world.screen().get(Target::key(NOTICE));
    assert!(
        notice
            .name
            .as_deref()
            .is_some_and(|said| said.starts_with("This session is running an older terminal.")),
        "{:?}",
        notice.name
    );
    world.gesture(|app| app.click(Target::key("session.s1.stale-build.reload")));
    assert_eq!(
        world.daemon.sent_of("reload_session").len(),
        1,
        "Reload session on the notice reloads the session"
    );

    world.gesture(|app| app.click(Target::key("session.s1.stale-build.dismiss")));
    assert!(!shows(&world));
    world.daemon.emit(SessionStateChangedMessage {
        session: stale_session(true),
        ..SessionStateChangedMessage::new()
    });
    world.settle();
    assert!(!shows(&world), "a dismissed notice stays away");
    assert!(world.runtime_is_at_rest());
}

#[test]
fn a_session_on_the_current_terminal_says_nothing_about_its_build() {
    let world = running(false);
    assert!(!shows(&world));
    assert!(world.runtime_is_at_rest());
}

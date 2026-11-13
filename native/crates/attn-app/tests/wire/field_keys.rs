use attn_app::Action;
use attn_testkit::{Daemon, Target, World};

use super::keys::{chord, shortcut_records, state, two_terminals};

/// A text field and how a user puts the keyboard in it.
struct Field {
    key: &'static str,
    settings: &'static [(&'static str, &'static str)],
    focus: fn(&mut World),
}

fn press(world: &mut World, action: Action) {
    let ev = chord(&action);
    world.gesture(|app| app.key(ev));
}

fn click(world: &mut World, key: &'static str) {
    world.gesture(|app| app.click(Target::key(key)));
}

const PALETTE: Field = Field {
    key: "palette.field",
    settings: &[],
    focus: |world| {
        world.open_desktop();
        press(world, Action::UiCommandPalette);
    },
};

const OPENER: Field = Field {
    key: "opener.field",
    settings: &[],
    focus: |world| {
        world.open_desktop();
        press(world, Action::FileOpen);
    },
};

const FIND_BAR: Field = Field {
    key: "terminal.r1.find.field",
    settings: &[],
    focus: |world| {
        world.open_desktop();
        press(world, Action::TerminalFind);
    },
};

const GARDEN_SEARCH: Field = Field {
    key: "garden.search.field",
    settings: &[],
    focus: |world| {
        world.open_desktop();
        press(world, Action::BoardOpen);
        click(world, "garden.search.field");
    },
};

const SESSIONS_QUERY: Field = Field {
    key: "ledger.query.field",
    settings: &[],
    focus: |world| {
        world.open_desktop();
        press(world, Action::SessionsOpen);
        click(world, "ledger.query.field");
    },
};

const QUEUE_FILTER: Field = Field {
    key: "queue.filter.field",
    settings: &[("queue_mode_enabled", "true")],
    focus: |world| press(world, Action::SidebarAgentList),
};

/// Shortcuts a field leaves to the app, on keys its editor would otherwise take: Return and an
/// arrow under modifiers. Focusing the next tile goes last: it may take the keyboard away.
const RUN: [Action; 2] = [Action::TerminalToggleMaximize, Action::TerminalFocusRight];

/// Shortcuts whose keys a field keeps, as the Tauri app leaves them to an input.
const KEPT: [Action; 3] = [
    Action::TerminalFind,
    Action::SessionNext,
    Action::TerminalToggleZoom,
];

fn focused_in(field: &Field) -> World {
    let settings: Vec<_> = [("analytics.enabled", "true")]
        .into_iter()
        .chain(field.settings.iter().copied())
        .collect();
    let mut world = World::start(Daemon::scripted().state(state(two_terminals(), &settings)));
    (field.focus)(&mut world);
    assert!(
        world.screen().get(Target::key(field.key)).is("focused"),
        "{} takes the keyboard",
        field.key
    );
    world
}

/// From `field`, the kept chords leave the app alone and the others run, each pressed while
/// the field still holds the keyboard.
fn chords_from(field: &Field) {
    let mut world = focused_in(field);
    for action in KEPT.into_iter().chain(RUN) {
        assert!(
            world.screen().get(Target::key(field.key)).is("focused"),
            "{} holds the keyboard for {}",
            field.key,
            action.id()
        );
        let before = shortcut_records(&world).len();
        press(&mut world, action.clone());
        let ran: &[String] = if KEPT.contains(&action) {
            &[]
        } else {
            &[action.id().to_owned()]
        };
        assert_eq!(
            shortcut_records(&world)[before..],
            *ran,
            "{} from {}",
            action.id(),
            field.key
        );
    }
}

#[test]
fn a_bound_chord_runs_from_the_palette_unless_the_field_keeps_it() {
    chords_from(&PALETTE);
}

#[test]
fn a_bound_chord_runs_from_the_opener_unless_the_field_keeps_it() {
    chords_from(&OPENER);
}

#[test]
fn a_bound_chord_runs_from_the_find_bar_unless_the_field_keeps_it() {
    chords_from(&FIND_BAR);
}

#[test]
fn a_bound_chord_runs_from_the_garden_search_unless_the_field_keeps_it() {
    chords_from(&GARDEN_SEARCH);
}

#[test]
fn a_bound_chord_runs_from_the_sessions_query_unless_the_field_keeps_it() {
    chords_from(&SESSIONS_QUERY);
}

#[test]
fn a_bound_chord_runs_from_the_queue_filter_unless_the_field_keeps_it() {
    chords_from(&QUEUE_FILTER);
}

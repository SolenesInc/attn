use std::time::Duration;

use attn_protocol::{DesktopPane, InitialStateMessage, Seed, Session, SettingsUpdatedMessage};
use attn_testkit::{Daemon, Node, Target, World, key_event, message, tree};
use rune::{Chord, Ime, Key, Mods, PhysicalInput, PhysicalKeyCode};
use serde_json::json;

use super::terminal::{inputs, output, output_frame_to, pane, profile, showing};
use super::terminal_ids::{
    AGENT_CARD, BUILDER, BUILDER_SHORT, MAPPER_SHORT, SEED, SEED_CARD, agent, builder, key, letter,
    mapped, mapper, rest_on, rests, said, sessions_shown, shown, start, write,
};
use crate::wire::mac;

pub(super) fn chord_mods() -> Mods {
    Mods {
        cmd: true,
        logo: mac(),
        ctrl: !mac(),
        shift: !mac(),
        ..Mods::default()
    }
}

fn physical(letter: char) -> PhysicalKeyCode {
    match letter {
        'a' => PhysicalKeyCode::KeyA,
        'd' => PhysicalKeyCode::KeyD,
        'f' => PhysicalKeyCode::KeyF,
        'l' => PhysicalKeyCode::KeyL,
        's' => PhysicalKeyCode::KeyS,
        other => panic!("no key for {other}"),
    }
}

pub(super) fn press(world: &mut World, letter: char, repeat: bool) {
    let chord = if mac() {
        format!("mod+{letter}")
    } else {
        format!("mod+shift+{letter}")
    };
    let ev = key_event(&Chord::parse(&chord).unwrap());
    world.gesture(|app| {
        app.runtime.physical_key_input(
            PhysicalInput::Key {
                key: physical(letter),
                mods: chord_mods(),
                pressed: true,
                repeat,
            },
            ev,
            Some(letter),
        )
    });
}

fn modifiers(world: &mut World, mods: Mods) {
    world.gesture(|app| app.hold(mods));
}

pub(super) fn hold(world: &mut World) {
    modifiers(world, chord_mods());
    press(world, 'l', false);
}

pub(super) fn lets_go(world: &mut World) {
    modifiers(world, Mods::default());
}

pub(super) fn labels_in(nodes: Vec<Node>) -> Vec<String> {
    nodes
        .into_iter()
        .filter(|n| n.role == "note")
        .filter_map(|n| n.name)
        .collect()
}

fn on_ids(world: &World, runtime: &str) -> Vec<String> {
    let part = format!("terminal.{runtime}.label.");
    let labels = world
        .screen()
        .descendants(Target::key(format!("terminal.{runtime}.hints")));
    let labels = labels
        .into_iter()
        .filter(|n| n.key.as_ref().is_some_and(|k| k.starts_with(&part)));
    labels_in(labels.collect())
}

fn listed(world: &World, runtime: &str) -> Vec<String> {
    let list = Target::key(format!("terminal.{runtime}.labels"));
    if world.screen().find(list.clone()).is_none() {
        return Vec::new();
    }
    labels_in(world.screen().descendants(list))
}

fn labelled(world: &World) -> bool {
    let screen = world.screen();
    screen.find(Target::key("terminal.r1.hints")).is_some()
}

fn settings(world: &mut World, key: &str, value: &str) {
    world.daemon.emit(SettingsUpdatedMessage {
        settings: Some([(key.to_owned(), value.into())].into()),
        ..SettingsUpdatedMessage::new()
    });
    world.settle();
}

const ON_BUILDER: &str = "S Fix the retry uploader, working";
const ON_MAPPER: &str = "D Map where uploads retry, closed";
const ON_SEED: &str = "F Map the uploader, harvested";

fn reading() -> World {
    let mut world = start(vec![builder()], vec![mapped()], vec![mapper()]);
    write(
        &mut world,
        1,
        &format!("{BUILDER_SHORT} took over {MAPPER_SHORT}\r\non {SEED} from {BUILDER}"),
    );
    world
}

#[test]
fn holding_the_chord_labels_every_id_with_what_it_names_until_a_modifier_comes_up() {
    let mut world = reading();
    hold(&mut world);
    assert_eq!(
        on_ids(&world, "r1"),
        [ON_BUILDER, ON_MAPPER, ON_SEED, ON_BUILDER],
        "every occurrence of a target has its one letter"
    );
    assert_eq!(listed(&world, "r1"), [ON_BUILDER, ON_MAPPER, ON_SEED]);

    lets_go(&mut world);
    assert!(!labelled(&world));
    assert_eq!(inputs(&world), []);
    rests(&mut world);
}

#[test]
fn the_labels_go_when_the_window_loses_focus() {
    let mut world = reading();
    world.gesture(|app| app.runtime.window_focus(true));
    hold(&mut world);
    assert!(labelled(&world));
    world.gesture(|app| app.runtime.window_focus(false));
    assert!(!labelled(&world));
    rests(&mut world);
}

#[test]
fn the_labels_go_when_the_desktop_changes() {
    let mut world = reading();
    hold(&mut world);
    world
        .daemon
        .emit(message(super::terminal::showing_desktop("d2")));
    world.settle();
    world
        .daemon
        .emit_frame(output_frame_to("r2", 1, SEED.as_bytes()));
    world.settle();
    let hints = Target::key("terminal.r2.hints");
    assert!(world.screen().find(hints).is_none());
    press(&mut world, 'l', true);
    assert_eq!(inputs(&world), []);
    lets_go(&mut world);
    hold(&mut world);
    assert_eq!(on_ids(&world, "r2").len(), 1);
    lets_go(&mut world);
    rests(&mut world);
}

#[test]
fn no_key_paste_or_composed_text_reaches_the_terminal_while_labels_show() {
    let mut world = reading();
    hold(&mut world);
    press(&mut world, 'a', false);
    letter(&mut world, "x");
    key(&mut world, Key::Enter);
    key(&mut world, Key::Tab);
    world.gesture(|app| app.runtime.paste("pasted"));
    world.gesture(|app| app.runtime.ime(Ime::Commit("é".into())));
    assert_eq!(inputs(&world), []);
    assert!(labelled(&world));
    rests(&mut world);
}

#[test]
fn the_chord_again_and_its_repeat_change_nothing() {
    let mut world = reading();
    hold(&mut world);
    let before = (on_ids(&world, "r1"), listed(&world, "r1"));
    press(&mut world, 'l', true);
    press(&mut world, 'l', false);
    assert_eq!((on_ids(&world, "r1"), listed(&world, "r1")), before);
    assert_eq!(inputs(&world), []);
    rests(&mut world);
}

#[test]
fn a_chord_still_down_after_the_modifiers_come_up_types_nothing() {
    let mut world = reading();
    hold(&mut world);
    lets_go(&mut world);
    let ev = rune::KeyEvent {
        key: Key::Text("l".into()),
        mods: Mods::default(),
    };
    world.gesture(|app| {
        app.runtime.physical_key_input(
            PhysicalInput::Key {
                key: PhysicalKeyCode::KeyL,
                mods: Mods::default(),
                pressed: true,
                repeat: true,
            },
            ev,
            Some('l'),
        )
    });
    assert_eq!(inputs(&world), []);
    letter(&mut world, "x");
    assert_eq!(inputs(&world), [("x".to_owned(), "user".to_owned())]);
    rests(&mut world);
}

#[test]
fn a_label_opens_a_live_agents_card_with_the_keyboard_and_return_opens_the_agent() {
    let mut world = reading();
    hold(&mut world);
    press(&mut world, 's', false);
    assert!(!labelled(&world));
    assert_eq!(
        said(&world, "name").as_deref(),
        Some("Fix the retry uploader")
    );
    lets_go(&mut world);
    assert!(shown(&world, AGENT_CARD), "letting go leaves the card open");
    key(&mut world, Key::Enter);
    assert_eq!(sessions_shown(&world), [BUILDER]);
    assert_eq!(inputs(&world), []);
    rests(&mut world);
}

#[test]
fn a_label_opens_a_closed_agents_card_with_the_keyboard_and_return_resumes_it() {
    let mut world = reading();
    hold(&mut world);
    press(&mut world, 'd', false);
    lets_go(&mut world);
    assert_eq!(
        said(&world, "name").as_deref(),
        Some("Map where uploads retry")
    );
    key(&mut world, Key::Enter);
    assert_eq!(world.daemon.sent_of("session_reopen").len(), 1);
    assert_eq!(inputs(&world), []);
}

#[test]
fn a_label_opens_a_seeds_card_with_the_keyboard_and_return_opens_the_seed() {
    let mut world = reading();
    hold(&mut world);
    press(&mut world, 'f', false);
    lets_go(&mut world);
    assert!(shown(&world, SEED_CARD));
    key(&mut world, Key::Enter);
    let opened = world.daemon.sent_of("open_seed");
    assert_eq!(opened.len(), 1);
    assert_eq!(
        (&opened[0]["seed_id"], &opened[0]["session_id"]),
        (&json!(SEED), &json!("s1"))
    );
    assert_eq!(inputs(&world), []);
    rests(&mut world);
}

#[test]
fn escape_closes_a_labels_card_and_gives_the_terminal_the_keyboard() {
    let mut world = reading();
    hold(&mut world);
    press(&mut world, 's', false);
    lets_go(&mut world);
    key(&mut world, Key::Escape);
    assert!(!shown(&world, AGENT_CARD));
    assert_eq!(inputs(&world), []);
    letter(&mut world, "x");
    assert_eq!(inputs(&world), [("x".to_owned(), "user".to_owned())]);
    rests(&mut world);
}

#[test]
fn a_labels_key_held_down_runs_no_shortcut_once_its_card_is_open() {
    let mut world = reading();
    hold(&mut world);
    press(&mut world, 'd', false);
    let sent = world.daemon.sent().len();
    press(&mut world, 'd', true);
    press(&mut world, 'd', true);
    assert_eq!(world.daemon.sent().len(), sent);
    assert!(shown(&world, AGENT_CARD));
    assert_eq!(inputs(&world), []);
}

#[test]
fn escape_puts_the_labels_away_and_reaches_nothing() {
    let mut world = reading();
    hold(&mut world);
    key(&mut world, Key::Escape);
    assert!(!labelled(&world));
    assert_eq!(inputs(&world), []);
    press(&mut world, 'l', false);
    assert!(labelled(&world), "the chord pressed again labels again");
    lets_go(&mut world);
    rests(&mut world);
}

#[test]
fn labels_put_away_an_open_card_and_no_card_opens_under_the_pointer() {
    let mut world = reading();
    rest_on(&mut world, 3, 0);
    assert!(shown(&world, AGENT_CARD));
    hold(&mut world);
    assert!(!shown(&world, AGENT_CARD));
    rest_on(&mut world, 24, 0);
    assert!(!shown(&world, AGENT_CARD));
    lets_go(&mut world);
    rests(&mut world);
}

#[test]
fn labels_follow_the_text_and_keep_their_letters() {
    let mut world = reading();
    hold(&mut world);
    write(
        &mut world,
        2,
        &format!("\x1b[2J\x1b[H{SEED}\r\n{BUILDER_SHORT}"),
    );
    assert_eq!(on_ids(&world, "r1"), [ON_SEED, ON_BUILDER]);
    press(&mut world, 'f', false);
    assert!(shown(&world, SEED_CARD));
}

fn many(count: usize) -> (Vec<Seed>, String) {
    let seeds: Vec<Seed> = (0..count)
        .map(|i| Seed {
            profile_id: "work".into(),
            id: format!("s-{i:06}"),
            title: format!("Seed {i}"),
            status: "growing".into(),
            ..Seed::default()
        })
        .collect();
    let ids: Vec<&str> = seeds.iter().map(|s| s.id.as_str()).collect();
    let text = ids.join("\r\n");
    (seeds, text)
}

#[test]
fn past_one_letter_each_every_label_has_two_and_none_starts_another() {
    let (seeds, text) = many(18);
    let mut world = start(vec![], seeds, vec![]);
    write(&mut world, 1, &text);
    hold(&mut world);
    let labels = on_ids(&world, "r1");
    assert_eq!(
        (labels.len(), labels[0].as_str(), labels[17].as_str()),
        (18, "SS Seed 0, growing", "DD Seed 17, growing")
    );
    press(&mut world, 'd', false);
    assert_eq!(
        on_ids(&world, "r1"),
        ["DS Seed 16, growing", "DD Seed 17, growing"],
        "the labels the first letter starts are left"
    );
    press(&mut world, 'd', false);
    assert!(shown(&world, SEED_CARD));
    key(&mut world, Key::Enter);
    assert_eq!(
        world.daemon.sent_of("open_seed")[0]["seed_id"],
        json!("s-000017")
    );
    assert_eq!(inputs(&world), []);
}

#[test]
fn an_id_that_appears_past_one_letter_each_is_labelled_and_a_late_letter_only_starts_a_label() {
    let (seeds, _) = many(17);
    let first: Vec<&str> = seeds[..16].iter().map(|s| s.id.as_str()).collect();
    let (first, last) = (first.join("\r\n"), seeds[16].id.clone());
    let mut world = start(vec![], seeds, vec![]);
    write(&mut world, 1, &first);
    hold(&mut world);
    let labels = on_ids(&world, "r1");
    assert_eq!(
        (labels.len(), labels[0].as_str(), labels[1].as_str()),
        (16, "S Seed 0, growing", "D Seed 1, growing")
    );

    write(&mut world, 2, &format!("\r\n{last}"));
    let labels = on_ids(&world, "r1");
    assert_eq!(
        (labels.len(), labels[0].as_str(), labels[16].as_str()),
        (17, "SS Seed 0, growing", "DS Seed 16, growing")
    );
    press(&mut world, 'd', false);
    assert!(!shown(&world, SEED_CARD), "D alone opens no card now");
    assert_eq!(on_ids(&world, "r1"), ["DS Seed 16, growing"]);
    press(&mut world, 's', false);
    lets_go(&mut world);
    assert!(shown(&world, SEED_CARD));
    key(&mut world, Key::Enter);
    assert_eq!(
        world.daemon.sent_of("open_seed")[0]["seed_id"],
        json!("s-000016")
    );
    assert_eq!(inputs(&world), []);
    rests(&mut world);
}

#[test]
fn a_half_typed_label_is_cleared_when_the_labels_get_one_letter_more() {
    let (seeds, _) = many(257);
    let ids: Vec<String> = seeds.iter().map(|s| s.id.clone()).collect();
    let mut world = start(vec![], seeds, vec![]);
    write(&mut world, 1, &ids[..17].join("\r\n"));
    hold(&mut world);
    let mut seq = 1;
    for screen in ids[..256].chunks(16) {
        seq += 1;
        write(
            &mut world,
            seq,
            &format!("\x1b[2J\x1b[H{}", screen.join("\r\n")),
        );
        assert_eq!(on_ids(&world, "r1").len(), screen.len());
    }
    write(
        &mut world,
        seq + 1,
        &format!("\x1b[2J\x1b[H{}\r\n{}", ids[1], ids[16]),
    );
    press(&mut world, 'd', false);
    assert_eq!(on_ids(&world, "r1"), ["DS Seed 16, growing"]);

    write(
        &mut world,
        seq + 2,
        &format!("\x1b[2J\x1b[H{}\r\n{}", ids[1], ids[256]),
    );
    assert_eq!(
        on_ids(&world, "r1"),
        ["SSD Seed 1, growing", "DSS Seed 256, growing"]
    );
    press(&mut world, 's', false);
    press(&mut world, 's', false);
    press(&mut world, 'd', false);
    lets_go(&mut world);
    key(&mut world, Key::Enter);
    assert_eq!(
        world.daemon.sent_of("open_seed")[0]["seed_id"],
        json!("s-000001")
    );
    assert_eq!(inputs(&world), []);
    rests(&mut world);
}

fn whole(world: &World, count: usize, drawing: &str) {
    let (pane, _) = world.runtime().presented("terminal.r1").unwrap();
    let elements = world.runtime().elements();
    let labels: Vec<_> = elements
        .iter()
        .filter(|e| {
            e.key
                .as_ref()
                .is_some_and(|k| k.starts_with("terminal.r1.label."))
        })
        .collect();
    assert_eq!(labels.len(), count, "{drawing}");
    for label in labels {
        let [x, y, w, h] = label.rect;
        assert!(
            x >= pane[0]
                && y >= pane[1]
                && x + w <= pane[0] + pane[2] + 0.5
                && y + h <= pane[1] + pane[3] + 0.5,
            "{drawing}: {:?} at {:?} leaves its pane {pane:?}",
            label.key,
            label.rect
        );
    }
}

#[test]
fn a_label_at_an_edge_of_its_pane_stays_whole_inside_it() {
    let mut world = start(vec![builder()], vec![mapped()], vec![]);
    let (cols, rows) = world.terminal_size("r1");
    let at_end = cols - SEED.len() as u16 + 1;
    write(
        &mut world,
        1,
        &format!(
            "{SEED}\x1b[1;{at_end}H{SEED}\x1b[{rows};1H{BUILDER_SHORT}\x1b[{rows};{at_end}H{SEED}"
        ),
    );
    hold(&mut world);
    whole(&world, 4, "Lantern");
    settings(&mut world, "id_labels", "name");
    whole(&world, 4, "Name in place");
    lets_go(&mut world);
    rests(&mut world);
}

#[test]
fn the_setting_names_targets_in_place_of_their_ids_with_no_list() {
    let mut world = reading();
    settings(&mut world, "id_labels", "name");
    hold(&mut world);
    assert_eq!(
        on_ids(&world, "r1"),
        [ON_BUILDER, ON_MAPPER, ON_SEED, ON_BUILDER]
    );
    assert_eq!(listed(&world, "r1"), [] as [&str; 0]);
    rests(&mut world);
    press(&mut world, 's', false);
    assert!(shown(&world, AGENT_CARD));
}

#[test]
fn settings_chooses_how_labels_look() {
    let mut world = reading();
    super::keys::press(&mut world, &attn_app::Action::UiOpenSettings);
    world
        .automate("press", json!({"target": {"key": "settings.nav.general"}}))
        .unwrap();
    world
        .automate(
            "press",
            json!({"target": {"role": "radio-button", "name": "Name in place"}}),
        )
        .unwrap();
    let written: Vec<_> = world.daemon.sent_of("set_setting");
    assert_eq!(
        (&written[0]["key"], &written[0]["value"]),
        (&json!("id_labels"), &json!("name"))
    );
}

#[test]
fn with_no_known_id_on_screen_holding_says_so() {
    let mut world = start(vec![], vec![], vec![]);
    write(&mut world, 1, "nothing to see");
    hold(&mut world);
    let screen = world.screen();
    let said = screen.descendants(Target::key("terminal.r1.labels"));
    assert!(
        said.iter()
            .any(|n| n.name.as_deref() == Some("No ids on screen")),
        "{screen}"
    );
    lets_go(&mut world);
    assert!(!labelled(&world));
    rests(&mut world);
}

fn reader(leaf: &str, runtime: &str, session: &str) -> DesktopPane {
    DesktopPane {
        session_id: session.into(),
        ..pane(leaf, runtime)
    }
}

fn with_sessions(panes: Vec<DesktopPane>, sessions: Vec<Session>) -> InitialStateMessage {
    let mut state = showing(panes, "daemon-1");
    state.sessions = Some(sessions);
    state
}

#[test]
fn every_terminal_on_screen_is_labelled_and_a_target_has_one_letter_across_them() {
    let panes = vec![reader("t1", "r1", "s1"), reader("t2", "r2", "s2")];
    let sessions = vec![
        agent("s1", "Reader"),
        agent("s2", "Second reader"),
        builder(),
    ];
    let mut state = with_sessions(panes, sessions);
    let stacked = tree::below(tree::pane("t1"), tree::pane("t2"));
    state.desktops.as_mut().unwrap()[0].tree_json = stacked.to_string();
    let mut world = World::start_sized(Daemon::scripted().state(state), (1400.0, 900.0));
    world.open_desktop();
    output(&world, 1, BUILDER_SHORT.as_bytes());
    let mut frame = vec![0x01, 2];
    frame.extend_from_slice(b"r2");
    frame.extend_from_slice(&1u32.to_be_bytes());
    frame.extend_from_slice(BUILDER_SHORT.as_bytes());
    world.daemon.emit_frame(frame);
    world.settle();

    hold(&mut world);
    assert_eq!(on_ids(&world, "r1"), [ON_BUILDER]);
    assert_eq!(on_ids(&world, "r2"), [ON_BUILDER]);
    assert_eq!(listed(&world, "r1"), [ON_BUILDER]);
    assert_eq!(listed(&world, "r2"), [] as [&str; 0]);
    press(&mut world, 's', false);
    assert!(
        shown(&world, AGENT_CARD),
        "the keyboard's terminal shows it"
    );
    rests(&mut world);
}

#[test]
fn a_popped_out_terminal_labels_its_ids_in_its_own_window() {
    let mut state = with_sessions(
        vec![reader("t1", "r1", "s1")],
        vec![agent("s1", "Reader"), builder()],
    );
    state.profiles = Some(vec![profile()]);
    let mut tile = tree::pane("t1");
    tile["window"] = json!({"x": 10, "y": 20, "width": 900, "height": 600});
    state.desktops.as_mut().unwrap()[0].tree_json = tree::row([tile]).to_string();
    let mut world = World::start(Daemon::scripted().state(state));
    world.open_desktop();
    let window = world.tile_windows()[0].key.clone();
    write(&mut world, 1, BUILDER_SHORT);

    let in_tile = |world: &mut World, chord: &str, mods: Mods| {
        let ev = Chord::parse(chord).unwrap();
        world.tile_gesture(&window, |app| {
            app.hold(mods);
            app.key(key_event(&ev));
        });
    };
    let (l, s) = if mac() {
        ("mod+l", "mod+s")
    } else {
        ("mod+shift+l", "mod+shift+s")
    };
    in_tile(&mut world, l, chord_mods());
    let tile = |world: &World, key: &str| {
        let screen = world.tile_screen(&window);
        screen.find(Target::key(key)).is_some()
    };
    assert!(tile(&world, "terminal.r1.hints"));
    assert!(!labelled(&world));
    in_tile(&mut world, s, chord_mods());
    assert!(tile(&world, AGENT_CARD));
    assert!(!tile(&world, "terminal.r1.hints"));
    assert_eq!(inputs(&world), []);
    world.advance(Duration::from_millis(250));
}

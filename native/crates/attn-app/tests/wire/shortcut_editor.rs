use std::collections::BTreeMap;
use std::sync::{Arc, Mutex};
use std::time::Duration;

use attn_app::{Action, Binding, Keymap};
use attn_protocol::{InitialStateMessage, Profile, SettingsUpdatedMessage};
use attn_testkit::{Daemon, Request, Target, World, key_event, message};
use rune::{Chord, Key, KeyEvent, KeyPlatform, Mods, PhysicalInput, PhysicalKeyCode, Shortcut};
use serde_json::{Value, json};

const SETTING: &str = "keybindings_config";

/// A daemon that keeps settings as the real one does: each write is answered, then broadcast.
fn keeping(settings: &[(&str, &str)]) -> Daemon {
    let stored: Arc<Mutex<BTreeMap<String, String>>> = Arc::new(Mutex::new(
        settings
            .iter()
            .map(|(k, v)| ((*k).to_owned(), (*v).to_owned()))
            .collect(),
    ));
    let daemon = Daemon::scripted().state(InitialStateMessage {
        selected_profile_id: Some("work".into()),
        profiles: Some(vec![Profile {
            id: "work".into(),
            name: "Work".into(),
            ..Profile::default()
        }]),
        settings: Some(stored.lock().unwrap().clone()),
        ..InitialStateMessage::new()
    });
    daemon.on("set_setting", move |r: &Request| {
        let key = r["key"].as_str().unwrap();
        let value = r["value"].as_str().unwrap();
        let mut settings = stored.lock().unwrap();
        if value.is_empty() {
            settings.remove(key);
        } else {
            settings.insert(key.into(), value.into());
        }
        vec![
            message(SettingsUpdatedMessage {
                request_id: r["request_id"].as_str().map(str::to_owned),
                changed_key: Some(key.into()),
                success: Some(true),
                ..SettingsUpdatedMessage::new()
            }),
            message(SettingsUpdatedMessage {
                settings: Some(settings.clone()),
                ..SettingsUpdatedMessage::new()
            }),
        ]
    });
    daemon
}

fn config(overrides: Value) -> String {
    json!({"version": 1, "overrides": overrides, "dock": {"collapsed": false, "items": ["dock.attention"]}})
        .to_string()
}

fn start(overrides: Option<Value>) -> World {
    let setting = overrides.map(config);
    let settings: Vec<(&str, &str)> = setting.iter().map(|s| (SETTING, s.as_str())).collect();
    World::start(keeping(&settings))
}

fn platform() -> KeyPlatform {
    KeyPlatform::current()
}

fn defaults() -> Keymap {
    Keymap::from_setting(None, platform())
}

/// The single combination `action` has by default on this platform, as `Chord::parse` reads it.
fn default_chord(action: &Action) -> Chord {
    match defaults().binding(action) {
        Some(Binding::Chord(chord)) => chord,
        other => panic!("{} has no single chord: {other:?}", action.id()),
    }
}

fn label(binding: Binding) -> String {
    defaults().label(binding)
}

fn press(world: &mut World, ev: KeyEvent) {
    world.gesture(|app| app.key(ev));
}

fn escape(world: &mut World) {
    press(
        world,
        KeyEvent {
            key: Key::Escape,
            mods: Mods::default(),
        },
    );
}

fn shows(world: &World, role: &str, name: &str) -> bool {
    world.screen().find(Target::role(role).name(name)).is_some()
}

fn editor_open(world: &World) -> bool {
    shows(world, "dialog", "Customize Shortcuts")
}

fn open_editor(world: &mut World) {
    press(world, key_event(&default_chord(&Action::UiShowShortcuts)));
    click(world, "shortcuts.edit");
    assert!(editor_open(world), "{}", world.screen());
    assert!(!shows(world, "dialog", "Keyboard shortcuts"));
    let focused = world.screen().focused().and_then(|n| n.name);
    assert_eq!(focused.as_deref(), Some("Filter shortcuts"));
}

fn click(world: &mut World, key: &str) {
    world
        .automate("press", json!({"target": {"key": key}}))
        .unwrap_or_else(|why| panic!("press {key}: {why}"));
}

fn keys_of(action: &Action) -> String {
    format!("shortcut-editor.keys.{}", action.id())
}

/// The keys `spec` names, pressed and let go as the keyboard reports them by position.
fn physical(world: &mut World, spec: &str) {
    let shortcut = Shortcut::parse_on(spec, platform()).unwrap_or_else(|why| panic!("{why}"));
    let (key, mods) = (shortcut.physical_key(), shortcut.modifiers());
    hit(world, key, mods);
}

fn hit(world: &mut World, key: PhysicalKeyCode, mods: Mods) {
    world.gesture(|app| {
        for pressed in [true, false] {
            app.runtime.physical_input(PhysicalInput::Key {
                key,
                mods,
                pressed,
                repeat: false,
            });
        }
    });
}

/// Records `spec` on `action`'s row, as clicking its keys and pressing them does.
fn record(world: &mut World, action: &Action, spec: &str) {
    click(world, &keys_of(action));
    physical(world, spec);
}

fn record_chord(world: &mut World, action: &Action, leader: &str, then: &str) {
    click(world, &format!("shortcut-editor.chord.{}", action.id()));
    physical(world, leader);
    physical(world, then);
}

/// The chord `spec` as `Chord::parse` reads it, the same keys on this platform.
fn chord(spec: &str) -> Chord {
    Chord::parse(spec).unwrap()
}

fn writes(world: &World) -> Vec<Value> {
    world
        .daemon
        .sent_of("set_setting")
        .iter()
        .filter(|r| r["key"] == SETTING)
        .map(|r| serde_json::from_str(r["value"].as_str().unwrap()).unwrap())
        .collect()
}

fn saved(world: &World) -> Value {
    writes(world).pop().expect("a keybindings write")
}

/// What the saved setting binds `action` to on this platform.
fn saved_binding(world: &World, action: &Action) -> Option<Binding> {
    Keymap::from_setting(Some(&saved(world).to_string()), platform()).binding(action)
}

fn row_has(world: &World, action: &Action, key: &str) -> bool {
    world
        .screen()
        .find(Target::key(format!(
            "shortcut-editor.{key}.{}",
            action.id()
        )))
        .is_some()
}

fn badge(world: &World, action: &Action, badge: &str) -> bool {
    world
        .screen()
        .find(Target::key(format!(
            "shortcut-editor.badge.{}.{badge}",
            action.id()
        )))
        .is_some()
}

/// The keys the row's recorder shows.
fn shown(world: &World, action: &Action) -> String {
    world
        .screen()
        .get(Target::key(keys_of(action)))
        .value
        .unwrap_or_default()
}

fn keycaps(binding: Binding) -> String {
    let chord = match binding {
        Binding::Chord(chord) => chord,
        Binding::Sequence { leader, .. } => leader,
    };
    Shortcut::parse_on(&chord.to_string(), platform())
        .unwrap()
        .keycaps()
        .join(" ")
}

fn pending(world: &World) -> Option<String> {
    world
        .screen()
        .find(Target::key("shortcut-editor.pending.text"))
        .and_then(|n| n.name)
}

fn alert(world: &World) -> Option<String> {
    world
        .screen()
        .all(Target::role("alert"))
        .into_iter()
        .find_map(|n| n.name.filter(|n| !n.is_empty()))
}

/// Lets the scroll bar a press scrolled into view fade, then checks that nothing else waits.
fn rest(world: &mut World) {
    world.advance(Duration::from_secs(1));
    assert!(world.runtime_is_at_rest());
}

fn overrides(world: &World) -> Value {
    saved(world)["overrides"].clone()
}

#[test]
fn recording_takes_the_keys_that_would_run_an_action_and_saves_them() {
    let mut world = start(None);
    open_editor(&mut world);

    record(&mut world, &Action::TilePopOut, "mod+shift+y");

    assert_eq!(
        overrides(&world)["tile.popOut"],
        json!({"key": "y", "meta": true, "shift": true})
    );
    assert_eq!(
        shown(&world, &Action::TilePopOut),
        keycaps(Binding::Chord(chord("mod+shift+y")))
    );
    assert!(editor_open(&world));
    assert!(world.runtime_is_at_rest());
}

#[test]
fn every_category_lists_its_current_keys_and_the_required_rows_cannot_be_unbound() {
    let mut world = start(None);
    open_editor(&mut world);

    for heading in [
        "Desktops & Sessions",
        "Panes & Terminals",
        "Markdown & Annotations",
        "Review & Git",
        "App",
    ] {
        assert!(shows(&world, "heading", heading), "{heading}");
    }
    let new_session = Action::SessionNew;
    assert_eq!(
        shown(&world, &new_session),
        keycaps(defaults().binding(&new_session).unwrap())
    );
    for action in [
        Action::SessionPrev,
        Action::SessionNext,
        Action::SessionHistoryBack,
        Action::SessionHistoryForward,
    ] {
        assert!(row_has(&world, &action, "unbind"), "{}", action.id());
    }
    assert!(badge(&world, &Action::UiOpenSettings, "required"));
    assert!(!row_has(&world, &Action::UiOpenSettings, "unbind"));
    assert!(world.runtime_is_at_rest());
}

#[test]
fn a_rebound_row_shows_its_keys_and_its_reset_drops_the_rebind() {
    let rebound = chord("mod+shift+m");
    let mut world = start(Some(
        json!({"session.new": {"key": "m", "meta": true, "shift": true}}),
    ));
    open_editor(&mut world);
    assert_eq!(
        shown(&world, &Action::SessionNew),
        keycaps(Binding::Chord(rebound))
    );

    let default = label(defaults().binding(&Action::SessionNew).unwrap());
    let reset = world
        .screen()
        .get(Target::key("shortcut-editor.reset.session.new"));
    assert_eq!(reset.name, Some(format!("Reset to {default}")));
    click(&mut world, "shortcut-editor.reset.session.new");

    assert_eq!(overrides(&world), json!({}));
    assert!(!badge(&world, &Action::SessionNew, "customized"));
    assert!(world.runtime_is_at_rest());
}

#[test]
fn the_shelf_width_keys_have_rows() {
    let mut world = start(None);
    open_editor(&mut world);

    for action in [Action::ShelfFewerTiles, Action::ShelfMoreTiles] {
        let row = world
            .screen()
            .get(Target::key(format!("shortcut-editor.row.{}", action.id())));
        assert_eq!(row.name.as_deref(), Some(action.label()));
        assert_eq!(
            shown(&world, &action),
            keycaps(defaults().binding(&action).unwrap())
        );
    }
    assert!(shows(&world, "group", "Fewer, wider tiles"));
    assert!(shows(&world, "group", "More, narrower tiles"));
}

#[test]
fn the_sidebar_width_keys_have_rows_and_resetting_the_width_has_none() {
    let mut world = start(None);
    open_editor(&mut world);

    assert!(shows(&world, "group", "Widen the sidebar"));
    assert!(shows(&world, "group", "Narrow the sidebar"));
    assert_eq!(
        shown(&world, &Action::SidebarNarrower),
        keycaps(defaults().binding(&Action::SidebarNarrower).unwrap())
    );
    assert!(!shows(&world, "group", "Reset sidebar width"));
}

#[test]
fn the_rows_name_where_new_tiles_open() {
    let mut world = start(None);
    open_editor(&mut world);

    for name in ["New session below", "New shell beside", "New shell below"] {
        assert!(shows(&world, "group", name), "{name}");
    }
}

#[test]
fn a_rebound_row_is_marked_customized_and_offers_its_default() {
    let mut world = start(Some(
        json!({"session.new": {"key": "m", "meta": true, "alt": true}}),
    ));
    open_editor(&mut world);

    assert!(badge(&world, &Action::SessionNew, "customized"));
    let default = label(defaults().binding(&Action::SessionNew).unwrap());
    assert!(shows(&world, "button", &format!("Reset to {default}")));
    assert!(!badge(&world, &Action::SessionNewHorizontal, "customized"));
    assert!(!row_has(&world, &Action::SessionNewHorizontal, "reset"));
}

#[test]
fn the_shortcuts_that_need_a_terminal_say_so_customized_or_not() {
    let mut world = start(Some(
        json!({"terminal.find": {"key": "y", "meta": true, "alt": true}}),
    ));
    open_editor(&mut world);

    assert!(badge(
        &world,
        &Action::TerminalToggleMaximize,
        "needs terminal"
    ));
    assert!(!badge(&world, &Action::SessionNew, "needs terminal"));
    assert!(badge(&world, &Action::TerminalFind, "customized"));
    assert!(badge(&world, &Action::TerminalFind, "needs terminal"));
}

#[test]
fn unbinding_a_shortcut_frees_its_keys_for_another_action() {
    let mut world = start(None);
    open_editor(&mut world);

    click(&mut world, "shortcut-editor.unbind.session.new");
    assert_eq!(overrides(&world)["session.new"], Value::Null);
    assert_eq!(shown(&world, &Action::SessionNew), "Record shortcut");
    assert!(!row_has(&world, &Action::SessionNew, "unbind"));

    let freed = default_chord(&Action::SessionNew);
    record(
        &mut world,
        &Action::TerminalToggleMaximize,
        &freed.to_string(),
    );

    assert_eq!(pending(&world), None);
    assert_eq!(
        saved_binding(&world, &Action::TerminalToggleMaximize),
        Some(Binding::Chord(freed))
    );
    rest(&mut world);
}

#[test]
fn taking_the_keys_of_another_shortcut_asks_first_naming_what_they_run() {
    for (holder, keys) in [
        (
            Action::DockAttention,
            default_chord(&Action::DockAttention).to_string(),
        ),
        (
            Action::DesktopSelect1,
            default_chord(&Action::DesktopSelect1).to_string(),
        ),
        (Action::DesktopSend8, "mod+alt+8".to_owned()),
        (Action::SidebarNarrower, "mod+alt+[".to_owned()),
    ] {
        let mut world = start(None);
        open_editor(&mut world);

        record(&mut world, &Action::SessionNew, &keys);

        assert_eq!(
            pending(&world),
            Some(format!(
                "{} is “{}”.",
                label(Binding::Chord(chord(&keys))),
                holder.label()
            )),
            "{keys}"
        );
        assert!(writes(&world).is_empty(), "{keys}");
        assert!(world.runtime_is_at_rest());
    }
}

#[test]
fn reassigning_takes_the_keys_and_unbinds_their_holder() {
    let mut world = start(None);
    open_editor(&mut world);
    let keys = default_chord(&Action::TerminalSplitHorizontal);

    record(&mut world, &Action::SessionNew, &keys.to_string());
    assert!(pending(&world).unwrap().contains("New shell below"));
    click(&mut world, "shortcut-editor.reassign");

    let overrides = overrides(&world);
    assert_eq!(
        overrides.as_object().unwrap().keys().collect::<Vec<_>>(),
        ["session.new", "terminal.splitHorizontal"]
    );
    assert_eq!(overrides["terminal.splitHorizontal"], Value::Null);
    assert_eq!(
        saved_binding(&world, &Action::SessionNew),
        Some(Binding::Chord(keys))
    );
    assert_eq!(pending(&world), None);
    assert!(world.runtime_is_at_rest());
}

/// `chord` as the Tauri app stores a combination.
fn stored(chord: Chord) -> Value {
    let spec = chord.to_string();
    let key = spec.rsplit('+').next().unwrap();
    let mut stored = json!({"key": key, "meta": true});
    for (name, held) in [
        ("alt", spec.contains("alt+")),
        ("shift", spec.contains("shift+")),
    ] {
        if held {
            stored[name] = json!(true);
        }
    }
    stored
}

#[test]
fn resetting_onto_a_default_another_action_now_holds_asks_first() {
    let new_session = default_chord(&Action::SessionNew);
    let mut world = start(Some(json!({
        "session.new": {"key": "j", "meta": true, "alt": true},
        "terminal.splitHorizontal": stored(new_session),
    })));
    open_editor(&mut world);

    click(&mut world, "shortcut-editor.reset.session.new");
    assert!(pending(&world).unwrap().contains("New shell below"));
    click(&mut world, "shortcut-editor.reassign");

    assert_eq!(overrides(&world), json!({"terminal.splitHorizontal": null}));
    assert!(world.runtime_is_at_rest());
}

/// Two chords led by `leader`: Agents with G and Desktop overview with H.
fn chords_led_by(leader: Chord) -> Value {
    json!({
        "ui.actionMenu": {"leader": stored(leader), "then": {"key": "g"}},
        "desktop.overview": {"leader": stored(leader), "then": {"key": "h"}},
    })
}

#[test]
fn taking_the_leader_of_several_chords_unbinds_every_one() {
    let leader = chord("mod+alt+y");
    let mut world = start(Some(chords_led_by(leader)));
    open_editor(&mut world);

    record(&mut world, &Action::SessionNew, &leader.to_string());
    assert_eq!(
        pending(&world),
        Some(format!(
            "{} is “{}” and “{}”.",
            label(Binding::Chord(leader)),
            Action::DesktopOverview.label(),
            Action::UiActionMenu.label(),
        ))
    );
    click(&mut world, "shortcut-editor.reassign");

    assert_eq!(
        saved_binding(&world, &Action::SessionNew),
        Some(Binding::Chord(leader))
    );
    for freed in [Action::UiActionMenu, Action::DesktopOverview] {
        assert_eq!(saved_binding(&world, &freed), None, "{}", freed.id());
        assert!(!row_has(&world, &freed, "unbind"), "{}", freed.id());
    }
    click(&mut world, "shortcut-editor.done");
    world.gesture(|app| app.press(&leader.to_string()));
    assert!(new_session_dialog(&world));
}

#[test]
fn taking_a_leader_that_also_leads_a_required_chord_is_refused() {
    let leader = chord("mod+alt+y");
    let mut world = start(Some(json!({
        "session.new": {"leader": stored(leader), "then": {"key": "g"}},
        "app.quit": {"leader": stored(leader), "then": {"key": "h"}},
    })));
    open_editor(&mut world);

    record(
        &mut world,
        &Action::SessionToggleSidebar,
        &leader.to_string(),
    );

    assert_eq!(
        alert(&world),
        Some(format!(
            "{} is reserved by “{}” and can’t be reassigned.",
            label(Binding::Chord(leader)),
            Action::AppQuit.label()
        ))
    );
    assert_eq!(pending(&world), None);
    assert!(writes(&world).is_empty());
}

#[test]
fn resetting_onto_the_leader_of_several_chords_unbinds_every_one_and_keeps_the_default() {
    let new_session = default_chord(&Action::SessionNew);
    let mut setting = chords_led_by(new_session);
    setting["session.new"] = json!({"key": "j", "meta": true, "alt": true});
    let mut world = start(Some(setting));
    open_editor(&mut world);

    click(&mut world, "shortcut-editor.reset.session.new");
    click(&mut world, "shortcut-editor.reassign");

    assert_eq!(
        overrides(&world),
        json!({"ui.actionMenu": null, "desktop.overview": null})
    );
    assert_eq!(
        saved_binding(&world, &Action::SessionNew),
        Some(Binding::Chord(new_session))
    );
    assert_eq!(
        shown(&world, &Action::SessionNew),
        keycaps(Binding::Chord(new_session))
    );
}

#[test]
fn a_modifier_the_keymap_cannot_match_is_refused_rather_than_dropped() {
    let (spec, refused) = if platform() == KeyPlatform::Mac {
        ("mod+ctrl+y", "Control")
    } else {
        ("mod+super+y", "Super")
    };
    let mut world = start(None);
    open_editor(&mut world);
    let before = shown(&world, &Action::TilePopOut);

    record(&mut world, &Action::TilePopOut, spec);

    let alert = alert(&world).unwrap_or_default();
    assert!(
        alert.starts_with(&format!("{refused} isn’t available as a shortcut modifier")),
        "{alert}"
    );
    assert!(writes(&world).is_empty());
    hit(&mut world, PhysicalKeyCode::Escape, Mods::default());
    assert_eq!(shown(&world, &Action::TilePopOut), before);
}

#[test]
fn close_session_keeps_the_keys_it_shares_with_closing_a_pane() {
    let mut world = start(Some(
        json!({"session.close": {"key": "j", "meta": true, "alt": true}}),
    ));
    open_editor(&mut world);

    record(
        &mut world,
        &Action::SessionClose,
        &default_chord(&Action::SessionClose).to_string(),
    );

    assert_eq!(pending(&world), None);
    assert_eq!(overrides(&world), json!({}));
    assert!(world.runtime_is_at_rest());
}

#[test]
fn recording_saves_the_accelerator_and_waits_past_a_lone_modifier() {
    let mut world = start(None);
    open_editor(&mut world);
    let held = Mods {
        cmd: true,
        ..Mods::default()
    };

    click(&mut world, &keys_of(&Action::TerminalToggleMaximize));
    let modifier = if platform() == KeyPlatform::Mac {
        PhysicalKeyCode::SuperLeft
    } else {
        PhysicalKeyCode::ControlLeft
    };
    world.gesture(|app| {
        app.runtime.physical_input(PhysicalInput::Key {
            key: modifier,
            mods: held,
            pressed: true,
            repeat: false,
        })
    });
    assert!(writes(&world).is_empty());
    hit(&mut world, PhysicalKeyCode::KeyU, held);

    assert_eq!(
        overrides(&world)["terminal.toggleMaximize"],
        json!({"key": "u", "meta": true})
    );
    assert!(world.runtime_is_at_rest());
}

#[cfg(target_os = "macos")]
#[test]
fn control_is_refused_as_a_modifier_on_macos() {
    let mut world = start(None);
    open_editor(&mut world);

    record(&mut world, &Action::TerminalToggleMaximize, "ctrl+k");

    let alert = alert(&world).unwrap_or_default();
    assert!(
        alert.starts_with("Control isn’t available as a shortcut modifier on macOS."),
        "{alert}"
    );
    assert!(writes(&world).is_empty());
}

#[test]
fn a_recorded_combination_runs_its_action_once_the_editor_closes() {
    let mut world = start(None);
    open_editor(&mut world);

    record(&mut world, &Action::UiActionMenu, "mod+shift+y");
    assert_eq!(
        shown(&world, &Action::UiActionMenu),
        keycaps(Binding::Chord(chord("mod+shift+y")))
    );
    assert_eq!(
        overrides(&world)["ui.actionMenu"],
        json!({"key": "y", "meta": true, "shift": true})
    );
    click(&mut world, "shortcut-editor.done");
    assert!(!editor_open(&world));

    world.gesture(|app| app.press("mod+shift+y"));
    assert!(shows(&world, "dialog", "Agents"));
}

#[test]
fn escape_stops_recording_and_keeps_the_binding() {
    let mut world = start(None);
    open_editor(&mut world);
    let before = shown(&world, &Action::UiActionMenu);

    click(&mut world, &keys_of(&Action::UiActionMenu));
    assert_eq!(shown(&world, &Action::UiActionMenu), "Press shortcut");
    hit(&mut world, PhysicalKeyCode::Escape, Mods::default());

    assert_eq!(shown(&world, &Action::UiActionMenu), before);
    assert!(editor_open(&world));
    assert!(writes(&world).is_empty());
    assert!(world.runtime_is_at_rest());
}

#[test]
fn a_shortcut_without_an_accelerator_warns_that_it_types_in_terminals() {
    let mut world = start(None);
    open_editor(&mut world);
    assert!(!row_has(&world, &Action::TerminalToggleMaximize, "risky"));

    record(&mut world, &Action::TerminalToggleMaximize, "q");

    assert_eq!(
        overrides(&world)["terminal.toggleMaximize"],
        json!({"key": "q"})
    );
    let warning = world
        .screen()
        .get(Target::key("shortcut-editor.risky.terminal.toggleMaximize"))
        .name
        .unwrap_or_default();
    assert!(
        warning.starts_with("No ")
            && warning.ends_with(" modifier — this may collide with typing in the terminal"),
        "{warning}"
    );
}

#[test]
fn a_chord_leader_without_a_modifier_is_refused_and_the_binding_stays() {
    let mut world = start(None);
    open_editor(&mut world);
    let before = shown(&world, &Action::TerminalToggleZoom);

    record_chord(&mut world, &Action::TerminalToggleZoom, "y", "g");

    let alert = alert(&world).unwrap_or_default();
    assert!(
        alert.starts_with("A chord leader needs a ") && alert.ends_with(" modifier."),
        "{alert}"
    );
    assert!(writes(&world).is_empty());
    hit(&mut world, PhysicalKeyCode::Escape, Mods::default());
    assert_eq!(shown(&world, &Action::TerminalToggleZoom), before);
}

#[test]
fn a_recorded_chord_runs_its_action_once_the_daemon_has_it() {
    let mut world = start(None);
    open_editor(&mut world);

    record_chord(&mut world, &Action::UiShowShortcuts, "mod+shift+y", "g");

    assert_eq!(
        overrides(&world)["ui.showShortcuts"],
        json!({"leader": {"key": "y", "meta": true, "shift": true}, "then": {"key": "g"}})
    );
    let then = world
        .screen()
        .get(Target::key("shortcut-editor.then-key.ui.showShortcuts"));
    assert!(then.name.unwrap_or_default().ends_with('G'));
    escape(&mut world);
    assert!(!editor_open(&world));

    world.gesture(|app| app.press("mod+shift+y"));
    world.gesture(|app| app.type_text("g"));
    assert!(shows(&world, "dialog", "Keyboard shortcuts"));
}

#[test]
fn a_chord_may_lead_with_the_rows_own_keys() {
    let mut world = start(None);
    open_editor(&mut world);
    let own = default_chord(&Action::UiActionMenu);

    record_chord(&mut world, &Action::UiActionMenu, &own.to_string(), "d");

    assert_eq!(
        saved_binding(&world, &Action::UiActionMenu),
        Some(Binding::Sequence {
            leader: own,
            then: chord("d")
        })
    );
    assert!(world.runtime_is_at_rest());
}

#[test]
fn a_chord_whose_leader_another_shortcut_uses_asks_first() {
    let mut world = start(None);
    open_editor(&mut world);
    let overview = default_chord(&Action::DesktopOverview);

    record_chord(
        &mut world,
        &Action::TerminalToggleZoom,
        &overview.to_string(),
        "x",
    );

    assert!(pending(&world).unwrap().ends_with("is “Desktop overview”."));
    assert!(writes(&world).is_empty());
}

#[test]
fn a_change_made_before_the_daemon_has_the_last_one_keeps_both() {
    let mut world = start(Some(json!({
        "session.new": {"key": "m", "meta": true, "shift": true},
        "terminal.find": {"key": "j", "meta": true, "shift": true},
    })));
    world.daemon.on("set_setting", |_: &Request| ());
    open_editor(&mut world);

    click(&mut world, "shortcut-editor.reset.session.new");
    click(&mut world, "shortcut-editor.reset.terminal.find");

    let writes = writes(&world);
    assert_eq!(writes.len(), 2);
    assert_eq!(writes[1]["overrides"], json!({}));
}

#[test]
fn restore_defaults_drops_every_rebind_and_keeps_the_rest_of_the_setting() {
    let mut world = start(Some(
        json!({"session.new": {"key": "m", "meta": true, "alt": true}}),
    ));
    open_editor(&mut world);

    click(&mut world, "shortcut-editor.restore");

    let saved = saved(&world);
    assert_eq!(saved["overrides"], json!({}));
    assert_eq!(saved["dock"]["items"], json!(["dock.attention"]));
    assert!(!badge(&world, &Action::SessionNew, "customized"));
    assert!(world.runtime_is_at_rest());
}

fn new_session_dialog(world: &World) -> bool {
    shows(world, "dialog", "New Session Location")
}

#[test]
fn the_platform_default_opens_a_new_session() {
    let mut world = start(None);

    press(&mut world, key_event(&default_chord(&Action::SessionNew)));

    assert!(new_session_dialog(&world));
}

#[test]
fn a_saved_rebinding_moves_new_session_to_its_keys() {
    let mut world = start(Some(
        json!({"session.new": {"key": "m", "meta": true, "alt": true}}),
    ));

    press(&mut world, key_event(&default_chord(&Action::SessionNew)));
    assert!(!new_session_dialog(&world));
    world.gesture(|app| app.press("mod+alt+m"));

    assert!(new_session_dialog(&world));
}

#[test]
fn a_saved_unbinding_leaves_new_session_without_keys() {
    let mut world = start(Some(json!({"session.new": null})));

    press(&mut world, key_event(&default_chord(&Action::SessionNew)));

    assert!(!new_session_dialog(&world));
}

fn filter(world: &mut World, text: &str) {
    world.gesture(|app| app.type_text(text));
}

fn filter_text(world: &World) -> String {
    world
        .screen()
        .get(Target::role("text-input").name("Filter shortcuts"))
        .value
        .unwrap_or_default()
}

#[test]
fn the_filter_keeps_the_rows_whose_label_or_keys_match() {
    let mut world = start(None);
    open_editor(&mut world);
    filter(&mut world, "focus active");
    assert!(shows(&world, "group", "Focus active pane"));
    assert!(!shows(&world, "group", "New session on this desktop"));

    let mut world = start(None);
    open_editor(&mut world);
    let keys = label(defaults().binding(&Action::SessionNewHorizontal).unwrap());
    filter(&mut world, &keys.to_lowercase());
    assert!(shows(&world, "group", "New session below"), "{keys}");
    assert!(!shows(&world, "group", "New session on this desktop"));
    assert!(world.runtime_is_at_rest());
}

#[test]
fn a_filter_that_matches_nothing_says_so() {
    let mut world = start(None);
    open_editor(&mut world);

    filter(&mut world, "  zzznope  ");

    assert!(shows(&world, "status", "No shortcuts match “zzznope”"));
    assert!(!shows(&world, "heading", "Panes & Terminals"));
}

#[test]
fn keys_typed_into_the_filter_are_never_recorded_and_typing_drops_a_reassign() {
    let mut world = start(None);
    open_editor(&mut world);
    let holder = default_chord(&Action::TerminalSplitHorizontal);

    record(&mut world, &Action::SessionNew, &holder.to_string());
    assert!(pending(&world).is_some());
    click(&mut world, "input");
    filter(&mut world, "new session");
    assert_eq!(pending(&world), None);
    assert!(!shows(&world, "button", "Reassign"));

    let mut world = start(None);
    open_editor(&mut world);
    click(&mut world, &keys_of(&Action::TerminalToggleMaximize));
    assert_eq!(
        shown(&world, &Action::TerminalToggleMaximize),
        "Press shortcut"
    );
    click(&mut world, "input");
    filter(&mut world, "zoom");

    assert_eq!(filter_text(&world), "zoom");
    assert!(writes(&world).is_empty());
    assert!(world.runtime_is_at_rest());
}

#[test]
fn reopening_starts_with_no_filter_and_nothing_recording() {
    let mut world = start(None);
    open_editor(&mut world);
    filter(&mut world, "palette");
    click(
        &mut world,
        &format!("shortcut-editor.chord.{}", Action::UiActionMenu.id()),
    );
    physical(
        &mut world,
        &default_chord(&Action::UiActionMenu).to_string(),
    );
    assert!(
        world
            .screen()
            .find(Target::key("shortcut-editor.then"))
            .is_some()
    );

    click(&mut world, "shortcut-editor.done");
    open_editor(&mut world);

    assert_eq!(filter_text(&world), "");
    assert!(
        world
            .screen()
            .find(Target::key("shortcut-editor.then"))
            .is_none()
    );
    assert_eq!(
        shown(&world, &Action::UiActionMenu),
        keycaps(defaults().binding(&Action::UiActionMenu).unwrap())
    );
    assert!(writes(&world).is_empty());
}

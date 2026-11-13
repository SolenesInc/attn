use std::sync::{Arc, Mutex};
use std::time::Duration;

use attn_app::{Action, Binding, Keymap};
use attn_protocol::{
    Desktop, GetCommandUsageResultMessage, InitialStateMessage, Profile,
    ProfileActionResultMessage, ProfileArrangementChangedMessage, ProfilesChangedMessage,
};
use attn_testkit::{Daemon, Request, Target, World, key_event, message};
use rune::{Key, KeyEvent, KeyPlatform, Mods};
use serde_json::{Value, json};

#[test]
fn a_reconnected_app_asks_for_the_profile_the_daemon_scoped_it_to() {
    let daemon = Daemon::scripted().state(InitialStateMessage {
        profiles: Some(vec![Profile {
            id: "work".into(),
            name: "Work".into(),
            ..Profile::default()
        }]),
        selected_profile_id: Some("work".into()),
        ..InitialStateMessage::new()
    });
    let mut world = World::start(daemon);

    world.reconnect();

    let hellos = world.daemon.sent_of("client_hello");
    assert_eq!(hellos.len(), 2);
    assert!(hellos[0].get("profile_id").is_none());
    assert_eq!(hellos[1]["profile_id"], "work");
    assert!(world.runtime_is_at_rest());
}

fn profile(id: &str, used: &str) -> Profile {
    Profile {
        id: id.into(),
        name: id.into(),
        current_desktop_id: format!("{id}-d1"),
        last_used_at: Some(used.into()),
        revision: 1,
    }
}

fn desktop_of(profile: &str) -> Desktop {
    Desktop {
        id: format!("{profile}-d1"),
        profile_id: profile.into(),
        name: String::new(),
        order_key: "k1".into(),
        shortcut_slot: Some(1),
        ..Desktop::default()
    }
}

fn work() -> Profile {
    profile("work", "2026-09-23T12:00:00Z")
}

fn home() -> Profile {
    profile("home", "2026-09-23T11:00:00Z")
}

fn side() -> Profile {
    profile("side", "2026-09-23T10:00:00Z")
}

/// The daemon's profiles as the real one keeps them: one connection scoped to `selected`.
struct Kept {
    profiles: Vec<Profile>,
    selected: String,
    not_empty: Vec<String>,
}

/// A daemon that keeps profiles as the real one does: it answers each command after the
/// arrangement it changes and before the broadcast, refuses a name another live profile holds
/// (ignoring case), a profile in `not_empty`, and the last profile.
fn keeping(profiles: Vec<Profile>, selected: &str, not_empty: &[&str]) -> Daemon {
    let kept = Arc::new(Mutex::new(Kept {
        profiles: profiles.clone(),
        selected: selected.into(),
        not_empty: not_empty.iter().map(|p| (*p).to_owned()).collect(),
    }));
    let daemon = Daemon::scripted().state(InitialStateMessage {
        desktops: Some(vec![desktop_of(selected)]),
        profiles: Some(profiles),
        selected_profile_id: Some(selected.into()),
        ..InitialStateMessage::new()
    });
    daemon.on("get_command_usage", |r: &Request| {
        message(GetCommandUsageResultMessage {
            profile_id: r["profile_id"].as_str().unwrap_or_default().into(),
            success: true,
            ..GetCommandUsageResultMessage::new()
        })
    });
    let select = kept.clone();
    daemon.on("profile_select", move |r: &Request| {
        let mut kept = select.lock().unwrap();
        let id = r["profile_id"].as_str().unwrap().to_owned();
        kept.selected = id.clone();
        let profile = kept.profiles.iter().find(|p| p.id == id).unwrap().clone();
        vec![arrangement(&profile), done("profile_select", Some(profile))]
    });
    let create = kept.clone();
    daemon.on("profile_create", move |r: &Request| {
        let mut kept = create.lock().unwrap();
        let name = r["name"].as_str().unwrap().to_owned();
        if let Some(refusal) = name_taken(&kept, &name, None) {
            return vec![refused("profile_create", &refusal, "name_taken")];
        }
        let made = Profile {
            id: format!("p-{}", name.to_lowercase()),
            name,
            current_desktop_id: String::new(),
            last_used_at: None,
            revision: 1,
        };
        kept.profiles.push(made.clone());
        vec![done("profile_create", Some(made)), changed(&kept)]
    });
    let rename = kept.clone();
    daemon.on("profile_rename", move |r: &Request| {
        let mut kept = rename.lock().unwrap();
        let id = r["profile_id"].as_str().unwrap().to_owned();
        let name = r["name"].as_str().unwrap().to_owned();
        if let Some(refusal) = name_taken(&kept, &name, Some(&id)) {
            return vec![refused("profile_rename", &refusal, "name_taken")];
        }
        let renamed = kept.profiles.iter_mut().find(|p| p.id == id).unwrap();
        renamed.name = name;
        renamed.revision += 1;
        let renamed = renamed.clone();
        vec![done("profile_rename", Some(renamed)), changed(&kept)]
    });
    let delete = kept;
    daemon.on("profile_delete", move |r: &Request| {
        let mut kept = delete.lock().unwrap();
        let id = r["profile_id"].as_str().unwrap().to_owned();
        let name = kept.profiles.iter().find(|p| p.id == id).unwrap().name.clone();
        if kept.not_empty.contains(&id) {
            let refusal = format!(
                "profile {name:?} is not empty: 1 live agents, 0 crew members, 0 automations, 0 tiles, 1 open seeds and 0 pending delegations and 0 running Garden reviews; clean up the profile yourself or ask your agent before deleting it"
            );
            return vec![refused("profile_delete", &refusal, "invalid")];
        }
        if kept.profiles.len() == 1 {
            let refusal =
                format!("profile {name:?} ({id}) is the last profile and cannot be deleted");
            return vec![refused("profile_delete", &refusal, "last_profile")];
        }
        kept.profiles.retain(|p| p.id != id);
        let mut replies = Vec::new();
        if kept.selected == id {
            let remaining = kept.profiles[0].clone();
            kept.selected = remaining.id.clone();
            replies.push(arrangement(&remaining));
        }
        replies.push(done("profile_delete", None));
        replies.push(changed(&kept));
        replies
    });
    daemon
}

fn name_taken(kept: &Kept, name: &str, renaming: Option<&str>) -> Option<String> {
    let holder = kept
        .profiles
        .iter()
        .find(|p| p.name.eq_ignore_ascii_case(name) && Some(p.id.as_str()) != renaming)?;
    Some(format!(
        "profile name {name:?} is already used by profile {:?} ({}); names ignore case",
        holder.name, holder.id
    ))
}

fn arrangement(profile: &Profile) -> Value {
    message(ProfileArrangementChangedMessage {
        profile: profile.clone(),
        desktops: vec![desktop_of(&profile.id)],
        ..ProfileArrangementChangedMessage::new()
    })
}

fn done(action: &str, profile: Option<Profile>) -> Value {
    message(ProfileActionResultMessage {
        action: action.into(),
        success: true,
        profile,
        ..ProfileActionResultMessage::new()
    })
}

fn refused(action: &str, error: &str, code: &str) -> Value {
    json!({
        "event": "profile_action_result",
        "action": action,
        "success": false,
        "error": error,
        "error_code": code,
    })
}

/// The live profiles, broadcast after a change.
fn changed(kept: &Kept) -> Value {
    message(ProfilesChangedMessage {
        profiles: kept.profiles.clone(),
        ..ProfilesChangedMessage::new()
    })
}

fn three(selected: &str) -> World {
    World::start(keeping(vec![work(), home(), side()], selected, &[]))
}

fn press(world: &mut World, key: Key) {
    world.gesture(|app| {
        app.key(KeyEvent {
            key,
            mods: Mods::default(),
        })
    });
}

fn letter(world: &mut World, letter: &str) {
    press(world, Key::Text(letter.into()));
}

fn press_action(world: &mut World, action: &Action) {
    let keymap = Keymap::from_setting(None, KeyPlatform::current());
    let Some(Binding::Chord(chord)) = keymap.binding(action) else {
        panic!("{} has no chord", action.id());
    };
    world.gesture(|app| app.key(key_event(&chord)));
}

fn open_switcher(world: &mut World) {
    press_action(world, &Action::ProfileSwitch);
    assert!(switcher_open(world), "{}", world.screen());
}

fn switcher_open(world: &World) -> bool {
    world
        .screen()
        .find(Target::role("menu").name("Switch profile"))
        .is_some()
}

fn rows(world: &World) -> Vec<String> {
    world
        .screen()
        .all(Target::role("menu-item"))
        .into_iter()
        .filter_map(|n| n.name)
        .collect()
}

fn focused(world: &World) -> Option<String> {
    world.screen().focused().and_then(|n| n.name.or(n.key))
}

fn focused_value(world: &World) -> Option<String> {
    world.screen().focused().and_then(|n| n.value)
}

fn alert(world: &World) -> Option<String> {
    world
        .screen()
        .find(Target::role("alert"))
        .and_then(|n| n.name)
}

fn selects(world: &World) -> Vec<String> {
    world
        .daemon
        .sent_of("profile_select")
        .iter()
        .map(|r| r["profile_id"].as_str().unwrap().to_owned())
        .collect()
}

fn selected(world: &mut World) -> String {
    let profiles = world.automate("state", json!({"of": "profiles"})).unwrap();
    profiles["selected"].as_str().unwrap().to_owned()
}

fn click(world: &mut World, key: &str) {
    world
        .automate("press", json!({"target": {"key": key}}))
        .unwrap_or_else(|why| panic!("press {key}: {why}"));
}

fn rest(world: &mut World) {
    world.advance(Duration::from_secs(1));
    assert!(world.runtime_is_at_rest());
}

#[test]
fn the_highlighted_profile_switches_even_after_another_client_reorders_the_list() {
    let mut world = three("work");
    open_switcher(&mut world);
    assert_eq!(rows(&world), ["work", "home", "side"]);
    assert_eq!(focused(&world).as_deref(), Some("Switch profile"));

    press(&mut world, Key::Down);
    world.daemon.emit(ProfilesChangedMessage {
        profiles: vec![work(), profile("home", "2026-09-23T09:00:00Z"), side()],
        ..ProfilesChangedMessage::new()
    });
    world.settle();
    assert_eq!(rows(&world), ["work", "side", "home"]);
    press(&mut world, Key::Enter);

    assert_eq!(selects(&world), ["home"]);
    assert!(!switcher_open(&world));
    assert_eq!(selected(&mut world), "home");
    rest(&mut world);
}

#[test]
fn return_on_a_row_tab_reached_switches_to_that_row() {
    let mut world = three("work");
    open_switcher(&mut world);

    while focused(&world).as_deref() != Some("home") {
        press(&mut world, Key::Tab);
    }
    press(&mut world, Key::Enter);

    assert_eq!(selects(&world), ["home"]);
    assert!(!switcher_open(&world));
    rest(&mut world);
}

#[test]
fn the_first_profile_takes_the_highlight_when_the_highlighted_one_is_deleted() {
    let mut world = three("side");
    open_switcher(&mut world);

    press(&mut world, Key::Up);
    world.daemon.emit(ProfilesChangedMessage {
        profiles: vec![work(), side()],
        ..ProfilesChangedMessage::new()
    });
    world.settle();
    press(&mut world, Key::Enter);

    assert_eq!(selects(&world), ["work"]);
    rest(&mut world);
}

#[test]
fn n_names_a_new_profile_then_switches_to_it_and_closes() {
    let mut world = three("work");
    open_switcher(&mut world);

    letter(&mut world, "n");
    assert_eq!(focused(&world).as_deref(), Some("New profile name"));
    world.gesture(|app| app.type_text("Personal"));
    press(&mut world, Key::Enter);

    let created = world.daemon.sent_of("profile_create");
    assert_eq!(created.len(), 1);
    assert_eq!(created[0]["name"], "Personal");
    assert_eq!(selects(&world), ["p-personal"]);
    assert!(!switcher_open(&world));
    assert_eq!(selected(&mut world), "p-personal");
    rest(&mut world);
}

#[test]
fn a_refused_rename_shows_in_place_and_keeps_the_draft() {
    let mut world = three("work");
    open_switcher(&mut world);

    letter(&mut world, "r");
    assert_eq!(focused(&world).as_deref(), Some("Rename work"));
    assert_eq!(focused_value(&world).as_deref(), Some("work"));
    for _ in 0.."work".len() {
        press(&mut world, Key::Backspace);
    }
    world.gesture(|app| app.type_text("Home"));
    press(&mut world, Key::Enter);

    let renames = world.daemon.sent_of("profile_rename");
    assert_eq!(renames.len(), 1);
    assert_eq!(
        (&renames[0]["profile_id"], &renames[0]["name"]),
        (&json!("work"), &json!("Home"))
    );
    assert!(
        alert(&world).is_some_and(|said| said.contains("already used by profile \"home\"")),
        "{}",
        world.screen()
    );
    assert_eq!(focused(&world).as_deref(), Some("Rename work"));
    assert_eq!(focused_value(&world).as_deref(), Some("Home"));
    rest(&mut world);
}

#[test]
fn delete_asks_the_user_to_clean_up_the_profile_then_deletes_it() {
    let mut world = three("work");
    open_switcher(&mut world);

    press(&mut world, Key::Backspace);
    let ask = world
        .screen()
        .find(Target::key("profile-switcher.delete.ask"))
        .and_then(|n| n.name);
    assert_eq!(
        ask.as_deref(),
        Some("Delete work? Clean up its agents, crew, automations and tiles first.")
    );
    assert!(world.daemon.sent_of("profile_delete").is_empty());
    press(&mut world, Key::Enter);

    let deletes = world.daemon.sent_of("profile_delete");
    assert_eq!(deletes.len(), 1);
    assert_eq!(
        (&deletes[0]["profile_id"], &deletes[0]["expected_revision"]),
        (&json!("work"), &json!(1))
    );
    assert!(switcher_open(&world));
    assert_eq!(rows(&world), ["home", "side"]);
    assert_eq!(focused(&world).as_deref(), Some("Switch profile"));
    assert_eq!(selected(&mut world), "home");
    rest(&mut world);
}

#[test]
fn a_profile_that_holds_work_is_refused_in_place() {
    let mut world = World::start(keeping(vec![work(), home()], "work", &["home"]));
    open_switcher(&mut world);

    press(&mut world, Key::Down);
    press(&mut world, Key::Backspace);
    press(&mut world, Key::Enter);

    assert!(
        alert(&world).is_some_and(|said| said.starts_with("profile \"home\" is not empty:")),
        "{}",
        world.screen()
    );
    assert_eq!(rows(&world), Vec::<String>::new());
    press(&mut world, Key::Escape);
    assert_eq!(rows(&world), ["work", "home"]);
    assert_eq!(alert(&world), None);
    rest(&mut world);
}

#[test]
fn the_last_profile_says_why_it_cannot_be_deleted() {
    let mut world = World::start(keeping(vec![work()], "work", &[]));
    open_switcher(&mut world);

    press(&mut world, Key::Delete);

    assert_eq!(
        alert(&world).as_deref(),
        Some("work is the last profile, and attn always keeps one.")
    );
    assert!(world.daemon.sent_of("profile_delete").is_empty());
    rest(&mut world);
}

#[test]
fn escape_closes_the_list_wherever_focus_is_in_it() {
    let mut world = three("work");
    open_switcher(&mut world);
    while focused(&world).as_deref() != Some("home") {
        press(&mut world, Key::Tab);
    }

    press(&mut world, Key::Escape);

    assert!(!switcher_open(&world));
    assert!(selects(&world).is_empty());
    rest(&mut world);
}

#[test]
fn escape_returns_from_a_pointer_opened_rename_delete_or_new_profile_and_the_keys_still_work() {
    for (button, field, renamed) in [
        (
            "profile-switcher.rename.home",
            Some("Rename home"),
            "Rename home",
        ),
        ("profile-switcher.delete.home", None, "Rename home"),
        (
            "profile-switcher.new",
            Some("New profile name"),
            "Rename work",
        ),
    ] {
        let mut world = three("work");
        open_switcher(&mut world);

        click(&mut world, button);
        match field {
            Some(field) => assert_eq!(focused(&world).as_deref(), Some(field), "{button}"),
            None => assert_eq!(
                focused(&world).as_deref(),
                Some("Switch profile"),
                "{button}"
            ),
        }
        press(&mut world, Key::Escape);
        assert!(switcher_open(&world), "{button}");
        assert_eq!(
            focused(&world).as_deref(),
            Some("Switch profile"),
            "{button}"
        );

        letter(&mut world, "r");
        assert_eq!(focused(&world).as_deref(), Some(renamed), "{button}");
        press(&mut world, Key::Escape);
        press(&mut world, Key::Backspace);
        assert!(
            world
                .screen()
                .find(Target::key("profile-switcher.delete.ask"))
                .is_some(),
            "{button}"
        );
        press(&mut world, Key::Escape);
        assert!(switcher_open(&world), "{button}");
        press(&mut world, Key::Escape);
        assert!(!switcher_open(&world), "{button}");
        assert!(selects(&world).is_empty(), "{button}");
        rest(&mut world);
    }
}

#[test]
fn the_title_bars_profile_pill_names_the_profile_and_opens_the_list_under_it() {
    let mut world = three("work");
    let pill = world.screen().get(Target::key("titlebar.profile"));
    assert_eq!(pill.role, "button");
    assert_eq!(pill.name.as_deref(), Some("work"));
    assert!(
        pill.description
            .as_deref()
            .is_some_and(|d| d.starts_with("Switch profile")),
        "{pill:?}"
    );

    click(&mut world, "titlebar.profile");

    assert!(switcher_open(&world));
    let rect = |world: &mut World, key: &str| -> [f64; 4] {
        let found = world
            .automate("find", json!({"target": {"key": key}}))
            .unwrap();
        serde_json::from_value(found["rect"].clone()).unwrap()
    };
    let [px, py, pw, ph] = rect(&mut world, "titlebar.profile");
    let [mx, my, mw, _] = rect(&mut world, "profile-switcher");
    assert!(my >= py + ph, "the list opens under the pill");
    assert!(
        (mx + mw - (px + pw)).abs() <= 1.0,
        "the list's right edge meets the pill's"
    );
    click(&mut world, "titlebar.profile");
    assert!(!switcher_open(&world));
    rest(&mut world);
}

#[test]
fn the_palette_opens_the_switcher_and_records_a_switch_in_the_profile_it_was_picked_in() {
    let mut world = three("work");

    press_action(&mut world, &Action::UiCommandPalette);
    world.gesture(|app| app.type_text("Switch profile"));
    press(&mut world, Key::Enter);
    assert!(switcher_open(&world));
    press(&mut world, Key::Escape);
    assert!(!switcher_open(&world));

    press_action(&mut world, &Action::UiCommandPalette);
    world.gesture(|app| app.type_text("Switch to side"));
    press(&mut world, Key::Enter);

    let sent: Vec<Value> = world
        .daemon
        .sent()
        .iter()
        .filter(|r| r["cmd"] == "record_command_usage" || r["cmd"] == "profile_select")
        .map(|r| {
            let mut r = r.clone();
            r.as_object_mut().unwrap().remove("request_id");
            r
        })
        .collect();
    assert_eq!(
        sent,
        [
            json!({"cmd": "record_command_usage", "profile_id": "work", "command_id": "switch-profile"}),
            json!({"cmd": "record_command_usage", "profile_id": "work", "command_id": "profile-side"}),
            json!({"cmd": "profile_select", "profile_id": "side"}),
        ]
    );
    assert_eq!(selected(&mut world), "side");
    rest(&mut world);
}

#[test]
fn the_window_moves_to_another_profile_when_its_arrangement_arrives_not_when_the_select_answers() {
    let mut world = three("work");
    world.daemon.answer_in_turn(
        "profile_select",
        [attn_testkit::Answer::Reply(done("profile_select", None))],
    );

    press_action(&mut world, &Action::UiCommandPalette);
    world.gesture(|app| app.type_text("Switch to home"));
    press(&mut world, Key::Enter);
    assert_eq!(selects(&world), ["home"]);
    assert_eq!(selected(&mut world), "work");

    world.daemon.emit(ProfileArrangementChangedMessage {
        profile: home(),
        desktops: vec![desktop_of("home")],
        ..ProfileArrangementChangedMessage::new()
    });
    world.settle();

    assert_eq!(selected(&mut world), "home");
    let desktops = world.automate("state", json!({"of": "desktops"})).unwrap();
    assert!(desktops.to_string().contains("home-d1"), "{desktops}");
    assert!(!desktops.to_string().contains("work-d1"), "{desktops}");
    rest(&mut world);
}

#[test]
fn the_window_moves_to_the_profile_the_daemon_scoped_it_to_when_its_profile_is_deleted() {
    let mut world = three("work");

    world.daemon.emit(ProfilesChangedMessage {
        profiles: vec![home(), side()],
        ..ProfilesChangedMessage::new()
    });
    world.daemon.emit(ProfileArrangementChangedMessage {
        profile: home(),
        desktops: vec![desktop_of("home")],
        ..ProfileArrangementChangedMessage::new()
    });
    world.settle();

    assert_eq!(selected(&mut world), "home");
    let pill = world.screen().get(Target::key("titlebar.profile"));
    assert_eq!(pill.name.as_deref(), Some("home"));
    rest(&mut world);
}

#[test]
fn the_window_ends_on_the_profile_whose_arrangement_arrived_last() {
    let mut world = three("work");

    world.daemon.emit(ProfileArrangementChangedMessage {
        profile: work(),
        desktops: vec![desktop_of("work")],
        ..ProfileArrangementChangedMessage::new()
    });
    world.daemon.emit(ProfileArrangementChangedMessage {
        profile: home(),
        desktops: vec![desktop_of("home")],
        ..ProfileArrangementChangedMessage::new()
    });
    world.settle();

    assert_eq!(selected(&mut world), "home");
    rest(&mut world);
}

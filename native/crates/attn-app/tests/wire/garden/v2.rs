use std::collections::BTreeMap;

use attn_protocol::{
    Ask, GardenBed, GardenBedDeleteResultMessage, GardenBedSaveResultMessage,
    GardenBedsUpdatedMessage, InitialStateMessage, PartyView, Profile, Seed,
    SeedBedSetResultMessage, SeedPlotProgress, Session, SessionStateChangedMessage,
    SettingsUpdatedMessage,
};
use attn_testkit::{Daemon, Request, Target, message};
use rune::{Key, KeyEvent, Mods};

use super::*;

fn bed(id: &str, name: &str, repos: &[&str]) -> GardenBed {
    GardenBed {
        id: id.into(),
        profile_id: PROFILE.into(),
        name: name.into(),
        slug: name.to_lowercase().replace(' ', "-"),
        note: None,
        repos: repos.iter().map(|r| (*r).to_owned()).collect(),
        created_at: "2026-11-13T09:00:00Z".into(),
        updated_at: "2026-11-13T09:00:00Z".into(),
    }
}

fn v2_daemon(
    enabled: bool,
    beds: Vec<GardenBed>,
    seeds: Vec<Seed>,
    sessions: Vec<Session>,
    asks: Vec<Ask>,
) -> Daemon {
    let mut settings = BTreeMap::new();
    settings.insert(
        "garden_v2_enabled".into(),
        if enabled { "true" } else { "false" }.into(),
    );
    let daemon = Daemon::scripted().state(InitialStateMessage {
        selected_profile_id: Some(PROFILE.into()),
        profiles: Some(vec![Profile {
            id: PROFILE.into(),
            name: "Work".into(),
            ..Profile::default()
        }]),
        settings: Some(settings),
        sessions: Some(sessions.clone()),
        asks: Some(asks),
        seeds_total: Some(seeds.len() as i64),
        seeds: Some(seeds.clone()),
        garden_beds: Some(beds.clone()),
        ..InitialStateMessage::new()
    });
    attach_garden_handlers(&daemon, seeds, sessions, beds);
    daemon
}

fn text_key(ch: &str) -> KeyEvent {
    KeyEvent {
        key: Key::Text(ch.into()),
        mods: Mods::default(),
    }
}

fn plain_key(key: Key) -> KeyEvent {
    KeyEvent {
        key,
        mods: Mods::default(),
    }
}

#[test]
fn flag_off_keeps_v1_list_board_and_dock_unchanged() {
    let beds = vec![bed("b-pay001", "Payments", &["payments-api"])];
    let s1 = with(seed("s-one001", "Refactor checkout"), |s| {
        s.bed_id = Some("b-pay001".into());
        s.bed = Some("Payments".into());
        s.pulse = Some("Running wire tests".into());
    });
    let mut world = open_garden_sized(
        v2_daemon(false, beds, vec![s1], Vec::new(), Vec::new()),
        TWO_COLUMNS,
    );
    assert!(
        world
            .screen()
            .find(Target::key("garden.dock.beds"))
            .is_none()
    );
    assert!(
        world
            .screen()
            .find(Target::key("garden.dock.lens"))
            .is_none()
    );

    world.gesture(|app| app.key(board()));
    assert!(world.screen().find(Target::key("garden.beds")).is_none());
    assert!(world.screen().find(Target::key("garden.lens")).is_none());

    click(&mut world, "board");
    assert!(world.screen().find(Target::key("garden.beds")).is_none());
    assert!(world.runtime_is_at_rest());
}

#[test]
fn v2_fullscreen_list_separates_automations_filters_beds_and_lenses_and_shows_pulse() {
    let beds = vec![
        bed("b-attn01", "attn", &["attn"]),
        bed("b-pay001", "Payments", &["payments-api"]),
    ];
    let waiting_session = Session {
        id: "sess-wait".into(),
        label: "garden-cleanup".into(),
        agent: "claude".into(),
        state: "waiting_input".into(),
        turn_owed: Some(true),
        profile_id: PROFILE.into(),
        ..Session::default()
    };
    let working_session = Session {
        id: "sess-work".into(),
        label: "checkout-worker".into(),
        agent: "codex".into(),
        state: "working".into(),
        profile_id: PROFILE.into(),
        ..Session::default()
    };

    let waiting_seed = with(seed("s-wait01", "Garden beds & lenses"), |s| {
        s.status = "growing".into();
        s.claimed = true;
        s.bed_id = Some("b-attn01".into());
        s.bed = Some("attn".into());
        s.pulse = Some("Awaiting UI sign-off".into());
        s.tender = Some(PartyView {
            name: "garden-cleanup".into(),
            r#ref: "session:sess-wait".into(),
            session_id: Some("sess-wait".into()),
        });
    });
    let live_seed = with(seed("s-live01", "Idempotent Stripe retries"), |s| {
        s.status = "growing".into();
        s.claimed = true;
        s.bed_id = Some("b-pay001".into());
        s.bed = Some("Payments".into());
        s.pulse = Some("Running integration suite".into());
        s.tender = Some(PartyView {
            name: "checkout-worker".into(),
            r#ref: "session:sess-work".into(),
            session_id: Some("sess-work".into()),
        });
    });
    let quiet_seed = with(seed("s-quiet1", "Detached HEAD sweep guard"), |s| {
        s.status = "growing".into();
        s.claimed = false;
        s.bed_id = Some("b-attn01".into());
        s.bed = Some("attn".into());
    });
    let unassigned_ready = with(seed("s-un0001", "Tax docs cleanup"), |s| {
        s.ready = true;
    });
    let auto_seed = with(seed("s-auto01", "Daily main review"), |s| {
        s.status = "growing".into();
        s.claimed = true;
        s.automation_id = Some(42);
        s.pulse = Some("Scanning 6 commits".into());
    });

    let mut world = open_garden_sized(
        v2_daemon(
            true,
            beds,
            vec![
                waiting_seed,
                live_seed,
                quiet_seed,
                unassigned_ready,
                auto_seed,
            ],
            vec![waiting_session, working_session],
            Vec::new(),
        ),
        TWO_COLUMNS,
    );
    world.gesture(|app| app.key(board()));

    assert!(world.screen().find(Target::key("garden.beds")).is_some());
    assert!(
        world
            .screen()
            .find(Target::key("garden.bed.b-attn01"))
            .is_some()
    );
    assert!(
        world
            .screen()
            .find(Target::key("garden.bed.b-pay001"))
            .is_some()
    );
    assert!(
        world
            .screen()
            .find(Target::key("garden.bed.unassigned"))
            .is_some()
    );
    assert!(
        world
            .screen()
            .find(Target::key("garden.bed.automations"))
            .is_some()
    );

    assert!(has_row(&world, "Garden beds & lenses"));
    assert!(has_row(&world, "Idempotent Stripe retries"));
    assert!(!has_row(&world, "Detached HEAD sweep guard"));
    assert!(!has_row(&world, "Tax docs cleanup"));
    assert!(!has_row(&world, "Daily main review"));
    assert!(row_says(&world, "s-wait01", "Awaiting UI sign-off"));
    assert!(
        world
            .screen()
            .find(Target::key("garden.group.waiting"))
            .is_some()
    );
    assert!(
        world
            .screen()
            .find(Target::key("garden.group.rest"))
            .is_some()
    );

    world.gesture(|app| app.click(Target::key("garden.lens.triage")));
    assert!(has_row(&world, "Detached HEAD sweep guard"));
    assert!(has_row(&world, "Tax docs cleanup"));
    assert!(!has_row(&world, "Garden beds & lenses"));
    assert!(!has_row(&world, "Daily main review"));

    world.gesture(|app| app.click(Target::key("garden.lens.all")));
    assert!(has_row(&world, "Garden beds & lenses"));
    assert!(has_row(&world, "Idempotent Stripe retries"));
    assert!(has_row(&world, "Detached HEAD sweep guard"));
    assert!(has_row(&world, "Tax docs cleanup"));
    assert!(!has_row(&world, "Daily main review"));

    world.gesture(|app| app.click(Target::key("garden.bed.b-pay001")));
    assert_eq!(rows(&world), vec!["Idempotent Stripe retries"]);

    world.gesture(|app| app.click(Target::key("garden.bed.automations")));
    assert_eq!(rows(&world), vec!["Daily main review"]);

    world.gesture(|app| app.click(Target::key("garden.bed.all")));
    open_row(&mut world, "Garden beds & lenses");
    assert!(
        world
            .screen()
            .find(Target::key("garden.head.bed"))
            .is_some()
    );
    assert!(
        world
            .screen()
            .find(Target::key("garden.head.pulse"))
            .is_some()
    );
    assert!(
        world
            .screen()
            .find(Target::key("garden.head.pulse.session"))
            .is_some()
    );
    assert!(world.runtime_is_at_rest());
}

#[test]
fn v2_inline_bed_form_creates_edits_and_deletes_beds_over_ws() {
    let existing = bed("b-attn01", "attn", &["attn"]);
    let daemon = v2_daemon(
        true,
        vec![existing.clone()],
        vec![seed("s-one001", "Seed one")],
        Vec::new(),
        Vec::new(),
    );
    daemon.on("garden_bed_save", |r: &Request| {
        let name = r["name"].as_str().unwrap_or("").to_owned();
        let id = r["id"]
            .as_str()
            .map(str::to_owned)
            .unwrap_or_else(|| "b-new001".to_owned());
        message(GardenBedSaveResultMessage {
            request_id: r["request_id"].as_str().unwrap().into(),
            success: true,
            bed: Some(GardenBed {
                id,
                profile_id: PROFILE.into(),
                name: name.clone(),
                slug: name.to_lowercase(),
                note: r["note"].as_str().map(str::to_owned),
                repos: r["repos"]
                    .as_array()
                    .map(|a| {
                        a.iter()
                            .filter_map(|v| v.as_str().map(str::to_owned))
                            .collect()
                    })
                    .unwrap_or_default(),
                created_at: "2026-11-13T09:00:00Z".into(),
                updated_at: "2026-11-13T09:00:00Z".into(),
            }),
            ..GardenBedSaveResultMessage::new()
        })
    });
    daemon.on("garden_bed_delete", |r: &Request| {
        message(GardenBedDeleteResultMessage {
            request_id: r["request_id"].as_str().unwrap().into(),
            success: true,
            unassigned_seeds: Some(0),
            ..GardenBedDeleteResultMessage::new()
        })
    });

    let mut world = open_garden_sized(daemon, TWO_COLUMNS);
    world.gesture(|app| app.key(board()));
    world.gesture(|app| app.click(Target::key("garden.beds.add")));
    assert!(
        world
            .screen()
            .find(Target::key("garden.bed-form"))
            .is_some()
    );

    world.gesture(|app| app.type_text("Relay"));
    world.gesture(|app| app.click(Target::key("garden.bed-form.repos")));
    world.gesture(|app| {
        app.type_text("cloud-relay");
        app.key(plain_key(Key::Enter));
        app.type_text("/work/payments,eu");
    });
    assert!(world.daemon.sent_of("garden_bed_save").is_empty());
    world.gesture(|app| app.click(Target::key("garden.bed-form.save")));

    let sent = world.daemon.sent_of("garden_bed_save");
    assert_eq!(sent.len(), 1);
    assert!(sent[0].get("id").is_none() || sent[0]["id"].is_null());
    assert_eq!(sent[0]["name"], "Relay");
    assert_eq!(
        sent[0]["repos"],
        serde_json::json!(["cloud-relay", "/work/payments,eu"])
    );
    assert!(
        world
            .screen()
            .find(Target::key("garden.bed-form"))
            .is_none()
    );

    world.daemon.emit(GardenBedsUpdatedMessage {
        profile_id: PROFILE.into(),
        beds: vec![
            existing.clone(),
            bed("b-new001", "Relay", &["cloud-relay", "/work/payments,eu"]),
        ],
        ..GardenBedsUpdatedMessage::new()
    });
    world.settle();
    assert!(
        world
            .screen()
            .find(Target::key("garden.bed.b-new001"))
            .is_some()
    );

    let [x, y, w, h] = world
        .screen()
        .bounds(Target::key("garden.bed.b-new001"))
        .unwrap();
    world.gesture(|app| app.move_to((x + w * 0.5, y + h * 0.5)));
    world.gesture(|app| app.click(Target::key("garden.bed.b-new001.edit")));
    assert_eq!(
        world
            .screen()
            .get(Target::key("garden.bed-form.name"))
            .value
            .as_deref(),
        Some("Relay")
    );
    assert_eq!(
        world
            .screen()
            .get(Target::key("garden.bed-form.repos"))
            .value
            .as_deref(),
        Some("cloud-relay\n/work/payments,eu")
    );
    assert!(
        world
            .screen()
            .find(Target::key("garden.bed-form.delete"))
            .is_some()
    );

    world.gesture(|app| app.type_text("Cloud Relay"));
    world.gesture(|app| app.click(Target::key("garden.bed-form.save")));
    let saves = world.daemon.sent_of("garden_bed_save");
    assert_eq!(saves.len(), 2);
    assert_eq!(saves[1]["id"], "b-new001");
    assert_eq!(saves[1]["name"], "Cloud Relay");
    assert_eq!(saves[1]["repos"], sent[0]["repos"]);
    assert!(
        world
            .screen()
            .find(Target::key("garden.bed-form"))
            .is_none()
    );

    world.gesture(|app| app.move_to((x + w * 0.5, y + h * 0.5)));
    world.gesture(|app| app.click(Target::key("garden.bed.b-new001.edit")));
    world.gesture(|app| app.click(Target::key("garden.bed-form.delete")));

    let deleted = world.daemon.sent_of("garden_bed_delete");
    assert_eq!(deleted.len(), 1);
    assert_eq!(deleted[0]["id"], "b-new001");
    assert!(
        world
            .screen()
            .find(Target::key("garden.bed-form"))
            .is_none()
    );
    assert!(world.runtime_is_at_rest());
}

#[test]
fn v2_b_shortcut_assigns_bed_over_ws_and_board_scopes_by_bed() {
    let beds = vec![
        bed("b-attn01", "attn", &["attn"]),
        bed("b-pay001", "Payments", &["payments-api"]),
    ];
    let s1 = with(seed("s-one001", "Card one"), |s| {
        s.status = "growing".into();
        s.claimed = true;
        s.bed_id = Some("b-attn01".into());
        s.bed = Some("attn".into());
        s.pulse = Some("Testing shortcut".into());
    });
    let s2 = with(seed("s-two002", "Card two"), |s| {
        s.status = "planted".into();
        s.ready = true;
        s.bed_id = Some("b-pay001".into());
        s.bed = Some("Payments".into());
    });
    let auto = with(seed("s-auto01", "Nightly job"), |s| {
        s.status = "growing".into();
        s.claimed = true;
        s.automation_id = Some(9);
    });
    let daemon = v2_daemon(
        true,
        beds,
        vec![s1.clone(), s2, auto],
        Vec::new(),
        Vec::new(),
    );
    daemon.on("seed_bed_set", move |r: &Request| {
        let mut updated = s1.clone();
        updated.bed_id = r["bed_id"].as_str().map(str::to_owned);
        updated.bed = Some("Payments".into());
        message(SeedBedSetResultMessage {
            request_id: r["request_id"].as_str().unwrap().into(),
            success: true,
            seed: Some(updated),
            ..SeedBedSetResultMessage::new()
        })
    });

    let mut world = open_garden_sized(daemon, TWO_COLUMNS);
    world.gesture(|app| app.key(board()));
    open_row(&mut world, "Card one");
    world.gesture(|app| app.key(text_key("b")));
    assert!(
        world
            .screen()
            .find(Target::key("garden.bed-picker"))
            .is_some()
    );
    world.gesture(|app| app.click(Target::key("garden.bed-picker.item.b-pay001")));
    let assigns = world.daemon.sent_of("seed_bed_set");
    assert_eq!(assigns.len(), 1);
    assert_eq!(assigns[0]["seed_id"], "s-one001");
    assert_eq!(assigns[0]["bed_id"], "b-pay001");
    assert!(
        world
            .screen()
            .find(Target::key("garden.bed-picker"))
            .is_none()
    );

    click(&mut world, "board");
    assert!(world.screen().find(Target::key("garden.beds")).is_some());
    assert!(
        world
            .screen()
            .find(Target::key("garden.board.card.s-one001"))
            .is_some()
    );
    assert!(
        world
            .screen()
            .find(Target::key("garden.board.card.s-two002"))
            .is_some()
    );
    assert!(
        world
            .screen()
            .find(Target::key("garden.board.card.s-auto01"))
            .is_none()
    );
    assert!(
        world
            .screen()
            .find(Target::key("garden.board.pulse.s-one001"))
            .is_some()
    );

    world.gesture(|app| app.click(Target::key("garden.bed.b-pay001")));
    assert!(
        world
            .screen()
            .find(Target::key("garden.board.card.s-one001"))
            .is_none()
    );
    assert!(
        world
            .screen()
            .find(Target::key("garden.board.card.s-two002"))
            .is_some()
    );

    world.gesture(|app| app.click(Target::key("garden.bed.automations")));
    assert!(
        world
            .screen()
            .find(Target::key("garden.board.card.s-auto01"))
            .is_some()
    );
    assert!(
        world
            .screen()
            .find(Target::key("garden.board.card.s-two002"))
            .is_none()
    );

    world.gesture(|app| app.key(plain_key(Key::Down)));
    world.gesture(|app| app.key(text_key("b")));
    assert!(
        world
            .screen()
            .find(Target::key("garden.bed-picker"))
            .is_none()
    );

    world.gesture(|app| app.click(Target::key("garden.bed.b-pay001")));
    world.gesture(|app| app.key(plain_key(Key::Down)));
    world.gesture(|app| app.key(text_key("b")));
    assert!(
        world
            .screen()
            .find(Target::key("garden.bed-picker"))
            .is_some()
    );
    world.gesture(|app| app.key(plain_key(Key::Escape)));
    assert!(
        world
            .screen()
            .find(Target::key("garden.bed-picker"))
            .is_none()
    );
    assert!(world.runtime_is_at_rest());
}

#[test]
fn v2_dock_shows_bed_chips_and_now_all_toggle_and_plot_bubbles_child_waiting() {
    let beds = vec![
        bed("b-attn01", "attn", &["attn"]),
        bed("b-mob001", "Mobile", &["mobile"]),
    ];
    let plot_seed = with(plot("s-plot01", "Auth rollout", ""), |s| {
        s.status = "planted".into();
        s.bed_id = Some("b-attn01".into());
        s.bed = Some("attn".into());
        s.plot_progress = Some(SeedPlotProgress {
            total: 2,
            done: 1,
            growing: 1,
            ..SeedPlotProgress::default()
        });
    });
    let child_ask = with(part_of("s-plot01", "s-kid001", "Confirm prod key"), |s| {
        s.status = "planted".into();
        s.claimed = false;
        s.bed_id = Some("b-attn01".into());
        s.bed = Some("attn".into());
    });
    let open_ask = Ask {
        id: "ask-1".into(),
        seed_id: Some("s-kid001".into()),
        subject: "Approve key rotation?".into(),
        ..Ask::default()
    };
    let ready_mobile = with(seed("s-mob001", "Swipe to settle"), |s| {
        s.ready = true;
        s.bed_id = Some("b-mob001".into());
        s.bed = Some("Mobile".into());
    });

    let mut world = open_garden_with(v2_daemon(
        true,
        beds,
        vec![plot_seed, child_ask, ready_mobile],
        Vec::new(),
        vec![open_ask],
    ));
    assert!(
        world
            .screen()
            .find(Target::key("garden.dock.beds"))
            .is_some()
    );
    assert!(
        world
            .screen()
            .find(Target::key("garden.dock.lens"))
            .is_some()
    );
    assert!(
        world
            .screen()
            .find(Target::key("garden.group.waiting"))
            .is_some()
    );
    assert!(
        world
            .screen()
            .find(Target::key("garden.group.rest"))
            .is_none()
    );
    assert_eq!(
        world
            .screen()
            .get(Target::key("garden.dock.bed.b-attn01"))
            .description
            .as_deref(),
        Some("Waiting on you")
    );
    assert!(has_row(&world, "Auth rollout"));
    assert!(!has_row(&world, "Swipe to settle"));

    world.gesture(|app| app.click(Target::key("garden.dock.lens")));
    assert!(has_row(&world, "Auth rollout"));
    assert!(has_row(&world, "Swipe to settle"));

    world.gesture(|app| app.click(Target::key("garden.dock.bed.b-mob001")));
    assert_eq!(rows(&world), vec!["Swipe to settle"]);
    assert!(world.runtime_rests_for(std::time::Duration::from_secs(30)));
}

#[test]
fn v2_waiting_group_follows_a_tenders_owed_turn_in_attention_queue_flow() {
    let tender = Session {
        id: "queue-tender".into(),
        label: "Rollout agent".into(),
        agent: "claude".into(),
        state: "waiting_input".into(),
        profile_id: PROFILE.into(),
        turn_owed: Some(true),
        ..Session::default()
    };
    let growing = with(seed("s-one001", "Check rollout"), |s| {
        s.status = "growing".into();
        s.claimed = true;
        s.tender = Some(PartyView {
            name: tender.label.clone(),
            r#ref: format!("session:{}", tender.id),
            session_id: Some(tender.id.clone()),
        });
    });
    let mut world = open_garden_with(v2_daemon(
        true,
        Vec::new(),
        vec![growing],
        vec![tender.clone()],
        Vec::new(),
    ));
    let settings = |queue| SettingsUpdatedMessage {
        settings: Some(BTreeMap::from([
            ("garden_v2_enabled".into(), "true".into()),
            ("queue_mode_enabled".into(), queue),
        ])),
        ..SettingsUpdatedMessage::new()
    };
    let waiting = |world: &World| {
        world
            .screen()
            .find(Target::key("garden.group.waiting"))
            .is_some()
    };
    world.daemon.emit(settings("true".into()));
    world.settle();
    assert!(waiting(&world));

    world.daemon.emit(SessionStateChangedMessage {
        session: Session {
            turn_owed: None,
            ..tender
        },
        ..SessionStateChangedMessage::new()
    });
    world.settle();
    assert!(has_row(&world, "Check rollout"));
    assert!(!waiting(&world));

    world.daemon.emit(settings("false".into()));
    world.settle();
    assert!(waiting(&world));
    assert!(world.runtime_is_at_rest());
}

#[test]
fn v2_virtualizes_large_gardens_and_rebuilds_only_one_changed_row_and_card() {
    let beds = vec![bed("b-attn01", "attn", &["attn"])];
    let seeds: Vec<Seed> = (0..250)
        .map(|i| {
            with(
                seed(&format!("s-v2r{i:03}"), &format!("Large seed {i}")),
                |s| {
                    s.ready = true;
                    s.bed_id = Some("b-attn01".into());
                    s.bed = Some("attn".into());
                },
            )
        })
        .collect();
    let mut world = open_garden_sized(
        v2_daemon(true, beds, seeds.clone(), Vec::new(), Vec::new()),
        TWO_COLUMNS,
    );
    world.gesture(|app| app.key(board()));
    let before_all = attn_app::garden::rows_built();
    world.gesture(|app| app.click(Target::key("garden.lens.all")));
    let built_initial = attn_app::garden::rows_built() - before_all;
    assert!(
        built_initial > 0 && built_initial < 80,
        "expected virtualized list to build only visible rows, built {built_initial}"
    );

    let before_change = attn_app::garden::rows_built();
    world.daemon.emit(GardenSeedsChangedMessage {
        profile_id: PROFILE.into(),
        order: seeds.iter().map(|s| s.id.clone()).collect(),
        seeds: vec![with(seeds[2].clone(), |s| {
            s.pulse = Some("Updated pulse".into());
        })],
        total: 250,
        ..GardenSeedsChangedMessage::new()
    });
    world.settle();
    assert_eq!(attn_app::garden::rows_built() - before_change, 1);

    click(&mut world, "board");
    let before_board = attn_app::garden::rows_built();
    world.daemon.emit(GardenSeedsChangedMessage {
        profile_id: PROFILE.into(),
        order: seeds.iter().map(|s| s.id.clone()).collect(),
        seeds: vec![with(seeds[1].clone(), |s| {
            s.pulse = Some("Board pulse".into());
        })],
        total: 250,
        ..GardenSeedsChangedMessage::new()
    });
    world.settle();
    assert_eq!(attn_app::garden::rows_built() - before_board, 1);
    assert!(world.runtime_is_at_rest());
}

#[test]
fn v2_keyboard_picker_assigns_root_for_child_clears_and_walks_with_jk() {
    let beds = vec![
        bed("b-attn01", "attn", &["attn"]),
        bed("b-pay001", "Payments", &["payments-api"]),
    ];
    let plot_seed = with(plot("s-plot01", "Checkout plot", ""), |s| {
        s.status = "growing".into();
        s.claimed = true;
        s.bed_id = Some("b-attn01".into());
        s.bed = Some("attn".into());
        s.plot_progress = Some(SeedPlotProgress {
            total: 1,
            growing: 1,
            ..SeedPlotProgress::default()
        });
    });
    let child = with(part_of("s-plot01", "s-kid001", "Webhook step"), |s| {
        s.status = "growing".into();
        s.claimed = true;
        s.bed_id = Some("b-attn01".into());
        s.bed = Some("attn".into());
    });
    let quiet_other = with(seed("s-pay009", "Ready billing task"), |s| {
        s.ready = true;
        s.bed_id = Some("b-pay001".into());
        s.bed = Some("Payments".into());
    });
    let daemon = v2_daemon(
        true,
        beds,
        vec![plot_seed.clone(), child, quiet_other],
        Vec::new(),
        Vec::new(),
    );
    daemon.on("seed_bed_set", move |r: &Request| {
        let mut updated = plot_seed.clone();
        updated.bed_id = r["bed_id"].as_str().map(str::to_owned);
        message(SeedBedSetResultMessage {
            request_id: r["request_id"].as_str().unwrap().into(),
            success: true,
            seed: Some(updated),
            ..SeedBedSetResultMessage::new()
        })
    });

    let mut world = open_garden_sized(daemon, TWO_COLUMNS);
    world.gesture(|app| app.key(board()));
    world.gesture(|app| app.click(Target::key("garden.bed.b-pay001")));
    assert!(
        world
            .screen()
            .find(Target::key("garden.v2.show-all"))
            .is_some()
    );
    world.gesture(|app| app.click(Target::key("garden.v2.show-all")));
    assert!(has_row(&world, "Ready billing task"));

    world.gesture(|app| app.click(Target::key("garden.bed.all")));
    world.gesture(|app| app.key(text_key("j")));
    assert_eq!(
        world.screen().focused().and_then(|n| n.key).as_deref(),
        Some("garden.row.s-plot01")
    );
    world.gesture(|app| app.key(text_key("j")));
    assert_eq!(
        world.screen().focused().and_then(|n| n.key).as_deref(),
        Some("garden.row.s-pay009")
    );
    world.gesture(|app| app.key(text_key("k")));
    assert_eq!(
        world.screen().focused().and_then(|n| n.key).as_deref(),
        Some("garden.row.s-plot01")
    );

    open_row(&mut world, "Checkout plot");
    world.gesture(|app| app.click(Target::key("garden.row.s-kid001")));
    assert_ne!(
        world.screen().get(Target::key("garden.head.bed")).role,
        "button"
    );
    world.gesture(|app| app.key(text_key("b")));
    assert!(
        world
            .screen()
            .find(Target::key("garden.bed-picker"))
            .is_some()
    );
    world.gesture(|app| app.key(plain_key(Key::Down)));
    world.gesture(|app| app.key(plain_key(Key::Enter)));
    let assigns = world.daemon.sent_of("seed_bed_set");
    assert_eq!(assigns.len(), 1);
    assert_eq!(assigns[0]["seed_id"], "s-plot01");
    assert_eq!(assigns[0]["bed_id"], "b-pay001");

    world.gesture(|app| app.key(text_key("b")));
    world.gesture(|app| app.click(Target::key("garden.bed-picker.clear")));
    let assigns = world.daemon.sent_of("seed_bed_set");
    assert_eq!(assigns.len(), 2);
    assert_eq!(assigns[1]["seed_id"], "s-plot01");
    assert!(assigns[1].get("bed_id").is_none() || assigns[1]["bed_id"].is_null());
    assert!(world.runtime_is_at_rest());
}

#[test]
fn v2_bed_search_autocomplete_pulse_session_jump_and_flag_off_keeps_bed_literal() {
    let beds = vec![
        bed("b-attn01", "attn", &["attn"]),
        bed("b-pay001", "Cloud Payments", &["payments-api"]),
        bed("b-pay002", "Cloud Payments Express", &["payments-express"]),
    ];
    let s1 = with(seed("s-one001", "Auth work"), |s| {
        s.status = "growing".into();
        s.claimed = true;
        s.bed_id = Some("b-attn01".into());
        s.bed = Some("attn".into());
        s.pulse = Some("Waiting on OAuth callback".into());
        s.tender = Some(PartyView {
            name: "auth-agent".into(),
            r#ref: "session:sess-1".into(),
            session_id: Some("sess-1".into()),
        });
    });
    let s2 = with(seed("s-two002", "Stripe retry"), |s| {
        s.ready = true;
        s.bed_id = Some("b-pay001".into());
        s.bed = Some("Cloud Payments".into());
    });
    let express = with(seed("s-exp001", "Express checkout"), |s| {
        s.bed_id = Some("b-pay002".into());
        s.bed = Some("Cloud Payments Express".into());
    });
    let sess = Session {
        id: "sess-1".into(),
        label: "auth-agent".into(),
        state: "waiting_input".into(),
        profile_id: PROFILE.into(),
        ..Session::default()
    };

    let mut world = open_garden_sized(
        v2_daemon(
            true,
            beds.clone(),
            vec![s1.clone(), s2.clone(), express],
            vec![sess],
            Vec::new(),
        ),
        TWO_COLUMNS,
    );
    world.gesture(|app| app.key(board()));
    world.gesture(|app| app.click(Target::key("garden.bed.b-attn01")));
    world.gesture(|app| app.click(Target::key("garden.search.field")));
    world.gesture(|app| app.type_text("OAuth"));
    assert_eq!(rows(&world), vec!["Auth work"]);
    assert!(row_says(&world, "s-one001", "Waiting on OAuth callback"));
    world.gesture(|app| {
        app.press("mod+a");
        app.type_text("bed:payments");
    });
    assert!(has_row(&world, "Stripe retry"));
    assert!(has_row(&world, "Express checkout"));
    world.gesture(|app| {
        app.press("mod+a");
        app.type_text("bed:");
    });
    assert!(
        world
            .screen()
            .find(Target::key("garden.search.bed.b-pay001"))
            .is_some()
    );
    world.gesture(|app| app.click(Target::key("garden.search.bed.b-pay001")));
    assert_eq!(rows(&world), vec!["Stripe retry"]);
    world.gesture(|app| app.key(plain_key(Key::Enter)));
    assert_eq!(
        world
            .screen()
            .get(Target::key("garden.head.title"))
            .name
            .as_deref(),
        Some("Stripe retry")
    );
    assert!(has_row(&world, "Stripe retry"));

    world.gesture(|app| app.click(Target::key("garden.bed.b-attn01")));
    open_row(&mut world, "Auth work");
    world.gesture(|app| app.click(Target::key("garden.head.pulse.session")));
    assert!(world.screen().find(Target::key("garden.body")).is_none());
    assert!(world.runtime_is_at_rest());

    let literal = with(seed("s-literal", "Literal bed token"), |s| {
        s.body = "Instructions mention bed:payments in plain text.".into();
    });
    let mut off_world = open_garden_sized(
        v2_daemon(false, beds, vec![s1, s2, literal], Vec::new(), Vec::new()),
        TWO_COLUMNS,
    );
    off_world.gesture(|app| app.key(board()));
    off_world.gesture(|app| app.click(Target::key("garden.search.field")));
    off_world.gesture(|app| app.type_text("bed:"));
    assert!(
        off_world
            .screen()
            .find(Target::key("garden.search.bed.b-pay001"))
            .is_none()
    );
    off_world.gesture(|app| app.type_text("payments"));
    assert_eq!(rows(&off_world), vec!["Literal bed token"]);
    assert!(off_world.runtime_is_at_rest());
}

#[test]
fn v2_bed_search_keeps_a_delegated_seed_in_its_root_automations_bed() {
    let run = with(plot("s-auto01", "Daily automation", ""), |s| {
        s.status = "growing".into();
        s.claimed = true;
        s.automation_id = Some(1);
    });
    let child = part_of(&run.id, "s-kid001", "Delegated work");
    let ordinary = seed("s-plain1", "Unassigned work");
    let mut world = open_garden_sized(
        v2_daemon(
            true,
            Vec::new(),
            vec![run, child, ordinary],
            Vec::new(),
            Vec::new(),
        ),
        TWO_COLUMNS,
    );
    world.gesture(|app| app.key(board()));
    world.gesture(|app| app.click(Target::key("garden.bed.automations")));
    open_row(&mut world, "Daily automation");
    assert!(has_row(&world, "Delegated work"));

    world.gesture(|app| app.click(Target::key("garden.search.field")));
    world.gesture(|app| app.type_text("bed:automations"));
    let screen = world.screen();
    let results = Target::key("garden.results");
    assert!(screen.find(results.clone()).is_some());
    assert!(
        screen
            .descendants(results)
            .iter()
            .any(|row| row.name.as_deref() == Some("Delegated work"))
    );
    let last = world
        .daemon
        .sent_of("garden_search")
        .into_iter()
        .last()
        .expect("garden_search for bed:automations");
    assert_eq!(last["beds"], serde_json::json!(["automations"]));
    assert!(last.get("bed_scope").is_none());

    world.gesture(|app| app.click(Target::key("garden.bed.all")));
    world.gesture(|app| app.click(Target::key("garden.search.field")));
    world.gesture(|app| {
        app.press("mod+a");
        app.type_text("bed:unassigned");
    });
    assert_eq!(rows(&world), vec!["Unassigned work"]);
    let last = world
        .daemon
        .sent_of("garden_search")
        .into_iter()
        .last()
        .expect("garden_search for bed:unassigned");
    assert_eq!(last["beds"], serde_json::json!(["unassigned"]));
    assert!(last.get("bed_scope").is_none());
    assert!(world.runtime_is_at_rest());
}

#[test]
fn v2_closed_automation_history_is_reachable_from_its_bed_and_an_exact_id() {
    let run = with(seed("s-auto01", "Nightly review"), |s| {
        s.status = "harvested".into();
        s.automation_id = Some(1);
    });
    let mut world = open_garden_sized(
        v2_daemon(true, Vec::new(), vec![run], Vec::new(), Vec::new()),
        TWO_COLUMNS,
    );
    assert!(
        world
            .screen()
            .find(Target::key("garden.dock.bed.automations"))
            .is_some()
    );
    world.gesture(|app| app.key(board()));
    assert!(!has_row(&world, "Nightly review"));
    world.gesture(|app| app.click(Target::key("garden.bed.automations")));
    world.gesture(|app| app.click(Target::key("garden.closed")));
    assert_eq!(rows(&world), vec!["Nightly review"]);

    world.gesture(|app| app.click(Target::key("garden.bed.all")));
    assert!(!has_row(&world, "Nightly review"));
    world.gesture(|app| app.click(Target::key("garden.search.field")));
    world.gesture(|app| {
        app.press("mod+a");
        app.type_text("s-auto01 is:any");
    });
    assert_eq!(rows(&world), vec!["Nightly review"]);
    let last = world
        .daemon
        .sent_of("garden_search")
        .into_iter()
        .last()
        .expect("garden_search for exact automation ID");
    assert_eq!(last["bed_scope"], "all");
    assert_eq!(last["terms"], serde_json::json!(["s-auto01"]));
    assert_eq!(last["is"], serde_json::json!(["any"]));
    assert!(world.runtime_is_at_rest());
}

#[test]
fn v2_walks_into_the_children_of_the_plot_opened_in_the_wide_list() {
    let plot = with(plot("s-plot01", "Checkout plot", ""), |s| {
        s.status = "growing".into();
        s.claimed = true;
        s.plot_progress.as_mut().unwrap().total = 2;
    });
    let first = part_of(&plot.id, "s-kid001", "First step");
    let second = part_of(&plot.id, "s-kid002", "Second step");
    let mut world = open_garden_sized(
        v2_daemon(
            true,
            Vec::new(),
            vec![plot, first, second],
            Vec::new(),
            Vec::new(),
        ),
        TWO_COLUMNS,
    );
    world.gesture(|app| app.key(board()));
    world.gesture(|app| app.key(plain_key(Key::Down)));
    assert_eq!(focused_row(&world).as_deref(), Some("Checkout plot"));
    world.gesture(|app| app.key(plain_key(Key::Right)));
    assert_eq!(
        world
            .screen()
            .get(Target::key("garden.head.title"))
            .name
            .as_deref(),
        Some("Checkout plot")
    );
    world.gesture(|app| app.key(plain_key(Key::Down)));
    assert_eq!(focused_row(&world).as_deref(), Some("First step"));
    world.gesture(|app| app.key(text_key("j")));
    assert_eq!(focused_row(&world).as_deref(), Some("Second step"));
    world.gesture(|app| app.key(text_key("k")));
    assert_eq!(focused_row(&world).as_deref(), Some("First step"));
    assert!(world.runtime_is_at_rest());
}

#[test]
fn v2_board_shows_readiness_reasons_and_harvest_conditions() {
    let blocker = with(seed("s-one001", "Start here"), |s| {
        s.ready = true;
        s.edges.push(edge("blocks", "s-two002"));
    });
    let blocked = seed("s-two002", "Waiting for work");
    let packet = with(seed("s-pack01", "Release packet"), |s| s.template = true);
    let gate = with(seed("s-gate01", "Release gate"), |s| s.gate = true);
    let merge = with(seed("s-pr0001", "Merge release"), |s| {
        s.harvest_when = Some(super::board::armed_on(42));
    });
    let parked_merge = with(seed("s-pr0002", "Parked for merge"), |s| {
        s.status = "parked".into();
        s.harvest_when = Some(super::board::armed_on(43));
    });
    let ready_merge = with(seed("s-pr0003", "Ready and armed"), |s| {
        s.ready = true;
        s.harvest_when = Some(super::board::armed_on(44));
    });
    let mut world = open_garden_sized(
        v2_daemon(
            true,
            Vec::new(),
            vec![
                blocker,
                blocked,
                packet,
                gate,
                merge,
                parked_merge,
                ready_merge,
            ],
            Vec::new(),
            Vec::new(),
        ),
        TWO_COLUMNS,
    );
    world.gesture(|app| app.key(board()));
    click(&mut world, "board");
    let screen = world.screen();
    for (id, reason) in [
        ("s-two002", "blocked by 1"),
        ("s-pack01", "packet"),
        ("s-gate01", "gate"),
        ("s-pr0001", "harvests on #42"),
        ("s-pr0002", "harvests on #43"),
        ("s-pr0003", "harvests on #44"),
    ] {
        let card = Target::key(format!("garden.board.card.{id}"));
        assert!(
            screen
                .descendants(card)
                .iter()
                .any(|row| row.name.as_deref() == Some(reason)),
            "{id} should show {reason}"
        );
    }
    assert!(world.runtime_is_at_rest());
}

#[test]
fn v2_uses_bed_scoped_closed_counts_shows_archived_automations_rail_and_pages_by_bed() {
    let beds = vec![bed("b-pay001", "Payments", &["payments-api"])];
    let open_pay = with(seed("s-open01", "Active payment"), |s| {
        s.bed_id = Some("b-pay001".into());
        s.bed = Some("Payments".into());
    });
    let archived_pay = with(
        closed(seed("s-payold", "Archived payment"), "harvested"),
        |s| {
            s.bed_id = Some("b-pay001".into());
            s.bed = Some("Payments".into());
        },
    );
    let archived_auto = with(
        closed(seed("s-autold", "Archived cron"), "harvested"),
        |s| {
            s.automation_id = Some(9);
        },
    );
    let mut settings = BTreeMap::new();
    settings.insert("garden_v2_enabled".into(), "true".into());
    let d = Daemon::scripted().state(InitialStateMessage {
        selected_profile_id: Some(PROFILE.into()),
        profiles: Some(vec![Profile {
            id: PROFILE.into(),
            name: "Work".into(),
            ..Profile::default()
        }]),
        settings: Some(settings),
        seeds: Some(vec![open_pay]),
        seeds_total: Some(13),
        garden_beds: Some(beds),
        garden_closed_counts: Some(GardenClosedCounts {
            all: GardenClosedTally {
                harvested: 10,
                withered: 0,
            },
            automations: GardenClosedTally {
                harvested: 2,
                withered: 0,
            },
            unassigned: GardenClosedTally {
                harvested: 3,
                withered: 0,
            },
            by_bed: vec![GardenBedClosedTally {
                bed_id: "b-pay001".into(),
                harvested: 7,
                withered: 0,
            }],
        }),
        ..InitialStateMessage::new()
    });
    let (ap, aa) = (archived_pay.clone(), archived_auto.clone());
    d.on("garden_seeds_page", move |r: &Request| {
        let seeds = match r["bed"].as_str() {
            Some("b-pay001") => vec![ap.clone()],
            Some("automations") => vec![aa.clone()],
            _ => Vec::new(),
        };
        let total = seeds.len() as i64;
        message(GardenSeedsPageResultMessage {
            request_id: r["request_id"].as_str().unwrap().into(),
            success: true,
            seeds,
            total,
            harvested: total,
            ..GardenSeedsPageResultMessage::new()
        })
    });
    let mut world = open_garden_sized(d, TWO_COLUMNS);
    world.gesture(|app| app.key(board()));
    assert!(!shows(&world, "The garden holds"));
    assert!(
        world
            .screen()
            .find(Target::key("garden.bed.automations"))
            .is_some()
    );
    assert_eq!(
        world
            .screen()
            .get(Target::key("garden.closed"))
            .name
            .as_deref(),
        Some("10 closed")
    );

    world.gesture(|app| app.click(Target::key("garden.bed.b-pay001")));
    assert_eq!(
        world
            .screen()
            .get(Target::key("garden.closed"))
            .name
            .as_deref(),
        Some("7 closed")
    );
    assert!(world.daemon.sent_of("garden_seeds_page").is_empty());
    world.gesture(|app| app.click(Target::key("garden.closed")));
    assert!(has_row(&world, "Archived payment"));

    world.gesture(|app| app.click(Target::key("garden.bed.automations")));
    assert!(has_row(&world, "Archived cron"));
    let pages = world.daemon.sent_of("garden_seeds_page");
    assert_eq!(pages.len(), 2);
    assert_eq!(pages[0]["bed"], "b-pay001");
    assert_eq!(pages[1]["bed"], "automations");
    assert!(world.runtime_is_at_rest());
}

#[test]
fn v2_opening_archived_search_hit_aligns_bed_and_lens_from_ancestors_and_cached_readings() {
    let beds = vec![bed("b-pay001", "Payments", &["payments-api"])];
    let open_other = with(seed("s-open01", "Other open"), |s| s.ready = true);
    let archived_auto_root = with(
        closed(plot("s-aroot1", "Nightly plot", ""), "harvested"),
        |s| s.automation_id = Some(4),
    );
    let archived_auto_child = closed(
        part_of("s-aroot1", "s-akid01", "Archived automation step"),
        "harvested",
    );
    let archived_pay_root = with(
        closed(seed("s-apay01", "Archived payment root"), "harvested"),
        |s| {
            s.bed_id = Some("b-pay001".into());
            s.bed = Some("Payments".into());
        },
    );
    let mut settings = BTreeMap::new();
    settings.insert("garden_v2_enabled".into(), "true".into());
    let d = Daemon::scripted().state(InitialStateMessage {
        selected_profile_id: Some(PROFILE.into()),
        profiles: Some(vec![Profile {
            id: PROFILE.into(),
            name: "Work".into(),
            ..Profile::default()
        }]),
        settings: Some(settings),
        seeds: Some(vec![open_other]),
        seeds_total: Some(4),
        garden_beds: Some(beds),
        garden_closed_counts: Some(GardenClosedCounts {
            all: GardenClosedTally {
                harvested: 1,
                withered: 0,
            },
            automations: GardenClosedTally {
                harvested: 1,
                withered: 0,
            },
            by_bed: vec![GardenBedClosedTally {
                bed_id: "b-pay001".into(),
                harvested: 1,
                withered: 0,
            }],
            ..GardenClosedCounts::default()
        }),
        ..InitialStateMessage::new()
    });
    let (hit_child, hit_pay) = (archived_auto_child.clone(), archived_pay_root.clone());
    d.on("garden_search", move |r: &Request| {
        let terms: Vec<&str> = r["terms"]
            .as_array()
            .into_iter()
            .flatten()
            .filter_map(|v| v.as_str())
            .collect();
        let hits = if terms.contains(&"s-akid01") {
            vec![GardenSearchHit {
                seed: hit_child.clone(),
                r#where: "id".into(),
                id_hit: true,
                title_ranges: None,
                snippet: None,
                snippet_ranges: None,
                plot_title: Some("Nightly plot".into()),
            }]
        } else if terms.contains(&"s-apay01") {
            vec![GardenSearchHit {
                seed: hit_pay.clone(),
                r#where: "id".into(),
                id_hit: true,
                title_ranges: None,
                snippet: None,
                snippet_ranges: None,
                plot_title: None,
            }]
        } else {
            Vec::new()
        };
        message(GardenSearchResultMessage {
            request_id: r["request_id"].as_str().unwrap().into(),
            success: true,
            total: hits.len() as i64,
            hits,
            ..GardenSearchResultMessage::new()
        })
    });
    let (doc_root, doc_child, doc_pay) = (
        archived_auto_root.clone(),
        archived_auto_child.clone(),
        archived_pay_root.clone(),
    );
    d.on("seed_document_get", move |r: &Request| {
        match r["seed_id"].as_str() {
            Some("s-akid01") => message(SeedDocumentGetResultMessage {
                request_id: r["request_id"].as_str().unwrap().into(),
                success: true,
                document: Some(SeedDocument {
                    seed: doc_child.clone(),
                    ancestors: Some(vec![doc_root.clone()]),
                    ..SeedDocument::default()
                }),
                ..SeedDocumentGetResultMessage::new()
            }),
            Some("s-apay01") => message(SeedDocumentGetResultMessage {
                request_id: r["request_id"].as_str().unwrap().into(),
                success: true,
                document: Some(SeedDocument {
                    seed: doc_pay.clone(),
                    ..SeedDocument::default()
                }),
                ..SeedDocumentGetResultMessage::new()
            }),
            _ => document_reply(r, None),
        }
    });
    let mut world = open_garden_sized(d, TWO_COLUMNS);
    world.gesture(|app| app.key(board()));
    assert!(
        world
            .screen()
            .get(Target::key("garden.lens.now"))
            .is("checked")
    );

    world.gesture(|app| app.click(Target::key("garden.search.field")));
    world.gesture(|app| app.type_text("s-akid01 is:any"));
    open_row(&mut world, "Archived automation step");
    assert_eq!(
        world
            .screen()
            .get(Target::key("garden.head.title"))
            .name
            .as_deref(),
        Some("Archived automation step")
    );
    assert_eq!(
        world
            .screen()
            .get(Target::key("garden.bed.automations"))
            .value
            .as_deref(),
        Some("selected")
    );
    assert!(
        world
            .screen()
            .get(Target::key("garden.lens.all"))
            .is("checked")
    );

    world.gesture(|app| app.click(Target::key("garden.search.field")));
    world.gesture(|app| app.type_text("s-apay01 is:any"));
    open_row(&mut world, "Archived payment root");
    assert_eq!(
        world
            .screen()
            .get(Target::key("garden.bed.b-pay001"))
            .value
            .as_deref(),
        Some("selected")
    );
    let doc_gets_before = world.daemon.sent_of("seed_document_get").len();
    world.gesture(|app| app.click(Target::key("garden.lens.now")));
    assert!(
        world
            .screen()
            .get(Target::key("garden.lens.now"))
            .is("checked")
    );
    world.gesture(|app| app.click(Target::key("garden.search.field")));
    world.gesture(|app| app.type_text("s-apay01 is:any"));
    open_row(&mut world, "Archived payment root");
    assert_eq!(
        world.daemon.sent_of("seed_document_get").len(),
        doc_gets_before,
        "reopening an archived root already cached in readings must align without re-reading"
    );
    assert_eq!(
        world
            .screen()
            .get(Target::key("garden.bed.b-pay001"))
            .value
            .as_deref(),
        Some("selected")
    );
    assert!(
        world
            .screen()
            .get(Target::key("garden.lens.all"))
            .is("checked")
    );
    assert!(world.runtime_is_at_rest());
}

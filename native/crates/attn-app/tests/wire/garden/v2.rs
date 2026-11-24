use std::collections::BTreeMap;

use attn_protocol::{
    Ask, GardenBed, GardenBedDeleteResultMessage, GardenBedSaveResultMessage,
    GardenBedsUpdatedMessage, InitialStateMessage, PartyView, Profile, Seed,
    SeedBedSetResultMessage, SeedPlotProgress, Session,
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
    let known = seeds.clone();
    let daemon = Daemon::scripted().state(InitialStateMessage {
        selected_profile_id: Some(PROFILE.into()),
        profiles: Some(vec![Profile {
            id: PROFILE.into(),
            name: "Work".into(),
            ..Profile::default()
        }]),
        settings: Some(settings),
        sessions: Some(sessions),
        asks: Some(asks),
        seeds_total: Some(seeds.len() as i64),
        seeds: Some(seeds),
        garden_beds: Some(beds),
        ..InitialStateMessage::new()
    });
    daemon.on("seed_document_get", move |r: &Request| {
        document_reply(r, known.iter().find(|s| s.id == r["seed_id"]).cloned())
    });
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
    world.gesture(|app| app.type_text("cloud-relay, ~/code/relay"));
    world.gesture(|app| app.click(Target::key("garden.bed-form.save")));

    let sent = world.daemon.sent_of("garden_bed_save");
    assert_eq!(sent.len(), 1);
    assert!(sent[0].get("id").is_none() || sent[0]["id"].is_null());
    assert_eq!(sent[0]["name"], "Relay");
    assert_eq!(sent[0]["repos"][0], "cloud-relay");
    assert_eq!(sent[0]["repos"][1], "~/code/relay");
    assert!(
        world
            .screen()
            .find(Target::key("garden.bed-form"))
            .is_none()
    );

    world.daemon.emit(GardenBedsUpdatedMessage {
        profile_id: PROFILE.into(),
        beds: vec![existing.clone(), bed("b-new001", "Relay", &["cloud-relay"])],
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
        Some("cloud-relay")
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
    assert_eq!(saves[1]["repos"][0], "cloud-relay");
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
            vec![s1.clone(), s2.clone()],
            vec![sess],
            Vec::new(),
        ),
        TWO_COLUMNS,
    );
    world.gesture(|app| app.key(board()));
    world.gesture(|app| app.click(Target::key("garden.bed.b-attn01")));
    world.gesture(|app| app.click(Target::key("garden.search.field")));
    world.gesture(|app| app.type_text("bed:"));
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

    let mut off_world = open_garden_sized(
        v2_daemon(false, beds, vec![s1, s2], Vec::new(), Vec::new()),
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
    assert!(off_world.runtime_is_at_rest());
}

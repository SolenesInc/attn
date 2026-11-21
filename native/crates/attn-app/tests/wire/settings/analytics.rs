use std::collections::BTreeMap;
use std::sync::{Arc, Mutex};

use attn_protocol::{
    AnalyticsBuild, AnalyticsOutboxRow, ClearAnalyticsRecordsResultMessage,
    ForgetAnalyticsInstallResultMessage, GardenSeedsUpdatedMessage,
    GetAnalyticsStatusResultMessage, InitialStateMessage, ListAnalyticsRecordsResultMessage,
    OutpostInfo, OutpostsUpdatedMessage, PlantVentSeedResultMessage, Profile, Seed,
    SetVentDismissedResultMessage, SetVentKindResultMessage, SetVentSendResultMessage,
    UnplantVentSeedResultMessage, Vent, VentContext, VentTranscript, VentsUpdatedMessage,
};
use attn_testkit::{Daemon, Request, Target, World, message};
use rune::{Key, KeyEvent, Mods};
use serde_json::{Value, json};

use super::super::garden::seed;
use super::super::sidebar::session;
use super::{accepting, click, open, state, writes};

const PROFILE: &str = "work";
const INGEST: &str = "https://attn-telemetry.caketroll.cloud";

fn build(dirty: bool) -> AnalyticsBuild {
    AnalyticsBuild {
        version: "0.42.1".into(),
        commit: "a3f9e1c22".into(),
        channel: "source".into(),
        dirty,
        os: "darwin".into(),
        arch: "arm64".into(),
        ..AnalyticsBuild::default()
    }
}

/// The vent the sharing tests triage: an attn vent from a live session, with no stake seed.
fn plain_vent() -> Vent {
    Vent {
        id: "v-new".into(),
        session_id: "s1".into(),
        kind: "attn".into(),
        text: "`attn seed tend s-f68sja` refused: held by a session that ended two hours ago."
            .into(),
        created_at: "2026-10-24T14:02:11Z".into(),
        context: VentContext {
            session_label: "fix garden bell staleness".into(),
            harness: "claude".into(),
            build: build(false),
            ..VentContext::default()
        },
        ..Vent::default()
    }
}

/// The vent the triage tests work on: with its model, stake seed and transcript.
fn vent(id: &str) -> Vent {
    let mut vent = plain_vent();
    vent.id = id.into();
    vent.context = VentContext {
        harness_version: Some("2.4.1".into()),
        model: Some("claude-opus-5-5".into()),
        model_requested: Some("opus".into()),
        seed_id: Some("s-stake1".into()),
        build: build(true),
        experimental: vec!["asks_enabled".into()],
        transcript: Some(VentTranscript {
            path: "/tmp/transcript.jsonl".into(),
            bytes: 4096,
        }),
        ..vent.context
    };
    vent
}

fn withdrawn() -> Vent {
    let mut gone = vent("v-gone");
    gone.text = "kubectl cannot reach the cluster.".into();
    gone.withdrawn_at = Some("2026-10-24T14:05:00Z".into());
    gone.context.session_label = "songcheck deploy".into();
    gone
}

fn newer() -> Vent {
    let mut newer = vent("v-newer");
    newer.text = "make test ran the Go suite for an app-only change.".into();
    newer
}

fn slugged(id: &str, slug: &str) -> Seed {
    Seed {
        step_slug: slug.into(),
        ..seed(id, slug)
    }
}

fn status() -> GetAnalyticsStatusResultMessage {
    GetAnalyticsStatusResultMessage {
        success: true,
        records: 40,
        bytes: 4096,
        waiting: 12,
        last_sent_at: Some("2026-10-24T14:00:12Z".into()),
        ingest_url: INGEST.into(),
        ..GetAnalyticsStatusResultMessage::new()
    }
}

fn answering_status(
    status: GetAnalyticsStatusResultMessage,
) -> impl Fn(&Request) -> Value + Send + 'static {
    move |r| {
        message(GetAnalyticsStatusResultMessage {
            request_id: r["request_id"].as_str().unwrap().into(),
            ..status.clone()
        })
    }
}

fn request_id(r: &Request) -> String {
    r["request_id"].as_str().unwrap().into()
}

fn vent_id(r: &Request) -> String {
    r["vent_id"].as_str().unwrap().into()
}

type Vents = Arc<Mutex<Vec<Vent>>>;

fn updated(vents: &Vents) -> Value {
    message(VentsUpdatedMessage {
        profile_id: PROFILE.into(),
        vents: vents.lock().unwrap().clone(),
        ..VentsUpdatedMessage::new()
    })
}

/// A daemon that keeps the vents as the real one does: each change is answered, then
/// broadcast.
fn serve_vents(daemon: &Daemon, vents: Vents) {
    let kept = vents.clone();
    daemon.on("set_vent_send", move |r: &Request| {
        let send = r["send"].as_bool().unwrap();
        for v in kept
            .lock()
            .unwrap()
            .iter_mut()
            .filter(|v| v.id == vent_id(r))
        {
            v.send = send.then(|| "queued".into());
        }
        vec![
            message(SetVentSendResultMessage {
                request_id: request_id(r),
                success: true,
                vent_id: vent_id(r),
                ..SetVentSendResultMessage::new()
            }),
            updated(&kept),
        ]
    });
    let kept = vents.clone();
    daemon.on("set_vent_kind", move |r: &Request| {
        let kind = r["kind"].as_str().unwrap().to_owned();
        for v in kept
            .lock()
            .unwrap()
            .iter_mut()
            .filter(|v| v.id == vent_id(r))
        {
            if kind == "tools" {
                v.send = None;
            }
            v.kind = kind.clone();
        }
        vec![
            message(SetVentKindResultMessage {
                request_id: request_id(r),
                success: true,
                vent_id: vent_id(r),
                ..SetVentKindResultMessage::new()
            }),
            updated(&kept),
        ]
    });
    let kept = vents.clone();
    daemon.on("set_vent_dismissed", move |r: &Request| {
        let dismissed = r["dismissed"].as_bool().unwrap();
        for v in kept
            .lock()
            .unwrap()
            .iter_mut()
            .filter(|v| v.id == vent_id(r))
        {
            v.dismissed_at = dismissed.then(|| "2026-10-24T14:10:00Z".into());
        }
        vec![
            message(SetVentDismissedResultMessage {
                request_id: request_id(r),
                success: true,
                vent_id: vent_id(r),
                ..SetVentDismissedResultMessage::new()
            }),
            updated(&kept),
        ]
    });
    let kept = vents.clone();
    daemon.on("plant_vent_seed", move |r: &Request| {
        for v in kept
            .lock()
            .unwrap()
            .iter_mut()
            .filter(|v| v.id == vent_id(r))
        {
            v.planted_seed_id = Some("s-planted".into());
        }
        let planted = slugged("s-planted", "seed-tend-refused-held-session");
        vec![
            message(PlantVentSeedResultMessage {
                request_id: request_id(r),
                success: true,
                vent_id: vent_id(r),
                seed: Some(planted.clone()),
                ..PlantVentSeedResultMessage::new()
            }),
            message(GardenSeedsUpdatedMessage {
                profile_id: PROFILE.into(),
                seeds: vec![slugged("s-stake1", "garden-show-stale-bell"), planted],
                total: 2,
                ..GardenSeedsUpdatedMessage::new()
            }),
            updated(&kept),
        ]
    });
    let kept = vents;
    daemon.on("unplant_vent_seed", move |r: &Request| {
        for v in kept
            .lock()
            .unwrap()
            .iter_mut()
            .filter(|v| v.id == vent_id(r))
        {
            v.planted_seed_id = None;
        }
        vec![
            message(UnplantVentSeedResultMessage {
                request_id: request_id(r),
                success: true,
                vent_id: vent_id(r),
                ..UnplantVentSeedResultMessage::new()
            }),
            updated(&kept),
        ]
    });
    daemon.on("forget_analytics_install", |r: &Request| {
        message(ForgetAnalyticsInstallResultMessage {
            request_id: request_id(r),
            success: true,
            ..ForgetAnalyticsInstallResultMessage::new()
        })
    });
}

/// The dialog open on Analytics, turned on with `settings`, over a daemon that holds `vents`
/// and `seeds` and the live session s1.
fn on_analytics(
    settings: &[(&str, &str)],
    vents: Vec<Vent>,
    seeds: Vec<Seed>,
    serve: impl FnOnce(&Daemon),
) -> World {
    let mut all = vec![("analytics.enabled", "true")];
    all.extend_from_slice(settings);
    let (daemon, _) = accepting(&all);
    let daemon = daemon.state(InitialStateMessage {
        selected_profile_id: Some(PROFILE.into()),
        profiles: Some(vec![Profile {
            id: PROFILE.into(),
            name: "Work".into(),
            ..Profile::default()
        }]),
        settings: Some(
            all.iter()
                .map(|(k, v)| ((*k).to_owned(), (*v).to_owned()))
                .collect::<BTreeMap<_, _>>(),
        ),
        sessions: Some(vec![session("s1", "idle")]),
        seeds_total: Some(seeds.len() as i64),
        seeds: Some(seeds),
        vents: Some(vents),
        ..InitialStateMessage::new()
    });
    serve(&daemon);
    let mut world = World::start(daemon);
    open(&mut world);
    click(&mut world, "settings.nav.analytics");
    world
}

/// Analytics as the sharing tests open it: `settings` on, `status` read, one vent.
fn sharing(
    settings: &[(&str, &str)],
    status: GetAnalyticsStatusResultMessage,
    vents: Vec<Vent>,
) -> World {
    let kept: Vents = Arc::new(Mutex::new(vents.clone()));
    on_analytics(settings, vents, Vec::new(), move |daemon| {
        daemon.on("get_analytics_status", answering_status(status));
        serve_vents(daemon, kept);
    })
}

/// Analytics as the vent tests open it: a new vent and a withdrawn one, with the stake seed.
fn triage() -> World {
    triage_of(vec![vent("v-new"), withdrawn()])
}

fn triage_of(vents: Vec<Vent>) -> World {
    let kept: Vents = Arc::new(Mutex::new(vents.clone()));
    on_analytics(
        &[],
        vents,
        vec![slugged("s-stake1", "garden-show-stale-bell")],
        move |daemon| {
            daemon.on(
                "get_analytics_status",
                answering_status(GetAnalyticsStatusResultMessage {
                    records: 0,
                    bytes: 0,
                    waiting: 0,
                    last_sent_at: None,
                    ..status()
                }),
            );
            serve_vents(daemon, kept);
        },
    )
}

fn press(world: &mut World, typed: &str) {
    let key = match typed {
        "Enter" => Key::Enter,
        other => Key::Text(other.into()),
    };
    world.gesture(|app| {
        app.key(KeyEvent {
            key,
            mods: Mods::default(),
        })
    });
}

fn named(world: &World, key: &str) -> String {
    world
        .screen()
        .find(Target::key(key))
        .and_then(|n| n.name)
        .unwrap_or_default()
}

fn shows(world: &World, key: &str) -> bool {
    world.screen().find(Target::key(key)).is_some()
}

/// Every name under the element, in reading order.
fn text_of(world: &World, key: &str) -> Vec<String> {
    world
        .screen()
        .descendants(Target::key(key))
        .into_iter()
        .filter_map(|n| n.name)
        .collect()
}

fn reads_as(world: &World, key: &str) -> String {
    text_of(world, key).join(" ")
}

fn inspector(world: &World) -> Vec<String> {
    text_of(world, "settings.analytics.inspector")
}

fn machine(world: &World) -> String {
    reads_as(world, "settings.analytics.machine")
}

/// The rows of the shown list, each with the group or list it sits in.
fn rows(world: &World) -> Vec<String> {
    let mut group = String::new();
    let mut rows = Vec::new();
    for node in world
        .screen()
        .descendants(Target::key("settings.analytics.list"))
    {
        match node.role.as_str() {
            "group" | "list-box" | "heading" => {
                if let Some(name) = node.name {
                    group = name;
                }
            }
            "list-box-option" => rows.push(format!("{group}: {}", node.name.unwrap_or_default())),
            _ => {}
        }
    }
    rows
}

/// What the last action said.
fn said(world: &World) -> String {
    let toast = world.screen().get(Target::key("settings.analytics.toast"));
    toast
        .description
        .filter(|d| !d.is_empty())
        .or(toast.name)
        .unwrap_or_default()
}

fn sent(world: &World, cmd: &str, field: &str) -> Vec<Value> {
    world
        .daemon
        .sent_of(cmd)
        .iter()
        .map(|r| r[field].clone())
        .collect()
}

fn stat(world: &World, id: &str) -> String {
    named(world, &format!("settings.analytics.stat.{id}"))
}

fn record(id: i64, stream: &str, name: &str, at: &str, extra: &str) -> AnalyticsOutboxRow {
    AnalyticsOutboxRow {
        id,
        record: format!(r#"{{"stream":"{stream}","seq":{id},"name":"{name}","at":"{at}"{extra}}}"#),
    }
}

fn records() -> Vec<AnalyticsOutboxRow> {
    vec![
        record(
            9,
            "bus",
            "session.state.changed",
            "2026-10-24T14:03:12Z",
            r#","subject":"h:77c2e01fa4d3""#,
        ),
        record(
            8,
            "bus",
            "pr.updated",
            "2026-10-24T14:02:58Z",
            r#","subject":"h:0d7e44a1b9c2""#,
        ),
        record(
            7,
            "bus",
            "pr.updated",
            "2026-10-24T14:02:58Z",
            r#","subject":"h:3a19f0c27e18""#,
        ),
        record(
            6,
            "bus",
            "pr.updated",
            "2026-10-24T14:02:58Z",
            r#","subject":"h:c4e1022b9d07""#,
        ),
        record(
            5,
            "app",
            "shortcut",
            "2026-10-24T14:00:44Z",
            r#","props":{"id":"queue.next"}"#,
        ),
    ]
}

/// A daemon that keeps the records as the real one does, filtering by stream and clearing them.
fn serve_records(daemon: &Daemon) {
    serve_these_records(daemon, records());
}

fn serve_these_records(daemon: &Daemon, rows: Vec<AnalyticsOutboxRow>) {
    let stored = Arc::new(Mutex::new(rows));
    let kept = stored.clone();
    daemon.on("list_analytics_records", move |r: &Request| {
        let stream = r["stream"].as_str().map(|s| format!(r#""stream":"{s}""#));
        let rows: Vec<AnalyticsOutboxRow> = kept
            .lock()
            .unwrap()
            .iter()
            .filter(|row| stream.as_ref().is_none_or(|s| row.record.contains(s)))
            .cloned()
            .collect();
        message(ListAnalyticsRecordsResultMessage {
            request_id: request_id(r),
            success: true,
            waiting: rows.len() as i64,
            records: Some(rows),
            ..ListAnalyticsRecordsResultMessage::new()
        })
    });
    let kept = stored.clone();
    daemon.on("get_analytics_status", move |r: &Request| {
        let rows = kept.lock().unwrap();
        message(GetAnalyticsStatusResultMessage {
            request_id: request_id(r),
            success: true,
            records: rows.len() as i64,
            bytes: rows.iter().map(|row| row.record.len() as i64).sum(),
            waiting: rows.len() as i64,
            ingest_url: INGEST.into(),
            ..GetAnalyticsStatusResultMessage::new()
        })
    });
    daemon.on("clear_analytics_records", move |r: &Request| {
        let mut rows = stored.lock().unwrap();
        let records = rows.len() as i64;
        rows.clear();
        message(ClearAnalyticsRecordsResultMessage {
            request_id: request_id(r),
            success: true,
            records,
            vents: 1,
            ..ClearAnalyticsRecordsResultMessage::new()
        })
    });
}

fn on_records() -> World {
    on_analytics(&[], Vec::new(), Vec::new(), serve_records)
}

#[test]
fn no_record_list_is_read_until_one_is_shown_then_one_page_per_list() {
    let mut world = on_records();
    assert!(world.daemon.sent_of("list_analytics_records").is_empty());

    press(&mut world, "]");
    press(&mut world, "]");

    let asked: Vec<(Value, Value, Value)> = world
        .daemon
        .sent_of("list_analytics_records")
        .iter()
        .map(|r| {
            (
                r["stream"].clone(),
                r["limit"].clone(),
                r["before_id"].clone(),
            )
        })
        .collect();
    assert_eq!(
        asked,
        [
            (Value::Null, json!(200), Value::Null),
            (json!("bus"), json!(200), Value::Null)
        ]
    );
}

#[test]
fn a_pressed_tab_shows_its_list_and_the_keys_keep_working() {
    let mut world = on_records();
    click(&mut world, "settings.analytics.views.app");
    press(&mut world, "[");

    let streams: Vec<Value> = world
        .daemon
        .sent_of("list_analytics_records")
        .iter()
        .map(|r| r["stream"].clone())
        .collect();
    assert_eq!(streams, [json!("app"), json!("bus")]);
}

#[test]
fn a_same_second_burst_folds_opens_with_enter_and_each_record_shows_exactly_as_stored() {
    let mut world = on_records();
    press(&mut world, "]");
    let listed = rows(&world);
    assert_eq!(listed.len(), 3, "{listed:?}");
    assert!(listed[1].contains("pr.updated ×3"), "{listed:?}");

    press(&mut world, "j");
    assert_eq!(
        named(&world, "settings.analytics.record.heading"),
        "3 records in the same second"
    );

    press(&mut world, "Enter");
    assert_eq!(rows(&world).len(), 5);
    assert!(inspector(&world).contains(&records()[1].record));
    press(&mut world, "j");
    press(&mut world, "j");
    press(&mut world, "j");
    assert!(inspector(&world).contains(&records()[4].record));
}

#[test]
fn records_are_read_again_only_when_asked() {
    let mut world = on_records();
    press(&mut world, "]");
    press(&mut world, "r");
    click(&mut world, "settings.analytics.refresh");
    assert_eq!(world.daemon.sent_of("list_analytics_records").len(), 3);
}

#[test]
fn the_record_count_and_size_show_and_clear_empties_them_after_a_confirm() {
    let mut world = on_records();
    let size: usize = records().iter().map(|r| r.record.len()).sum();
    assert_eq!(stat(&world, "kept"), format!("7 days · {size} B"));

    press(&mut world, "m");
    assert!(machine(&world).contains(&format!(
        "5 records, {size} B. Dismissed and withdrawn vents are kept 30 days."
    )));
    click(&mut world, "settings.analytics.clear");
    assert!(world.daemon.sent_of("clear_analytics_records").is_empty());
    click(&mut world, "settings.analytics.confirm.yes");

    assert_eq!(world.daemon.sent_of("clear_analytics_records").len(), 1);
    assert_eq!(said(&world), "Cleared 5 records and 1 dismissed vent.");
    assert!(machine(&world).contains("0 records, 0 B."));
    assert_eq!(world.daemon.sent_of("get_analytics_status").len(), 2);
}

#[test]
fn turning_analytics_off_keeps_its_page_open_marked_off_and_unusable() {
    let mut world = on_analytics(&[], Vec::new(), Vec::new(), |_| {});
    assert!(!shows(&world, "settings.nav-off.analytics"));

    click(&mut world, "settings.feature.analytics.enabled");

    assert!(writes(&world).contains(&("analytics.enabled".to_owned(), "false".to_owned())));
    assert_eq!(state(&mut world)["activeSection"], "analytics");
    assert!(shows(&world, "settings.analytics"));
    assert!(shows(&world, "settings.nav-off.analytics"));
    let content = world
        .screen()
        .get(Target::key("settings.section.analytics"));
    assert!(content.is("disabled"), "{content:?}");
}

fn outpost(reach: &str) -> OutpostInfo {
    OutpostInfo {
        id: "o1".into(),
        name: "ai-sandbox".into(),
        destination: "ai-sandbox".into(),
        state: if reach == "reachable" {
            "connected".into()
        } else {
            "lost".into()
        },
        reach: reach.into(),
        state_since: "2026-10-24T14:00:00Z".into(),
        ..OutpostInfo::default()
    }
}

fn outposts(world: &mut World, reach: &str) {
    world.daemon.emit(OutpostsUpdatedMessage {
        outposts: vec![outpost(reach)],
        ..OutpostsUpdatedMessage::new()
    });
    world.settle();
}

#[test]
fn an_unreachable_outpost_is_named_until_it_reconnects() {
    let mut world = sharing(&[], status(), vec![plain_vent()]);
    assert!(!shows(&world, "settings.analytics.stat.outposts"));

    outposts(&mut world, "unreachable");
    assert_eq!(stat(&world, "outposts"), "1 of 1 unreachable");
    assert_eq!(
        named(&world, "settings.analytics.outposts"),
        "ai-sandbox: unreachable, so what it holds isn't shown here."
    );

    outposts(&mut world, "reachable");
    assert_eq!(stat(&world, "outposts"), "Up to 5 min behind");
    assert!(!shows(&world, "settings.analytics.outposts"));
}

#[test]
fn with_sharing_off_every_vent_is_kept_here_and_x_does_nothing() {
    let mut world = sharing(&[], status(), vec![plain_vent()]);
    assert_eq!(stat(&world, "sharing"), "Off");
    assert_eq!(
        named(&world, "settings.analytics.vent.destination"),
        "Kept on this machine"
    );
    assert_eq!(
        named(&world, "settings.analytics.vent.destination-note"),
        "· sharing is off"
    );

    press(&mut world, "x");
    assert!(world.daemon.sent_of("set_vent_send").is_empty());
}

#[test]
fn a_vent_queued_earlier_waits_for_sharing_and_x_keeps_it_here() {
    let queued = Vent {
        send: Some("queued".into()),
        ..plain_vent()
    };
    let mut world = sharing(&[("analytics.share_vents", "true")], status(), vec![queued]);
    assert_eq!(
        named(&world, "settings.analytics.vent.destination"),
        "Sending once sharing is on"
    );

    press(&mut world, "x");
    assert_eq!(sent(&world, "set_vent_send", "send"), [json!(false)]);
    assert_eq!(
        named(&world, "settings.analytics.vent.destination"),
        "Kept on this machine"
    );

    press(&mut world, "m");
    assert!(machine(&world).contains("New attn vents are sent with their text."));
}

#[test]
fn x_sends_a_vent_x_again_keeps_it_and_u_undoes() {
    let mut world = sharing(
        &[("analytics.sharing_enabled", "true")],
        status(),
        vec![plain_vent()],
    );
    assert!(
        stat(&world, "sharing").starts_with("On · sent "),
        "{}",
        stat(&world, "sharing")
    );

    press(&mut world, "x");
    assert_eq!(
        said(&world),
        "Sending “fix garden bell staleness” with the next batch."
    );
    assert_eq!(
        named(&world, "settings.analytics.vent.destination"),
        "Sending with the next batch"
    );
    press(&mut world, "x");
    assert_eq!(
        named(&world, "settings.analytics.vent.destination"),
        "Kept on this machine"
    );
    press(&mut world, "u");

    assert_eq!(
        sent(&world, "set_vent_send", "send"),
        [json!(true), json!(false), json!(true)]
    );
}

#[test]
fn a_tool_vent_stays_here_until_s_files_it_under_attn_and_u_puts_it_back_in_the_queue() {
    let tools = Vent {
        kind: "tools".into(),
        ..plain_vent()
    };
    let mut world = sharing(
        &[("analytics.sharing_enabled", "true")],
        status(),
        vec![tools],
    );
    assert_eq!(
        named(&world, "settings.analytics.vent.destination-note"),
        "· tool vents never leave it"
    );
    assert!(rows(&world)[0].contains("fix garden bell staleness · tools"));
    press(&mut world, "x");
    assert!(world.daemon.sent_of("set_vent_send").is_empty());

    press(&mut world, "s");
    assert_eq!(
        said(&world),
        "Filed “fix garden bell staleness” under attn."
    );
    assert!(!shows(&world, "settings.analytics.vent.destination-note"));
    press(&mut world, "x");
    press(&mut world, "s");
    assert_eq!(
        said(&world),
        "Filed “fix garden bell staleness” under tools; it stays on this machine."
    );
    assert_eq!(
        named(&world, "settings.analytics.vent.destination"),
        "Kept on this machine"
    );
    assert_eq!(
        sent(&world, "set_vent_kind", "kind"),
        [json!("attn"), json!("tools")]
    );
    assert_eq!(sent(&world, "set_vent_send", "send"), [json!(true)]);

    press(&mut world, "u");
    assert_eq!(
        named(&world, "settings.analytics.vent.destination"),
        "Sending with the next batch"
    );
    assert_eq!(
        sent(&world, "set_vent_kind", "kind"),
        [json!("attn"), json!("tools"), json!("attn")]
    );
    assert_eq!(
        sent(&world, "set_vent_send", "send"),
        [json!(true), json!(true)]
    );
}

#[test]
fn tool_vents_turn_on_from_the_machine_settings() {
    let mut world = sharing(&[], status(), vec![plain_vent()]);
    press(&mut world, "m");
    assert!(
        named(&world, "settings.analytics.machine.tool-vents.copy")
            .starts_with("Agents vent only about attn.")
    );

    click(&mut world, "settings.analytics.switch.analytics.tool_vents");
    assert_eq!(
        writes(&world),
        [("analytics.tool_vents".to_owned(), "true".to_owned())]
    );
}

const REFUSAL: &str = "attn-ingest refused the batch: events_per_install_per_minute is 10000, and this batch would make it 10200; retrying in 41s";

#[test]
fn a_refusal_shows_with_its_limit_on_the_strip_and_in_the_share_row() {
    let mut world = sharing(
        &[("analytics.sharing_enabled", "true")],
        GetAnalyticsStatusResultMessage {
            last_refusal: Some(REFUSAL.into()),
            ..status()
        },
        vec![plain_vent()],
    );
    assert_eq!(stat(&world, "sharing"), "Refused · 12 waiting");
    assert_eq!(named(&world, "settings.analytics.refusal"), REFUSAL);

    press(&mut world, "m");
    assert_eq!(
        named(&world, "settings.analytics.machine.share.copy"),
        REFUSAL
    );
}

#[test]
fn the_share_row_says_where_uploads_go() {
    let mut world = sharing(
        &[("analytics.sharing_enabled", "true")],
        status(),
        vec![plain_vent()],
    );
    press(&mut world, "m");
    assert!(
        named(&world, "settings.analytics.machine.share.copy")
            .ends_with(&format!("12 waiting · uploads go to {INGEST}"))
    );
}

#[test]
fn an_instance_that_is_not_production_says_why_it_uploads_nowhere_before_and_after_sharing_is_on() {
    let reason = "Uploads go to http://127.0.0.1:1, where nothing listens, because this is instance dev, not production. Start its daemon with ATTN_ANALYTICS_INGEST_URL=<url> to upload to an attn-ingest.";
    let off = GetAnalyticsStatusResultMessage {
        ingest_url: "http://127.0.0.1:1".into(),
        ingest_off_reason: Some(reason.into()),
        ..status()
    };
    let mut before = sharing(&[], off.clone(), vec![plain_vent()]);
    press(&mut before, "m");
    assert_eq!(
        named(&before, "settings.analytics.machine.share.copy"),
        reason
    );

    let mut after = sharing(
        &[("analytics.sharing_enabled", "true")],
        GetAnalyticsStatusResultMessage {
            last_refusal: Some("dial tcp 127.0.0.1:1: connect: connection refused".into()),
            ..off
        },
        vec![plain_vent()],
    );
    assert_eq!(named(&after, "settings.analytics.refusal"), reason);
    assert!(!reads_as(&after, "settings.analytics").contains("connection refused"));
    press(&mut after, "m");
    assert_eq!(
        named(&after, "settings.analytics.machine.share.copy"),
        reason
    );
}

#[test]
fn turning_sharing_off_offers_to_delete_the_server_copy() {
    let mut world = sharing(
        &[
            ("analytics.sharing_enabled", "true"),
            ("analytics.share_vents", "true"),
        ],
        status(),
        vec![plain_vent()],
    );
    press(&mut world, "m");
    assert!(machine(&world).contains("Last sent"));
    assert!(machine(&world).contains("New attn vents are sent with their text."));

    click(&mut world, "settings.analytics.switch.sharing");
    assert_eq!(
        writes(&world),
        [("analytics.sharing_enabled".to_owned(), "false".to_owned())]
    );
    assert_eq!(
        named(&world, "settings.analytics.machine.share.copy"),
        "Sharing is off. Delete what the server already holds?"
    );
    click(&mut world, "settings.analytics.confirm.yes");

    assert_eq!(world.daemon.sent_of("forget_analytics_install").len(), 1);
    assert!(said(&world).starts_with("Forgot this install."));
}

#[test]
fn forgetting_this_install_waits_for_a_confirmation() {
    let mut world = sharing(
        &[("analytics.sharing_enabled", "true")],
        status(),
        vec![plain_vent()],
    );
    press(&mut world, "m");
    click(&mut world, "settings.analytics.forget");
    assert!(world.daemon.sent_of("forget_analytics_install").is_empty());

    click(&mut world, "settings.analytics.confirm.yes");
    assert_eq!(world.daemon.sent_of("forget_analytics_install").len(), 1);
}

#[test]
fn new_vents_list_before_done_ones_and_the_selected_vent_shows_its_context() {
    let mut world = triage();

    let listed = rows(&world);
    assert!(
        listed[0].starts_with("New: fix garden bell staleness"),
        "{listed:?}"
    );
    assert!(
        listed[1].starts_with("Done: songcheck deploy") && listed[1].contains("withdrawn"),
        "{listed:?}"
    );
    assert_eq!(
        named(&world, "settings.analytics.vent.seed"),
        "garden-show-stale-bell"
    );
    assert_eq!(
        named(&world, "settings.analytics.vent.build"),
        "Source a3f9e1c, uncommitted changes"
    );
    assert_eq!(
        named(&world, "settings.analytics.vent.experimental"),
        "Asks"
    );
    assert_eq!(
        named(&world, "settings.analytics.vent.agent"),
        "Claude 2.4.1 · claude-opus-5-5 (requested opus)"
    );
    assert_eq!(
        named(&world, "settings.analytics.vent.destination"),
        "Kept on this machine"
    );
    super::rest(&mut world);
}

#[test]
fn d_dismisses_u_undoes_and_the_withdrawn_vent_cannot_be_dismissed() {
    let mut world = triage();

    press(&mut world, "d");
    assert_eq!(said(&world), "Dismissed “fix garden bell staleness”.");
    assert_eq!(
        named(&world, "settings.analytics.vent.outcome"),
        "Dismissed"
    );
    press(&mut world, "u");
    press(&mut world, "j");
    press(&mut world, "d");

    let asked: Vec<(String, Value)> = world
        .daemon
        .sent_of("set_vent_dismissed")
        .iter()
        .map(|r| (vent_id(r), r["dismissed"].clone()))
        .collect();
    assert_eq!(
        asked,
        [
            ("v-new".to_owned(), json!(true)),
            ("v-new".to_owned(), json!(false))
        ]
    );
    assert!(!shows(&world, "settings.analytics.vent.action.d"));
}

#[test]
fn dismissing_a_vent_moves_on_to_the_next_new_one() {
    let mut world = triage_of(vec![newer(), vent("v-new"), withdrawn()]);
    press(&mut world, "j");
    press(&mut world, "k");

    press(&mut world, "d");

    let asked: Vec<String> = world
        .daemon
        .sent_of("set_vent_dismissed")
        .iter()
        .map(vent_id)
        .collect();
    assert_eq!(asked, ["v-newer"]);
    assert!(
        named(&world, "settings.analytics.vent.text")
            .starts_with("`attn seed tend s-f68sja` refused")
    );
}

#[test]
fn p_plants_a_seed_from_the_vent_shows_it_done_with_its_seed_and_u_undoes() {
    let mut world = triage();

    press(&mut world, "p");
    let planted: Vec<String> = world
        .daemon
        .sent_of("plant_vent_seed")
        .iter()
        .map(vent_id)
        .collect();
    assert_eq!(planted, ["v-new"]);
    assert_eq!(
        said(&world),
        "Planted seed-tend-refused-held-session from “fix garden bell staleness”."
    );
    let listed = rows(&world);
    assert!(
        listed
            .iter()
            .any(|r| r.starts_with("Done: fix garden bell staleness")
                && r.contains("seed-tend-refused-held-session")),
        "{listed:?}"
    );
    assert_eq!(
        named(&world, "settings.analytics.vent.outcome"),
        "Planted · seed-tend-refused-held-session"
    );
    assert!(!shows(&world, "settings.analytics.vent.action.p"));

    press(&mut world, "u");
    let unplanted: Vec<String> = world
        .daemon
        .sent_of("unplant_vent_seed")
        .iter()
        .map(vent_id)
        .collect();
    assert_eq!(unplanted, ["v-new"]);
    assert_eq!(said(&world), "Undone.");
    assert!(rows(&world)[0].starts_with("New: fix garden bell staleness"));
}

#[test]
fn t_opens_the_transcript_and_g_the_stake_seed_closing_settings() {
    let mut world = triage();

    press(&mut world, "t");
    assert_eq!(world.opened_links(), ["file:///tmp/transcript.jsonl"]);
    assert_eq!(state(&mut world)["open"], true);

    press(&mut world, "g");
    assert_eq!(state(&mut world)["open"], false);
}

#[test]
fn a_vent_that_arrives_shows_at_once_without_reading_records() {
    let mut world = triage();
    world.daemon.emit(VentsUpdatedMessage {
        profile_id: PROFILE.into(),
        vents: vec![newer(), vent("v-new"), withdrawn()],
        ..VentsUpdatedMessage::new()
    });
    world.settle();

    assert!(rows(&world)[0].contains("make test ran the Go suite"));
    assert!(world.daemon.sent_of("list_analytics_records").is_empty());
}

/// The options of the list, in order, each with whether it reads as selected or unselected.
fn selection(world: &World) -> Vec<&'static str> {
    world
        .screen()
        .descendants(Target::key("settings.analytics.list"))
        .into_iter()
        .filter(|n| n.role == "list-box-option")
        .map(|n| match (n.is("selected"), n.is("unselected")) {
            (true, _) => "selected",
            (_, true) => "unselected",
            _ => "neither",
        })
        .collect()
}

fn tab_once(world: &mut World) {
    world.gesture(|app| {
        app.key(KeyEvent {
            key: Key::Tab,
            mods: Mods::default(),
        })
    });
}

fn focused(world: &World) -> Option<String> {
    world.screen().focused().and_then(|n| n.key)
}

#[test]
fn the_chosen_row_reads_as_selected_and_moves_with_the_keys() {
    let mut world = triage();
    assert_eq!(selection(&world), ["selected", "unselected"]);
    press(&mut world, "j");
    assert_eq!(selection(&world), ["unselected", "selected"]);
}

#[test]
fn tab_from_the_view_tabs_stops_on_the_chosen_option_before_what_it_holds() {
    let mut world = triage();
    let tab = "settings.analytics.views.vents";
    for _ in 0..40 {
        if focused(&world).as_deref() == Some(tab) {
            break;
        }
        tab_once(&mut world);
    }
    assert_eq!(
        focused(&world).as_deref(),
        Some(tab),
        "Tab reaches the view tabs"
    );
    let mut order = Vec::new();
    for _ in 0..12 {
        tab_once(&mut world);
        order.extend(focused(&world));
    }
    let list = order.iter().position(|k| k == "item0");
    let inspector = order
        .iter()
        .position(|k| k.starts_with("settings.analytics.vent."));
    assert!(
        list.is_some_and(|l| inspector.is_none_or(|i| l < i)),
        "Tab stops on the chosen option before what it holds: {order:?}"
    );
}

/// `count` vents in the review's mix: tool vents with a long English text and attn vents with a
/// long Japanese one, the last fifth done, at `scale` with sharing refused.
fn dense(count: usize, scale: &str) -> World {
    let vents = (0..count)
        .map(|i| {
            let mut v = vent(&format!("v-{i:02}"));
            v.kind = if i % 2 == 0 { "tools" } else { "attn" }.into();
            v.created_at = format!("2026-11-12T08:59:{:02}Z", 59 - i);
            v.context.session_label = if i % 2 == 0 {
                "attn--settings-analytics-migration-keyboard-navigation-and-accessibility-review"
                    .into()
            } else {
                "分析と設定の表示を検証するための長い作業ツリーの名前とエラー報告".repeat(3)
            };
            v.text = format!(
                "Vent {i:02} {}",
                if i % 2 == 0 {
                    "The daemon refused to write the settings after the user opened Analytics while its outpost was unreachable. ".repeat(6)
                } else {
                    "設定を保存できません。変更内容を確認し、接続が復旧してから再試行してください。".repeat(14)
                }
            );
            if i >= count * 4 / 5 && i > 1 {
                v.dismissed_at = Some("2026-11-12T09:00:00Z".into());
            }
            v
        })
        .collect();
    let mut refused = status();
    refused.last_refusal = Some(
        "Analytics upload returned 413: this outpost exceeded the server's request size limit. 接続設定を確認してから同期を再試行してください。".into(),
    );
    sharing(
        &[
            ("uiScale", scale),
            ("analytics.sharing_enabled", "true"),
            ("analytics.share_vents", "true"),
        ],
        refused,
        vents,
    )
}

/// Whether the element keyed `key` lies whole inside the page's view.
fn in_view(world: &World, key: &str) -> bool {
    let body = super::rect_of(world, "settings.body");
    let shown = super::rect_of(world, key);
    shown[3] > 0.0 && shown[1] >= body[1] - 0.5 && shown[1] + shown[3] <= body[1] + body[3] + 0.5
}

#[test]
fn tab_into_a_long_list_shows_its_chosen_option() {
    let mut world = dense(36, "1.5");
    world.resize((800.0, 600.0));
    for _ in 0..20 {
        press(&mut world, "j");
    }
    super::tab_to(&mut world, "settings.analytics.views.vents");
    tab_once(&mut world);
    assert_eq!(
        focused(&world).as_deref(),
        Some("item20"),
        "Tab lands on the chosen option"
    );
    assert!(
        in_view(&world, "item20"),
        "the chosen option at {:?} shows in the page's view {:?}",
        super::rect_of(&world, "item20"),
        super::rect_of(&world, "settings.body")
    );
    press(&mut world, "j");
    assert_eq!(
        focused(&world).as_deref(),
        Some("item21"),
        "j moves the focus with the choice"
    );
    assert!(in_view(&world, "item21"), "the next option shows");
}

#[test]
fn a_long_japanese_vent_with_a_refusal_keeps_send_in_view_at_the_regular_size() {
    let mut world = dense(4, "1");
    world.resize((1380.0, 900.0));
    press(&mut world, "j");
    assert!(
        named(&world, "settings.analytics.vent.text").starts_with("Vent 01 "),
        "the Japanese vent is chosen"
    );
    assert!(
        in_view(&world, "settings.analytics.vent.action.x"),
        "Send at {:?} shows whole in the page's view {:?} before any Tab",
        super::rect_of(&world, "settings.analytics.vent.action.x"),
        super::rect_of(&world, "settings.body")
    );
}

#[test]
fn show_all_moves_neither_column_sideways() {
    // 900 points high, the review's window; 840, where the vent opening first makes the page
    // overflow, so a scroll bar that took its room would narrow both columns.
    for high in [900.0, 840.0] {
        let mut world = dense(4, "1");
        world.resize((1380.0, high));
        assert!(
            shows(&world, "settings.analytics.vent.more"),
            "the long vent offers Show all"
        );
        let recording = world.record(60.0, 0.5, |_, app| {
            app.click(Target::key("settings.analytics.vent.more"))
        });
        assert!(
            !shows(&world, "settings.analytics.vent.more"),
            "Show all opened the vent"
        );
        for column in ["settings.analytics.inspector", "settings.analytics.list"] {
            let track = recording
                .tracks
                .iter()
                .find(|t| t.path.rsplit('/').next() == Some(column))
                .unwrap_or_else(|| panic!("{column} was recorded"));
            let across: Vec<(f32, f32)> = track
                .frames
                .iter()
                .map(|(_, state)| (state.rect[0], state.rect[2]))
                .collect();
            assert!(
                across.windows(2).all(|w| w[0] == w[1]),
                "{high}: {column} keeps its x and width on every frame across Show all: {across:?}"
            );
        }
    }
}

#[test]
fn keys_keep_answering_after_the_focused_vent_moves_to_done_and_back() {
    let mut world = triage();
    super::tab_to(&mut world, "settings.analytics.views.vents");
    tab_once(&mut world);
    assert_eq!(
        focused(&world).as_deref(),
        Some("item0"),
        "the vent's option has the focus"
    );
    press(&mut world, "d");
    assert!(
        focused(&world).is_some_and(|k| k.starts_with("item")),
        "the focus stays on an option after the vent moves to Done"
    );
    press(&mut world, "u");
    assert_eq!(said(&world), "Undone.");
}

fn key_press(world: &mut World, key: Key) {
    world.gesture(|app| {
        app.key(KeyEvent {
            key,
            mods: Mods::default(),
        })
    });
}

/// A full first page of records, one a minute so none fold, with older ones to read.
fn a_full_page_of_records() -> World {
    let rows = (1..=200)
        .rev()
        .map(|id| {
            record(
                id,
                "bus",
                "pr.updated",
                &format!("2026-10-24T{:02}:{:02}:00Z", id / 60, id % 60),
                "",
            )
        })
        .collect();
    on_analytics(&[], Vec::new(), Vec::new(), move |daemon| {
        serve_these_records(daemon, rows)
    })
}

#[test]
fn tab_in_a_record_view_stops_on_the_chosen_option_then_older_and_refresh() {
    let mut world = a_full_page_of_records();
    press(&mut world, "]");
    super::tab_to(&mut world, "settings.analytics.views.all");
    let mut order = Vec::new();
    for _ in 0..3 {
        tab_once(&mut world);
        order.extend(focused(&world));
    }
    assert_eq!(
        order,
        [
            "item0",
            "settings.analytics.older",
            "settings.analytics.refresh"
        ],
        "Tab after the view tabs"
    );
    key_press(&mut world, Key::Tab);
    world.gesture(|app| {
        app.key(KeyEvent {
            key: Key::Tab,
            mods: Mods {
                shift: true,
                ..Mods::default()
            },
        })
    });
    world.gesture(|app| {
        app.key(KeyEvent {
            key: Key::Tab,
            mods: Mods {
                shift: true,
                ..Mods::default()
            },
        })
    });
    assert_eq!(focused(&world).as_deref(), Some("settings.analytics.older"));
    press(&mut world, " ");
    let asked: Vec<Value> = world
        .daemon
        .sent_of("list_analytics_records")
        .iter()
        .map(|r| r["before_id"].clone())
        .collect();
    assert_eq!(
        asked,
        [Value::Null, json!(1)],
        "Space on Older reads the page before the oldest"
    );
}

#[test]
fn home_and_end_in_a_record_list_choose_its_ends_and_ask_the_daemon_nothing() {
    let mut world = on_records();
    press(&mut world, "]");
    super::tab_to(&mut world, "settings.analytics.views.all");
    tab_once(&mut world);
    assert_eq!(focused(&world).as_deref(), Some("item0"));
    let asked = world.daemon.sent_of("list_analytics_records").len();
    key_press(&mut world, Key::End);
    assert_eq!(
        focused(&world).as_deref(),
        Some("item2"),
        "End reaches the last option"
    );
    assert_eq!(selection(&world), ["unselected", "unselected", "selected"]);
    key_press(&mut world, Key::Home);
    assert_eq!(
        focused(&world).as_deref(),
        Some("item0"),
        "Home reaches the first"
    );
    assert_eq!(
        world.daemon.sent_of("list_analytics_records").len(),
        asked,
        "moving in the list reads nothing"
    );
}

/// Two new vents and one done, the done one `newest` or oldest of the three.
fn two_new_and_one_done(newest: bool) -> World {
    let mut first = vent("v-00");
    first.created_at = "2026-11-12T08:59:50Z".into();
    let mut second = vent("v-01");
    second.created_at = "2026-11-12T08:59:40Z".into();
    let mut done = vent("v-02");
    done.created_at = if newest {
        "2026-11-12T08:59:59Z"
    } else {
        "2026-11-12T08:59:00Z"
    }
    .into();
    done.dismissed_at = Some("2026-11-12T09:00:00Z".into());
    triage_of(vec![first, second, done])
}

#[test]
fn dismissing_a_focused_vent_keeps_the_keyboard_in_the_list_so_u_undoes_it() {
    for newest in [true, false] {
        let mut world = two_new_and_one_done(newest);
        super::tab_to(&mut world, "settings.analytics.views.vents");
        tab_once(&mut world);
        press(&mut world, "j");
        assert_eq!(focused(&world).as_deref(), Some("item1"));
        press(&mut world, "d");
        let after = focused(&world);
        assert!(
            after.as_deref().is_some_and(|k| k.starts_with("item")),
            "the focus stays on an option after d, the done vent newest: {newest}, on {after:?}"
        );
        assert_eq!(
            selection(&world)
                .iter()
                .position(|s| *s == "selected")
                .map(|at| format!("item{at}")),
            after,
            "the focused option is the chosen one"
        );
        press(&mut world, "u");
        let asked: Vec<(String, Value)> = world
            .daemon
            .sent_of("set_vent_dismissed")
            .iter()
            .map(|r| (vent_id(r), r["dismissed"].clone()))
            .collect();
        assert_eq!(
            asked,
            [("v-01".into(), json!(true)), ("v-01".into(), json!(false))],
            "u undoes the dismissal, the done vent newest: {newest}"
        );
    }
}

#[test]
fn home_end_and_the_page_keys_move_the_choice_in_the_list_and_show_it_whole() {
    let mut world = dense(36, "1.5");
    world.resize((800.0, 600.0));
    super::tab_to(&mut world, "settings.analytics.views.vents");
    tab_once(&mut world);
    let walk = |world: &mut World, key: Key| {
        let pressed = format!("{key:?}");
        key_press(world, key);
        let on = focused(world).unwrap_or_default();
        assert!(
            on.starts_with("item"),
            "{pressed} keeps the focus on an option, not {on:?}"
        );
        let chosen = selection(world).iter().position(|s| *s == "selected");
        assert_eq!(
            Some(format!("item{}", chosen.unwrap_or(99))),
            Some(on.clone())
        );
        assert!(
            in_view(world, &on),
            "{pressed} shows {on} whole at {:?} in {:?}",
            super::rect_of(world, &on),
            super::rect_of(world, "settings.body")
        );
        on
    };
    assert_eq!(walk(&mut world, Key::End), "item35");
    assert_eq!(walk(&mut world, Key::Home), "item0");
    let down = walk(&mut world, Key::PageDown);
    assert_ne!(down, "item0", "Page Down moves the choice");
    let further = walk(&mut world, Key::PageDown);
    assert_ne!(further, down);
    assert_eq!(
        walk(&mut world, Key::PageUp),
        down,
        "Page Up comes back a page"
    );
    for _ in 0..30 {
        key_press(&mut world, Key::Down);
    }
    assert_eq!(
        walk(&mut world, Key::Home),
        "item0",
        "Home from a done option"
    );
    assert_eq!(walk(&mut world, Key::End), "item35");
}

#[test]
fn m_and_the_view_keys_keep_the_keyboard_on_the_page() {
    let mut world = triage();
    super::tab_to(&mut world, "settings.analytics.views.vents");
    tab_once(&mut world);
    press(&mut world, "m");
    assert_eq!(
        focused(&world).as_deref(),
        Some("item0"),
        "m opens Machine settings and the option keeps the focus"
    );
    press(&mut world, "]");
    assert_eq!(
        focused(&world).as_deref(),
        Some("settings.analytics.views.all"),
        "] puts the focus on the view it shows"
    );
    press(&mut world, "[");
    assert_eq!(
        focused(&world).as_deref(),
        Some("settings.analytics.views.vents")
    );
    tab_once(&mut world);
    assert_eq!(focused(&world).as_deref(), Some("item0"));
}

/// The bus record list, its burst the last option, with the keyboard on its first option.
fn on_bus_records() -> World {
    let mut world = on_records();
    press(&mut world, "]");
    press(&mut world, "]");
    super::tab_to(&mut world, "settings.analytics.views.bus");
    tab_once(&mut world);
    assert_eq!(focused(&world).as_deref(), Some("item0"));
    assert!(
        rows(&world)[1].contains("pr.updated ×3"),
        "{:?}",
        rows(&world)
    );
    world
}

/// The keys move the choice and only move it: the burst stays folded, the keyboard stays on
/// the option, and the daemon is asked nothing.
fn moves_without_opening(world: &mut World, keys: &[Key]) {
    let asked = world.daemon.sent_of("list_analytics_records").len();
    for key in keys {
        key_press(world, key.clone());
    }
    assert_eq!(
        rows(world).len(),
        2,
        "the burst stays folded: {:?}",
        rows(world)
    );
    assert_eq!(
        named(world, "settings.analytics.record.heading"),
        "3 records in the same second"
    );
    assert_eq!(
        focused(world).as_deref(),
        Some("item1"),
        "the keyboard stays on the option"
    );
    assert_eq!(world.daemon.sent_of("list_analytics_records").len(), asked);
}

#[test]
fn end_on_the_chosen_last_burst_moves_nothing_and_opens_nothing() {
    let mut world = on_bus_records();
    moves_without_opening(&mut world, &[Key::End, Key::End]);
    key_press(&mut world, Key::Home);
    key_press(&mut world, Key::End);
    assert_eq!(rows(&world).len(), 2, "Home then End opens nothing");
    press(&mut world, "Enter");
    assert_eq!(rows(&world).len(), 4, "Return still opens the burst");
}

#[test]
fn page_down_and_up_choose_by_the_list_and_never_open_a_burst() {
    let mut world = on_bus_records();
    moves_without_opening(&mut world, &[Key::PageDown, Key::PageDown]);
    key_press(&mut world, Key::PageUp);
    assert_eq!(focused(&world).as_deref(), Some("item0"));
    assert_eq!(rows(&world).len(), 2);
}

#[test]
fn a_click_on_the_chosen_burst_opens_it_and_the_keyboard_stays_on_the_list() {
    let mut world = on_bus_records();
    key_press(&mut world, Key::End);
    click(&mut world, "item1");
    assert_eq!(rows(&world).len(), 4, "the click opens the chosen burst");
    assert_eq!(
        focused(&world).as_deref(),
        Some("item1"),
        "the keyboard stays on the opened burst's first record"
    );
}

use std::sync::{Arc, Mutex};

use attn_app::Action;
use attn_protocol::{
    InitialStateMessage, OutpostInfo, PartyView, Profile, ProfilesChangedMessage, Seed,
    SessionClosedMessage, SessionLedgerEntry, SessionLedgerFacet, SessionLedgerFacets,
    SessionLedgerProfileFacet, SessionListResult, SessionListResultMessage, SessionReopen,
    SessionReopenResult, SessionReopenResultMessage, SettingsUpdatedMessage,
};
use attn_testkit::{Daemon, Request, Target, World, message};
use rune::{Key, KeyEvent, Mods};

mod conversations;
mod worktrees;

use super::queue::ago;
use super::sidebar::{chord, desktop, pane, session, state};

pub(super) fn entry(id: &str, label: &str, minutes_ago: i64) -> SessionLedgerEntry {
    SessionLedgerEntry {
        id: id.into(),
        label: label.into(),
        agent: "claude".into(),
        directory: format!("/Users/victor/projects/{id}"),
        profile_id: "work".into(),
        profile_name: "Work".into(),
        repository: Some("/Users/victor/projects/attn".into()),
        state: "waiting_input".into(),
        last_seen: ago(minutes_ago),
        ..SessionLedgerEntry::default()
    }
}

pub(super) fn closed(mut row: SessionLedgerEntry, by: &str) -> SessionLedgerEntry {
    row.closed_at = Some(row.last_seen.clone());
    row.closed_by = Some(by.into());
    row.state = "idle".into();
    row
}

fn live_api() -> SessionLedgerEntry {
    entry("api", "API work", 5)
}

pub(super) fn closed_docs() -> SessionLedgerEntry {
    closed(entry("docs", "Docs pass", 30), "user")
}

fn closed_ledger() -> SessionLedgerEntry {
    closed(entry("ledger", "Ledger rewrite", 60), "user")
}

fn facets() -> SessionLedgerFacets {
    SessionLedgerFacets {
        repositories: vec![SessionLedgerFacet {
            value: "/Users/victor/projects/attn".into(),
            count: 3,
        }],
        profiles: vec![SessionLedgerProfileFacet {
            profile_id: "work".into(),
            name: "Work".into(),
            count: 3,
            ..SessionLedgerProfileFacet::default()
        }],
    }
}

pub(super) fn page(request: &Request, entries: Vec<SessionLedgerEntry>) -> serde_json::Value {
    page_of(request, entries, 0, None)
}

fn page_of(
    request: &Request,
    entries: Vec<SessionLedgerEntry>,
    omitted: i64,
    next_before: Option<&str>,
) -> serde_json::Value {
    message(SessionListResultMessage {
        request_id: request["request_id"].as_str().unwrap().into(),
        success: true,
        result: Some(SessionListResult {
            entries,
            omitted,
            next_before: next_before.map(str::to_owned),
            facets: request["before"].is_null().then(facets),
            ..SessionListResult::default()
        }),
        ..SessionListResultMessage::new()
    })
}

/// A daemon whose ledger holds `rows`, answered by the scope each read asks for.
pub(super) fn ledger_daemon(initial: InitialStateMessage, rows: Vec<SessionLedgerEntry>) -> Daemon {
    let daemon = super::palette::daemon(initial, vec![]);
    daemon.on("session_list", move |r| {
        let closed = r["closed"] == true;
        let all = r["all"] == true;
        let shown = rows
            .iter()
            .filter(|e| all || e.closed_at.is_some() == closed)
            .cloned()
            .collect();
        page(r, shown)
    });
    daemon
}

pub(super) fn world() -> World {
    World::start(ledger_daemon(
        state(
            vec![session("api", "waiting_input")],
            vec![desktop("d1", "a", "One", vec![pane("d1", "p1", "api")])],
        ),
        vec![live_api(), closed_docs(), closed_ledger()],
    ))
}

pub(super) fn open(world: &mut World) {
    world.gesture(|app| app.key(chord(Action::SessionsOpen)));
}

pub(super) fn rows(world: &World) -> Vec<String> {
    world
        .screen()
        .descendants(Target::key("ledger.list"))
        .into_iter()
        .filter_map(|n| {
            n.key?
                .strip_prefix("ledger.row.")
                .filter(|k| !k.contains('.'))
                .map(str::to_owned)
        })
        .collect()
}

pub(super) fn selected(world: &World) -> Option<String> {
    world
        .screen()
        .descendants(Target::key("ledger.list"))
        .into_iter()
        .find(|n| {
            n.value.as_deref() == Some("selected")
                && n.key
                    .as_deref()
                    .is_some_and(|k| k.starts_with("ledger.row."))
        })
        .and_then(|n| n.key?.strip_prefix("ledger.row.").map(str::to_owned))
}

pub(super) fn has(world: &World, key: &str) -> bool {
    world.screen().find(Target::key(key)).is_some()
}

pub(super) fn name(world: &World, key: &str) -> String {
    world
        .screen()
        .get(Target::key(key))
        .name
        .unwrap_or_default()
}

/// The words of `key` and everything inside it.
pub(super) fn text_in(world: &World, key: &str) -> String {
    let screen = world.screen();
    std::iter::once(screen.get(Target::key(key)))
        .chain(screen.descendants(Target::key(key)))
        .filter_map(|n| n.name)
        .collect::<Vec<_>>()
        .join(" ")
}

pub(super) fn query_text(world: &World) -> String {
    world
        .screen()
        .get(Target::key("ledger.query.field"))
        .value
        .unwrap_or_default()
}

pub(super) fn focused(world: &World) -> Option<String> {
    world.screen().focused().and_then(|n| n.key)
}

pub(super) fn click(world: &mut World, key: &str) {
    let key = key.to_owned();
    world.gesture(|app| app.click(Target::key(key)));
}

pub(super) fn plain(world: &mut World, key: Key) {
    world.gesture(|app| {
        app.key(KeyEvent {
            key,
            mods: Mods::default(),
        })
    });
}

pub(super) fn letter(world: &mut World, c: &str) {
    plain(world, Key::Text(c.into()));
}

pub(super) fn type_query(world: &mut World, text: &str) {
    click(world, "ledger.query.field");
    let text = text.to_owned();
    world.gesture(|app| app.type_text(&text));
}

pub(super) fn reads(world: &World) -> Vec<Request> {
    world.daemon.sent_of("session_list")
}

fn last_read(world: &World) -> Request {
    reads(world).pop().expect("the ledger read a page")
}

pub(super) fn hold(world: &World, cmd: &str) -> Arc<Mutex<Vec<Request>>> {
    let held: Arc<Mutex<Vec<Request>>> = Arc::default();
    let keep = held.clone();
    world
        .daemon
        .on(cmd, move |r| keep.lock().unwrap().push(r.clone()));
    held
}

pub(super) fn shown_session(world: &World) -> Vec<String> {
    world
        .daemon
        .sent_of("desktop_show_session")
        .iter()
        .map(|r| r["session_id"].as_str().unwrap_or_default().to_owned())
        .collect()
}

fn close_event(world: &mut World, row: SessionLedgerEntry) {
    world.daemon.emit(SessionClosedMessage {
        session_ledger_entry: row,
        ..SessionClosedMessage::new()
    });
    world.settle();
}

#[test]
fn the_shortcut_opens_every_session_newest_first_on_the_first_row_and_escape_closes_it() {
    let mut world = world();
    open(&mut world);

    let read = last_read(&world);
    assert_eq!(
        (read["all"].as_bool(), read["limit"].as_i64()),
        (Some(true), Some(50))
    );
    assert!(read["closed"].is_null() && read["since"].is_null());
    assert!(
        read.get("reopen").is_none(),
        "the list asked for eligibility: {read}"
    );
    assert!(world.daemon.sent_of("session_reopen").is_empty());
    assert_eq!(name(&world, "ledger.row.docs.verb"), "Reopen");
    assert_eq!(name(&world, "ledger.row.ledger.verb"), "Reopen");
    assert_eq!(rows(&world), ["api", "docs", "ledger"]);
    assert_eq!(selected(&world).as_deref(), Some("api"));
    assert_eq!(focused(&world).as_deref(), Some("ledger.list"));
    assert_eq!(
        name(&world, "ledger.counts"),
        "3 sessions  ·  1 live  ·  2 closed"
    );

    plain(&mut world, Key::Escape);
    assert!(!has(&world, "ledger.panel"));
}

#[test]
fn the_palette_opens_the_sessions_list() {
    let mut world = world();
    world.gesture(|app| app.key(chord(Action::UiCommandPalette)));
    world.gesture(|app| app.type_text("open the sessions list"));
    click(&mut world, "palette.row.command:sessions");
    assert_eq!(rows(&world), ["api", "docs", "ledger"]);
}

#[test]
fn a_scope_reads_its_rows_and_is_remembered_for_the_next_open() {
    let mut world = world();
    open(&mut world);
    click(&mut world, "ledger.scope.closed");

    assert_eq!(last_read(&world)["closed"].as_bool(), Some(true));
    assert_eq!(rows(&world), ["docs", "ledger"]);
    let read_once = reads(&world).len();
    world.settle();
    world.settle();
    assert_eq!(reads(&world).len(), read_once);
    let remembered = world.daemon.sent_of("set_setting");
    let setting = remembered
        .iter()
        .rfind(|r| r["key"] == "sessions.filters")
        .expect("the filters were remembered");
    let stored: serde_json::Value =
        serde_json::from_str(setting["value"].as_str().unwrap()).unwrap();
    assert_eq!(stored["scope"], "closed");

    plain(&mut world, Key::Escape);
    world.daemon.emit(SettingsUpdatedMessage {
        settings: Some(
            [(
                "sessions.filters".to_owned(),
                setting["value"].as_str().unwrap().to_owned(),
            )]
            .into_iter()
            .collect(),
        ),
        ..SettingsUpdatedMessage::new()
    });
    world.settle();
    let before = reads(&world).len();
    open(&mut world);
    assert_eq!(reads(&world).len(), before + 1);
    assert_eq!(last_read(&world)["closed"].as_bool(), Some(true));
    assert_eq!(rows(&world), ["docs", "ledger"]);
}

#[test]
fn typed_filters_resolve_through_the_facets_and_a_token_nothing_matches_says_so() {
    let mut world = world();
    open(&mut world);
    type_query(&mut world, "7d repo:attn profile:work");

    let read = last_read(&world);
    assert!(
        read["since"].as_str().is_some_and(|s| s.ends_with('Z')),
        "{read}"
    );
    assert_eq!(read["repository"], "/Users/victor/projects/attn");
    assert_eq!(read["profile_id"], "work");

    type_query(&mut world, " repo:nope");
    let chips: Vec<(String, Option<String>)> = world
        .screen()
        .descendants(Target::key("ledger.panel"))
        .into_iter()
        .filter(|n| {
            n.key
                .as_deref()
                .is_some_and(|k| k.starts_with("ledger.chip."))
        })
        .map(|n| (n.name.unwrap_or_default(), n.value))
        .collect();
    assert!(
        chips.contains(&("repo:nope".into(), Some("unresolved".into()))),
        "{chips:?}"
    );
}

#[test]
fn words_and_dir_narrow_the_page_without_asking_the_daemon() {
    let mut world = world();
    open(&mut world);
    let before = reads(&world).len();

    type_query(&mut world, "ledger");
    assert_eq!(rows(&world), ["ledger"]);
    assert_eq!(
        name(&world, "ledger.counts"),
        "1 session  ·  1 closed  ·  2 hidden by the query"
    );

    plain(&mut world, Key::Escape);
    type_query(&mut world, "dir:~/projects/docs");
    assert_eq!(rows(&world), ["docs"]);
    assert_eq!(
        reads(&world).len(),
        before,
        "the page narrowed without a read"
    );
}

#[test]
fn a_backwards_custom_range_says_why_and_reads_nothing() {
    let mut world = world();
    open(&mut world);
    type_query(&mut world, "from:2026-09-01 to:2026-09-03");
    let read = last_read(&world);
    let (since, until) = (
        read["since"].as_str().unwrap(),
        read["until"].as_str().unwrap(),
    );
    assert_eq!(hours(until) - hours(since), 72, "{since} .. {until}");
    plain(&mut world, Key::Escape);
    type_query(&mut world, "from:2026-09-03 to:2026-09-01");
    let before = reads(&world).len();
    type_query(&mut world, " ");

    assert_eq!(rows(&world), Vec::<String>::new());
    assert_eq!(
        name(&world, "ledger.empty"),
        "The range ends before it starts; swap the two dates"
    );
    assert_eq!(
        reads(&world).len(),
        before,
        "a backwards range asked the daemon"
    );
}

#[test]
fn older_rows_load_from_the_cursor_and_append_to_the_page() {
    let mut world = World::start({
        let daemon = super::palette::daemon(state(vec![], vec![]), vec![]);
        daemon.on("session_list", |r| {
            if r["before"].is_null() {
                page_of(r, vec![closed_docs()], 1, Some("docs"))
            } else {
                page(r, vec![closed_ledger()])
            }
        });
        daemon
    });
    open(&mut world);
    assert_eq!(name(&world, "ledger.older"), "1 older ↓");

    click(&mut world, "ledger.older");
    assert_eq!(last_read(&world)["before"], "docs");
    assert_eq!(rows(&world), ["docs", "ledger"]);
    assert!(!has(&world, "ledger.older"));
}

#[test]
fn a_page_for_filters_the_user_left_is_dropped() {
    let mut world = world();
    let held = hold(&world, "session_list");
    open(&mut world);
    click(&mut world, "ledger.scope.closed");
    let asked = held.lock().unwrap().clone();
    assert_eq!(asked.len(), 2);

    world
        .daemon
        .reply_to(&asked[1], page(&asked[1], vec![closed_docs()]));
    world
        .daemon
        .reply_to(&asked[0], page(&asked[0], vec![live_api()]));
    world.settle();
    assert_eq!(rows(&world), ["docs"]);
}

#[test]
fn reopen_shows_the_row_busy_then_leaves_for_the_reopened_session() {
    let mut world = world();
    let held = hold(&world, "session_reopen");
    open(&mut world);
    plain(&mut world, Key::Down);
    plain(&mut world, Key::Enter);

    let asked = held.lock().unwrap().clone();
    assert_eq!(asked.len(), 1);
    assert_eq!(
        (asked[0]["session_id"].as_str(), asked[0]["action"].as_str()),
        (Some("docs"), Some("reopen"))
    );
    assert_eq!(name(&world, "ledger.row.docs.verb"), "reopening…");

    world.daemon.reply_to(
        &asked[0],
        SessionReopenResultMessage {
            success: true,
            result: Some(SessionReopenResult {
                session_id: "docs".into(),
                action: "reopen".into(),
                ..SessionReopenResult::default()
            }),
            ..SessionReopenResultMessage::new()
        },
    );
    world.settle();
    assert!(!has(&world, "ledger.panel"));
    assert_eq!(shown_session(&world), ["docs"]);
}

pub(super) fn refuse_with(world: &World, offer: Option<SessionReopen>, error: &str) {
    let error = error.to_owned();
    world.daemon.on("session_reopen", move |r| {
        message(SessionReopenResultMessage {
            request_id: r["request_id"].as_str().unwrap().into(),
            success: false,
            error: Some(error.clone()),
            reopen: offer.clone(),
            ..SessionReopenResultMessage::new()
        })
    });
}

#[test]
fn a_refusal_offers_what_the_session_takes_instead_from_enter_the_menu_and_a_digit() {
    let mut world = world();
    refuse_with(
        &world,
        Some(SessionReopen {
            reopenable: false,
            reason: Some("the worktree /private/tmp/a/b/wt is gone".into()),
            actions: vec![
                "recreate_worktree_and_reopen".into(),
                "fetch_recreate_and_reopen".into(),
            ],
            directory_state: "missing".into(),
            profile_id: "work".into(),
            ..SessionReopen::default()
        }),
        "the worktree is gone",
    );
    open(&mut world);
    plain(&mut world, Key::Down);
    plain(&mut world, Key::Enter);

    assert_eq!(
        name(&world, "ledger.row.docs.note"),
        "Reopen was refused; it offers Recreate the worktree instead"
    );
    assert_eq!(
        name(&world, "ledger.row.docs.verb"),
        "Recreate the worktree"
    );
    let inspector = text_in(&world, "ledger.inspector.reopen");
    assert!(
        inspector.contains("the worktree …/b/wt is gone"),
        "{inspector}"
    );
    assert!(
        inspector.contains("lands on its last or current desktop"),
        "{inspector}"
    );
    assert!(text_in(&world, "ledger.inspector").contains("directory is gone"));

    plain(&mut world, Key::Enter);
    letter(&mut world, ".");
    assert!(has(&world, "ledger.row.docs.menu"));
    letter(&mut world, "2");
    let actions: Vec<_> = world
        .daemon
        .sent_of("session_reopen")
        .iter()
        .map(|r| r["action"].as_str().unwrap_or_default().to_owned())
        .collect();
    assert_eq!(
        actions,
        [
            "reopen",
            "recreate_worktree_and_reopen",
            "fetch_recreate_and_reopen"
        ]
    );
}

#[test]
fn a_failure_that_offers_nothing_keeps_reopen_and_says_why() {
    let mut world = world();
    refuse_with(
        &world,
        None,
        "conversation 12345678-1234-1234-1234-123456789abc is no longer in codex's storage",
    );
    open(&mut world);
    plain(&mut world, Key::Down);
    plain(&mut world, Key::Enter);

    assert_eq!(
        name(&world, "ledger.row.docs.note"),
        "conversation 12345678-1234-1234-1234-123456789abc is no longer in codex's storage"
    );
    assert_eq!(name(&world, "ledger.row.docs.verb"), "Reopen");
}

#[test]
fn a_close_lands_on_the_page_in_place_and_forgets_the_rows_refusal() {
    let mut world = world();
    open(&mut world);
    let before = reads(&world).len();
    assert_eq!(name(&world, "ledger.row.api.verb"), "Focus");

    close_event(&mut world, closed(live_api(), "user"));
    assert_eq!(rows(&world), ["api", "docs", "ledger"]);
    assert_eq!(name(&world, "ledger.row.api.verb"), "Reopen");
    assert_eq!(reads(&world).len(), before);

    refuse_with(&world, None, "the conversation is gone");
    click(&mut world, "ledger.row.docs.verb");
    assert_eq!(
        name(&world, "ledger.row.docs.note"),
        "the conversation is gone"
    );
    let mut again = closed_docs();
    again.closed_at = Some(ago(2));
    close_event(&mut world, again);
    assert!(!has(&world, "ledger.row.docs.note"));

    click(&mut world, "ledger.scope.live");
    close_event(&mut world, closed(entry("web", "Web", 1), "user"));
    assert!(!rows(&world).contains(&"web".to_owned()));
}

#[test]
fn a_close_of_a_row_not_on_the_page_lands_first_when_the_filters_hold_it() {
    let mut world = world();
    open(&mut world);
    click(&mut world, "ledger.scope.closed");
    close_event(&mut world, closed(entry("web", "Web", 1), "user"));
    assert_eq!(rows(&world), ["web", "docs", "ledger"]);
}

#[test]
fn focus_leaves_the_list_for_the_live_agent() {
    let mut world = world();
    open(&mut world);
    click(&mut world, "ledger.row.api.verb");
    assert!(!has(&world, "ledger.panel"));
    assert_eq!(shown_session(&world), ["api"]);
}

#[test]
fn a_reconnect_reads_the_page_again() {
    let mut world = world();
    open(&mut world);
    let before = reads(&world).len();
    world.reconnect();
    assert_eq!(reads(&world).len(), before + 1);
    assert_eq!(rows(&world), ["api", "docs", "ledger"]);
}

#[test]
fn a_price_change_reads_the_page_again_and_nothing_else_does() {
    let mut world = world();
    open(&mut world);
    let before = reads(&world).len();
    world.daemon.emit(SettingsUpdatedMessage {
        settings: Some(
            [("theme".to_owned(), "dark".to_owned())]
                .into_iter()
                .collect(),
        ),
        ..SettingsUpdatedMessage::new()
    });
    world.settle();
    assert_eq!(reads(&world).len(), before);
    world.daemon.emit(SettingsUpdatedMessage {
        settings: Some(
            [("session_cost.price.claude-opus".to_owned(), "1".to_owned())]
                .into_iter()
                .collect(),
        ),
        ..SettingsUpdatedMessage::new()
    });
    world.settle();
    assert_eq!(reads(&world).len(), before + 1);
}

#[test]
fn rows_name_who_closed_them_and_people_read_titles_never_ids() {
    let mut by_agent = closed(entry("docs", "Docs pass", 30), "api");
    by_agent.close_reason = Some("finished; 11111111-2222-3333-4444-555555555555 took over".into());
    let by_unknown = closed(entry("ledger", "", 60), "ghost-session");
    let by_user = closed(entry("web", "Web", 90), "user");
    let mut world = World::start(ledger_daemon(
        state(vec![session("api", "waiting_input")], vec![]),
        vec![live_api(), by_agent, by_unknown, by_user],
    ));
    open(&mut world);

    let docs = name(&world, "ledger.row.docs.meta");
    assert!(
        docs.contains("closed by API work: finished; a session took over"),
        "{docs}"
    );
    let ledger = name(&world, "ledger.row.ledger.meta");
    assert!(ledger.contains("closed by ghost-session"), "{ledger}");
    assert_eq!(name(&world, "ledger.row.ledger"), "untitled session");
    assert!(name(&world, "ledger.row.web.meta").contains("closed by you"));
}

#[test]
fn a_session_whose_profile_was_deleted_offers_no_reopening() {
    let mut gone = closed_docs();
    gone.profile_deleted = Some(true);
    let mut world = World::start(ledger_daemon(state(vec![], vec![]), vec![gone]));
    open(&mut world);
    assert_eq!(name(&world, "ledger.row.docs.verb"), "Keep conversation");
    assert!(
        !has(&world, "ledger.row.docs.more"),
        "a deleted profile's row offered more than keeping"
    );
    letter(&mut world, "2");
    assert!(world.daemon.sent_of("session_reopen").is_empty());
    let reopen = text_in(&world, "ledger.inspector.reopen");
    assert!(
        reopen.contains("Its profile was deleted. This session cannot be reopened."),
        "{reopen}"
    );
    assert!(name(&world, "ledger.row.docs.meta").contains("Work (deleted)"));
}

#[test]
fn a_row_shows_its_outpost_by_name_and_by_host_once_the_outpost_is_gone() {
    let mut there = closed_docs();
    there.host = Some("host-1".into());
    let mut gone = closed_ledger();
    gone.host = Some("host-2".into());
    let mut initial = state(vec![], vec![]);
    initial.outposts = Some(vec![OutpostInfo {
        id: "host-1".into(),
        name: "builder".into(),
        ..OutpostInfo::default()
    }]);
    let mut world = World::start(ledger_daemon(initial, vec![there, gone]));
    open(&mut world);
    assert!(name(&world, "ledger.row.docs.meta").contains("claude  ·  builder"));
    assert!(name(&world, "ledger.row.ledger.meta").contains("claude  ·  host-2"));
}

#[test]
fn a_hidden_live_session_is_marked_on_its_row_and_in_the_inspector() {
    let mut hidden = live_api();
    hidden.hidden = Some(true);
    let mut world = World::start(ledger_daemon(
        state(vec![session("api", "waiting_input")], vec![]),
        vec![hidden, closed_docs()],
    ));
    open(&mut world);
    assert!(name(&world, "ledger.row.api.meta").ends_with("hidden"));
    assert!(text_in(&world, "ledger.inspector").contains("hidden"));
    assert!(!name(&world, "ledger.row.docs.meta").contains("hidden"));
}

#[test]
fn the_keys_walk_rows_open_the_menu_copy_the_path_and_back_out_one_layer_at_a_time() {
    let mut world = world();
    open(&mut world);
    letter(&mut world, "j");
    assert_eq!(selected(&world).as_deref(), Some("docs"));
    plain(&mut world, Key::End);
    assert_eq!(selected(&world).as_deref(), Some("ledger"));
    letter(&mut world, "k");
    assert_eq!(selected(&world).as_deref(), Some("docs"));

    letter(&mut world, "y");
    assert_eq!(
        world.clipboard.text().map(|t| t.to_string()).as_deref(),
        Some("/Users/victor/projects/docs")
    );
    assert!(has(&world, "ledger.copied"));

    letter(&mut world, "?");
    assert!(has(&world, "ledger.legend"));
    plain(&mut world, Key::Escape);
    assert!(!has(&world, "ledger.legend"));
    assert!(has(&world, "ledger.panel"));

    letter(&mut world, "/");
    assert_eq!(focused(&world).as_deref(), Some("ledger.query.field"));
    world.gesture(|app| app.type_text("d"));
    assert_eq!(
        focused(&world).as_deref(),
        Some("ledger.query.field"),
        "after d"
    );
    world.gesture(|app| app.type_text("ocs"));
    assert_eq!(rows(&world), ["docs"]);
    assert_eq!(
        focused(&world).as_deref(),
        Some("ledger.query.field"),
        "after typing"
    );
    plain(&mut world, Key::Escape);
    assert_eq!(rows(&world), ["api", "docs", "ledger"]);
    assert_eq!(focused(&world).as_deref(), Some("ledger.query.field"));
    plain(&mut world, Key::Escape);
    assert!(has(&world, "ledger.panel"));
    assert_eq!(focused(&world).as_deref(), Some("ledger.list"));
    plain(&mut world, Key::Escape);
    assert!(!has(&world, "ledger.panel"));
}

#[test]
fn a_seed_the_session_tends_opens_from_its_row() {
    let mut initial = state(vec![session("api", "waiting_input")], vec![]);
    initial.seeds = Some(vec![Seed {
        id: "s-cache1".into(),
        title: "Cache store migration".into(),
        tender: Some(PartyView {
            session_id: Some("api".into()),
            ..PartyView::default()
        }),
        ..Seed::default()
    }]);
    let mut world = World::start(ledger_daemon(initial, vec![live_api()]));
    open(&mut world);
    letter(&mut world, ".");
    assert_eq!(
        name(&world, "ledger.row.api.menu.1"),
        "Seed · Cache store migration"
    );
    letter(&mut world, "2");
    assert!(!has(&world, "ledger.panel"));
    assert_eq!(world.daemon.sent_of("open_seed")[0]["seed_id"], "s-cache1");
}

fn failure(request: &Request, error: &str) -> serde_json::Value {
    message(SessionListResultMessage {
        request_id: request["request_id"].as_str().unwrap().into(),
        success: false,
        error: Some(error.into()),
        ..SessionListResultMessage::new()
    })
}

pub(super) fn hang_up(world: &mut World) {
    world.daemon.refuse_connections(true);
    world.daemon.disconnect();
    world.settle();
}

pub(super) fn come_back(world: &mut World) {
    world.daemon.refuse_connections(false);
    let before = world.daemon.connections();
    for _ in 0..30 {
        if world.daemon.connections() > before && !has(world, "connection.reconnecting") {
            return;
        }
        world.advance(std::time::Duration::from_secs(1));
    }
    panic!("the app did not reconnect within 30 retry seconds");
}

fn reprice(world: &mut World) {
    world.daemon.emit(SettingsUpdatedMessage {
        settings: Some(
            [("session_cost.claude".to_owned(), "2".to_owned())]
                .into_iter()
                .collect(),
        ),
        ..SettingsUpdatedMessage::new()
    });
    world.settle();
}

#[test]
fn a_failed_refresh_keeps_the_rows_and_filters_whose_read_failed_show_none() {
    let mut world = World::start({
        let daemon = super::palette::daemon(state(vec![], vec![]), vec![]);
        let served = Arc::new(Mutex::new(0));
        daemon.on("session_list", move |r| {
            let mut n = served.lock().unwrap();
            *n += 1;
            if *n == 1 {
                page_of(r, vec![closed_docs()], 1, Some("docs"))
            } else {
                failure(r, "timeout")
            }
        });
        daemon
    });
    open(&mut world);
    assert_eq!(name(&world, "ledger.older"), "1 older ↓");

    reprice(&mut world);
    assert_eq!(rows(&world), ["docs"]);
    assert!(!has(&world, "ledger.older"));

    click(&mut world, "ledger.scope.closed");
    assert_eq!(rows(&world), Vec::<String>::new());
    assert_eq!(name(&world, "ledger.empty"), "timeout");
}

#[test]
fn filters_changed_while_disconnected_show_no_rows_drop_the_old_error_and_read_on_reconnect() {
    let mut world = World::start({
        let daemon = super::palette::daemon(state(vec![], vec![]), vec![]);
        let served = Arc::new(Mutex::new(0));
        daemon.on("session_list", move |r| {
            let mut n = served.lock().unwrap();
            *n += 1;
            match *n {
                1 => failure(r, "timeout"),
                _ => page(r, vec![closed_docs()]),
            }
        });
        daemon
    });
    open(&mut world);
    assert_eq!(name(&world, "ledger.empty"), "timeout");

    hang_up(&mut world);
    click(&mut world, "ledger.scope.closed");
    assert_eq!(
        name(&world, "ledger.empty"),
        "No closed sessions yet. Closing one records it here."
    );
    assert_eq!(reads(&world).len(), 1);

    come_back(&mut world);
    assert_eq!(rows(&world), ["docs"]);
    assert_eq!(last_read(&world)["closed"].as_bool(), Some(true));
}

#[test]
fn a_reconnect_drops_a_pending_older_page_and_offers_it_again() {
    let mut world = World::start({
        let daemon = super::palette::daemon(state(vec![], vec![]), vec![]);
        daemon.on("session_list", |r| {
            r["before"]
                .is_null()
                .then(|| page_of(r, vec![closed_docs()], 1, Some("docs")))
        });
        daemon
    });
    open(&mut world);
    click(&mut world, "ledger.older");
    assert_eq!(name(&world, "ledger.older"), "loading…");

    world.reconnect();
    assert_eq!(name(&world, "ledger.older"), "1 older ↓");
    click(&mut world, "ledger.older");
    assert_eq!(last_read(&world)["before"], "docs");
}

#[test]
fn the_first_read_asks_for_the_remembered_filters_and_shows_them_as_tokens() {
    let mut initial = state(vec![], vec![]);
    initial.settings.as_mut().unwrap().insert(
        "sessions.filters".into(),
        r#"{"scope":"closed","range":"7d","customFrom":"","customTo":"","profileId":"work","repository":"/Users/victor/projects/attn"}"#.into(),
    );
    let mut world = World::start(ledger_daemon(initial, vec![closed_docs()]));
    open(&mut world);

    let read = last_read(&world);
    assert_eq!(read["closed"].as_bool(), Some(true));
    assert_eq!(read["repository"], "/Users/victor/projects/attn");
    assert_eq!(read["profile_id"], "work");
    assert!(
        read["since"].as_str().is_some_and(|s| s.ends_with('Z')),
        "{read}"
    );
    assert_eq!(reads(&world).len(), 1);
    assert_eq!(query_text(&world), "repo:attn profile:work 7d");
    assert!(
        world.daemon.sent_of("set_setting").is_empty(),
        "opening rewrote the filters"
    );
}

#[test]
fn a_custom_range_is_restored_exactly_as_it_was_left() {
    let mut initial = state(vec![], vec![]);
    initial.settings.as_mut().unwrap().insert(
        "sessions.filters".into(),
        r#"{"scope":"all","range":"custom","customFrom":"2026-08-01","customTo":"2026-08-03","profileId":"","repository":""}"#.into(),
    );
    let mut world = World::start(ledger_daemon(initial, vec![]));
    open(&mut world);
    let read = last_read(&world);
    assert!(
        read["since"].is_string() && read["until"].is_string(),
        "{read}"
    );
    assert_eq!(query_text(&world), "from:2026-08-01 to:2026-08-03");
}

fn rename_profile(world: &mut World, name: &str) {
    world.daemon.emit(ProfilesChangedMessage {
        profiles: vec![Profile {
            id: "work".into(),
            name: name.into(),
            ..Profile::default()
        }],
        ..ProfilesChangedMessage::new()
    });
    world.settle();
}

#[test]
fn a_profile_rename_reads_again_and_the_query_follows_the_new_name() {
    let mut world = world();
    open(&mut world);
    type_query(&mut world, "profile:work");
    let before = reads(&world).len();

    rename_profile(&mut world, "Work");
    assert_eq!(reads(&world).len(), before);

    rename_profile(&mut world, "Office");
    assert_eq!(reads(&world).len(), before + 1);
    assert_eq!(query_text(&world), "profile:office");
    assert_eq!(last_read(&world)["profile_id"], "work");
}

/// Hours since the epoch of a UTC `YYYY-MM-DDTHH:...Z` instant.
fn hours(instant: &str) -> i64 {
    let n = |at: std::ops::Range<usize>| instant[at].parse::<i64>().unwrap();
    let (y, m, d) = (n(0..4), n(5..7), n(8..10));
    let (y, m) = if m <= 2 { (y - 1, m + 9) } else { (y, m - 3) };
    let era = y.div_euclid(400);
    let yoe = y - era * 400;
    let doe = yoe * 365 + yoe / 4 - yoe / 100 + (153 * m + 2) / 5 + d - 1;
    (era * 146_097 + doe - 719_468) * 24 + n(11..13)
}

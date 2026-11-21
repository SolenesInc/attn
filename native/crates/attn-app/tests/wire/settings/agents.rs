use std::sync::{Arc, Mutex};

use attn_protocol::SettingsUpdatedMessage;
use attn_testkit::{Daemon, Node, Request, Target, World};
use rune::Key;
use serde_json::json;

use super::harness_catalogs::{
    enter_model, harnesses, open_route, options, pick_effort, pick_model, picker_open, serve,
};
use super::{
    Settings, accepting, click, field, keeping, open, press, rest, retype, state, tab_to, value,
    write, writes,
};

const BOTH: &[(&str, &str)] = &[("claude_available", "true"), ("codex_available", "true")];

fn on_agents_with(daemon: Daemon, stored: &Settings) -> World {
    serve(&daemon, stored, harnesses());
    let mut world = World::start(daemon);
    open(&mut world);
    click(&mut world, "settings.nav.agents");
    assert_eq!(state(&mut world)["activeSection"], "agents");
    world.settle();
    world
}

fn on_agents(settings: &[(&str, &str)]) -> World {
    let (daemon, stored) = accepting(settings);
    on_agents_with(daemon, &stored)
}

fn both<'a>(settings: &[(&'a str, &'a str)]) -> Vec<(&'a str, &'a str)> {
    BOTH.iter().chain(settings).copied().collect()
}

fn saved(pairs: &[(&str, &str)]) -> Vec<(String, String)> {
    pairs
        .iter()
        .map(|(k, v)| ((*k).to_owned(), (*v).to_owned()))
        .collect()
}

fn node(world: &World, key: &str) -> Node {
    world.screen().get(Target::key(key))
}

fn shows(world: &World, key: &str) -> bool {
    world.screen().find(Target::key(key)).is_some()
}

fn toggle(setting: &str) -> String {
    format!("settings.toggle.{setting}")
}

fn press_named(world: &mut World, role: &str, name: &str) {
    world
        .automate("press", json!({"target": {"role": role, "name": name}}))
        .unwrap_or_else(|why| panic!("press {role} {name}: {why}"));
}

fn resume_choice(world: &World, name: &str) -> Node {
    world
        .screen()
        .descendants(Target::key("settings.choice.garden.tender_resume"))
        .into_iter()
        .find(|n| n.role == "radio-button" && n.name.as_deref() == Some(name))
        .unwrap_or_else(|| panic!("no {name} resume choice"))
}

fn verdicts(world: &World) -> Vec<String> {
    world
        .screen()
        .children(Target::key("settings.standby.cases"))
        .into_iter()
        .filter_map(|n| n.name)
        .collect()
}

/// The agent groups, by name, in the page's order.
fn groups(world: &World) -> Vec<String> {
    world
        .screen()
        .all(Target::role("group"))
        .into_iter()
        .filter(|n| {
            n.key
                .as_deref()
                .is_some_and(|k| k.starts_with("settings.agent.") && k.ends_with(".toggle.part"))
        })
        .filter_map(|n| n.name)
        .collect()
}

/// What an agent group's head says beside its name: its route, then whether it is available.
fn said_with(world: &World, key: &str) -> String {
    node(world, key).description.unwrap_or_default()
}

fn open_group(world: &mut World, agent: &str) {
    let key = format!("settings.agent.{agent}.toggle");
    if !node(world, &key).is("expanded") {
        click(world, &key);
    }
}

fn open_advanced(world: &mut World, agent: &str) {
    open_group(world, agent);
    click(world, &format!("settings.agent.{agent}.advanced"));
}

fn capabilities(world: &World, agent: &str) -> Vec<String> {
    world
        .screen()
        .children(Target::key(format!("settings.agent.{agent}.capabilities")))
        .into_iter()
        .filter_map(|n| n.name)
        .collect()
}

fn held_saves(daemon: &Daemon) -> Arc<Mutex<Vec<Request>>> {
    let held: Arc<Mutex<Vec<Request>>> = Arc::default();
    let keep = held.clone();
    daemon.on("set_setting", move |r| keep.lock().unwrap().push(r.clone()));
    held
}

fn answer(world: &mut World, stored: &Settings, request: &Request) {
    for reply in write(stored, &|_: &str, _: &str| None, request) {
        world.daemon.emit(reply);
    }
    world.settle();
}

#[test]
fn ringing_on_my_own_pr_comments_turns_on_and_back_off() {
    let mut world = on_agents(&[("pull_requests.own_comments", "quiet")]);
    let key = toggle("pull_requests.own_comments");
    assert_eq!(
        node(&world, "settings.block.seeds-on-standby")
            .name
            .as_deref(),
        Some("Seeds on standby")
    );
    let switch = node(&world, &key);
    assert_eq!(switch.role, "switch");
    assert_eq!(switch.name.as_deref(), Some("Ring on my own PR comments"));
    assert_eq!(
        switch.description.as_deref(),
        Some("Enable if you want to give feedback to agents by writing comments on the PR")
    );
    assert!(!switch.is("checked"));

    click(&mut world, &key);
    assert!(node(&world, &key).is("checked"));
    click(&mut world, &key);
    assert!(!node(&world, &key).is("checked"));
    assert_eq!(
        writes(&world),
        saved(&[
            ("pull_requests.own_comments", "ring"),
            ("pull_requests.own_comments", "quiet"),
        ])
    );
    rest(&mut world);
}

#[test]
fn a_profile_that_rings_shows_the_switch_on_and_turning_it_off_saves_quiet() {
    let mut world = on_agents(&[("pull_requests.own_comments", "ring")]);
    let key = toggle("pull_requests.own_comments");
    assert!(node(&world, &key).is("checked"));
    click(&mut world, &key);
    assert_eq!(
        writes(&world),
        saved(&[("pull_requests.own_comments", "quiet")])
    );
    assert!(!node(&world, &key).is("checked"));
    rest(&mut world);
}

#[test]
fn the_resume_choice_saves_and_says_which_mail_resumes_a_standby_agent() {
    let mut world = on_agents(&[("garden.tender_resume", "any")]);
    assert!(resume_choice(&world, "Any mail").is("checked"));
    assert_eq!(
        verdicts(&world),
        [
            "Resumes PR #123 got approved",
            "Resumes Its reminder came up",
            "Resumes Another agent messaged the seed",
            "Resumes You answered its ask",
        ]
    );

    press_named(&mut world, "radio-button", "PRs and reminders");
    assert!(resume_choice(&world, "PRs and reminders").is("checked"));
    assert_eq!(
        verdicts(&world),
        [
            "Resumes PR #123 got approved",
            "Resumes Its reminder came up",
            "Waits Another agent messaged the seed",
            "Waits You answered its ask",
        ]
    );

    press_named(&mut world, "radio-button", "Never");
    assert_eq!(
        verdicts(&world),
        [
            "Waits PR #123 got approved",
            "Waits Its reminder came up",
            "Waits Another agent messaged the seed",
            "Waits You answered its ask",
        ]
    );
    assert_eq!(
        writes(&world),
        saved(&[
            ("garden.tender_resume", "watches"),
            ("garden.tender_resume", "off"),
        ])
    );
    let crew =
        "attn always starts a session for a crew member who tends the seed, whatever you pick.";
    assert!(
        world
            .screen()
            .descendants(Target::key("settings.block.seeds-on-standby"))
            .iter()
            .any(|n| n.name.as_deref() == Some(crew))
    );
    rest(&mut world);
}

#[test]
fn the_saved_resume_choice_shows_and_a_profile_without_one_resumes_on_any_mail() {
    let mut world = on_agents(&[("garden.tender_resume", "off")]);
    assert!(resume_choice(&world, "Never").is("checked"));
    assert!(!resume_choice(&world, "Any mail").is("checked"));
    rest(&mut world);

    let mut world = on_agents(&[]);
    assert!(resume_choice(&world, "Any mail").is("checked"));
    rest(&mut world);
}

#[test]
fn auto_approve_and_direct_launches_start_on_and_switch_both_ways() {
    let mut world = on_agents(&both(&[("auto_approve_enabled", "false")]));
    let approve = toggle("auto_approve_enabled");
    assert_eq!(node(&world, &approve).name.as_deref(), Some("Auto-approve"));
    assert!(!node(&world, &approve).is("checked"));
    click(&mut world, &approve);
    assert!(node(&world, &approve).is("checked"));
    assert_eq!(writes(&world), saved(&[("auto_approve_enabled", "true")]));
    rest(&mut world);

    let mut world = on_agents(BOTH);
    assert!(node(&world, &approve).is("checked"));
    click(&mut world, &approve);
    let direct = toggle("direct_agent_launch_enabled");
    assert!(node(&world, &direct).is("checked"));
    click(&mut world, &direct);
    assert!(!node(&world, &direct).is("checked"));
    click(&mut world, &direct);
    assert!(node(&world, &direct).is("checked"));
    assert_eq!(
        writes(&world),
        saved(&[
            ("auto_approve_enabled", "false"),
            ("direct_agent_launch_enabled", "false"),
            ("direct_agent_launch_enabled", "true"),
        ])
    );
    rest(&mut world);
}

#[test]
fn the_default_model_keeps_its_harness_and_saves_the_model_then_the_effort() {
    let mut world = on_agents(BOTH);
    assert_eq!(
        value(&world, "settings.route.default-claude"),
        "Claude · Claude default · default"
    );
    open_route(&mut world, "Claude default model");
    assert!(!shows(&world, "settings.route-picker.harnesses"));
    pick_model(&mut world, "Opus 5.5");
    pick_effort(&mut world, "high");
    assert!(!picker_open(&world));
    assert_eq!(
        writes(&world),
        saved(&[
            ("default_model_claude", "opus"),
            ("default_effort_claude", "high"),
        ])
    );
    rest(&mut world);
}

#[test]
fn a_retired_stored_model_and_effort_stay_shown_without_a_write() {
    let mut world = on_agents(&both(&[
        ("default_model_claude", "retired-model"),
        ("default_effort_claude", "future-effort"),
    ]));
    open_route(&mut world, "Claude default model");
    let retired = options(&world, "models")
        .into_iter()
        .find(|(name, _, _)| name.starts_with("retired-model"))
        .expect("the retired model is offered");
    assert!(retired.1, "the retired model is selected");
    assert_eq!(
        value(&world, "settings.route-picker.effort"),
        "future-effort"
    );
    assert!(writes(&world).is_empty());
    press(&mut world, Key::Escape);
    rest(&mut world);
}

#[test]
fn the_harness_default_row_clears_the_default_model() {
    let mut world = on_agents(&both(&[("default_model_claude", "opus")]));
    open_route(&mut world, "Claude default model");
    pick_model(&mut world, "Claude default");
    assert_eq!(writes(&world), saved(&[("default_model_claude", "")]));
    rest(&mut world);
}

#[test]
fn the_keyboard_opens_and_closes_each_agent_group_and_its_advanced_part_apart() {
    let mut world = on_agents(BOTH);
    let space = || Key::Text(" ".into());
    let (claude, codex) = (
        "settings.agent.claude.toggle",
        "settings.agent.codex.toggle",
    );
    assert!(node(&world, claude).is("expanded"));
    assert!(!node(&world, codex).is("expanded"));

    tab_to(&mut world, codex);
    press(&mut world, space());
    assert!(node(&world, codex).is("expanded"));
    assert!(
        node(&world, claude).is("expanded"),
        "opening Codex leaves Claude open"
    );

    let advanced = "settings.agent.codex.advanced";
    tab_to(&mut world, advanced);
    press(&mut world, space());
    assert!(node(&world, advanced).is("expanded"));
    assert!(shows(&world, &field("default_context_window_cap_codex")));
    assert!(!shows(&world, &field("default_context_window_cap_claude")));

    tab_to(&mut world, claude);
    press(&mut world, space());
    assert!(!node(&world, claude).is("expanded"));
    assert!(
        node(&world, codex).is("expanded"),
        "closing Claude leaves Codex open"
    );
    rest(&mut world);
}

#[test]
fn each_agent_shows_its_context_cap_and_an_unset_one_is_blank() {
    let mut world = on_agents(&both(&[("default_context_window_cap_claude", "800000")]));
    open_advanced(&mut world, "claude");
    open_advanced(&mut world, "codex");
    assert_eq!(
        value(&world, &field("default_context_window_cap_claude")),
        "800000"
    );
    assert_eq!(
        value(&world, &field("default_context_window_cap_codex")),
        ""
    );
    rest(&mut world);
}

#[test]
fn a_context_cap_saves_when_focus_leaves_it_and_clearing_it_uncaps() {
    let mut world = on_agents(BOTH);
    open_advanced(&mut world, "claude");
    let cap = field("default_context_window_cap_claude");
    retype(&mut world, &cap, "800000");
    click(&mut world, "settings.heading");
    retype(&mut world, &cap, "");
    click(&mut world, "settings.heading");
    assert_eq!(
        writes(&world),
        saved(&[
            ("default_context_window_cap_claude", "800000"),
            ("default_context_window_cap_claude", ""),
        ])
    );
    rest(&mut world);
}

#[test]
fn a_refused_context_cap_keeps_its_group_open_and_the_dialog_with_it() {
    let reason = "context window cap must be between 10000 and 2000000";
    let (daemon, stored) = keeping(BOTH, move |key, _| {
        (key == "default_context_window_cap_claude").then(|| reason.to_owned())
    });
    let mut world = on_agents_with(daemon, &stored);
    open_advanced(&mut world, "claude");
    retype(&mut world, &field("default_context_window_cap_claude"), "1");
    click(&mut world, "settings.heading");
    let refusal = "settings.refusal.default_context_window_cap_claude";
    assert_eq!(node(&world, refusal).name.as_deref(), Some(reason));

    click(&mut world, "settings.agent.claude.toggle");
    assert!(shows(&world, refusal), "a group with a refusal stays open");
    press(&mut world, Key::Escape);
    assert_eq!(state(&mut world)["open"], true);
    assert_eq!(node(&world, refusal).name.as_deref(), Some(reason));
    rest(&mut world);
}

#[test]
fn a_plugin_agent_lists_its_capabilities_without_an_executable_field() {
    let mut world = on_agents(&[("snipe_available", "true"), ("snipe_cap_resume", "true")]);
    assert!(groups(&world).contains(&"Snipe".to_owned()));
    open_advanced(&mut world, "snipe");
    assert_eq!(capabilities(&world, "snipe"), ["Resume Supported"]);
    let list = world
        .screen()
        .get(Target::role("list").name("Capabilities"));
    assert_eq!(
        list.key.as_deref(),
        Some("settings.agent.snipe.capabilities")
    );
    let items = world
        .screen()
        .children(Target::key("settings.agent.snipe.capabilities"));
    assert!(
        items.iter().all(|item| item.role == "list-item"),
        "{items:?}"
    );
    assert!(!shows(&world, &field("snipe_executable")));
    rest(&mut world);
}

#[test]
fn asks_sit_under_agents_and_models_on_by_default_and_turn_off() {
    let (daemon, stored) = accepting(&[]);
    serve(&daemon, &stored, harnesses());
    let mut world = World::start(daemon);
    open(&mut world);
    click(&mut world, "settings.search.field");
    world.gesture(|app| app.type_text("asks"));
    click(&mut world, "settings.nav.agents");
    let asks = "settings.feature.asks_enabled";
    assert!(node(&world, asks).is("checked"));
    click(&mut world, asks);
    assert_eq!(writes(&world), saved(&[("asks_enabled", "false")]));
    rest(&mut world);
}

#[test]
fn asks_and_the_shared_host_stay_out_of_experimental() {
    let (daemon, _) = accepting(&[]);
    let mut world = World::start(daemon);
    open(&mut world);
    click(&mut world, "settings.nav.experimental");
    assert!(!shows(&world, "settings.feature.asks_enabled"));
    assert!(!shows(&world, "settings.feature.pty_shared_host_enabled"));
    assert!(!shows(&world, &toggle("pty_shared_host_enabled")));
    rest(&mut world);
}

#[test]
fn the_shared_host_shows_what_the_daemon_holds_not_what_was_sent() {
    let (daemon, stored) = accepting(&[("pty_backend_mode", "migrating")]);
    let held = held_saves(&daemon);
    let mut world = on_agents_with(daemon, &stored);
    let host = toggle("pty_shared_host_enabled");
    assert!(node(&world, &host).is("checked"));
    click(&mut world, &host);
    assert_eq!(
        writes(&world),
        saved(&[("pty_shared_host_enabled", "false")])
    );
    assert!(node(&world, &host).is("checked"));

    let request = held.lock().unwrap().remove(0);
    answer(&mut world, &stored, &request);
    assert!(!node(&world, &host).is("checked"));
    rest(&mut world);
}

#[test]
fn the_shared_host_turns_back_on_from_a_stored_false() {
    let mut world = on_agents(&[
        ("pty_backend_mode", "migrating"),
        ("pty_shared_host_enabled", "false"),
        ("pty_shared_host_active", "false"),
    ]);
    let host = toggle("pty_shared_host_enabled");
    assert!(!node(&world, &host).is("checked"));
    click(&mut world, &host);
    assert_eq!(
        writes(&world),
        saved(&[("pty_shared_host_enabled", "true")])
    );
    rest(&mut world);
}

#[test]
fn the_shared_host_cannot_be_switched_unless_the_backend_is_migrating() {
    for mode in ["worker", "shared", "unknown"] {
        let mut world = on_agents(&[("pty_backend_mode", mode)]);
        let host = toggle("pty_shared_host_enabled");
        assert!(node(&world, &host).is("disabled"), "{mode}");
        let _ = world.automate("press", json!({"target": {"key": host}}));
        assert!(writes(&world).is_empty(), "{mode}");
        rest(&mut world);
    }
}

#[test]
fn searching_for_terminal_hosting_finds_the_shared_host_under_agents_and_models() {
    let (daemon, stored) = accepting(&[]);
    serve(&daemon, &stored, harnesses());
    let mut world = World::start(daemon);
    open(&mut world);
    for query in ["terminal", "pty", "Shared terminal host"] {
        retype(&mut world, "settings.search.field", query);
        assert!(shows(&world, "settings.nav.agents"), "{query}");
    }
    click(&mut world, "settings.nav.agents");
    assert!(shows(&world, &toggle("pty_shared_host_enabled")));
    rest(&mut world);
}

#[test]
fn a_default_agent_reversal_made_while_the_first_choice_saves_is_kept() {
    let (daemon, stored) = accepting(&both(&[("new_session_agent", "claude")]));
    let held = held_saves(&daemon);
    let mut world = on_agents_with(daemon, &stored);
    let (codex, claude) = (
        "settings.default-agent.codex",
        "settings.default-agent.claude",
    );
    click(&mut world, codex);
    click(&mut world, claude);
    assert_eq!(writes(&world).len(), 1);

    world.daemon.emit(SettingsUpdatedMessage {
        changed_key: Some("new_session_agent".into()),
        settings: Some([("new_session_agent".to_owned(), "codex".to_owned())].into()),
        ..SettingsUpdatedMessage::new()
    });
    world.settle();
    assert!(node(&world, claude).is("checked"));

    let first = held.lock().unwrap().remove(0);
    answer(&mut world, &stored, &first);
    let second = held.lock().unwrap().remove(0);
    answer(&mut world, &stored, &second);
    assert_eq!(
        writes(&world),
        saved(&[
            ("new_session_agent", "codex"),
            ("new_session_agent", "claude"),
        ])
    );
    assert!(node(&world, claude).is("checked"));
    rest(&mut world);
}

#[test]
fn an_agent_being_checked_reads_checking_until_the_daemon_knows() {
    let mut world = on_agents(&[
        ("new_session_agent", "claude"),
        ("claude_available", "checking"),
        ("codex_available", "checking"),
    ]);
    let claude = "settings.default-agent.claude";
    assert!(said_with(&world, "settings.agent.claude.toggle").ends_with(", Checking"));
    assert!(!node(&world, claude).is("disabled"));
    assert!(node(&world, claude).is("checked"));
    assert_eq!(
        node(&world, claude).description.as_deref(),
        Some("Checking for Claude on this machine")
    );
    assert!(shows(&world, "settings.default-agent.claude.checking"));
    assert!(!shows(&world, "settings.agents.none"));

    world.daemon.emit(SettingsUpdatedMessage {
        settings: Some(
            [
                ("new_session_agent".to_owned(), "claude".to_owned()),
                ("claude_available".to_owned(), "true".to_owned()),
                ("codex_available".to_owned(), "true".to_owned()),
            ]
            .into(),
        ),
        ..SettingsUpdatedMessage::new()
    });
    world.settle();
    assert!(said_with(&world, "settings.agent.claude.toggle").ends_with(", Available"));
    assert!(
        node(&world, claude)
            .description
            .is_none_or(|d| d.is_empty())
    );
    assert!(!shows(&world, "settings.default-agent.claude.checking"));
    rest(&mut world);
}

#[test]
fn an_agent_missing_from_path_cannot_be_the_default_and_none_found_says_so() {
    let mut world = on_agents(&[("claude_available", "false"), ("codex_available", "false")]);
    let claude = node(&world, "settings.default-agent.claude");
    assert!(claude.is("disabled"));
    assert_eq!(
        claude.description.as_deref(),
        Some("Claude CLI not found in PATH")
    );
    assert_eq!(
        node(&world, "settings.agents.none").name.as_deref(),
        Some("No supported agent CLI found in PATH.")
    );
    rest(&mut world);
}

#[test]
fn a_missing_agent_says_why_under_the_pointer_and_takes_no_press_or_tab_stop() {
    let mut world = on_agents(&[
        ("new_session_agent", "codex"),
        ("claude_available", "false"),
        ("codex_available", "true"),
    ]);
    let claude = "settings.default-agent.claude";
    assert!(node(&world, claude).is("disabled"));
    assert!(!shows(&world, "settings.agents.none"));

    let [x, y, w, h] = world.screen().bounds(Target::key(claude)).unwrap();
    world.gesture(|app| app.move_to((x + w / 2.0, y + h / 2.0)));
    world.advance(std::time::Duration::from_secs(2));
    assert!(
        world
            .screen()
            .find(Target::role("tooltip").name("Claude CLI not found in PATH"))
            .is_some(),
        "the dimmed Claude option says why it is unavailable:\n{}",
        world.screen()
    );

    world.gesture(|app| app.click(Target::key(claude)));
    world.settle();
    assert!(
        writes(&world).is_empty(),
        "pressing the missing agent saved {:?}",
        writes(&world)
    );
    assert!(node(&world, "settings.default-agent.codex").is("checked"));
    assert_ne!(
        world.screen().focused().and_then(|n| n.key).as_deref(),
        Some(claude),
        "the press focused the missing agent"
    );

    let mut seen = Vec::new();
    loop {
        press(&mut world, Key::Tab);
        let focused = world.screen().focused().and_then(|n| n.key);
        assert_ne!(
            focused.as_deref(),
            Some(claude),
            "Tab stops on the missing agent"
        );
        if seen.contains(&focused) {
            break;
        }
        seen.push(focused);
    }
    rest(&mut world);
}

#[test]
fn a_long_plugin_name_stacks_the_default_agent_in_the_smallest_window_and_down_still_chooses() {
    let mut world = on_agents(&both(&[
        ("uiScale", "1.5"),
        ("new_session_agent", "claude"),
        ("snipe_available", "true"),
        (
            "snipe_display_name",
            "Engineering observability staging review agent with a long name",
        ),
    ]));
    let agents = ["claude", "codex", "snipe"].map(|a| format!("settings.default-agent.{a}"));
    let bounds = |world: &World| {
        agents
            .iter()
            .map(|key| world.screen().bounds(Target::key(key)).unwrap())
            .collect::<Vec<_>>()
    };
    let one_line = |b: &[[f32; 4]]| b.iter().all(|r| r[1] == b[0][1]);

    world.resize((800.0, 600.0));
    let stacked = bounds(&world);
    assert!(
        !one_line(&stacked)
            && stacked
                .iter()
                .all(|r| r[0] == stacked[0][0] && r[2] == stacked[0][2]),
        "the default agent stacks its options at one width: {stacked:?}"
    );
    let long = "Engineering observability staging review agent with a long name";
    assert_eq!(node(&world, &agents[2]).name.as_deref(), Some(long));
    let [x, y, w, h] = world.screen().bounds(Target::key(&agents[2])).unwrap();
    world.gesture(|app| app.move_to((x + w / 2.0, y + h / 2.0)));
    world.advance(std::time::Duration::from_secs(2));
    assert!(
        world
            .screen()
            .find(Target::role("tooltip").name(long))
            .is_some(),
        "the cut option says its whole name under the pointer:\n{}",
        world.screen()
    );

    tab_to(&mut world, &agents[0]);
    press(&mut world, Key::Down);
    assert!(node(&world, &agents[1]).is("checked"), "Down chooses Codex");

    world.resize((1600.0, 1000.0));
    assert!(
        one_line(&bounds(&world)),
        "a wide window puts them back on one line"
    );
    rest(&mut world);
}

const LONG_PLUGIN: &[(&str, &str)] = &[
    ("uiScale", "1.5"),
    ("claude_available", "true"),
    ("codex_available", "true"),
    ("new_session_agent", "claude"),
    ("snipe_available", "true"),
    (
        "snipe_display_name",
        "Engineering observability staging review agent with a long name",
    ),
];

const DEFAULT_AGENTS: [&str; 3] = [
    "settings.default-agent.claude",
    "settings.default-agent.codex",
    "settings.default-agent.snipe",
];

/// Whether the default agent's options stood stacked on one frame: each on its own line, all
/// at one left edge and one width.
fn stacked(rects: &[[f32; 4]]) -> bool {
    rects.windows(2).all(|w| w[1][1] > w[0][1])
        && rects
            .iter()
            .all(|r| r[0] == rects[0][0] && r[2] == rects[0][2])
}

fn one_line(rects: &[[f32; 4]]) -> bool {
    rects.iter().all(|r| r[1] == rects[0][1])
}

/// The default agent's options on each frame of `rec` that drew all of them.
fn options_by_frame(rec: &rune::motion_gate::Recording) -> Vec<Vec<[f32; 4]>> {
    let tracks: Vec<_> = DEFAULT_AGENTS
        .iter()
        .map(|key| {
            rec.tracks
                .iter()
                .find(|t| t.path.ends_with(&format!("/{key}")))
                .unwrap_or_else(|| panic!("{key} is never drawn"))
        })
        .collect();
    (0..rec.frames)
        .filter_map(|k| {
            tracks
                .iter()
                .map(|t| t.frames.iter().find(|(i, _)| *i == k).map(|(_, s)| s.rect))
                .collect()
        })
        .collect()
}

#[test]
fn a_cut_missing_agent_says_its_whole_name_and_why_it_is_off_in_one_tooltip() {
    let long = "Engineering observability staging review agent with a long name";
    let mut world = on_agents(&both(&[
        ("uiScale", "1.5"),
        ("new_session_agent", "claude"),
        ("snipe_available", "false"),
        ("snipe_display_name", long),
    ]));
    world.resize((800.0, 600.0));
    let snipe = "settings.default-agent.snipe";
    assert!(node(&world, snipe).is("disabled"));
    let [x, y, w, h] = world.screen().bounds(Target::key(snipe)).unwrap();
    world.gesture(|app| app.move_to((x + w / 2.0, y + h / 2.0)));
    world.advance(std::time::Duration::from_secs(2));
    let tips: Vec<String> = world
        .screen()
        .find(Target::role("tooltip"))
        .and_then(|n| n.name)
        .into_iter()
        .collect();
    let reason = format!("{long} CLI not found in PATH");
    assert!(
        tips.iter()
            .any(|t| t.contains(long) && t.contains(&reason) && t.find(long) < t.rfind(&reason)),
        "one tooltip says the whole name, then why it is off: {tips:?}"
    );
    rest(&mut world);
}

#[test]
fn the_default_agent_is_stacked_from_the_first_frame_the_smallest_window_draws_it() {
    let (daemon, stored) = accepting(LONG_PLUGIN);
    serve(&daemon, &stored, harnesses());
    let mut world = World::start_sized(daemon, (800.0, 600.0));
    open(&mut world);
    let rec = world.record(60.0, 0.5, |_, app| {
        app.click(Target::key("settings.nav.agents"))
    });
    let frames = options_by_frame(&rec);
    assert!(!frames.is_empty(), "the default agent is never drawn");
    for (k, rects) in frames.iter().enumerate() {
        assert!(
            stacked(rects),
            "frame {k} after opening the page draws the options as {rects:?}"
        );
    }
    rest(&mut world);
}

#[test]
fn the_default_agent_switches_once_at_one_width_and_holds_still_either_side() {
    let at_full_text: Vec<_> = LONG_PLUGIN
        .iter()
        .copied()
        .filter(|(k, _)| *k != "uiScale")
        .collect();
    let (daemon, stored) = accepting(&at_full_text);
    serve(&daemon, &stored, harnesses());
    let mut world = World::start_sized(daemon, (800.0, 600.0));
    open(&mut world);
    click(&mut world, "settings.nav.agents");
    let widths: Vec<f32> = (0..=120).map(|i| 800.0 + 4.0 * i as f32).collect();
    let sweep = |world: &mut World, widths: &mut dyn Iterator<Item = &f32>| {
        widths
            .map(|&w| {
                world.resize((w, 600.0));
                let rec = world.record(60.0, 0.25, |_, _| {});
                let frames = options_by_frame(&rec);
                assert!(
                    frames.windows(2).all(|f| f[0] == f[1]),
                    "at {w} wide the options move between frames: {frames:?}"
                );
                assert!(
                    rec.awake.iter().skip(1).all(|a| !a),
                    "at {w} wide the window keeps drawing: {:?}",
                    rec.awake
                );
                assert!(
                    world.runtime_is_at_rest(),
                    "at {w} wide the window is not at rest"
                );
                let rects = &frames[0];
                assert!(
                    stacked(rects) || one_line(rects),
                    "at {w} wide the options stand neither stacked nor on one line: {rects:?}"
                );
                (w, stacked(rects))
            })
            .collect::<Vec<_>>()
    };
    let widening = sweep(&mut world, &mut widths.iter());
    let mut narrowing = sweep(&mut world, &mut widths.iter().rev());
    narrowing.reverse();
    assert_eq!(
        widening, narrowing,
        "the options take the same form at each width, widening or narrowing"
    );
    let switches = widening.windows(2).filter(|p| p[0].1 != p[1].1).count();
    assert!(
        widening.first().is_some_and(|f| f.1)
            && widening.last().is_some_and(|l| !l.1)
            && switches == 1,
        "the options stack below one width and stand on one line above it: {widening:?}"
    );
    rest(&mut world);
}

#[test]
fn a_plugin_agent_shows_its_declared_name() {
    let mut world = on_agents(&[
        ("snipe_available", "true"),
        ("snipe_display_name", "Xirp Agent"),
    ]);
    assert!(groups(&world).contains(&"Xirp Agent".to_owned()));
    rest(&mut world);
}

#[test]
fn the_preferred_agent_comes_first_with_what_each_agent_supports() {
    let mut world = on_agents(&[
        ("new_session_agent", "plug"),
        ("plug_available", "true"),
        ("mistral_available", "true"),
        ("codex_cap_transcript_watcher", "true"),
        ("codex_cap_classifier", "false"),
        ("plug_cap_custom_cap", "true"),
    ]);
    assert_eq!(groups(&world), ["Plug", "Codex", "Claude", "Mistral"]);
    for agent in ["plug", "codex", "claude", "mistral"] {
        open_advanced(&mut world, agent);
    }
    assert_eq!(capabilities(&world, "plug"), ["custom cap Supported"]);
    assert_eq!(
        capabilities(&world, "codex"),
        ["Transcript watch Supported", "Classifier Unavailable"]
    );
    assert!(capabilities(&world, "claude").is_empty());
    assert!(capabilities(&world, "mistral").is_empty());
    rest(&mut world);
}

#[test]
fn only_codex_and_claude_offer_the_executable_attn_launches() {
    let mut world = on_agents(&[
        ("codex_executable", "/usr/local/bin/codex"),
        ("snipe_executable", "/opt/snipe"),
    ]);
    assert_eq!(groups(&world), ["Claude", "Codex"]);
    open_advanced(&mut world, "codex");
    assert_eq!(
        value(&world, &field("codex_executable")),
        "/usr/local/bin/codex"
    );
    rest(&mut world);
}

#[test]
fn each_default_model_save_settles_by_its_own_request_and_only_the_refused_one_shows() {
    let (daemon, stored) = accepting(BOTH);
    let held = held_saves(&daemon);
    let mut world = on_agents_with(daemon, &stored);
    open_group(&mut world, "codex");
    for (label, model) in [
        ("Claude default model", "sonnet"),
        ("Codex default model", "test-model"),
    ] {
        open_route(&mut world, label);
        enter_model(&mut world, model, None);
        if picker_open(&world) {
            press(&mut world, Key::Escape);
        }
    }
    let (first, second) = {
        let held = held.lock().unwrap();
        (held[0].clone(), held[1].clone())
    };
    assert_eq!(first["key"], "default_model_claude");
    assert_eq!(second["key"], "default_model_codex");

    world.daemon.emit(SettingsUpdatedMessage {
        changed_key: Some("default_model_claude".into()),
        settings: Some([("default_model_claude".to_owned(), "sonnet".to_owned())].into()),
        ..SettingsUpdatedMessage::new()
    });
    world.daemon.emit(SettingsUpdatedMessage {
        request_id: Some("another-client".into()),
        success: Some(true),
        ..SettingsUpdatedMessage::new()
    });
    world.settle();
    let status = || "settings.autosave";
    assert_eq!(node(&world, status()).name.as_deref(), Some("Saving…"));
    assert!(!shows(&world, "settings.refusal.default_model_codex"));

    world.daemon.emit(SettingsUpdatedMessage {
        request_id: second["request_id"].as_str().map(str::to_owned),
        success: Some(false),
        error: Some("Storage unavailable".into()),
        ..SettingsUpdatedMessage::new()
    });
    world.settle();
    assert_eq!(
        node(&world, "settings.refusal.default_model_codex")
            .name
            .as_deref(),
        Some("Storage unavailable")
    );
    assert_eq!(node(&world, status()).name.as_deref(), Some("Saving…"));

    world.daemon.emit(SettingsUpdatedMessage {
        request_id: first["request_id"].as_str().map(str::to_owned),
        success: Some(true),
        ..SettingsUpdatedMessage::new()
    });
    world.settle();
    assert_ne!(node(&world, status()).name.as_deref(), Some("Saving…"));
    let alerts = world
        .screen()
        .descendants(Target::key("settings.body"))
        .into_iter()
        .filter(|n| n.role == "alert")
        .count();
    assert_eq!(alerts, 1);
    rest(&mut world);
}

const RATES: &str = r#"{"input_usd_per_mtok":3,"output_usd_per_mtok":15,"cache_read_usd_per_mtok":0.3,"cache_write_5m_usd_per_mtok":3.75,"cache_write_1h_usd_per_mtok":6}"#;
const ADDED: &str = r#"{"input_usd_per_mtok":2,"output_usd_per_mtok":10,"cache_read_usd_per_mtok":0.2,"cache_write_5m_usd_per_mtok":2.5,"cache_write_1h_usd_per_mtok":4}"#;
const NEW_RATES: [(&str, &str); 5] = [
    ("input_usd_per_mtok", "2"),
    ("output_usd_per_mtok", "10"),
    ("cache_read_usd_per_mtok", "0.2"),
    ("cache_write_5m_usd_per_mtok", "2.5"),
    ("cache_write_1h_usd_per_mtok", "4"),
];
const NEW_MODEL: &str = "settings.price-new.model";

fn rate(model: &str, rate: &str) -> String {
    format!("settings.setting.session_cost.price.{model}#{rate}")
}

fn new_rate(rate: &str) -> String {
    format!("settings.price-new.{rate}")
}

fn on_pricing_with(daemon: Daemon, stored: &Settings) -> World {
    let mut world = on_agents_with(daemon, stored);
    click(&mut world, "settings.pricing");
    world
}

fn on_pricing(settings: &[(&str, &str)]) -> World {
    let (daemon, stored) = accepting(settings);
    on_pricing_with(daemon, &stored)
}

fn fill_new(world: &mut World, model: &str) {
    retype(world, NEW_MODEL, model);
    for (key, typed) in NEW_RATES {
        retype(world, &new_rate(key), typed);
    }
}

fn cards(world: &World) -> Vec<String> {
    world
        .screen()
        .descendants(Target::key("settings.prices"))
        .into_iter()
        .filter(|n| {
            n.role == "list-item"
                && n.key
                    .as_deref()
                    .is_some_and(|k| k.starts_with("settings.price."))
        })
        .filter_map(|n| n.name)
        .collect()
}

#[test]
fn codex_auto_review_bills_as_gpt_6_luna_until_another_model_is_picked() {
    let mut world = on_pricing(&[]);
    let trigger = "settings.price.billed-as-trigger";
    assert_eq!(value(&world, trigger), "gpt-6-luna (default)");
    click(&mut world, trigger);
    press_named(&mut world, "list-box-option", "gpt-6-sol");
    assert_eq!(
        writes(&world),
        saved(&[("session_cost.billed_as.codex-auto-review", "gpt-6-sol")])
    );
    assert_eq!(value(&world, trigger), "gpt-6-sol");
    rest(&mut world);
}

#[test]
fn saved_overrides_list_by_model_id_and_leave_out_removed_ones() {
    let a_model = RATES.replace(r#""input_usd_per_mtok":3"#, r#""input_usd_per_mtok":1"#);
    let mut world = on_pricing(&[
        ("session_cost.price.z-model", RATES),
        ("session_cost.price.a-model", &a_model),
        ("session_cost.price.removed-model", ""),
    ]);
    assert_eq!(cards(&world), ["a-model", "z-model"]);
    assert_eq!(value(&world, &rate("a-model", "input_usd_per_mtok")), "1");
    assert_eq!(
        value(&world, &rate("z-model", "cache_write_1h_usd_per_mtok")),
        "6"
    );
    rest(&mut world);
}

#[test]
fn a_complete_new_override_saves_under_its_trimmed_id_and_the_form_clears() {
    let mut world = on_pricing(&[]);
    fill_new(&mut world, "  claude-new-exact  ");
    click(&mut world, "settings.heading");
    assert_eq!(
        writes(&world),
        saved(&[("session_cost.price.claude-new-exact", ADDED)])
    );
    assert_eq!(value(&world, NEW_MODEL), "");
    assert_eq!(cards(&world), ["claude-new-exact"]);
    rest(&mut world);
}

#[test]
fn a_refused_new_override_stays_in_the_form_and_retry_sends_it_again() {
    let refusals = Arc::new(Mutex::new(1));
    let (daemon, stored) = keeping(&[], move |_, _| {
        let mut left = refusals.lock().unwrap();
        (*left > 0).then(|| {
            *left -= 1;
            "Disconnected".to_owned()
        })
    });
    let mut world = on_pricing_with(daemon, &stored);
    fill_new(&mut world, "new-model");
    click(&mut world, "settings.heading");
    let refusal = "settings.refusal.session_cost.price.new-model";
    assert_eq!(node(&world, refusal).name.as_deref(), Some("Disconnected"));
    assert_eq!(value(&world, NEW_MODEL), "new-model");
    assert_eq!(value(&world, &new_rate("input_usd_per_mtok")), "2");

    click(&mut world, "settings.retry.session_cost.price.new-model");
    assert_eq!(
        writes(&world),
        saved(&[
            ("session_cost.price.new-model", ADDED),
            ("session_cost.price.new-model", ADDED),
        ])
    );
    assert_eq!(value(&world, NEW_MODEL), "");
    assert!(!shows(&world, refusal));
    rest(&mut world);
}

#[test]
fn a_refused_new_override_sends_the_form_as_edited_when_submitted_again() {
    let refusals = Arc::new(Mutex::new(1));
    let (daemon, stored) = keeping(&[], move |_, _| {
        let mut left = refusals.lock().unwrap();
        (*left > 0).then(|| {
            *left -= 1;
            "Disconnected".to_owned()
        })
    });
    let mut world = on_pricing_with(daemon, &stored);
    fill_new(&mut world, "new-model");
    click(&mut world, "settings.heading");
    assert!(shows(
        &world,
        "settings.refusal.session_cost.price.new-model"
    ));

    retype(&mut world, &new_rate("input_usd_per_mtok"), "3");
    click(&mut world, "settings.heading");
    let edited = ADDED.replace(r#""input_usd_per_mtok":2"#, r#""input_usd_per_mtok":3"#);
    assert_eq!(
        writes(&world),
        saved(&[
            ("session_cost.price.new-model", ADDED),
            ("session_cost.price.new-model", &edited),
        ])
    );
    assert_eq!(value(&world, NEW_MODEL), "");
    rest(&mut world);
}

#[test]
fn an_incomplete_negative_or_duplicate_new_override_writes_nothing() {
    let mut world = on_pricing(&[("session_cost.price.existing-model", RATES)]);
    retype(&mut world, NEW_MODEL, "new-model");
    click(&mut world, "settings.heading");
    assert_eq!(
        node(&world, "settings.price-new.incomplete")
            .name
            .as_deref(),
        Some("Complete every rate with a non-negative number.")
    );

    for (key, typed) in NEW_RATES {
        let typed = if key == "cache_read_usd_per_mtok" {
            "-1"
        } else {
            typed
        };
        retype(&mut world, &new_rate(key), typed);
    }
    click(&mut world, "settings.heading");
    assert!(shows(&world, "settings.price-new.incomplete"));

    retype(&mut world, NEW_MODEL, "existing-model");
    retype(&mut world, &new_rate("cache_read_usd_per_mtok"), "0.2");
    click(&mut world, "settings.heading");
    assert_eq!(
        node(&world, "settings.price-new.exists").name.as_deref(),
        Some("That model already has an override above.")
    );
    assert!(writes(&world).is_empty());
    rest(&mut world);
}

#[test]
fn one_rate_of_a_saved_override_saves_alone_and_remove_clears_the_override() {
    let mut world = on_pricing(&[("session_cost.price.claude-priced", RATES)]);
    retype(
        &mut world,
        &rate("claude-priced", "cache_read_usd_per_mtok"),
        "0.25",
    );
    click(&mut world, "settings.heading");
    click(&mut world, "settings.price.claude-priced.remove");
    assert_eq!(
        writes(&world),
        saved(&[
            (
                "session_cost.price.claude-priced",
                &RATES.replace("0.3", "0.25"),
            ),
            ("session_cost.price.claude-priced", ""),
        ])
    );
    assert!(cards(&world).is_empty());
    rest(&mut world);
}

#[test]
fn a_saved_override_with_a_missing_rate_or_an_unknown_field_says_it_is_invalid_and_can_be_removed()
{
    for raw in [
        r#"{"input_usd_per_mtok":3}"#.to_owned(),
        RATES.replace('}', r#","currency":"USD"}"#),
    ] {
        let mut world = on_pricing(&[("session_cost.price.broken-model", &raw)]);
        assert!(
            node(&world, "settings.price.broken-model.invalid")
                .name
                .is_some_and(|n| n.contains("saved override is invalid")),
            "{raw}"
        );
        click(&mut world, "settings.price.broken-model.remove");
        assert_eq!(
            writes(&world),
            saved(&[("session_cost.price.broken-model", "")])
        );
        rest(&mut world);
    }
}

#[test]
fn a_choice_keeps_to_its_own_width_and_the_default_agent_leaves_its_description_the_row() {
    let mut world = on_agents(&both(&[("garden.tender_resume", "any")]));
    let block = world.rect("settings.block.seeds-on-standby");
    let choice = world.rect("settings.choice.garden.tender_resume");
    assert!(
        choice[2] < block[2] / 2.0,
        "the resume choice spans {} of its block's {}",
        choice[2],
        block[2]
    );
    let agent = world.rect("settings.default-agent");
    let body = world.rect("settings.body");
    assert!(
        agent[2] < body[2] / 3.0,
        "the default agent choice spans {} of the page's {}",
        agent[2],
        body[2]
    );
}

#[test]
fn a_long_refusal_keeps_the_refused_executable_in_view_in_the_smallest_window() {
    let path = "/home/someone/projects/engineering/platform/worktrees/upgrade-agent-tooling/custom-home/.local/share/claude/versions/2.1.45/bin/claude";
    let settings = both(&[("uiScale", "1.5")]);
    let (daemon, stored) = keeping(&settings, |key, value| {
        (key == "claude_executable").then(|| {
            format!("exec: \"{value}\": stat {value}: no such file or directory; claude_executable must name an executable file: {value}")
        })
    });
    let mut world = on_agents_with(daemon, &stored);
    world.resize((800.0, 600.0));
    open_advanced(&mut world, "claude");
    let input = field("claude_executable");
    retype(&mut world, &input, path);
    press(&mut world, Key::Enter);
    assert!(
        world
            .screen()
            .find(Target::key("settings.refusal.claude_executable"))
            .is_some(),
        "the refusal shows"
    );
    let body = world.rect("settings.body");
    let focused = world.rect(&input);
    assert!(
        focused[1] >= body[1] && focused[1] + focused[3] <= body[1] + body[3],
        "the refused field at {focused:?} stays inside the page's view {body:?}"
    );
    rest(&mut world);
}

#[test]
fn an_unavailable_switch_is_still_heard_with_its_name_and_value_and_takes_no_tab_or_press() {
    let mut world = on_agents(&[]);
    let host = toggle("pty_shared_host_enabled");
    let switch = node(&world, &host);
    assert_eq!(switch.role, "switch");
    assert_eq!(switch.name.as_deref(), Some("Shared terminal host"));
    assert!(switch.is("checked"), "the switch says it is on");
    assert!(switch.is("disabled"), "the switch says it is unavailable");

    let mut seen = Vec::new();
    loop {
        press(&mut world, Key::Tab);
        let focused = world.screen().focused().and_then(|n| n.key);
        assert_ne!(
            focused.as_deref(),
            Some(host.as_str()),
            "Tab stops on the unavailable switch"
        );
        if seen.contains(&focused) {
            break;
        }
        seen.push(focused);
    }

    let _refused = world.automate("press", json!({"target": {"key": host}}));
    world.settle();
    assert!(
        writes(&world).is_empty(),
        "pressing the unavailable switch saved {:?}",
        writes(&world)
    );
    assert!(node(&world, &host).is("checked"));
}

#[test]
fn a_refused_rate_shows_its_reason_and_retry_whole_in_the_smallest_window_at_the_largest_text() {
    const REASON: &str = "cannot create directory /Users/someone/プロジェクト/社内プラットフォーム/InternationalEngineeringObservability/ワークツリー: permission denied";
    for typed in [
        "input_usd_per_mtok",
        "output_usd_per_mtok",
        "cache_read_usd_per_mtok",
        "cache_write_5m_usd_per_mtok",
        "cache_write_1h_usd_per_mtok",
    ] {
        let (daemon, stored) = keeping(
            &[
                ("uiScale", "1.5"),
                ("session_cost.price.claude-priced", RATES),
            ],
            |key, _| (key == "session_cost.price.claude-priced").then(|| REASON.to_owned()),
        );
        let mut world = on_pricing_with(daemon, &stored);
        world.resize((800.0, 600.0));
        retype(&mut world, &rate("claude-priced", typed), "7");
        press(&mut world, Key::Enter);
        let body = world.rect("settings.body");
        for part in [
            "settings.refusal.session_cost.price.claude-priced",
            "settings.retry.session_cost.price.claude-priced",
        ] {
            let shown = world.rect(part);
            assert!(
                shown[1] >= body[1] && shown[1] + shown[3] <= body[1] + body[3],
                "{typed}: {part} at {shown:?} lies whole inside the page's view {body:?}"
            );
        }
        rest(&mut world);
    }
}

const LONG_MODEL: &str =
    "anthropic.claude-opus-5-5-20261101-v1:0-with-extended-thinking-eu-north-1";

/// Agents in the smallest window at the largest text, Claude's route picker open from its
/// default model.
fn smallest_picker() -> World {
    let mut world = on_agents(&both(&[("uiScale", "1.5")]));
    world.resize((800.0, 600.0));
    world.settle();
    let chip = "settings.route.default-claude";
    world.gesture(|app| app.update(|_, tx| tx.scroll_center_in_instant("settings.body", chip)));
    world.settle();
    open_route(&mut world, "Claude default model");
    world
}

fn inside(inner: [f64; 4], outer: [f64; 4]) -> bool {
    inner[1] >= outer[1] - 0.5
        && inner[1] + inner[3] <= outer[1] + outer[3] + 0.5
        && inner[0] >= outer[0] - 0.5
        && inner[0] + inner[2] <= outer[0] + outer[2] + 0.5
}

#[test]
fn a_route_picker_with_an_exact_id_typed_keeps_its_effort_inside_it_in_the_smallest_window() {
    let mut world = smallest_picker();
    click(&mut world, "settings.route-picker.filter");
    world.gesture(|app| app.type_text(LONG_MODEL));
    world.settle();
    let effort = "settings.route-picker.effort-control";
    world.gesture(|app| app.update(|_, tx| tx.scroll_into_view(effort)));
    world.settle();
    let picker = world.rect("settings.route-picker");
    let at = world.rect(effort);
    assert!(
        inside(at, picker),
        "the effort control, revealed, at {at:?} is not whole inside the picker at {picker:?}"
    );
    rest(&mut world);
}

fn one_whole_model(world: &mut World) -> bool {
    let list = world.rect("settings.route-picker.models");
    let picker = world.rect("settings.route-picker");
    let options: Vec<String> = world
        .screen()
        .descendants(Target::key("settings.route-picker.models"))
        .into_iter()
        .filter(|n| n.role == "list-box-option")
        .filter_map(|n| n.key)
        .collect();
    options.iter().any(|key| {
        let at = world.rect(key);
        inside(at, list) && inside(at, picker)
    })
}

#[test]
fn a_route_picker_shows_at_least_one_whole_model_in_the_smallest_window() {
    let mut world = smallest_picker();
    assert!(
        one_whole_model(&mut world),
        "no model is whole as the picker opens"
    );
    click(&mut world, "settings.route-picker.filter");
    world.gesture(|app| app.type_text("opus"));
    world.settle();
    assert!(
        one_whole_model(&mut world),
        "no model is whole under its tier with a filter typed"
    );
    rest(&mut world);
}

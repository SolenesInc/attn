//! Settings → Agents and models: the defaults new sessions start with, which mail resumes an
//! agent on standby, each agent's default model, executable, context cap and capabilities, and
//! the model pricing overrides.

mod pricing;

use std::collections::{BTreeMap, BTreeSet};

use attn_ui::Tokens;
use attn_ui::blocks::{
    Block, Disclosure, Facts, Inline, Notice, Prose, Section, SettingRow, Stack,
};
use attn_ui::system::{
    Beside, Control, DotShape, DotSize, Segment, Segmented, Tone, Type, dot, switch, tag, words,
};
use rune::{El, Tx, text};

use super::autosave::{self, Codec, PLAIN, TRIMMED};
use super::controls::{SettingField, feature, feature_note, feature_switch, segmented};
use super::harnesses::{self, Availability};
use super::route::{self, Route, RouteHarness, Rules, Target, TierInfo};
use crate::{State, setting};

const DEFAULT_AGENT: &str = "new_session_agent";
const AUTO_APPROVE: &str = "auto_approve_enabled";
const ASKS: &str = "asks_enabled";
const DIRECT_LAUNCH: &str = "direct_agent_launch_enabled";
const SHARED_HOST: &str = "pty_shared_host_enabled";
const TENDER_RESUME: &str = "garden.tender_resume";
const OWN_PR_COMMENTS: &str = "pull_requests.own_comments";

const CAPABILITY_ORDER: &[(&str, &str)] = &[
    ("yolo", "YOLO"),
    ("resume", "Resume"),
    ("hooks", "Hooks"),
    ("transcript", "Transcript"),
    ("transcript_watcher", "Transcript watch"),
    ("classifier", "Classifier"),
    ("headless_task", "Headless task"),
];

const RESUME_CHOICES: &[(&str, &str)] = &[
    ("any", "Any mail"),
    ("watches", "PRs and reminders"),
    ("off", "Never"),
];

const STANDBY_MAIL: &[(&str, &[&str])] = &[
    ("PR #123 got approved", &["any", "watches"]),
    ("Its reminder came up", &["any", "watches"]),
    ("Another agent messaged the seed", &["any"]),
    ("You answered its ask", &["any"]),
];

/// Which agent groups and Advanced parts the user opened or closed.
#[derive(Default)]
pub(crate) struct Agents {
    shown: BTreeMap<String, bool>,
    advanced: BTreeSet<String>,
    pricing: pricing::Pricing,
}

fn model_key(agent: &str) -> String {
    format!("default_model_{agent}")
}

fn effort_key(agent: &str) -> String {
    format!("default_effort_{agent}")
}

fn cap_key(agent: &str) -> String {
    format!("default_context_window_cap_{agent}")
}

fn executable_key(agent: &str) -> String {
    format!("{agent}_executable")
}

const DEFAULT_AGENT_CODEC: Codec = Codec {
    actual: |settings, _| harnesses::resolved_default(settings),
    serialize: |_, draft| Ok(draft.to_owned()),
};

fn ordered(settings: &BTreeMap<String, String>) -> Vec<String> {
    let availability = harnesses::availability(settings);
    let preferred = harnesses::resolved_default(settings);
    harnesses::ordered(&availability, Some(&preferred))
}

/// The built-in agents whose model, effort and context cap the page sets: those this machine
/// has, and any with one of them stored.
fn override_agents(settings: &BTreeMap<String, String>) -> Vec<String> {
    let availability = harnesses::availability(settings);
    let mut list: Vec<String> = ordered(settings)
        .into_iter()
        .filter(|a| harnesses::builtin(a) && harnesses::selectable(&availability, a))
        .collect();
    for agent in ["claude", "codex"] {
        let stored = [model_key(agent), effort_key(agent), cap_key(agent)]
            .iter()
            .any(|k| settings.get(k).is_some_and(|v| !v.trim().is_empty()));
        if stored && !list.iter().any(|a| a == agent) {
            list.push(agent.to_owned());
        }
    }
    list
}

fn executable_agents(settings: &BTreeMap<String, String>) -> Vec<String> {
    ordered(settings)
        .into_iter()
        .filter(|a| harnesses::builtin(a))
        .collect()
}

/// The page's fields, with how each reads and saves.
pub(super) fn fields(state: &State) -> Vec<(String, Codec)> {
    let settings = &state.facts.settings;
    let mut fields = vec![(DEFAULT_AGENT.to_owned(), DEFAULT_AGENT_CODEC)];
    for agent in override_agents(settings) {
        fields.push((model_key(&agent), TRIMMED));
        fields.push((effort_key(&agent), PLAIN));
        fields.push((cap_key(&agent), TRIMMED));
    }
    for agent in executable_agents(settings) {
        fields.push((executable_key(&agent), PLAIN));
    }
    fields.extend(pricing::fields(state));
    fields
}

/// Follows the drafts the pricing fields show.
pub(super) fn follow(state: &mut State) -> bool {
    pricing::follow(state)
}

pub(super) fn overlay(state: &State, t: &Tokens, layers: &mut Vec<El>) {
    if state.window.settings.section == super::Section::Agents {
        pricing::overlay(state, t, layers);
    }
}

const ROUTE_PREFIX: &str = "default-";

fn target(agent: &str) -> Target {
    Target {
        id: format!("{ROUTE_PREFIX}{agent}"),
        read: read_route,
        write: write_route,
    }
}

fn rules(state: &State, agent: &str) -> Rules {
    Rules {
        harnesses: harnesses::listed(state)
            .iter()
            .filter(|h| h.id == agent)
            .map(RouteHarness::from)
            .collect(),
        fixed_harness: true,
        ..Rules::default()
    }
}

fn read_route(state: &State, id: &str) -> Option<(Route, Rules)> {
    let agent = id.strip_prefix(ROUTE_PREFIX)?;
    let drafts = &state.window.settings.autosave;
    Some((
        Route::stored(
            agent,
            drafts.draft(&model_key(agent)),
            drafts.draft(&effort_key(agent)),
        ),
        rules(state, agent),
    ))
}

fn write_route(state: &mut State, id: &str, route: Route, tx: &mut Tx) {
    let Some(agent) = id.strip_prefix(ROUTE_PREFIX) else {
        return;
    };
    let (model, effort) = (model_key(agent), effort_key(agent));
    let stored = route.stored_model();
    if stored != state.window.settings.autosave.draft(&model) {
        autosave::apply(state, &model, &stored, tx);
    }
    if route.effort != state.window.settings.autosave.draft(&effort) {
        autosave::apply(state, &effort, &route.effort, tx);
    }
}

/// The harnesses the page's default-model fields show, whose models it discovers.
pub(super) fn wanted(state: &State) -> Vec<(String, bool)> {
    override_agents(&state.facts.settings)
        .iter()
        .filter_map(|agent| route::wanted(state, &target(agent)))
        .collect()
}

fn setting_toggle(
    key: &'static str,
    name: &str,
    disabled: bool,
    t: &Tokens,
    state: &State,
) -> Control {
    let on = setting::on(&state.facts.settings, key);
    switch(
        format!("settings.toggle.{key}"),
        name,
        on,
        Beside::Nothing,
        disabled,
        t,
        move |state: &mut State, tx: &mut Tx| {
            autosave::apply(state, key, if on { "false" } else { "true" }, tx)
        },
    )
}

fn drafts_error(state: &State, key: &str) -> Option<String> {
    state.window.settings.autosave.field(key)?.error.clone()
}

fn refused(state: &State, key: &str) -> bool {
    drafts_error(state, key).is_some()
}

fn default_agent(state: &State, t: &Tokens) -> Control {
    let settings = &state.facts.settings;
    let availability = harnesses::availability(settings);
    let chosen = state.window.settings.autosave.draft(DEFAULT_AGENT);
    let options = ordered(settings).into_iter().map(|agent| {
        let label = crate::picker::agents::named(settings, &agent);
        let status = harnesses::of(&availability, &agent);
        let unavailable = status == Availability::Unavailable;
        let mut option = Segment::new(format!("settings.default-agent.{agent}"), &label)
            .chosen(chosen == agent)
            .disabled(unavailable);
        if status == Availability::Checking {
            option = option.trailing(
                text("…")
                    .key(format!("settings.default-agent.{agent}.checking"))
                    .role("img")
                    .label("checking")
                    .color(t.text_tertiary),
            );
        }
        let help = match status {
            Availability::Unavailable => Some(format!("{label} CLI not found in PATH")),
            Availability::Checking => Some(format!("Checking for {label} on this machine")),
            Availability::Available => None,
        };
        if let Some(help) = help {
            option = option.help(help);
        }
        let picked = agent.clone();
        option.on_press_tx(move |state: &mut State, tx: &mut Tx| {
            let availability = harnesses::availability(&state.facts.settings);
            if harnesses::selectable(&availability, &picked) {
                autosave::apply(state, DEFAULT_AGENT, &picked, tx);
            }
        })
    });
    Segmented::new("settings.default-agent", "Default session agent")
        .options(options)
        .build(t)
}

fn session_defaults(state: &State, t: &Tokens) -> Block {
    let settings = &state.facts.settings;
    let availability = harnesses::availability(settings);
    let any = ordered(settings)
        .iter()
        .any(|a| harnesses::selectable(&availability, a));
    let migrating = settings
        .get("pty_backend_mode")
        .is_some_and(|m| m.to_lowercase() == "migrating");
    let mut section = Section::new("settings.block.session-defaults", "Session defaults");
    if !any {
        section = section.push(
            Notice::warn(
                "settings.agents.none",
                "No supported agent CLI found in PATH.",
            )
            .build(t),
        );
    }
    let asks = feature(ASKS);
    section
        .push(
            SettingRow::new(
                "settings.row.default-agent",
                "Default agent",
                "Used for new sessions and opening PRs.",
                default_agent(state, t),
            )
            .build(t),
        )
        .push(
            SettingRow::new(
                "settings.row.auto-approve",
                "Auto-approve",
                "Let Claude and Codex review approval requests automatically. On by default for new sessions; YOLO sessions already bypass approval.",
                setting_toggle(AUTO_APPROVE, "Auto-approve", false, t, state),
            )
            .build(t),
        )
        .push(
            SettingRow::new("settings.row.asks",asks.name, asks.summary, feature_switch(state, ASKS, Beside::Nothing, t))
                .note(feature_note(state, ASKS, t))
                .build(t),
        )
        .push(
            SettingRow::new(
                "settings.row.direct-launch",
                "Direct agent launches",
                "Start agents without an attn process beside each one, which uses less memory. On by default; applies to new and reloaded agents.",
                setting_toggle(DIRECT_LAUNCH, "Direct agent launches", false, t, state),
            )
            .build(t),
        )
        .push(
            SettingRow::new(
                "settings.row.shared-host",
                "Shared terminal host",
                "Run terminals in one shared process, which uses less memory, and stream output only to the terminals the window shows. On by default; applies to new and reloaded terminals.",
                setting_toggle(SHARED_HOST, "Shared terminal host", !migrating, t, state),
            )
            .build(t),
        )
        .build(t)
}

fn resume_choice(settings: &BTreeMap<String, String>) -> &'static str {
    match settings.get(TENDER_RESUME).map(String::as_str) {
        Some("watches") => "watches",
        Some("off") => "off",
        _ => "any",
    }
}

/// The verdicts' column: "Resumes" measures 68 as a tag.
const VERDICT_W: f32 = 72.0;

fn seeds_on_standby(state: &State, t: &Tokens) -> Block {
    let settings = &state.facts.settings;
    let choice = resume_choice(settings);
    let ring = settings.get(OWN_PR_COMMENTS).is_some_and(|v| v == "ring");
    let cases = STANDBY_MAIL.iter().map(|(case, resumes)| {
        let resumes = resumes.contains(&choice);
        let (verdict, tone) = if resumes {
            ("Resumes", Tone::Warn)
        } else {
            ("Waits", Tone::Neutral)
        };
        Inline::loose()
            .role("listitem")
            .label(format!("{verdict} {case}"))
            .child(
                rune::row()
                    .w(VERDICT_W)
                    .child(El::from(tag(
                        format!("settings.standby.{verdict}"),
                        verdict,
                        tone,
                        t,
                    )))
                    .a11y_hidden(),
            )
            .child(words(*case, Type::Body, t.text_primary).a11y_hidden())
    });
    let own_comments =
        "Enable if you want to give feedback to agents by writing comments on the PR";
    let ring_switch = El::from(switch(
        "settings.toggle.pull_requests.own_comments",
        "Ring on my own PR comments",
        ring,
        Beside::Nothing,
        false,
        t,
        move |state: &mut State, tx: &mut Tx| {
            autosave::apply(
                state,
                OWN_PR_COMMENTS,
                if ring { "quiet" } else { "ring" },
                tx,
            )
        },
    ))
    .description(own_comments);
    Section::new("settings.block.seeds-on-standby", "Seeds on standby")
        .description("Resume a closed agent when mail reaches its seed.")
        .push(Block::control(
            "settings.standby.resume",
            segmented(
            TENDER_RESUME,
            "Resume for",
            RESUME_CHOICES,
            choice,
            t,
            |state, value, tx| autosave::apply(state, TENDER_RESUME, value, tx),
        )))
        .push(Block::custom(
            "settings.standby.cases.block",
            Stack::tight()
                .key("settings.standby.cases")
                .role("list")
                .label("A closed agent on standby")
                .children(cases),
        ))
        .push(
            Prose::hint(
                "settings.standby.hint",
                "attn always starts a session for a crew member who tends the seed, whatever you pick.",
            )
            .build(t),
        )
        .push(SettingRow::new("settings.row.own-pr-comments","Ring on my own PR comments", own_comments, ring_switch).build(t))
        .build(t)
}

/// What an agent's head shows after its name: its route as the app draws a route, then
/// whether it is there, quiet while it is and marked when it is not.
fn head_summary(state: &State, agent: &str, status: Availability, t: &Tokens) -> El {
    let settings = &state.facts.settings;
    let value = Route::stored(
        agent,
        settings.get(&model_key(agent)).map_or("", String::as_str),
        settings.get(&effort_key(agent)).map_or("", String::as_str),
    );
    let word = availability_word(status);
    let key = format!("settings.agent.{agent}.availability");
    let availability = match status {
        Availability::Available => {
            words(format!("· {word}"), Type::Small, t.text_secondary).key(key)
        }
        Availability::Unavailable => El::from(tag(key, word, Tone::Warn, t)),
        Availability::Checking => Inline::tight()
            .key(key)
            .child(dot(t.text_dimmed, DotShape::Hollow, DotSize::Small))
            .child(words(word, Type::Small, t.text_secondary)),
    };
    Inline::tight()
        .child(route::badge(state, t, &value))
        .child(availability)
}

fn availability_word(status: Availability) -> &'static str {
    match status {
        Availability::Available => "Available",
        Availability::Unavailable => "Unavailable",
        Availability::Checking => "Checking",
    }
}

/// The agent's stored route as the badge in its group's head shows it.
fn route_badge(state: &State, agent: &str) -> String {
    let settings = &state.facts.settings;
    let value = Route::stored(
        agent,
        settings.get(&model_key(agent)).map_or("", String::as_str),
        settings.get(&effort_key(agent)).map_or("", String::as_str),
    );
    let rules = rules(state, agent);
    let view = route::view(state, &value, &rules);
    let mark = TierInfo::mark(view.tier.as_ref(), false);
    [
        view.harness_name.as_str(),
        view.model_label.as_str(),
        mark.as_str(),
        value.effort.as_str(),
    ]
    .into_iter()
    .filter(|part| !part.is_empty())
    .collect::<Vec<_>>()
    .join(" ")
}

fn capability_label(capability: &str) -> String {
    CAPABILITY_ORDER
        .iter()
        .find(|(key, _)| *key == capability)
        .map_or_else(
            || capability.replace(['_', '-'], " "),
            |(_, l)| (*l).to_owned(),
        )
}

fn capabilities(agent: &str, caps: Option<&BTreeMap<String, bool>>, t: &Tokens) -> Block {
    let empty = BTreeMap::new();
    let caps = caps.unwrap_or(&empty);
    let known = CAPABILITY_ORDER
        .iter()
        .map(|(key, _)| *key)
        .filter(|key| caps.contains_key(*key));
    let other = caps
        .keys()
        .map(String::as_str)
        .filter(|key| !CAPABILITY_ORDER.iter().any(|(k, _)| k == key));
    known
        .chain(other)
        .fold(
            Facts::new(format!("settings.agent.{agent}.capabilities")).list("Capabilities"),
            |facts, cap| {
                let supported = caps[cap];
                facts
                    .fact(
                        format!("settings.agent.{agent}.capability.{cap}"),
                        capability_label(cap),
                        if supported {
                            "Supported"
                        } else {
                            "Unavailable"
                        },
                    )
                    .tone((!supported).then_some(Tone::Neutral))
            },
        )
        .build(t)
}

fn advanced(state: &State, agent: &str, overrides: bool, open: bool, t: &Tokens) -> Block {
    let toggled = agent.to_owned();
    let mut part = Disclosure::new(
        format!("settings.agent.{agent}.advanced"),
        "Advanced",
        open,
        move |state: &mut State, _: &mut Tx| {
            let advanced = &mut state.window.settings.agents.advanced;
            if !advanced.remove(&toggled) {
                advanced.insert(toggled.clone());
            }
        },
    );
    if harnesses::builtin(agent) {
        part = part.push(
            SettingField::new(executable_key(agent), "Executable")
                .placeholder(agent)
                .hint("Empty uses the executable on PATH.")
                .build(state, t),
        );
    }
    if overrides {
        part = part.push(
            SettingField::new(cap_key(agent), "Context cap (tokens)")
                .placeholder("Agent default")
                .step(1000.0)
                .hint("Empty uses the agent's own compaction behavior.")
                .build(state, t),
        );
    }
    let all = harnesses::capabilities(&state.facts.settings);
    part.push(capabilities(agent, all.get(agent), t)).build(t)
}

fn agent_group(state: &State, agent: &str, t: &Tokens) -> Block {
    let settings = &state.facts.settings;
    let page = &state.window.settings.agents;
    let label = crate::picker::agents::named(settings, agent);
    let status = harnesses::of(&harnesses::availability(settings), agent);
    let overrides = override_agents(settings).iter().any(|a| a == agent);
    let executable = harnesses::builtin(agent);
    let refusal_inside = (executable && refused(state, &executable_key(agent)))
        || (overrides && refused(state, &cap_key(agent)));
    let open = refusal_inside
        || page
            .shown
            .get(agent)
            .copied()
            .unwrap_or_else(|| harnesses::resolved_default(settings) == agent);
    let toggled = agent.to_owned();
    let mut group = Disclosure::new(
        format!("settings.agent.{agent}.toggle"),
        label.clone(),
        open,
        move |state: &mut State, _: &mut Tx| {
            let was = state
                .window
                .settings
                .agents
                .shown
                .get(&toggled)
                .copied()
                .unwrap_or_else(|| harnesses::resolved_default(&state.facts.settings) == toggled);
            state
                .window
                .settings
                .agents
                .shown
                .insert(toggled.clone(), !was);
        },
    )
    .beside(
        head_summary(state, agent, status, t),
        format!(
            "{}, {}",
            route_badge(state, agent),
            availability_word(status)
        ),
    );
    group = if overrides {
        group.push(Block::custom(
            format!("settings.agent.{agent}.route.block"),
            route::field_chip(state, t, target(agent), &format!("{label} default model")),
        ))
    } else {
        group.push(
            Prose::hint(
                format!("settings.agent.{agent}.follows"),
                "Model and effort follow this agent's own configuration.",
            )
            .build(t),
        )
    };
    let advanced_open = refusal_inside || page.advanced.contains(agent);
    group
        .push(advanced(state, agent, overrides, advanced_open, t))
        .build(t)
}

pub(super) fn page(state: &State, t: &Tokens) -> Vec<Block> {
    let agents = ordered(&state.facts.settings);
    let groups = agents.iter().fold(
        Section::new("settings.block.agents", "Agents"),
        |section, agent| section.push(agent_group(state, agent, t)),
    );
    vec![
        session_defaults(state, t),
        seeds_on_standby(state, t),
        groups.build(t),
        Block::custom("settings.pricing.block", pricing::page(state, t)),
    ]
}

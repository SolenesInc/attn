//! Settings → Agents and models: the defaults new sessions start with, which mail resumes an
//! agent on standby, each agent's default model, executable, context cap and capabilities, and
//! the model pricing overrides.

mod pricing;

use std::collections::{BTreeMap, BTreeSet};

use attn_ui::Tokens;
use rune::tok::{space, type_};
use rune::{El, Tx, col, row, text};
use rune_kit::{Button, ButtonVariant, TextInput};

use super::autosave::{self, Codec, PLAIN, TRIMMED};
use super::harnesses::{self, Availability};
use super::pages::{CAPTION, S4, block, feature_lines, feature_switch, refusal, row_card};
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

/// Follows the drafts the pricing fields show, and adds a new override once its form leaves
/// focus complete.
pub(super) fn follow(state: &mut State) -> bool {
    pricing::follow(state)
}

pub(super) fn poll(state: &mut State, tx: &mut Tx) {
    pricing::poll(state, tx);
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

/// A button that reads Enable or Disable and is announced as the switch `name`.
fn toggle(
    key: &str,
    name: &str,
    on: bool,
    disabled: bool,
    t: &Tokens,
    press: impl Fn(&mut State, &mut Tx) + 'static,
) -> El {
    Button::new(if on { "Disable" } else { "Enable" })
        .disabled(disabled)
        .on_press_tx(press)
        .build(attn_ui::kit(t))
        .key(key.to_owned())
        .role("switch")
        .label(name.to_owned())
        .checked(on)
}

fn setting_toggle(key: &'static str, name: &str, disabled: bool, t: &Tokens, state: &State) -> El {
    let on = setting::on(&state.facts.settings, key);
    toggle(
        &format!("settings.toggle.{key}"),
        name,
        on,
        disabled,
        t,
        move |state, tx| autosave::apply(state, key, if on { "false" } else { "true" }, tx),
    )
}

fn warning(key: &str, said: &str, t: &Tokens) -> El {
    text(said.to_owned())
        .key(key.to_owned())
        .label(said.to_owned())
        .font(CAPTION)
        .color(t.warning)
        .wrap()
}

fn hint(said: &str, t: &Tokens) -> El {
    text(said.to_owned())
        .font(CAPTION)
        .color(t.text_tertiary)
        .wrap()
}

/// A field that edits the setting `key`, saved on Enter and when the focus leaves it, with its
/// label above, its hint below and its refusal beside it.
fn field(
    state: &State,
    key: String,
    label: &str,
    placeholder: &str,
    hint_text: &str,
    t: &Tokens,
) -> El {
    let drafts = &state.window.settings.autosave;
    let field = drafts.field(&key);
    let empty = rune::TextEdit::default();
    let (edit, submit) = (key.clone(), key.clone());
    let input = TextInput::new(field.map_or(&empty, |f| &f.draft))
        .key(super::field_key(&key))
        .label(label.to_owned())
        .placeholder(placeholder.to_owned())
        .invalid(field.is_some_and(|f| f.error.is_some()))
        .on_edit(move |state: &mut State| state.window.settings.autosave.draft_mut(&edit))
        .on_submit(move |state: &mut State, tx: &mut Tx| autosave::commit(state, &submit, tx))
        .width(Some(220.0))
        .build(attn_ui::kit(t));
    let saved = drafts.shows_saved_mark(&key).then(|| {
        text("Saved")
            .key(format!("settings.saved.{key}"))
            .role("status")
            .label("Saved")
            .font(CAPTION)
            .color(t.success)
    });
    let refusal_shown = refusal(state, &key, t);
    col()
        .gap(space::S1)
        .child(
            text(label.to_owned())
                .font(type_::LABEL)
                .color(t.text_primary),
        )
        .child(
            row()
                .gap(space::S2)
                .items_center()
                .child(input)
                .children(saved),
        )
        .child(hint(hint_text, t))
        .children(refusal_shown)
}

fn drafts_error(state: &State, key: &str) -> Option<String> {
    state.window.settings.autosave.field(key)?.error.clone()
}

fn refused(state: &State, key: &str) -> bool {
    drafts_error(state, key).is_some()
}

fn default_agent(state: &State, t: &Tokens) -> El {
    let settings = &state.facts.settings;
    let availability = harnesses::availability(settings);
    let chosen = state.window.settings.autosave.draft(DEFAULT_AGENT);
    let k = attn_ui::kit(t);
    let options = ordered(settings).into_iter().map(|agent| {
        let label = harnesses::label(settings, &agent);
        let status = harnesses::of(&availability, &agent);
        let active = chosen == agent;
        let mut button = Button::new(label.clone())
            .variant(if active {
                ButtonVariant::Neutral
            } else {
                ButtonVariant::GhostMuted
            })
            .disabled(status == Availability::Unavailable);
        match status {
            Availability::Unavailable => {
                button = button.help(format!("{label} CLI not found in PATH"))
            }
            Availability::Checking => {
                button = button.help(format!("Checking for {label} on this machine"))
            }
            Availability::Available => {}
        }
        let picked = agent.clone();
        let button = button
            .on_press_tx(move |state: &mut State, tx: &mut Tx| {
                let availability = harnesses::availability(&state.facts.settings);
                if harnesses::selectable(&availability, &picked) {
                    autosave::apply(state, DEFAULT_AGENT, &picked, tx);
                }
            })
            .build(k)
            .key(format!("settings.default-agent.{agent}"))
            .checked(active);
        row()
            .items_center()
            .child(button)
            .children((status == Availability::Checking).then(|| {
                text("…")
                    .key(format!("settings.default-agent.{agent}.checking"))
                    .role("img")
                    .label("checking")
                    .color(t.text_tertiary)
            }))
    });
    row()
        .key("settings.default-agent")
        .role("group")
        .label("Default session agent")
        .gap(space::S1)
        .items_center()
        .children(options)
}

fn asks(state: &State, t: &Tokens) -> El {
    let name = attn_protocol::go_values::FEATURES
        .iter()
        .find(|f| f.key == ASKS)
        .map_or("Asks", |f| f.name);
    row()
        .w_full()
        .gap(S4)
        .items_center()
        .padding(space::S3)
        .radius(6.0)
        .fill(t.bg_panel)
        .border(1.0, t.border_subtle)
        .child(
            col()
                .grow(1.0)
                .min_w(0.0)
                .gap(space::S1)
                .child(text(name).font(type_::LABEL).color(t.text_primary))
                .children(feature_lines(state, ASKS, t)),
        )
        .child(feature_switch(state, ASKS, false, t))
}

fn session_defaults(state: &State, t: &Tokens) -> El {
    let settings = &state.facts.settings;
    let availability = harnesses::availability(settings);
    let any = ordered(settings)
        .iter()
        .any(|a| harnesses::selectable(&availability, a));
    let migrating = settings
        .get("pty_backend_mode")
        .is_some_and(|m| m.to_lowercase() == "migrating");
    let mut body = Vec::new();
    if !any {
        body.push(warning(
            "settings.agents.none",
            "No supported agent CLI found in PATH.",
            t,
        ));
    }
    body.push(row_card(
        "Default agent",
        "Used for new sessions and opening PRs.",
        t,
        default_agent(state, t),
    ));
    body.push(row_card(
        "Auto-approve",
        "Let Claude and Codex review approval requests automatically. On by default for new sessions; YOLO sessions already bypass approval.",
        t,
        setting_toggle(AUTO_APPROVE, "Auto-approve", false, t, state),
    ));
    body.push(asks(state, t));
    body.push(row_card(
        "Direct agent launches",
        "Start agents without an attn process beside each one, which uses less memory. On by default; applies to new and reloaded agents.",
        t,
        setting_toggle(DIRECT_LAUNCH, "Direct agent launches", false, t, state),
    ));
    body.push(row_card(
        "Shared terminal host",
        "Run terminals in one shared process, which uses less memory, and stream output only to the terminals the window shows. On by default; applies to new and reloaded terminals.",
        t,
        setting_toggle(SHARED_HOST, "Shared terminal host", !migrating, t, state),
    ));
    block("session-defaults", "Session defaults", "", t, body)
}

fn resume_choice(settings: &BTreeMap<String, String>) -> &'static str {
    match settings.get(TENDER_RESUME).map(String::as_str) {
        Some("watches") => "watches",
        Some("off") => "off",
        _ => "any",
    }
}

fn seeds_on_standby(state: &State, t: &Tokens) -> El {
    let settings = &state.facts.settings;
    let choice = resume_choice(settings);
    let ring = settings.get(OWN_PR_COMMENTS).is_some_and(|v| v == "ring");
    let cases = STANDBY_MAIL.iter().map(|(case, resumes)| {
        let verdict = if resumes.contains(&choice) {
            "Resumes"
        } else {
            "Waits"
        };
        row()
            .role("listitem")
            .label(format!("{verdict} {case}"))
            .gap(space::S2)
            .child(
                text(verdict)
                    .font(type_::LABEL)
                    .color(if verdict == "Resumes" {
                        t.success
                    } else {
                        t.text_tertiary
                    })
                    .a11y_hidden(),
            )
            .child(
                text(*case)
                    .font(type_::BODY)
                    .color(t.text_secondary)
                    .a11y_hidden(),
            )
    });
    let own_comments =
        "Enable if you want to give feedback to agents by writing comments on the PR";
    block(
        "seeds-on-standby",
        "Seeds on standby",
        "Resume a closed agent when mail reaches its seed.",
        t,
        vec![
            super::pages::segmented(
                TENDER_RESUME,
                "Resume for",
                RESUME_CHOICES,
                choice,
                t,
                |state, value, tx| autosave::apply(state, TENDER_RESUME, value, tx),
            ),
            col()
                .gap(space::S1)
                .child(
                    text("A closed agent on standby")
                        .font(type_::LABEL)
                        .color(t.text_primary),
                )
                .child(
                    col()
                        .key("settings.standby.cases")
                        .role("list")
                        .label("A closed agent on standby")
                        .gap(space::S1)
                        .children(cases),
                )
                .child(hint(
                    "attn always starts a session for a crew member who tends the seed, whatever you pick.",
                    t,
                )),
            row_card(
                "Ring on my own PR comments",
                own_comments,
                t,
                toggle(
                    "settings.toggle.pull_requests.own_comments",
                    "Ring on my own PR comments",
                    ring,
                    false,
                    t,
                    move |state, tx| {
                        autosave::apply(
                            state,
                            OWN_PR_COMMENTS,
                            if ring { "quiet" } else { "ring" },
                            tx,
                        )
                    },
                )
                .description(own_comments),
            ),
        ],
    )
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

fn capabilities(agent: &str, caps: Option<&BTreeMap<String, bool>>, t: &Tokens) -> El {
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
    col()
        .key(format!("settings.agent.{agent}.capabilities"))
        .role("list")
        .label("Capabilities")
        .gap(space::S1)
        .children(known.chain(other).map(|cap| {
            let label = capability_label(cap);
            let value = if caps[cap] {
                "Supported"
            } else {
                "Unavailable"
            };
            row()
                .role("listitem")
                .label(format!("{label}: {value}"))
                .gap(space::S2)
                .child(
                    text(label)
                        .font(type_::LABEL)
                        .color(t.text_secondary)
                        .w(140.0)
                        .a11y_hidden(),
                )
                .child(
                    text(value)
                        .font(type_::BODY)
                        .color(t.text_primary)
                        .a11y_hidden(),
                )
        }))
}

fn agent_group(state: &State, agent: &str, t: &Tokens) -> El {
    let settings = &state.facts.settings;
    let page = &state.window.settings.agents;
    let label = harnesses::label(settings, agent);
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
    let summary = format!(
        "{} · {}",
        route_badge(state, agent),
        availability_word(status)
    );
    let toggled = agent.to_owned();
    let head = disclosure(
        format!("settings.agent.{agent}.toggle"),
        &label,
        Some(summary),
        open,
        t,
        move |state| {
            let shown = &mut state.window.settings.agents.shown;
            let was = shown
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
    );
    let mut group = col()
        .key(format!("settings.agent.{agent}"))
        .role("group")
        .label(label.clone())
        .w_full()
        .gap(space::S2)
        .padding(space::S2)
        .radius(6.0)
        .fill(t.bg_panel)
        .border(1.0, t.border_subtle)
        .child(head);
    if !open {
        return group;
    }
    if overrides {
        group = group.child(route::chip(
            state,
            t,
            target(agent),
            &format!("{label} default model"),
            true,
        ));
    } else {
        group = group.child(hint(
            "Model and effort follow this agent's own configuration.",
            t,
        ));
    }
    let advanced_open = refusal_inside || page.advanced.contains(agent);
    let mut fields = row().gap(S4).items_start();
    if executable {
        fields = fields.child(field(
            state,
            executable_key(agent),
            "Executable",
            agent,
            "Empty uses the executable on PATH.",
            t,
        ));
    }
    if overrides {
        fields = fields.child(field(
            state,
            cap_key(agent),
            "Context cap (tokens)",
            "Agent default",
            "Empty uses the agent's own compaction behavior.",
            t,
        ));
    }
    let toggled = agent.to_owned();
    group = group.child(disclosure(
        format!("settings.agent.{agent}.advanced"),
        "Advanced",
        None,
        advanced_open,
        t,
        move |state| {
            let advanced = &mut state.window.settings.agents.advanced;
            if !advanced.remove(&toggled) {
                advanced.insert(toggled.clone());
            }
        },
    ));
    if !advanced_open {
        return group;
    }
    let all_capabilities = harnesses::capabilities(settings);
    group
        .child(fields)
        .child(capabilities(agent, all_capabilities.get(agent), t))
}

/// A button that shows and hides what follows it, with a summary beside its title.
fn disclosure(
    key: String,
    title: &str,
    summary: Option<String>,
    open: bool,
    t: &Tokens,
    toggle: impl Fn(&mut State) + 'static,
) -> El {
    let ink_secondary = t.text_tertiary;
    let glyph = &attn_ui::kit(t).glyph;
    let triangle = if open {
        glyph.triangle_down
    } else {
        glyph.triangle_right
    };
    let (shown, said) = (title.to_owned(), summary.clone());
    let button = Button::new(title)
        .variant(ButtonVariant::Ghost)
        .expanded(open)
        .label_with(move |font, ink| {
            row()
                .gap(space::S2)
                .items_center()
                .child(rune::icon(triangle, 10.0).color(ink_secondary))
                .child(text(shown).font(font).color(ink))
                .children(said.map(|s| text(s).font(CAPTION).color(ink_secondary)))
        })
        .on_press(toggle)
        .build(attn_ui::kit(t))
        .key(key);
    match summary {
        Some(summary) => button.a11y_value(summary),
        None => button,
    }
}

pub(super) fn page(state: &State, t: &Tokens) -> Vec<El> {
    let agents = ordered(&state.facts.settings);
    vec![
        session_defaults(state, t),
        seeds_on_standby(state, t),
        col()
            .key("settings.agents.list")
            .w_full()
            .gap(space::S2)
            .pb(S4)
            .children(agents.iter().map(|agent| agent_group(state, agent, t))),
        pricing::page(state, t),
    ]
}

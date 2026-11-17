//! Settings → Agents and models: the defaults new sessions start with, which mail resumes an
//! agent on standby, each agent's default model, executable, context cap and capabilities, and
//! the model pricing overrides.

mod pricing;

use std::collections::{BTreeMap, BTreeSet};

use attn_ui::Tokens;
use attn_ui::type_;
use rune::tok::space;
use rune::{El, Tx, col, row, text};
use rune_kit::TextInput;

use super::autosave::{self, Codec, PLAIN, TRIMMED};
use super::harnesses::{self, Availability};
use super::pages::{
    CAPTION, S4, block, blocks, feature, feature_note, feature_switch, plain_row, refusal,
    row_card, row_words, toggle,
};
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

/// A field that edits the setting `key` and fills its column, saved on Enter and when the
/// focus leaves it, with its label above, its hint below and its refusal under that, as the
/// Tauri app's `.settings-field`.
fn field(
    state: &State,
    key: String,
    label: &str,
    placeholder: &str,
    hint_text: &str,
    step: Option<f64>,
    t: &Tokens,
) -> El {
    let drafts = &state.window.settings.autosave;
    let field = drafts.field(&key);
    let empty = rune::TextEdit::default();
    let (edit, submit) = (key.clone(), key.clone());
    let mut input = TextInput::new(field.map_or(&empty, |f| &f.draft))
        .key(super::field_key(&key))
        .label(label.to_owned())
        .placeholder(placeholder.to_owned())
        .invalid(field.is_some_and(|f| f.error.is_some()))
        .on_edit(move |state: &mut State| state.window.settings.autosave.draft_mut(&edit))
        .on_submit(move |state: &mut State, tx: &mut Tx| autosave::commit(state, &submit, tx))
        .width(None);
    if let Some(step) = step {
        input = input.trailing(super::pages::setting_arrows(&key, step, t));
    }
    let saved = drafts.shows_saved_mark(&key).then(|| {
        text("Saved")
            .key(format!("settings.saved.{key}"))
            .role("status")
            .label("Saved")
            .font(CAPTION)
            .color(t.success)
    });
    col()
        .min_w(0.0)
        .gap(7.0)
        .child(type_::upper(label, type_::BOLD_CAPS).color(t.text_dimmed))
        .child(
            row()
                .w_full()
                .gap(space::S2)
                .items_center()
                .child(
                    col()
                        .grow(1.0)
                        .min_w(0.0)
                        .child(super::pages::input(input, t)),
                )
                .children(saved),
        )
        .child(super::pages::hint(hint_text, t))
        .children(refusal(state, &key, t))
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
    let options = ordered(settings).into_iter().map(|agent| {
        let label = harnesses::label(settings, &agent);
        let status = harnesses::of(&availability, &agent);
        let unavailable = status == Availability::Unavailable;
        let mut option = super::pages::segment(
            format!("settings.default-agent.{agent}"),
            &label,
            chosen == agent,
            t,
        )
        .disabled(unavailable)
        .children((status == Availability::Checking).then(|| {
            text("…")
                .key(format!("settings.default-agent.{agent}.checking"))
                .role("img")
                .label("checking")
                .color(t.text_tertiary)
        }));
        let help = match status {
            Availability::Unavailable => Some(format!("{label} CLI not found in PATH")),
            Availability::Checking => Some(format!("Checking for {label} on this machine")),
            Availability::Available => None,
        };
        if unavailable {
            option = option.layer_opacity(0.45);
        }
        if let Some(help) = help {
            option = option.help(help.clone()).description(help);
        }
        let picked = agent.clone();
        option.on_press_tx(move |state: &mut State, tx: &mut Tx| {
            let availability = harnesses::availability(&state.facts.settings);
            if harnesses::selectable(&availability, &picked) {
                autosave::apply(state, DEFAULT_AGENT, &picked, tx);
            }
        })
    });
    row().child(
        super::pages::segment_track("settings.default-agent", "Default session agent", t)
            .items_center()
            .children(options),
    )
}

fn asks(state: &State, t: &Tokens) -> El {
    let feature = feature(ASKS);
    plain_row(
        row_words(feature.name, feature.summary, t).children(feature_note(state, ASKS, t)),
        feature_switch(state, ASKS, false, t),
    )
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
    body.push(plain_row(
        row_words("Default agent", "Used for new sessions and opening PRs.", t),
        default_agent(state, t),
    ));
    body.push(plain_row(
        row_words(
            "Auto-approve",
            "Let Claude and Codex review approval requests automatically. On by default for new sessions; YOLO sessions already bypass approval.",
            t,
        ),
        setting_toggle(AUTO_APPROVE, "Auto-approve", false, t, state),
    ));
    body.push(asks(state, t));
    body.push(plain_row(
        row_words(
            "Direct agent launches",
            "Start agents without an attn process beside each one, which uses less memory. On by default; applies to new and reloaded agents.",
            t,
        ),
        setting_toggle(DIRECT_LAUNCH, "Direct agent launches", false, t, state),
    ));
    body.push(plain_row(
        row_words(
            "Shared terminal host",
            "Run terminals in one shared process, which uses less memory, and stream output only to the terminals the window shows. On by default; applies to new and reloaded terminals.",
            t,
        ),
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

/// A standby case's verdict, as the Tauri app's `.settings-standby-verdict`.
const VERDICT: rune::TextStyle = rune::TextStyle::new(12.0, 700.0);
/// The title of the standby cases, as `.settings-standby-explain-title`.
const STANDBY_TITLE: rune::TextStyle = rune::TextStyle::new(12.0, 400.0).tracking(0.7);

fn seeds_on_standby(state: &State, t: &Tokens) -> El {
    let settings = &state.facts.settings;
    let choice = resume_choice(settings);
    let ring = settings.get(OWN_PR_COMMENTS).is_some_and(|v| v == "ring");
    let cases = STANDBY_MAIL.iter().map(|(case, resumes)| {
        let resumes = resumes.contains(&choice);
        let verdict = if resumes { "Resumes" } else { "Waits" };
        let (ink, fill) = if resumes {
            (t.warning, t.warning.alpha(0.14))
        } else {
            (t.text_secondary, t.bg_button)
        };
        row()
            .role("listitem")
            .label(format!("{verdict} {case}"))
            .gap(10.0)
            .items_center()
            .child(
                text(verdict)
                    .font(VERDICT)
                    .color(ink)
                    .a11y_hidden()
                    .w(70.0)
                    .py(2.0)
                    .center()
                    .radius(4.0)
                    .fill(fill),
            )
            .child(
                text(*case)
                    .font(type_::BODY)
                    .color(t.text_primary)
                    .wrap()
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
                .w_full()
                .max_w(640.0)
                .gap(8.0)
                .py(12.0)
                .px(14.0)
                .radius(8.0)
                .fill(t.bg_elevated)
                .border(1.0, t.border)
                .child(type_::upper("A closed agent on standby", STANDBY_TITLE).color(t.text_tertiary))
                .child(
                    col()
                        .key("settings.standby.cases")
                        .role("list")
                        .label("A closed agent on standby")
                        .gap(6.0)
                        .children(cases),
                )
                .child(super::pages::hint(
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
        .w_full()
        .mt(20.0)
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
                .w_full()
                .gap(S4)
                .py(6.0)
                .border_sides([0.0, 0.0, 1.0, 0.0], t.border)
                .child(
                    text(label)
                        .font(CAPTION)
                        .color(t.text_primary)
                        .grow(1.0)
                        .a11y_hidden(),
                )
                .child(
                    text(value)
                        .font(CAPTION)
                        .color(t.text_tertiary)
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
    let toggled = agent.to_owned();
    let head = agent_head(state, agent, &label, status, open, t, move |state| {
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
    });
    let group = col()
        .key(format!("settings.agent.{agent}"))
        .role("group")
        .label(label.clone())
        .w_full()
        .radius(6.0)
        .border(1.0, t.border)
        .child(head);
    if !open {
        return group;
    }
    let mut content = col().w_full().px(20.0).pb(20.0);
    if overrides {
        content = content.child(route::chip(
            state,
            t,
            target(agent),
            &format!("{label} default model"),
            true,
        ));
    } else {
        content = content.child(
            text("Model and effort follow this agent's own configuration.")
                .font(type_::BODY)
                .color(t.text_tertiary)
                .wrap(),
        );
    }
    let advanced_open = refusal_inside || page.advanced.contains(agent);
    let toggled = agent.to_owned();
    content = content.child(disclosure(
        format!("settings.agent.{agent}.advanced"),
        "Advanced",
        advanced_open,
        t,
        move |state| {
            let advanced = &mut state.window.settings.agents.advanced;
            if !advanced.remove(&toggled) {
                advanced.insert(toggled.clone());
            }
        },
    ));
    if advanced_open {
        let mut fields = row().w_full().equal_columns(2).gap(10.0).items_start();
        if executable {
            fields = fields.child(field(
                state,
                executable_key(agent),
                "Executable",
                agent,
                "Empty uses the executable on PATH.",
                None,
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
                Some(1000.0),
                t,
            ));
        }
        let all_capabilities = harnesses::capabilities(settings);
        content = content
            .child(fields)
            .child(capabilities(agent, all_capabilities.get(agent), t));
    }
    group.child(content)
}

/// An agent group's head, as the Tauri app's `.settings-agent > summary`: the chevron and the
/// agent's name, and at its right end the agent's default route and whether it is available.
fn agent_head(
    state: &State,
    agent: &str,
    label: &str,
    status: Availability,
    open: bool,
    t: &Tokens,
    toggle: impl Fn(&mut State) + 'static,
) -> El {
    let settings = &state.facts.settings;
    let value = Route::stored(
        agent,
        settings.get(&model_key(agent)).map_or("", String::as_str),
        settings.get(&effort_key(agent)).map_or("", String::as_str),
    );
    let word = availability_word(status);
    row()
        .key(format!("settings.agent.{agent}.toggle"))
        .role("button")
        .label(label.to_owned())
        .a11y_value(format!("{} · {word}", route_badge(state, agent)))
        .expanded(open)
        .focusable(true)
        .focus_ring(2.0, 2.0, t.text_secondary)
        .w_full()
        .gap(S4)
        .items_center()
        .px(20.0)
        .py(18.0)
        .child(chevron(open, t))
        .child(
            text(label.to_owned())
                .font(AGENT_NAME)
                .color(t.text_primary),
        )
        .child(rune::spacer())
        .child(
            row()
                .gap(space::S1)
                .items_center()
                .child(route::badge(state, t, &value))
                .child(
                    text(format!("· {word}"))
                        .font(CAPTION)
                        .color(t.text_secondary),
                ),
        )
        .on_press(toggle)
}

/// An agent's name in its group's head.
const AGENT_NAME: rune::TextStyle = rune::TextStyle::new(13.0, 600.0);

/// The `›` before an agent's name, turned down while its group is open.
fn chevron(open: bool, t: &Tokens) -> El {
    let icon = if open {
        crate::settings::pages::kit(t).glyph.chevron_down
    } else {
        &attn_ui::icon::EXPAND
    };
    rune::icon(icon, 12.0).color(t.text_tertiary)
}

/// A line that shows and hides what follows it, as the Tauri app's
/// `.settings-agent-advanced > summary`.
fn disclosure(
    key: String,
    title: &str,
    open: bool,
    t: &Tokens,
    toggle: impl Fn(&mut State) + 'static,
) -> El {
    let triangle = if open {
        attn_ui::glyph::CARET_DOWN
    } else {
        attn_ui::glyph::CARET_RIGHT
    };
    row()
        .key(key)
        .role("button")
        .label(title.to_owned())
        .expanded(open)
        .focusable(true)
        .focus_ring(2.0, 2.0, t.text_secondary)
        .w_full()
        .gap(6.0)
        .items_center()
        .py(16.0)
        .child(
            text(triangle)
                .font(type_::BODY)
                .color(t.text_secondary)
                .a11y_hidden(),
        )
        .child(
            text(title.to_owned())
                .font(type_::BODY)
                .color(t.text_secondary),
        )
        .on_press(toggle)
}

pub(super) fn page(state: &State, t: &Tokens) -> Vec<El> {
    let agents = ordered(&state.facts.settings);
    let mut page = blocks(
        vec![session_defaults(state, t), seeds_on_standby(state, t)],
        t,
    );
    page.extend([
        col()
            .key("settings.agents.list")
            .w_full()
            .gap(12.0)
            .children(agents.iter().map(|agent| agent_group(state, agent, t))),
        pricing::page(state, t),
    ]);
    page
}

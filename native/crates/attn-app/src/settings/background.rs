//! Settings → Background agents: the agent that writes session activity lines, the Garden
//! advisor, and the context cap of headless runs.

use attn_ui::Tokens;
use rune::tok::{space, type_};
use rune::{El, TextEdit, Tx, col, row, text};
use rune_kit::{Button, TextInput};
use serde::{Deserialize, Serialize};

use super::autosave::{self, Codec};
use super::harnesses;
use super::pages::{CAPTION, NUMBER_FIELD, block, refusal_beside, row_card, setting_field};
use super::route::{self, Route, Rules, Target};
use crate::State;

const ACTIVITY_ENABLED: &str = "activity.enabled";
const ACTIVITY_CONFIG: &str = "activity.config";
const ACTIVITY_INTERVALS: &str = "activity.intervals";
const GARDEN_ADVISOR: &str = "garden.advisor";
pub(super) const HEADLESS_CONTEXT_CAP: &str = "headless_context_window_cap";
const DEFAULT_CONTEXT_WINDOW_CAP: u64 = 128_000;
const INTERVAL_MIN_SECONDS: i64 = 30;
const INTERVAL_MAX_SECONDS: i64 = 3600;

/// The two refresh fields, which save together as one setting.
#[derive(Default)]
pub(crate) struct Background {
    watching: TextEdit,
    present: TextEdit,
    /// The intervals draft the two fields show.
    mirrored: Option<String>,
}

/// A route as the activity and advisor settings keep it, and as their drafts hold it.
#[derive(Clone, Debug, Default, Serialize, Deserialize)]
struct Config {
    #[serde(default)]
    agent: String,
    #[serde(default)]
    model: String,
    #[serde(default)]
    effort: String,
}

fn parse_activity(raw: Option<&str>) -> Config {
    raw.filter(|r| !r.trim().is_empty())
        .and_then(|r| serde_json::from_str::<Config>(r).ok())
        .unwrap_or_default()
}

fn parse_advisor(raw: Option<&str>) -> Config {
    let parsed = raw
        .filter(|r| !r.trim().is_empty())
        .and_then(|r| serde_json::from_str::<Config>(r).ok());
    let Some(parsed) = parsed else {
        return Config {
            agent: "codex".into(),
            ..Config::default()
        };
    };
    let agent = parsed.agent.trim().to_lowercase();
    Config {
        agent: if harnesses::builtin(&agent) {
            agent
        } else {
            "codex".into()
        },
        model: parsed.model.trim().to_owned(),
        effort: parsed.effort.trim().to_owned(),
    }
}

/// A route as the daemon stores it, its fields in the Tauri app's order.
#[derive(Serialize)]
struct Stored<'a> {
    agent: &'a str,
    #[serde(skip_serializing_if = "Option::is_none")]
    model: Option<&'a str>,
    #[serde(skip_serializing_if = "Option::is_none")]
    effort: Option<&'a str>,
}

fn stored(route: &Stored) -> String {
    serde_json::to_string(route).expect("a route serializes")
}

fn draft(config: &Config) -> String {
    serde_json::to_string(config).expect("a config serializes")
}

fn draft_config(draft: &str) -> Config {
    serde_json::from_str(draft).unwrap_or_default()
}

const ACTIVITY_CODEC: Codec = Codec {
    actual: |settings, key| draft(&parse_activity(settings.get(key).map(String::as_str))),
    serialize: |_, raw| {
        let config = draft_config(raw);
        if config.agent.is_empty() {
            return Ok(String::new());
        }
        Ok(stored(&Stored {
            agent: &config.agent,
            model: Some(config.model.trim()).filter(|m| !m.is_empty()),
            effort: Some(config.effort.as_str()).filter(|e| !e.is_empty()),
        }))
    },
};

const ADVISOR_CODEC: Codec = Codec {
    actual: |settings, key| draft(&parse_advisor(settings.get(key).map(String::as_str))),
    serialize: |_, raw| {
        let config = draft_config(raw);
        Ok(stored(&Stored {
            agent: &config.agent,
            model: Some(config.model.trim()),
            effort: Some(config.effort.trim()).filter(|e| !e.is_empty()),
        }))
    },
};

#[derive(Serialize, Deserialize)]
struct Intervals {
    watching: String,
    present: String,
}

fn parse_intervals(raw: Option<&str>) -> Intervals {
    #[derive(Deserialize)]
    struct Stored {
        watching: Option<serde_json::Number>,
        present: Option<serde_json::Number>,
    }
    let stored = raw
        .filter(|r| !r.trim().is_empty())
        .and_then(|r| serde_json::from_str::<Stored>(r).ok());
    match stored {
        Some(s) => Intervals {
            watching: s.watching.map_or_else(|| "120".into(), |n| n.to_string()),
            present: s.present.map_or_else(|| "300".into(), |n| n.to_string()),
        },
        None => Intervals {
            watching: "120".into(),
            present: "300".into(),
        },
    }
}

fn seconds(typed: &str) -> Option<i64> {
    let trimmed = typed.trim();
    let n: f64 = if trimmed.is_empty() {
        0.0
    } else {
        trimmed.parse().ok()?
    };
    (n.fract() == 0.0 && (INTERVAL_MIN_SECONDS as f64..=INTERVAL_MAX_SECONDS as f64).contains(&n))
        .then_some(n as i64)
}

const INTERVALS_CODEC: Codec = Codec {
    actual: |settings, key| {
        serde_json::to_string(&parse_intervals(settings.get(key).map(String::as_str)))
            .expect("intervals serialize")
    },
    serialize: |_, raw| {
        let typed: Intervals = serde_json::from_str(raw).map_err(|e| e.to_string())?;
        let check = |name: &str, value: &str| {
            seconds(value).ok_or_else(|| {
                let entered = if value.is_empty() { "empty" } else { value };
                format!(
                    "{name} refresh must be a whole number from {INTERVAL_MIN_SECONDS} to {INTERVAL_MAX_SECONDS} seconds; entered {entered}."
                )
            })
        };
        let watching = check("watching", &typed.watching)?;
        let present = check("present", &typed.present)?;
        Ok(format!(r#"{{"watching":{watching},"present":{present}}}"#))
    },
};

const CONTEXT_CAP_CODEC: Codec = Codec {
    actual: |settings, key| {
        settings
            .get(key)
            .filter(|v| !v.is_empty())
            .cloned()
            .unwrap_or_else(|| DEFAULT_CONTEXT_WINDOW_CAP.to_string())
    },
    serialize: |_, draft| Ok(draft.trim().to_owned()),
};

/// The page's fields, with how each reads and saves.
pub(super) fn fields() -> Vec<(String, Codec)> {
    vec![
        (ACTIVITY_CONFIG.into(), ACTIVITY_CODEC),
        (ACTIVITY_INTERVALS.into(), INTERVALS_CODEC),
        (GARDEN_ADVISOR.into(), ADVISOR_CODEC),
        (HEADLESS_CONTEXT_CAP.into(), CONTEXT_CAP_CODEC),
    ]
}

/// Shows the intervals draft in the two refresh fields when it changed under them.
pub(super) fn follow(state: &mut State) -> bool {
    let draft = state
        .window
        .settings
        .autosave
        .draft(ACTIVITY_INTERVALS)
        .to_owned();
    let page = &mut state.window.settings.background;
    if page.mirrored.as_deref() == Some(draft.as_str()) {
        return false;
    }
    let typed: Intervals = serde_json::from_str(&draft).unwrap_or(Intervals {
        watching: String::new(),
        present: String::new(),
    });
    page.watching = TextEdit::new(typed.watching);
    page.present = TextEdit::new(typed.present);
    page.mirrored = Some(draft);
    true
}

fn intervals_typed(state: &mut State, _: &mut Tx) {
    let page = &state.window.settings.background;
    let draft = serde_json::to_string(&Intervals {
        watching: page.watching.text.clone(),
        present: page.present.text.clone(),
    })
    .expect("intervals serialize");
    state.window.settings.background.mirrored = Some(draft.clone());
    *state.window.settings.autosave.draft_mut(ACTIVITY_INTERVALS) = TextEdit::new(draft);
}

fn config(state: &State, key: &str) -> Config {
    draft_config(state.window.settings.autosave.draft(key))
}

fn activity_agents(state: &State) -> Vec<String> {
    let configured = parse_activity(
        state
            .facts
            .settings
            .get(ACTIVITY_CONFIG)
            .map(String::as_str),
    );
    harnesses::headless_agents(&state.facts.settings, &configured.agent)
}

fn advisor_agents(state: &State) -> Vec<String> {
    let configured = parse_advisor(state.facts.settings.get(GARDEN_ADVISOR).map(String::as_str));
    harnesses::headless_agents(&state.facts.settings, &configured.agent)
}

fn route_of(config: Config) -> Route {
    Route {
        harness: config.agent,
        provider: String::new(),
        model: config.model,
        effort: config.effort,
    }
}

fn write(state: &mut State, key: &str, route: Route, tx: &mut Tx) {
    let next = draft(&Config {
        agent: route.harness,
        model: route.model,
        effort: route.effort,
    });
    autosave::apply(state, key, &next, tx);
}

const ACTIVITY_ROUTE: &str = "activity";
const ADVISOR_ROUTE: &str = "garden-advisor";

fn read_route(state: &State, id: &str) -> Option<(Route, Rules)> {
    match id {
        ACTIVITY_ROUTE => Some((
            route_of(config(state, ACTIVITY_CONFIG)),
            Rules {
                harnesses: harnesses::headless(state, &activity_agents(state)),
                allow_none: true,
                require_available: true,
                effort: Some(|harness| harness != "claude"),
                tier: Some("light"),
                default_effort: Some(|_| "low"),
                ..Rules::default()
            },
        )),
        ADVISOR_ROUTE => Some((
            route_of(config(state, GARDEN_ADVISOR)),
            Rules {
                harnesses: harnesses::headless(state, &advisor_agents(state)),
                require_available: true,
                tier: Some("light"),
                default_effort: Some(|harness| match harness {
                    "codex" => "xhigh",
                    "claude" => "medium",
                    _ => "",
                }),
                ..Rules::default()
            },
        )),
        _ => None,
    }
}

fn write_route(state: &mut State, id: &str, route: Route, tx: &mut Tx) {
    match id {
        ACTIVITY_ROUTE => write(state, ACTIVITY_CONFIG, route, tx),
        ADVISOR_ROUTE => write(state, GARDEN_ADVISOR, route, tx),
        _ => {}
    }
}

fn target(id: &str) -> Target {
    Target {
        id: id.into(),
        read: read_route,
        write: write_route,
    }
}

/// The harnesses the page's route fields show, whose models it discovers.
pub(super) fn wanted(state: &State) -> Vec<(String, bool)> {
    [ACTIVITY_ROUTE, ADVISOR_ROUTE]
        .into_iter()
        .filter_map(|id| route::wanted(state, &target(id)))
        .collect()
}

fn warning(key: &str, said: String, t: &Tokens) -> El {
    text(said.clone())
        .key(key.to_owned())
        .label(said)
        .font(CAPTION)
        .color(t.warning)
        .wrap()
}

fn interval_field(state: &State, part: &'static str, label: &str, t: &Tokens) -> El {
    let page = &state.window.settings.background;
    let edit = if part == "watching" {
        &page.watching
    } else {
        &page.present
    };
    let input = TextInput::new(edit)
        .key(super::part_field_key(ACTIVITY_INTERVALS, part))
        .label(label.to_owned())
        .on_edit(move |state: &mut State| {
            let page = &mut state.window.settings.background;
            if part == "watching" {
                &mut page.watching
            } else {
                &mut page.present
            }
        })
        .on_change(intervals_typed)
        .on_submit(|state: &mut State, tx: &mut Tx| autosave::commit(state, ACTIVITY_INTERVALS, tx))
        .width(Some(120.0))
        .build(attn_ui::kit(t));
    col()
        .gap(space::S1)
        .child(
            text(label.to_owned())
                .font(type_::LABEL)
                .color(t.text_primary),
        )
        .child(input)
}

fn session_activity(state: &State, t: &Tokens) -> El {
    let settings = &state.facts.settings;
    let saved = parse_activity(settings.get(ACTIVITY_CONFIG).map(String::as_str));
    let enabled = crate::setting::on(settings, ACTIVITY_ENABLED);
    let blocked = !enabled && saved.agent.is_empty();
    let mut toggle = Button::new(if enabled { "Disable" } else { "Enable" }).disabled(blocked);
    if blocked {
        toggle = toggle.help("Choose an agent first");
    }
    let toggle = toggle
        .on_press_tx(move |state: &mut State, tx: &mut Tx| {
            autosave::apply(
                state,
                ACTIVITY_ENABLED,
                if enabled { "false" } else { "true" },
                tx,
            )
        })
        .build(attn_ui::kit(t))
        .key("settings.activity.toggle");
    let mut body = vec![row_card(
        "Generate activity lines",
        "Off by default. Choose an agent below to enable.",
        t,
        toggle,
    )];
    if activity_agents(state).is_empty() {
        body.push(warning(
            "settings.activity.no-agents",
            "No installed agent supports scoped headless tasks.".into(),
            t,
        ));
    }
    body.extend(harnesses::failure_row(state, "activity", t));
    body.push(route::chip(
        state,
        t,
        target(ACTIVITY_ROUTE),
        "Session activity model",
        true,
    ));
    body.push(
        row()
            .gap(S4)
            .child(interval_field(
                state,
                "watching",
                "Refresh on home (seconds)",
                t,
            ))
            .child(interval_field(
                state,
                "present",
                "Refresh elsewhere in the app (seconds)",
                t,
            )),
    );
    body.extend(refusal_beside(
        state,
        ACTIVITY_INTERVALS,
        super::part_field_key(ACTIVITY_INTERVALS, "watching"),
        t,
    ));
    body.push(
        text("A session that has written nothing since its last line is skipped, so blocked and finished agents cost nothing however long home stays open.")
            .font(CAPTION)
            .color(t.text_tertiary)
            .wrap(),
    );
    block(
        "session-activity",
        "Session activity",
        "Summarize each session on Home using its transcript. This sends transcript excerpts to the selected model and incurs usage costs. Updates run only while you use attn.",
        t,
        body,
    )
}

const S4: f32 = super::pages::S4;

fn garden_advisor(state: &State, t: &Tokens) -> El {
    let settings = &state.facts.settings;
    let agent = config(state, GARDEN_ADVISOR).agent;
    let runs_here = |suffix: &str| {
        settings
            .get(&format!("{agent}_{suffix}"))
            .is_none_or(|v| v != "false")
    };
    let mut body = Vec::new();
    if !(runs_here("available") && runs_here("cap_headless_task")) {
        body.push(warning(
            "settings.garden-advisor.unavailable",
            format!(
                "{} is saved for Garden review but cannot run headless tasks here.",
                crate::picker::agents::label(&agent)
            ),
            t,
        ));
    }
    body.extend(harnesses::failure_row(state, "garden-advisor", t));
    body.push(route::chip(
        state,
        t,
        target(ADVISOR_ROUTE),
        "Garden advisor model",
        true,
    ));
    block(
        "garden-advisor",
        "Garden advisor",
        "Classifies seeds during Review garden and can draft a handoff. It runs in the background with this saved agent and cannot change a seed.",
        t,
        body,
    )
}

fn compaction(state: &State, t: &Tokens) -> El {
    block(
        "compaction",
        "Compaction",
        &format!(
            "Token threshold for background runs. An empty value uses the default of {} tokens.",
            thousands(DEFAULT_CONTEXT_WINDOW_CAP)
        ),
        t,
        vec![row_card(
            "Headless runs",
            "",
            t,
            setting_field(
                state,
                HEADLESS_CONTEXT_CAP,
                "Headless runs",
                "",
                NUMBER_FIELD,
                t,
            ),
        )],
    )
}

/// `128000` as `128,000`, as the Tauri page writes it.
pub(super) fn thousands(n: u64) -> String {
    let digits = n.to_string();
    let mut out = String::new();
    for (i, c) in digits.chars().enumerate() {
        if i > 0 && (digits.len() - i).is_multiple_of(3) {
            out.push(',');
        }
        out.push(c);
    }
    out
}

pub(super) fn page(state: &State, t: &Tokens) -> Vec<El> {
    vec![
        session_activity(state, t),
        garden_advisor(state, t),
        compaction(state, t),
    ]
}

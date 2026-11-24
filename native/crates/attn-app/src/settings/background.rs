//! Settings → Background agents: the agent that writes session activity lines and the context
//! cap of headless runs.

use attn_ui::Tokens;
use attn_ui::blocks::{Block, Field, Notice, Prose, Section, SettingRow};
use attn_ui::system::{Beside, FieldState, arrows, number_box, switch};
use rune::{El, TextEdit, Tx};
use rune_kit::TextInput;
use serde::{Deserialize, Serialize};

use super::autosave::{self, Codec};
use super::controls::{SettingField, refusal_beside, saved_mark, stepped_value};
use super::harnesses;
use super::route::{self, Route, Rules, Target};
use crate::State;

const ACTIVITY_ENABLED: &str = "activity.enabled";
const ACTIVITY_CONFIG: &str = "activity.config";
const ACTIVITY_INTERVALS: &str = "activity.intervals";
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

/// A route as the activity setting keeps it, and as its draft holds it.
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

/// A route as the daemon stores it.
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

fn route_of(config: Config) -> Route {
    Route::new(&config.agent, &config.model, &config.effort)
}

fn write(state: &mut State, key: &str, route: Route, tx: &mut Tx) {
    let next = draft(&Config {
        model: route.model.clone(),
        agent: route.harness,
        effort: route.effort,
    });
    autosave::apply(state, key, &next, tx);
}

const ACTIVITY_ROUTE: &str = "activity";

fn read_route(state: &State, id: &str) -> Option<(Route, Rules)> {
    match id {
        ACTIVITY_ROUTE => {
            let current = config(state, ACTIVITY_CONFIG);
            Some((
                route_of(current.clone()),
                Rules {
                    harnesses: harnesses::headless(state, &current.agent),
                    allow_none: true,
                    require_available: true,
                    tier: Some("light"),
                    one_shot: true,
                    ..Rules::default()
                },
            ))
        }
        _ => None,
    }
}

fn write_route(state: &mut State, id: &str, route: Route, tx: &mut Tx) {
    if id == ACTIVITY_ROUTE {
        write(state, ACTIVITY_CONFIG, route, tx);
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
    route::wanted(state, &target(ACTIVITY_ROUTE))
        .into_iter()
        .collect()
}

fn interval_field(state: &State, part: &'static str, label: &str, t: &Tokens) -> Block {
    let page = &state.window.settings.background;
    let edit = if part == "watching" {
        &page.watching
    } else {
        &page.present
    };
    let key = super::part_field_key(ACTIVITY_INTERVALS, part);
    let input = TextInput::new(edit)
        .key(key.clone())
        .label(label.to_owned())
        .on_edit(move |state: &mut State| interval_edit(state, part))
        .on_change(intervals_typed)
        .on_submit(|state: &mut State, tx: &mut Tx| autosave::commit(state, ACTIVITY_INTERVALS, tx))
        .on_blur(|state: &mut State, tx: &mut Tx| {
            autosave::commit_on_leave(state, ACTIVITY_INTERVALS, tx)
        })
        .trailing(arrows(&key, t, move |state: &mut State, up, tx| {
            let edit = interval_edit(state, part);
            let next = stepped_value(&edit.text, 1.0, up)
                .parse::<i64>()
                .map_or(INTERVAL_MIN_SECONDS, |n| {
                    n.clamp(INTERVAL_MIN_SECONDS, INTERVAL_MAX_SECONDS)
                });
            *edit = TextEdit::new(next.to_string());
            intervals_typed(state, tx);
            autosave::commit(state, ACTIVITY_INTERVALS, tx);
        }));
    let refused = state
        .window
        .settings
        .autosave
        .field(ACTIVITY_INTERVALS)
        .is_some_and(|f| f.error.is_some());
    let mut field = Field::new(
        super::part_field_key(ACTIVITY_INTERVALS, part),
        label.to_owned(),
    )
    .control(number_box(input, FieldState::failed_if(refused), t));
    if part == "watching" {
        field = field.mark(saved_mark(state, ACTIVITY_INTERVALS, t));
    } else {
        let first = super::part_field_key(ACTIVITY_INTERVALS, "watching");
        field = field.refusal(refusal_beside(state, ACTIVITY_INTERVALS, first, t));
    }
    field.build(t)
}

fn interval_edit<'a>(state: &'a mut State, part: &str) -> &'a mut TextEdit {
    let page = &mut state.window.settings.background;
    if part == "watching" {
        &mut page.watching
    } else {
        &mut page.present
    }
}

fn session_activity(state: &State, t: &Tokens) -> Block {
    let settings = &state.facts.settings;
    let saved = parse_activity(settings.get(ACTIVITY_CONFIG).map(String::as_str));
    let enabled = crate::setting::on(settings, ACTIVITY_ENABLED);
    let blocked = !enabled && saved.agent.is_empty();
    let mut toggle = El::from(switch(
        "settings.activity.toggle",
        "Generate activity lines",
        enabled,
        Beside::Nothing,
        blocked,
        t,
        move |state: &mut State, tx: &mut Tx| {
            autosave::apply(
                state,
                ACTIVITY_ENABLED,
                if enabled { "false" } else { "true" },
                tx,
            )
        },
    ));
    if blocked {
        toggle = toggle.help("Choose an agent first");
    }
    let mut section = Section::new("settings.block.session-activity", "Session activity")
        .description("Summarize each session on Home using its transcript. This sends transcript excerpts to the selected model and incurs usage costs. Updates run only while you use attn.")
        .push(
            SettingRow::new(
                "settings.row.activity",
                "Generate activity lines",
                "Off by default. Choose an agent below to enable.",
                toggle,
            )
            .build(t),
        );
    let saved = parse_activity(
        state
            .facts
            .settings
            .get(ACTIVITY_CONFIG)
            .map(String::as_str),
    );
    if state.facts.harnesses_loaded
        && saved.agent.is_empty()
        && !crate::harness::catalog::offered(&state.facts.harnesses)
            .any(|h| h.available() && h.options.one_shot)
    {
        section = section.push(
            Notice::warn(
                "settings.activity.no-agents",
                "No installed agent supports scoped headless tasks.",
            )
            .build(t),
        );
    }
    section
        .push(Block::custom(
            "settings.route.activity.block",
            route::field_chip(
            state,
            t,
            target(ACTIVITY_ROUTE),
            "Session activity model")))
        .push(interval_field(state, "watching", "Refresh on home (seconds)", t))
        .push(interval_field(
            state,
            "present",
            "Refresh elsewhere in the app (seconds)",
            t,
        ))
        .push(
            Prose::hint(
                "settings.activity.hint",
                "A session that has written nothing since its last line is skipped, so blocked and finished agents cost nothing however long home stays open.",
            )
            .build(t),
        )
        .build(t)
}

fn compaction(state: &State, t: &Tokens) -> Block {
    Section::new("settings.block.compaction", "Compaction")
        .description(format!(
            "Token threshold for background runs. An empty value uses the default of {} tokens.",
            thousands(DEFAULT_CONTEXT_WINDOW_CAP)
        ))
        .push(
            SettingField::new(HEADLESS_CONTEXT_CAP, "Headless runs")
                .step(1000.0)
                .build(state, t),
        )
        .build(t)
}

/// `128000` as `128,000`.
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

pub(super) fn page(state: &State, t: &Tokens) -> Vec<Block> {
    vec![session_activity(state, t), compaction(state, t)]
}

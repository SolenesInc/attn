use std::collections::BTreeMap;

use attn_protocol::SetSettingMessage;
use attn_ui::Tokens;
use attn_ui::blocks::{Block, Choices, Inline, Section, SettingRow, swatches};
use attn_ui::colors::{SCHEMES, has_overrides};
use attn_ui::system::{Button, Choice, Look, stepper};
use rune::{El, Tx};

use super::controls::{number_field, segmented, setting_switch};
use crate::State;
use crate::action::Action;
use crate::desktop::HOLD_OPENS_OVERVIEW;
use crate::hints;
use crate::sidebar::width;

pub(super) const NARROWEST_TILE_COLS: &str = "narrowest_tile_cols";
const THEME: &str = "theme";
const ID_LABELS: &[(&str, &str)] = &[
    (hints::LANTERN, "Lantern"),
    (hints::NAME_IN_PLACE, "Name in place"),
];
const COLOR_SCHEME: &str = "color_scheme";
const COLOR_OVERRIDES: &str = "color_overrides";
const UI_SCALE: &str = "uiScale";
const GARDEN_SCALE: &str = "gardenScale";
const MIN_SCALE: f32 = 0.7;
pub(crate) const MAX_SCALE: f32 = 1.5;
const SCALE_STEP: f32 = 0.1;

fn stored_scale(settings: &BTreeMap<String, String>, key: &str) -> Option<f32> {
    settings
        .get(key)?
        .parse::<f32>()
        .ok()
        .filter(|s| (MIN_SCALE..=MAX_SCALE).contains(s))
}

/// The app's text size: the daemon's `uiScale` where it lies in the range the app uses, else
/// 1. The view zooms by it.
pub(crate) fn text_scale(settings: &BTreeMap<String, String>) -> f32 {
    stored_scale(settings, UI_SCALE).unwrap_or(1.0)
}

/// The garden's text size: its own `gardenScale`, or the app's while it has none.
pub(crate) fn garden_scale(settings: &BTreeMap<String, String>) -> f32 {
    stored_scale(settings, GARDEN_SCALE).unwrap_or_else(|| text_scale(settings))
}

fn tenths(scale: f32) -> f32 {
    (scale * 10.0).round() / 10.0
}

fn clamp(scale: f32) -> f32 {
    tenths(scale).clamp(MIN_SCALE, MAX_SCALE)
}

fn store(state: &State, key: &str, value: String) {
    let _ = state.daemon.send(&SetSettingMessage {
        key: key.into(),
        value,
        ..SetSettingMessage::new()
    });
}

/// Saves `id` as the `color_scheme` setting; an empty id goes back to attn's own scheme.
pub(crate) fn choose_color_scheme(state: &State, id: &str) {
    store(state, COLOR_SCHEME, id.to_owned());
}

/// ⌘=, ⌘- and ⌘0: a tenth larger or smaller, within 70% to 150%, or back to 100%.
pub(super) fn step_ui_scale(state: &mut State, action: &Action, _: &mut Tx) {
    if let Some(message) = ui_scale_step(&state.facts.settings, action) {
        let _ = state.daemon.send(&message);
    }
}

/// The setting `action` writes to make the app's text a step larger or smaller, or back to
/// its size, when that changes it.
pub(crate) fn ui_scale_step(
    settings: &BTreeMap<String, String>,
    action: &Action,
) -> Option<SetSettingMessage> {
    let scale = text_scale(settings);
    let next = match action {
        Action::UiIncreaseFontSize => clamp(scale + SCALE_STEP),
        Action::UiDecreaseFontSize => clamp(scale - SCALE_STEP),
        _ => 1.0,
    };
    (next != scale).then(|| SetSettingMessage {
        key: UI_SCALE.into(),
        value: format_scale(next),
        ..SetSettingMessage::new()
    })
}

/// As JavaScript's `Number#toString` writes the scale, so both apps store the same text.
fn format_scale(scale: f32) -> String {
    let rounded = tenths(scale);
    if rounded.fract() == 0.0 {
        format!("{}", rounded as i64)
    } else {
        format!("{rounded}")
    }
}

fn garden_step(state: &mut State, by: f32) {
    let from = garden_scale(&state.facts.settings);
    store(state, GARDEN_SCALE, format_scale(clamp(from + by)));
}

fn percent(scale: f32) -> String {
    format!("{}%", (scale * 100.0).round() as i64)
}

fn choose_theme(state: &mut State, theme: &'static str, _: &mut Tx) {
    store(state, THEME, theme.into());
}

fn scheme_colors(t: &Tokens) -> [rune::Color; 7] {
    [
        t.bg_app,
        t.bg_panel,
        t.text_primary,
        t.accent,
        t.brand,
        t.state_working,
        t.state_waiting,
    ]
}

/// The color schemes, each marked with a few of its colors in the current appearance.
fn color_schemes(state: &State, t: &Tokens) -> Block {
    let chosen = crate::color_scheme(&state.facts.settings).id;
    let n = SCHEMES.len();
    Choices::new("settings.color-schemes", "Color scheme")
        .options(SCHEMES.iter().enumerate().map(|(i, scheme)| {
            let id = scheme.id;
            Choice::new(format!("settings.color-scheme.{id}"), scheme.name.clone())
                .leading(swatches(scheme_colors(&scheme.colors(t.dark).ui), t))
                .chosen(id == chosen)
                .build((i, n), t, move |state: &mut State, _: &mut Tx| {
                    choose_color_scheme(state, id)
                })
        }))
        .build()
}

/// While the `color_overrides` setting names a color: a row that says so, with Reset.
fn custom_colors(state: &State, t: &Tokens) -> Option<Block> {
    let overrides = state
        .facts
        .settings
        .get(COLOR_OVERRIDES)
        .map_or("", String::as_str);
    has_overrides(overrides).then(|| {
        SettingRow::new(
            "settings.color-overrides",
            "Custom colors",
            "Your own colors replace parts of the scheme.",
            Button::new("Reset")
                .on_press_tx(|state: &mut State, _: &mut Tx| {
                    store(state, COLOR_OVERRIDES, String::new())
                })
                .build(t)
                .key("settings.color-overrides.reset")
                .label("Reset"),
        )
        .build(t)
    })
}

/// A size between its step buttons, then the button that puts it back when `reset` is set.
fn scale_control(
    key: &str,
    what: &str,
    value: String,
    t: &Tokens,
    down: fn(&mut State, &mut Tx),
    up: fn(&mut State, &mut Tx),
    reset: Option<(&'static str, Press)>,
) -> El {
    Inline::loose()
        .child(El::from(stepper(
            &format!("settings.scale.{key}"),
            &format!("{what} font size"),
            value,
            false,
            t,
            down,
            up,
        )))
        .children(reset.map(|(label, press)| {
            El::from(
                Button::new(label)
                    .look(Look::Quiet)
                    .on_press_tx(press)
                    .build(t)
                    .key(format!("settings.scale.{key}.reset"))
                    .label(label),
            )
        }))
}

type Press = fn(&mut State, &mut Tx);

const THEMES: &[(&str, &str)] = &[("dark", "Dark"), ("light", "Light"), ("system", "System")];

fn reset_ui_scale(state: &mut State, tx: &mut Tx) {
    run(state, Action::UiResetFontSize, tx);
}

fn match_app(state: &mut State, _: &mut Tx) {
    store(state, GARDEN_SCALE, String::new());
}

fn run(state: &mut State, action: Action, tx: &mut Tx) {
    crate::dispatch(state, action, tx);
}

fn theme(state: &State, t: &Tokens) -> Section {
    let theme = match state.facts.settings.get(THEME).map(String::as_str) {
        Some("light") => "light",
        Some("system") => "system",
        _ => "dark",
    };
    Section::new("settings.block.theme", "Theme")
        .description("Choose the appearance and the color scheme of attn and its terminals.")
        .push(Block::control(
            "settings.choice.theme.block",
            segmented(THEME, "Theme preference", THEMES, theme, t, choose_theme),
        ))
        .push(color_schemes(state, t))
        .push_all(custom_colors(state, t))
}

fn font_size(state: &State, t: &Tokens) -> Section {
    let settings = &state.facts.settings;
    let scale = text_scale(settings);
    let garden = stored_scale(settings, GARDEN_SCALE);
    let keys = |action: &Action| {
        state
            .keymap
            .binding(action)
            .map(|b| state.keymap.label(b))
            .unwrap_or_default()
    };
    let app_copy = format!(
        "The whole interface, including terminals. Also adjustable with {} and {}.",
        keys(&Action::UiIncreaseFontSize),
        keys(&Action::UiDecreaseFontSize)
    );
    Section::new("settings.block.font-size", "Font Size")
        .description(
            "Scale text across attn. The garden can use its own size, independent of the rest of the app.",
        )
        .push(
            SettingRow::new(
                "settings.row.scale.app",
                "App",
                app_copy,
                scale_control(
                    "app",
                    "app",
                    percent(scale),
                    t,
                    |s, tx| run(s, Action::UiDecreaseFontSize, tx),
                    |s, tx| run(s, Action::UiIncreaseFontSize, tx),
                    (scale != 1.0).then_some(("Reset", reset_ui_scale as Press)),
                ),
            )
            .build(t),
        )
        .push(
            SettingRow::new(
                "settings.row.scale.garden",
                "The garden",
                "Seeds, plots, and their logs. Matches the app size until you change it.",
                scale_control(
                    "garden",
                    "garden",
                    garden.map_or_else(|| "Match app".to_owned(), percent),
                    t,
                    |s, _| garden_step(s, -SCALE_STEP),
                    |s, _| garden_step(s, SCALE_STEP),
                    garden
                        .is_some()
                        .then_some(("Match app", match_app as Press)),
                ),
            )
            .build(t),
        )
}

fn choose_id_labels(state: &mut State, drawing: &'static str, _: &mut Tx) {
    store(state, hints::DRAWING, drawing.into());
}

fn id_labels(state: &State, t: &Tokens) -> Section {
    let keys = state
        .keymap
        .binding(&Action::TerminalLabelIds)
        .map(|b| state.keymap.label(b));
    let copy = match keys {
        Some(keys) => format!("How the ids on screen look while you hold {keys}."),
        None => "How the ids on screen look while you hold their keys.".to_owned(),
    };
    Section::new("settings.block.id-labels", "Id Labels")
        .description(copy)
        .push(
            SettingRow::new(
                "settings.row.id-labels",
                "Label style",
                "Lantern lights up each id and lists them all in a corner. Name in place writes each name over its id.",
                segmented(
                    hints::DRAWING,
                    "Label style",
                    ID_LABELS,
                    hints::drawing(&state.facts.settings),
                    t,
                    choose_id_labels,
                ),
            )
            .build(t),
        )
}

fn layout(state: &State, t: &Tokens) -> Section {
    let draft = state.window.settings.autosave.draft(width::SETTING);
    let default_width = (width::DEFAULT as i64).to_string();
    let use_default = (!draft.is_empty() && draft != default_width).then(|| {
        El::from(
            Button::new("Use default")
                .look(Look::Quiet)
                .on_press_tx(|state: &mut State, tx: &mut Tx| {
                    super::autosave::apply(state, width::SETTING, "", tx)
                })
                .build(t)
                .key("settings.sidebar-width.default")
                .label("Use default"),
        )
    });
    Section::new("settings.block.layout", "Layout")
        .description("How wide tiles and the sidebar get.")
        .push(
            SettingRow::new(
                "settings.row.narrowest-tile-cols",
                "Narrowest tile width (columns)",
                "A tile never gets narrower than this. A narrower width fits more tiles in the row.",
                number_field(
                    state,
                    NARROWEST_TILE_COLS,
                    "Narrowest tile width (columns)",
                    "",
                    1.0,
                    t,
                ),
            )
            .build(t),
        )
        .push(
            SettingRow::new(
                "settings.row.sidebar-width",
                "Sidebar width (pixels)",
                format!(
                    "The open sidebar's width, from {} to {}. Empty uses the default of {}.",
                    width::MIN as i64,
                    width::MAX as i64,
                    width::DEFAULT as i64
                ),
                Inline::loose()
                    .child(number_field(
                        state,
                        width::SETTING,
                        "Sidebar width (pixels)",
                        &default_width,
                        1.0,
                        t,
                    ))
                    .children(use_default),
            )
            .build(t),
        )
}

fn hold_opens_overview(state: &State, t: &Tokens) -> Section {
    const TITLE: &str = "Open by holding the keys that move between tiles";
    Section::new("settings.block.overview", "Overview")
        .description("The desktop's whole row, drawn small.")
        .push(
            SettingRow::new(
                "settings.row.hold-opens-overview",
                TITLE,
                "Opens when a step leaves the screen, and letting go goes to the tile you picked.",
                setting_switch(state, HOLD_OPENS_OVERVIEW, TITLE, t),
            )
            .build(t),
        )
}

pub(super) fn page(state: &State, t: &Tokens) -> Vec<Block> {
    vec![
        theme(state, t).build(t),
        font_size(state, t).build(t),
        id_labels(state, t).build(t),
        layout(state, t).build(t),
        hold_opens_overview(state, t).build(t),
    ]
}

use std::collections::BTreeMap;

use attn_protocol::SetSettingMessage;
use attn_ui::Tokens;
use attn_ui::colors::{SCHEMES, has_overrides};
use attn_ui::type_;
use rune::tok::space;
use rune::{El, Tx, col, el, row, text};
use rune_kit::RadioItem;

use super::pages::{NUMBER_FIELD, block, button, named_button, row_card, segmented, setting_field};
use crate::State;
use crate::action::Action;
use crate::sidebar::width;

pub(super) const NARROWEST_TILE_COLS: &str = "narrowest_tile_cols";
const THEME: &str = "theme";
const COLOR_SCHEME: &str = "color_scheme";
const COLOR_OVERRIDES: &str = "color_overrides";
const UI_SCALE: &str = "uiScale";
const GARDEN_SCALE: &str = "gardenScale";
const MIN_SCALE: f32 = 0.7;
const MAX_SCALE: f32 = 1.5;
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

const SWATCHES: usize = 7;

fn swatches(t: &Tokens) -> [rune::Color; SWATCHES] {
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

/// The color schemes, each with a few of its colors in the current appearance, and while the
/// `color_overrides` setting names a color, a mark that says so with Reset.
fn color_schemes(state: &State, t: &Tokens) -> El {
    let settings = &state.facts.settings;
    let chosen = crate::color_scheme(settings).id;
    let kit = attn_ui::kit(t);
    let n = SCHEMES.len();
    let list = col()
        .key("settings.color-schemes")
        .role("radiogroup")
        .label("Color scheme")
        .focus_group()
        .gap(space::S2)
        .children(SCHEMES.iter().enumerate().map(|(i, scheme)| {
            let id = scheme.id;
            let strip = row()
                .radius(4.0)
                .clip_rounded(4.0)
                .border(1.0, t.border)
                .children(
                    swatches(&scheme.colors(t.dark).ui)
                        .map(|color| el().size(14.0, 14.0).fill(color)),
                )
                .a11y_hidden();
            RadioItem::new(scheme.name.clone(), id == chosen)
                .on_select_tx(move |state: &mut State, _: &mut Tx| {
                    store(state, COLOR_SCHEME, id.to_owned())
                })
                .build(kit)
                .key(format!("settings.color-scheme.{id}"))
                .position_in_set(i + 1, n)
                .w_full()
                .items_center()
                .child(el().grow(1.0).min_w(space::S6))
                .child(strip)
        }));
    let overrides = settings.get(COLOR_OVERRIDES).map_or("", String::as_str);
    let custom = has_overrides(overrides).then(|| {
        row()
            .key("settings.color-overrides")
            .gap(space::S2)
            .items_center()
            .child(
                text("Custom colors")
                    .font(type_::LABEL)
                    .color(t.text_secondary),
            )
            .child(button(
                "settings.color-overrides.reset".into(),
                "Reset",
                t,
                |state, _| store(state, COLOR_OVERRIDES, String::new()),
            ))
    });
    col()
        .gap(space::S3)
        .child(row().child(list))
        .children(custom)
}

fn scale_control(
    key: &str,
    what: &str,
    value: String,
    t: &Tokens,
    down: fn(&mut State, &mut Tx),
    up: fn(&mut State, &mut Tx),
    reset: Option<(&'static str, Press)>,
) -> El {
    row()
        .key(format!("settings.scale.{key}"))
        .gap(space::S2)
        .items_center()
        .child(named_button(
            format!("settings.scale.{key}.down"),
            &format!("Decrease {what} font size"),
            "−",
            t,
            down,
        ))
        .child(
            text(value.clone())
                .key(format!("settings.scale.{key}.value"))
                .role("status")
                .label(value)
                .font(type_::LABEL)
                .color(t.text_primary),
        )
        .child(named_button(
            format!("settings.scale.{key}.up"),
            &format!("Increase {what} font size"),
            "+",
            t,
            up,
        ))
        .children(
            reset.map(|(label, press)| {
                button(format!("settings.scale.{key}.reset"), label, t, press)
            }),
        )
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

pub(super) fn page(state: &State, t: &Tokens) -> Vec<El> {
    let settings = &state.facts.settings;
    let theme = match settings.get(THEME).map(String::as_str) {
        Some("light") => "light",
        Some("system") => "system",
        _ => "dark",
    };
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
    let sidebar_width = &state.window.settings.autosave;
    let sidebar_draft = sidebar_width.draft(width::SETTING);
    let default_width = (width::DEFAULT as i64).to_string();
    let use_default = (!sidebar_draft.is_empty() && sidebar_draft != default_width).then(|| {
        button(
            "settings.sidebar-width.default".into(),
            "Use default",
            t,
            |state, tx| super::autosave::apply(state, width::SETTING, "", tx),
        )
    });
    vec![
        block(
            "theme",
            "Theme",
            "Choose the appearance and the color scheme of attn and its terminals.",
            t,
            vec![
                segmented(THEME, "Theme preference", THEMES, theme, t, choose_theme),
                color_schemes(state, t),
            ],
        ),
        block(
            "font-size",
            "Font Size",
            "Scale text across attn. The garden can use its own size, independent of the rest of the app.",
            t,
            vec![
                row_card(
                    "App",
                    &app_copy,
                    t,
                    scale_control(
                        "app",
                        "app",
                        percent(scale),
                        t,
                        |s, tx| run(s, Action::UiDecreaseFontSize, tx),
                        |s, tx| run(s, Action::UiIncreaseFontSize, tx),
                        (scale != 1.0).then_some(("Reset", reset_ui_scale as Press)),
                    ),
                ),
                row_card(
                    "The garden",
                    "Seeds, plots, and their logs. Matches the app size until you change it.",
                    t,
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
                ),
            ],
        ),
        block(
            "layout",
            "Layout",
            "How wide tiles and the sidebar get.",
            t,
            vec![
                row_card(
                    "Narrowest tile width (columns)",
                    "A tile never gets narrower than this. A narrower width fits more tiles in the row.",
                    t,
                    setting_field(
                        state,
                        NARROWEST_TILE_COLS,
                        "Narrowest tile width (columns)",
                        "",
                        NUMBER_FIELD,
                        t,
                    ),
                ),
                row_card(
                    "Sidebar width (pixels)",
                    &format!(
                        "The open sidebar's width, from {} to {}. Empty uses the default of {}.",
                        width::MIN as i64,
                        width::MAX as i64,
                        width::DEFAULT as i64
                    ),
                    t,
                    row()
                        .gap(space::S2)
                        .items_center()
                        .children(use_default)
                        .child(setting_field(
                            state,
                            width::SETTING,
                            "Sidebar width (pixels)",
                            &default_width,
                            NUMBER_FIELD,
                            t,
                        )),
                ),
            ],
        ),
    ]
}

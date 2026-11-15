use crate::desktop::layout::TILE_MIN_WIDTH;

/// The setting that holds the sidebar's width in points; empty for the default.
pub const SETTING: &str = "sidebar_width";
pub const MIN: f32 = 200.0;
pub const DEFAULT: f32 = 240.0;
pub const MAX: f32 = 600.0;
pub const STEP: f32 = 40.0;

/// The width the setting asks for, before the window limits it.
pub fn stored(setting: Option<&str>) -> f32 {
    setting
        .and_then(|s| s.trim().parse::<f32>().ok())
        .filter(|px| *px != 0.0 && px.is_finite())
        .unwrap_or(DEFAULT)
}

/// The widest the sidebar goes in a window `window` points wide: tiles keep their minimum.
pub fn widest(window: f32) -> f32 {
    MAX.min(window - TILE_MIN_WIDTH)
}

/// The width the sidebar shows at: the asked width within its limits.
pub fn shown(px: f32, window: f32) -> f32 {
    px.round().min(widest(window)).max(MIN)
}

/// What the width label says while the width changes.
pub fn label(px: f32, window: f32) -> String {
    if px <= MIN {
        format!("{px}px is the narrowest")
    } else if px < widest(window) {
        format!("{px}px")
    } else if px == MAX {
        format!("{px}px is the widest")
    } else {
        format!("{px}px is the widest: tiles keep {TILE_MIN_WIDTH}px")
    }
}

#[derive(Clone, Debug, PartialEq)]
pub enum Step {
    To(f32),
    /// The sidebar is at its limit that way; the message names it.
    Limit(String),
}

/// One width step wider or narrower from `current`, or the limit that stops it.
pub fn step(current: f32, wider: bool, window: f32) -> Step {
    let shown = shown(current, window);
    let widest = widest(window);
    if !wider {
        if shown <= MIN {
            return Step::Limit(format!("The sidebar can't go below {MIN}px."));
        }
        return Step::To((shown - STEP).max(MIN));
    }
    if shown < widest {
        return Step::To((shown + STEP).min(widest));
    }
    Step::Limit(if widest == MAX {
        format!("The sidebar can't pass {widest}px.")
    } else {
        format!(
            "The sidebar can't pass {widest}px with the window at {window}px: tiles keep {TILE_MIN_WIDTH}px."
        )
    })
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn a_step_moves_by_forty_points_within_the_limits_and_names_the_limit_it_meets() {
        for (current, wider, window, then) in [
            (240.0, true, 1600.0, Step::To(280.0)),
            (240.0, false, 1600.0, Step::To(200.0)),
            (220.0, false, 1600.0, Step::To(200.0)),
            (
                200.0,
                false,
                1600.0,
                Step::Limit("The sidebar can't go below 200px.".into()),
            ),
            (580.0, true, 1600.0, Step::To(600.0)),
            (
                600.0,
                true,
                1600.0,
                Step::Limit("The sidebar can't pass 600px.".into()),
            ),
            (240.0, true, 760.0, Step::To(280.0)),
            (
                280.0,
                true,
                760.0,
                Step::Limit(
                    "The sidebar can't pass 280px with the window at 760px: tiles keep 480px."
                        .into(),
                ),
            ),
            (500.0, false, 760.0, Step::To(240.0)),
        ] {
            assert_eq!(
                step(current, wider, window),
                then,
                "{current} wider={wider} in {window}"
            );
        }
    }

    #[test]
    fn the_label_says_when_the_width_meets_a_limit() {
        assert_eq!(label(200.0, 1600.0), "200px is the narrowest");
        assert_eq!(label(320.0, 1600.0), "320px");
        assert_eq!(label(600.0, 1600.0), "600px is the widest");
        assert_eq!(label(280.0, 760.0), "280px is the widest: tiles keep 480px");
    }

    #[test]
    fn an_unset_or_unreadable_setting_reads_as_the_default() {
        assert_eq!(stored(None), DEFAULT);
        assert_eq!(stored(Some("")), DEFAULT);
        assert_eq!(stored(Some("wide")), DEFAULT);
        assert_eq!(stored(Some("320")), 320.0);
    }
}

/// How long the width label stays after a step, as in the Tauri app.
const LABEL_SECONDS: f64 = 1.5;

/// The window's part in the sidebar's width: the window's own width, a width being dragged
/// or sent and not yet back from the daemon, and how long the width label shows.
#[derive(Default)]
pub struct Width {
    pub window: f32,
    pub draft: Option<f32>,
    pub dragging: bool,
    pub label_until: Option<f64>,
}

pub(crate) fn of(state: &crate::State) -> f32 {
    let w = &state.window.width;
    w.draft.unwrap_or_else(|| {
        let px = stored(state.facts.settings.get(SETTING).map(String::as_str));
        if w.window > 0.0 {
            shown(px, w.window)
        } else {
            px.clamp(MIN, MAX)
        }
    })
}

/// Once the daemon holds the width this window sent, the window stops showing its own.
pub(crate) fn follow(state: &mut crate::State) {
    let px = stored(state.facts.settings.get(SETTING).map(String::as_str));
    let w = &mut state.window.width;
    if !w.dragging && w.draft == Some(px) {
        w.draft = None;
    }
}

fn send(state: &crate::State, px: f32) {
    let _ = state.daemon.send(&attn_protocol::SetSettingMessage {
        key: SETTING.into(),
        value: px.to_string(),
        ..attn_protocol::SetSettingMessage::new()
    });
}

/// Back to the default width, as a double-click on the edge does.
fn reset(state: &mut crate::State) {
    let _ = state.daemon.send(&attn_protocol::SetSettingMessage {
        key: SETTING.into(),
        value: String::new(),
        ..attn_protocol::SetSettingMessage::new()
    });
    state.window.width.draft = Some(DEFAULT);
}

/// The width keys: one step wider or narrower, or a notice naming the limit.
pub(crate) fn step_by_key(state: &mut crate::State, wider: bool, tx: &mut rune::Tx) {
    if state.window.sidebar_hidden {
        state.window.notify(
            "The sidebar is hidden. Show it to change its width.".into(),
            tx,
        );
        return;
    }
    match step(of(state), wider, state.window.width.window) {
        Step::Limit(message) => state.window.notify(message, tx),
        Step::To(px) => {
            send(state, px);
            let until = tx.clock() + LABEL_SECONDS;
            let w = &mut state.window.width;
            w.draft = Some(px);
            w.label_until = Some(until);
            tx.wake_at(until);
        }
    }
}

/// The edge the sidebar is dragged wider or narrower by: the width follows the pointer
/// within the limits, Escape puts it back, the release sends it, and a double-click goes back
/// to the default.
pub(crate) fn handle(t: &attn_ui::Tokens) -> rune::El {
    rune::el()
        .key("sidebar.resize")
        .role("separator")
        .label("Resize the sidebar")
        .pin(None, Some(0.0), Some(-3.0), Some(0.0))
        .w(6.0)
        .cursor(rune::Cursor::ResizeColumn)
        .hover(t.border)
        .on_drag(
            |state: &mut crate::State, d: &mut rune::Drag, _: &mut rune::Tx| match d.phase {
                rune::Phase::Down => {
                    d.data = of(state);
                    let w = &mut state.window.width;
                    w.dragging = true;
                    w.draft = Some(d.data);
                }
                rune::Phase::Move => {
                    let w = &mut state.window.width;
                    w.draft = Some(shown(d.data + d.dx, w.window));
                }
                rune::Phase::Up => {
                    let stored = stored(state.facts.settings.get(SETTING).map(String::as_str));
                    let w = &mut state.window.width;
                    w.dragging = false;
                    let draft = w
                        .draft
                        .take()
                        .filter(|px| !d.cancelled() && *px != d.data && *px != stored);
                    if let Some(px) = draft {
                        state.window.width.draft = Some(px);
                        send(state, px);
                    } else if d.clicks == 2 && !d.cancelled() {
                        reset(state);
                    }
                }
            },
        )
}

/// The width beside the edge while it is dragged and for a moment after a step.
pub(crate) fn label_view(state: &crate::State, t: &attn_ui::Tokens) -> Option<rune::El> {
    let w = &state.window.width;
    (w.dragging || w.label_until.is_some()).then(|| {
        let label = rune::text(label(of(state), w.window))
            .key("sidebar.width-label")
            .font(rune::tok::type_::MONO_CAPTION)
            .color(t.text_secondary)
            .absolute(rune::tok::space::S2, rune::tok::space::S2)
            .px(rune::tok::space::S2)
            .py(2.0);
        rune_kit::popover::raised(attn_ui::kit(t), label, 4.0)
    })
}

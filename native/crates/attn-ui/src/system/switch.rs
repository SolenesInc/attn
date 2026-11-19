use rune::{Tx, el, row};

use super::{Control, Radius, Size, Space, Type, disabled, door, focus, kit, layers, words};
use crate::Tokens;

const TRACK: (f32, f32) = (36.0, 20.0);
const KNOB: f32 = 14.0;
const KNOB_TRAVEL: (f32, f32) = (3.0, 19.0);

/// A switch, with its state word beside it when `show_state`.
pub fn switch<S: 'static>(
    key: impl Into<String>,
    label: &str,
    on: bool,
    show_state: bool,
    disabled: bool,
    t: &Tokens,
    toggle: impl Fn(&mut S, &mut Tx) + 'static,
) -> Control {
    let (track_fill, edge, knob_ink) = match (disabled, on) {
        (true, true) => (t.text_tertiary, t.text_tertiary, t.bg_panel),
        (false, true) => (t.warning, t.warning, crate::warning_ink(t)),
        (_, false) => (t.bg_button, t.border_hover, t.text_tertiary),
    };
    let knob = door::corner(el().key("thumb").size(KNOB, KNOB), Radius::Round)
        .absolute(
            if on { KNOB_TRAVEL.1 } else { KNOB_TRAVEL.0 },
            KNOB_TRAVEL.0,
        )
        .fill(knob_ink)
        .token_motion(kit(t).motion.control);
    let track = door::corner(el().key("control").size(TRACK.0, TRACK.1), Radius::Round)
        .fill(track_fill)
        .border(1.0, edge)
        .child(knob)
        .a11y_hidden();
    let switch = row()
        .key(key.into())
        .role("switch")
        .label(label.to_owned())
        .checked(on)
        .focusable(true)
        .self_start()
        .items_center();
    let state_word = show_state.then(|| {
        words(
            if on { "On" } else { "Off" },
            Type::Control,
            t.text_secondary,
        )
        .a11y_hidden()
    });
    if disabled {
        let switch = switch.child(track).children(state_word);
        return Control::new(
            disabled::dim(door::corner(door::gap(switch, Space::S8), Radius::Round)),
            Size::Compact,
        );
    }
    let toggle = std::rc::Rc::new(toggle);
    let on_track = toggle.clone();
    let track = layers::pressable(track, track_fill, t)
        .on_press_tx(move |s: &mut S, tx: &mut Tx| on_track(s, tx));
    let switch = switch
        .child(track)
        .children(state_word)
        .on_press_tx(move |s: &mut S, tx: &mut Tx| toggle(s, tx));
    let switch = door::corner(door::gap(switch, Space::S8), Radius::Round);
    Control::new(focus::ring(switch, t), Size::Compact)
}

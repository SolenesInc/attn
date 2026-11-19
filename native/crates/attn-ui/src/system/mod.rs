//! attn's design system: the spacing scale, the type roles, the corners and the controls every
//! view shares. Only this crate can make a step of the scale, so a view cannot invent one.

mod button;
mod choice;
mod control;
mod disabled;
mod disclosure;
pub(crate) mod door;
mod expander;
mod field_box;
mod focus;
mod icon_box;
mod layers;
mod press_area;
mod radius;
mod segmented;
mod size;
mod space;
mod stepper;
mod switch;
mod tag;
mod trigger;
mod type_role;

pub use button::{Button, Look};
pub use choice::{Choice, choices};
pub use control::Control;
pub use disclosure::disclosure;
pub use expander::{expander, opens_in_place};
pub use field_box::{area_box, line_box, number_box};
pub use icon_box::icon_box;
pub use layers::{hover_layer, pressed_layer};
pub use press_area::press_area;
pub use radius::Radius;
pub use segmented::{Segment, Segmented};
pub use size::Size;
pub(crate) use space::Space;
pub use stepper::{arrows, stepper};
pub use switch::switch;
pub use tag::{Tone, tag};
pub use trigger::{Trigger, TriggerLook};
pub use type_role::{Type, words};

use crate::Tokens;

/// The kit attn's controls build on: the warm accent as its brand and its focus, placeholders
/// dimmed.
pub(crate) fn kit(t: &Tokens) -> &'static rune_kit::Kit {
    crate::kit(&Tokens {
        brand: t.warning,
        border_focus: t.warning,
        text_tertiary: t.text_dimmed,
        ..*t
    })
}

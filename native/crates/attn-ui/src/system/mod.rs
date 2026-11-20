//! attn's design system: the spacing scale, the type roles, the corners and the controls every
//! view shares. Only this crate can make a step of the scale, so a view cannot invent one.

mod button;
mod chip;
mod choice;
mod control;
mod disabled;
mod disclosure;
pub(crate) mod door;
mod dot;
mod drop_zone;
mod expander;
mod field_box;
pub(crate) mod focus;
mod group_head;
mod icon_box;
mod icon_button;
mod icon_size;
pub(crate) mod layers;
mod list_row;
mod press_area;
mod radius;
mod search_field;
mod segmented;
mod size;
mod space;
mod stepper;
mod switch;
mod tag;
mod trigger;
mod type_role;

pub use button::{Button, Look};
pub use chip::{chip_button, count_badge, keycaps, keys_hint, number_chip, slot_badge};
pub use choice::{Choice, checks, choices};
pub use control::Control;
pub use disclosure::disclosure;
pub use dot::{DotShape, DotSize, dot};
pub use drop_zone::drop_zone;
pub use expander::{expander, opens_in_place};
pub use field_box::{FieldState, area_box, code_box, line_box, number_box};
pub use focus::RING_REACH;
pub use group_head::GroupHead;
pub use icon_box::icon_box;
pub use icon_button::{IconButton, icon_button};
pub use icon_size::{IconSize, glyph, icon};
pub use layers::{hover_layer, pressed_layer};
pub use list_row::{ListRow, Reveal};
pub use press_area::press_area;
pub use radius::Radius;
pub use search_field::search_field;
pub use segmented::{Segment, Segmented};
pub use size::Size;
pub(crate) use space::Space;
pub use stepper::{arrows, stepper};
pub use switch::switch;
pub use tag::{Tone, pill, tag};
pub use trigger::{Trigger, TriggerLook};
pub(crate) use type_role::words_and_code;
pub use type_role::{Type, verbatim, words};
pub(crate) use type_role::{lines_at, width_of};

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

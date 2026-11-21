//! attn's design system: the spacing scale, the type roles, the corners and the controls every
//! view shares. Only this crate can make a step of the scale, so a view cannot invent one.

mod ask;
mod attachment;
mod button;
mod check;
mod chip;
mod choice;
mod composer;
mod control;
pub(crate) mod disabled;
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
mod link;
mod list_row;
mod mark;
mod pager;
mod press_area;
mod radius;
mod reading;
mod said;
mod search_field;
mod segmented;
mod size;
mod space;
mod split;
mod stepper;
mod switch;
mod tag;
mod trigger;
mod type_role;

pub use ask::{AskChip, ask_mark};
pub use attachment::{drop_overlay, file_chip, image_tile};
pub use button::{Button, Look};
pub use check::Check;
pub use chip::{
    chip_button, chip_edge, chip_width, count_badge, keycaps, keycaps_width, keys_hint,
    keys_hint_width, number_chip, slot_badge,
};
pub use choice::{Choice, answers, checks, choices};
pub use composer::{composer, text_input};
pub use control::Control;
pub use disclosure::disclosure;
pub use dot::{DotShape, DotSize, RINGED_DOT, dot, ringed_dot, struck_dot};
pub use drop_zone::drop_zone;
pub use expander::{expander, opens_in_place};
pub use field_box::{FieldState, area_box, code_box, line_box, line_box_width, number_box};
pub use focus::{RING_REACH, ring_scroll, ring_scroll_beside_bar};
pub use group_head::GroupHead;
pub use icon_box::icon_box;
pub use icon_button::{IconButton, icon_button};
pub use icon_size::{IconSize, glyph, icon};
pub use layers::{hover_layer, pressed_layer};
pub use link::Link;
pub use list_row::{ListRow, Reveal};
pub use mark::{code_span, dot_on_line, grounded, marked, meter, with_command};
pub use pager::pager;
pub use press_area::{pick_area, press_area};
pub use radius::Radius;
pub use reading::{
    Reading, document_style, note_style, passage_mark, read_as, read_aside, written_as,
    written_cell,
};
pub use said::{Part, Said};
pub use search_field::{SEARCH_FLOOR, search_field};
pub use segmented::{Segment, Segmented};
pub use size::Size;
pub(crate) use space::Space;
pub use split::{Join, split};
pub use stepper::{arrows, stepper};
pub use switch::{Beside, switch};
pub use tag::{Tone, mark_chip, pill, tag, tag_width};
pub use trigger::{Trigger, TriggerLook};
pub(crate) use type_role::lines_at;
pub use type_role::{Type, width_of, words};

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

/// The menu a row's own button opens, such as a sidebar row's actions: the standard menu, near
/// its row, its separators across its whole width.
pub fn row_menu(key: impl Into<String>) -> rune_kit::Menu {
    door::row_menu(key.into())
}

/// `e` lifted over what is under it, on the raised surface with `radius` corners and a shadow.
pub fn raised(e: rune::El, radius: Radius, t: &Tokens) -> rune::El {
    door::raised(e, radius, t)
}

/// `e` centred over the window with the scale's `S8` round it, no larger than the window leaves
/// it, its scrolling part shrinking: a popover that fits beside its anchor on neither side.
pub fn over_window(e: rune::El) -> rune::El {
    door::inset(rune::col(), Space::S8, Space::S8)
        .absolute(0.0, 0.0)
        .w_full()
        .h_full()
        .center()
        .child(e.shrink().min_h(0.0).max_w_frac(1.0))
}

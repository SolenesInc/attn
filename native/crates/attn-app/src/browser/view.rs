use attn_client::TileKey;
use attn_ui::{GlyphButton, Tokens};
use rune::tok::{space, type_};
use rune::{El, Tx, col, edit_access, text, text_input};
use rune_kit::ButtonVariant;

use crate::State;

/// The address field's type: the Tauri app's 11 px monospace.
const ADDRESS_TYPE: rune::TextStyle = rune::TextStyle {
    size: 11.0,
    ..type_::MONO_CAPTION
};
const ADDRESS_HEIGHT: f32 = 24.0;

pub(crate) fn page_key(page: &TileKey) -> String {
    format!("tile.{}.page", page.tile)
}

pub(crate) fn address_key(page: &TileKey) -> String {
    format!("tile.{}.address", page.tile)
}

/// The tile's address field, in place of its title: it follows the page, and Enter takes the
/// page to what it says.
pub(crate) fn address_field(
    browsers: &super::Browsers,
    page: &TileKey,
    stored: &str,
    t: &Tokens,
) -> El {
    let shown = browsers
        .address(page)
        .cloned()
        .unwrap_or_else(|| rune::TextEdit::new(stored));
    let (edited, submitted) = (page.clone(), page.clone());
    text_input(&shown, ADDRESS_TYPE)
        .key(address_key(page))
        .single_line()
        .label("Browser address")
        .on_edit(edit_access(move |state: &mut State| {
            state.browser.address_mut(&edited)
        }))
        .on_submit(move |state: &mut State, tx: &mut Tx| {
            super::go_to_address(state, &submitted);
            tx.blur();
        })
        .grow(1.0)
        .min_w(0.0)
        .h(ADDRESS_HEIGHT)
        .px(space::S2)
        .items_center()
        .radius(5.0)
        .border(1.0, t.border)
        .fill(t.bg_input)
        .color(t.text_primary)
}

/// The header's button that loads the page again.
pub(crate) fn reload_button(page: &TileKey, t: &Tokens) -> El {
    let page = page.clone();
    let key = format!("tile.{}.reload", page.tile);
    GlyphButton::new("↻", "Reload browser")
        .variant(ButtonVariant::GhostFaint)
        .font(type_::LABEL)
        .side(type_::LABEL.line_px())
        .on_press_tx(move |_: &mut State, _: &mut Tx| super::reload(&page))
        .build(attn_ui::kit(t))
        .key(key)
        .px(space::S1)
        .radius(4.0)
}

/// Where the tile's page shows. The page is a native view the host places over this area in the
/// main window; a tile window, or a platform where no engine runs pages, says why none shows.
pub(crate) fn page_area(page: &TileKey, popped: bool, t: &Tokens) -> El {
    let area = col()
        .key(page_key(page))
        .w_full()
        .grow(1.0)
        .min_h(0.0)
        .on_layout_tx(|_: &mut State, _, tx: &mut Tx| tx.live_only())
        .on_drawn_layout_tx(|_: &mut State, _, tx: &mut Tx| tx.live_only());
    let why = if !super::hosts_pages() {
        "Browser tiles are not available in this app on this platform yet."
    } else if popped {
        "This window does not show browser pages yet. Bring the tile back to its desktop to see its page."
    } else {
        return area;
    };
    area.items_center()
        .justify_center()
        .padding(space::S6)
        .child(text(why).font(type_::BODY).color(t.text_secondary).wrap())
}

use attn_client::TileKey;
use attn_ui::Tokens;
use attn_ui::blocks::{Page, Prose};
use rune::{El, Tx, col};
use rune_kit::TextInput;

use crate::State;

pub(crate) fn page_key(page: &TileKey) -> String {
    format!("tile.{}.page", page.tile)
}

pub(crate) fn address_key(page: &TileKey) -> String {
    format!("tile.{}.address", page.tile)
}

/// The tile's address, which its header edits in place of its title: it follows the page, and
/// Enter takes the page to what it says.
pub(crate) fn address_input(browsers: &super::Browsers, page: &TileKey, stored: &str) -> TextInput {
    let shown = browsers
        .address(page)
        .cloned()
        .unwrap_or_else(|| rune::TextEdit::new(stored));
    let (edited, submitted) = (page.clone(), page.clone());
    TextInput::new(&shown)
        .key(address_key(page))
        .label("Browser address")
        .on_edit(move |state: &mut State| state.browser.address_mut(&edited))
        .on_submit(move |state: &mut State, tx: &mut Tx| {
            super::go_to_address(state, &submitted);
            tx.blur();
        })
}

/// Where a page shows: the host places the page's native view over this area.
pub(crate) fn page_slot(key: String) -> El {
    col()
        .key(key)
        .w_full()
        .grow(1.0)
        .min_h(0.0)
        .on_layout_tx(|_: &mut State, _, tx: &mut Tx| tx.live_only())
        .on_drawn_layout_tx(|_: &mut State, _, tx: &mut Tx| tx.live_only())
}

/// Where the tile's page shows. The page is a native view the host places over this area, in
/// the main window or the tile window the tile popped out into; where no engine runs pages, the
/// area says so.
pub(crate) fn page_area(page: &TileKey, t: &Tokens) -> El {
    let area = page_slot(page_key(page));
    if super::hosts_pages() {
        return area;
    }
    let key = format!("tile.{}.unavailable", page.tile);
    area.child(
        Page::new()
            .push(
                Prose::lead(
                    format!("{key}.why"),
                    "Browser tiles are not available in this app on this platform yet.",
                )
                .build(t),
            )
            .build(key, t),
    )
}

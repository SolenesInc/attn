use rune::{El, col, el};

use crate::{Backdrop, Tokens};

/// A finder's box at most this wide: `.palette-box`.
pub const WIDTH: f32 = 560.0;

/// A finder over its parent, as the Tauri app's `.palette` lays one out: the finder's wash
/// behind, and `panel` centered, at most `WIDTH` wide, a tenth of the parent's width down.
pub fn layer(t: &Tokens, key: impl Into<String>, panel: El) -> El {
    let key = key.into();
    el().key(key.clone())
        .absolute(0.0, 0.0)
        .w_full()
        .h_full()
        .child(Backdrop::finder(t).draw(format!("{key}.scrim")))
        .child(
            col()
                .w_full()
                .h_full()
                .child(el().w_full().aspect_ratio(10.0))
                .child(
                    col()
                        .w_full()
                        .grow(1.0)
                        .min_h(0.0)
                        .px(16.0)
                        .pb(16.0)
                        .items_center()
                        .child(panel.w_full().max_w(WIDTH)),
                ),
        )
}

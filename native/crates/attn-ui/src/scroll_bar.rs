//! The Tauri app's scroll bars. Its styled `::-webkit-scrollbar` is a scroller WebKit never
//! overlays, so it shows whenever the content overflows, whatever the system "Show scroll bars"
//! setting; the Markdown reader's thin bar is the system's own, and follows that setting.

use rune_kit::ScrollBarStyle;

use crate::Tokens;

/// `App.css`'s `::-webkit-scrollbar`, every scrolling box's bar unless it styles its own: an 8
/// point `scrollbar_thumb` thumb with round ends, `scrollbar_hover` under the pointer.
pub fn scroll_bar(t: &Tokens) -> ScrollBarStyle {
    ScrollBarStyle::native(t.scrollbar_thumb, t.scrollbar_hover)
        .webkit_parts(8.0, 0.0, 0.0)
        .inside()
        .always()
}

/// A dialog list's bar, `.shortcuts-body` and `.shortcut-editor-body`: the 8 point bar with its
/// thumb drawn 2 points in from every side, `border`, `border_hover` under the pointer.
pub fn dialog_scroll_bar(t: &Tokens) -> ScrollBarStyle {
    ScrollBarStyle::native(t.border, t.border_hover)
        .webkit_parts(8.0, 2.0, 0.0)
        .inside()
        .always()
}

/// The Markdown reader's bar, `.md-reader { scrollbar-width: thin; scrollbar-color:
/// var(--color-border) transparent }`: the system's thin scroller, a 7 point `border` thumb,
/// shown as the system "Show scroll bars" setting says.
pub fn document_scroll_bar(t: &Tokens) -> ScrollBarStyle {
    ScrollBarStyle::from_kit(crate::kit(t))
        .thumb(t.border)
        .thumb_hover(t.border_hover)
        .width(7.0)
        .hover_width(7.0)
}

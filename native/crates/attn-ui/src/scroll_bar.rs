//! The Tauri app's scroll bars. Its styled `::-webkit-scrollbar` is a scroller WebKit never
//! overlays, so it shows whenever the content overflows, whatever the system "Show scroll bars"
//! setting, and takes its width from the box while it does; the Markdown reader's thin bar is the
//! system's own, and follows that setting.

use rune_kit::ScrollBarStyle;

use crate::Tokens;

/// `App.css`'s `::-webkit-scrollbar`, every scrolling box's bar unless it styles its own: an 8
/// point `scrollbar_thumb` thumb with round ends, `scrollbar_hover` under the pointer.
pub fn scroll_bar(t: &Tokens) -> ScrollBarStyle {
    ScrollBarStyle::native(t.scrollbar_thumb, t.scrollbar_hover)
        .webkit_parts(8.0, 0.0, 0.0)
        .always()
        .room()
}

/// A dialog list's bar, `.shortcuts-body` and `.shortcut-editor-body`: the 8 point bar with its
/// thumb drawn 2 points in from every side, `border`, `border_hover` under the pointer.
pub fn dialog_scroll_bar(t: &Tokens) -> ScrollBarStyle {
    ScrollBarStyle::native(t.border, t.border_hover)
        .webkit_parts(8.0, 2.0, 0.0)
        .always()
        .room()
}

/// The asks' bar, `.asks-list` and `.asks-ask`: a 6 point `border` thumb with round ends,
/// `border_hover` under the pointer.
pub fn asks_scroll_bar(t: &Tokens) -> ScrollBarStyle {
    ScrollBarStyle::native(t.border, t.border_hover)
        .webkit_parts(6.0, 0.0, 0.0)
        .always()
        .room()
}

/// `scrollbar-width: thin` with the system's colours, the garden's columns: the system's thin
/// scroller, a 7 point thumb, shown as the system "Show scroll bars" setting says.
pub fn thin_scroll_bar(t: &Tokens) -> ScrollBarStyle {
    ScrollBarStyle::from_kit(crate::kit(t))
        .width(7.0)
        .hover_width(7.0)
}

/// The Markdown reader's bar, `.md-reader { scrollbar-width: thin; scrollbar-color:
/// var(--color-border) transparent }`, and the boxes under it that set `thin` too: the thin bar
/// in `border`.
pub fn document_scroll_bar(t: &Tokens) -> ScrollBarStyle {
    thin_scroll_bar(t)
        .thumb(t.border)
        .thumb_hover(t.border_hover)
}

/// A code block's bar under the Markdown reader, `.md-codeblock pre { overflow-x: auto }`, which
/// takes the reader's `scrollbar-color` but not its `thin`: the system's regular scroller in
/// `border`, 8 points at rest and 12, as the 2026-11 macOS pair measured it, under the pointer,
/// on a 16 point track the block adds under its code while the system shows classic bars.
pub fn code_scroll_bar(t: &Tokens) -> ScrollBarStyle {
    ScrollBarStyle::from_kit(crate::kit(t))
        .thumb(t.border)
        .thumb_hover(t.border_hover)
        .width(8.0)
        .hover_width(12.0)
}

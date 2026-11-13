use attn_ui::Tokens;
use rune::tok::type_;
use rune::{El, col, el, row, text};

use crate::State;

/// The notice over the top of a session's terminal while its terminal runs an older build than
/// the daemon, as the Tauri app shows it: where Reload session is, and a dismiss that holds for
/// the rest of the app's run.
pub(super) fn notice(session: &str, t: &Tokens) -> El {
    let dismissed = session.to_owned();
    let body = col()
        .gap(4.0)
        .grow(1.0)
        .min_w(0.0)
        .child(
            text("This session is running an older terminal.")
                .font(type_::LABEL)
                .color(t.text_primary)
                .wrap(),
        )
        .child(
            text("Reload it to bring it up to date.")
                .font(type_::LABEL)
                .color(t.text_secondary)
                .wrap(),
        )
        .child(
            text("An update normally swaps this out on its own. This one could not.")
                .font(type_::LABEL)
                .color(t.text_tertiary)
                .wrap()
                .pt(2.0),
        );
    let dismiss = text(attn_ui::icon::CLOSE)
        .key(format!("session.{session}.stale-build.dismiss"))
        .role("button")
        .label("Dismiss older-terminal notice")
        .font(type_::LABEL)
        .color(t.text_muted)
        .px(5.0)
        .py(2.0)
        .radius(4.0)
        .on_press(move |state: &mut State| {
            state
                .window
                .desk
                .stale_build_dismissed
                .insert(dismissed.clone());
        });
    let card = row()
        .key(format!("session.{session}.stale-build"))
        .role("status")
        .items_start()
        .gap(12.0)
        .max_w(520.0)
        .padding(12.0)
        .pl(14.0)
        .radius(8.0)
        .fill(t.bg_panel)
        .border(1.0, t.border)
        .children([reload_hint(t), body, dismiss]);
    el().pt(12.0).w_full().px(12.0).justify_center().child(card)
}

/// A sketch of the sidebar row whose ••• menu holds Reload session.
fn reload_hint(t: &Tokens) -> El {
    let bar = |w: f32| el().w(w).h(6.0).radius(3.0).fill(t.text_muted.alpha(0.5));
    let sidebar_row = row()
        .items_center()
        .gap(6.0)
        .w(86.0)
        .h(22.0)
        .px(8.0)
        .radius(5.0)
        .fill(t.bg_elevated)
        .border(1.0, t.border)
        .child(el().w(6.0).h(6.0).radius(3.0).fill(t.text_muted))
        .child(bar(38.0))
        .child(el().grow(1.0))
        .child(text("•••").font(type_::SECTION).color(t.text_secondary));
    let menu = col()
        .gap(5.0)
        .min_w(89.0)
        .padding(6.0)
        .radius(6.0)
        .fill(t.bg_elevated)
        .border(1.0, t.border)
        .child(bar(46.0))
        .child(bar(58.0))
        .child(el().h(1.0).w_full().fill(t.border))
        .child(
            text("↻ Reload session")
                .font(type_::SECTION)
                .color(t.text_primary)
                .single_line()
                .px(4.0)
                .radius(4.0)
                .fill(t.accent.alpha(0.25)),
        )
        .child(bar(40.0));
    row()
        .items_start()
        .gap(6.0)
        .child(sidebar_row)
        .child(text("→").font(type_::SECTION).color(t.text_muted).pt(26.0))
        .child(menu.mt(20.0))
}

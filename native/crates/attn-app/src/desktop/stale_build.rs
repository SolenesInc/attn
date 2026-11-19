use attn_ui::Tokens;
use attn_ui::blocks::{Block, Inline, Notice};
use attn_ui::system::{Button, IconSize, Look, Size, icon_button};
use rune::Tx;

use crate::State;

/// The notice over the top of a session's terminal while its terminal runs an older build than
/// the daemon: Reload session, which brings it up to date, and a dismiss that holds for the rest
/// of the app's run.
pub(super) fn notice(session: &str, t: &Tokens) -> Block {
    let (reloaded, dismissed) = (session.to_owned(), session.to_owned());
    let reload = Button::new("Reload session")
        .look(Look::Plain)
        .size(Size::Compact)
        .on_press_tx(move |state: &mut State, tx: &mut Tx| {
            crate::sidebar::session_menu::reload_session(state, &reloaded, tx);
        })
        .build(t)
        .key(format!("session.{session}.stale-build.reload"));
    let dismiss = icon_button(
        format!("session.{session}.stale-build.dismiss"),
        "Dismiss older-terminal notice",
        attn_ui::glyph::TIMES,
        IconSize::Regular,
    )
    .on_press(move |state: &mut State| {
        state
            .window
            .desk
            .stale_build_dismissed
            .insert(dismissed.clone());
    })
    .build(t);
    Notice::info(
        format!("session.{session}.stale-build"),
        "This session is running an older terminal. Reload it to bring it up to date. An update normally swaps this out on its own. This one could not.",
    )
    .action(Inline::joined().child(reload.into()).child(dismiss.into()))
    .build(t)
}

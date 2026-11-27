use attn_client::Desktop;
use attn_protocol::{
    ForkOfferDismissMessage, ForkOfferOpenMessage, Harness, ProfileActionResultMessage, Session,
};
use attn_ui::Tokens;
use attn_ui::blocks::{Block, Inline, Notice};
use attn_ui::system::{Button, IconSize, Look, Size, icon_button};
use rune::Tx;

use crate::State;
use crate::requests::Refused;

/// The forks whose original no tile in the profile shows, each with what its notice says it
/// was forked from.
pub(super) fn offers(
    sessions: &[Session],
    desktops: &[Desktop],
    harnesses: &[Harness],
) -> Vec<(String, String)> {
    sessions
        .iter()
        .filter_map(|fork| {
            let original = offered(fork, desktops)?;
            let named = sessions
                .iter()
                .find(|s| s.id == original)
                .map(|s| crate::sidebar::model::display_name(s, harnesses))
                .map_or_else(
                    || "an earlier conversation".to_owned(),
                    |name| format!("“{name}”"),
                );
            Some((fork.id.clone(), named))
        })
        .collect()
}

/// The original `fork` offers to open, while no tile in the profile shows it.
pub(crate) fn offered<'a>(fork: &'a Session, desktops: &[Desktop]) -> Option<&'a str> {
    let original = fork.fork_offer.as_deref().filter(|id| !id.is_empty())?;
    let shown = desktops
        .iter()
        .any(|d| d.panes.iter().any(|p| p.session_id == original));
    (!shown).then_some(original)
}

/// The notice over the top of a fork's terminal: Open original beside this, and a dismiss that
/// ends the offer in every window.
pub(super) fn notice(fork: &str, named: &str, t: &Tokens) -> Block {
    let (opened, dismissed) = (fork.to_owned(), fork.to_owned());
    let open_original = Button::new("Open original beside this")
        .look(Look::Plain)
        .size(Size::Compact)
        .on_press_tx(move |state: &mut State, tx: &mut Tx| open(state, &opened, tx))
        .build(t)
        .key(format!("session.{fork}.fork-offer.open"));
    let dismiss_offer = icon_button(
        format!("session.{fork}.fork-offer.dismiss"),
        "Dismiss fork offer",
        attn_ui::glyph::TIMES,
        IconSize::Regular,
    )
    .on_press(move |state: &mut State| dismiss(state, &dismissed))
    .build(t);
    Notice::info(
        format!("session.{fork}.fork-offer"),
        format!("Forked from {named}."),
    )
    .action(
        Inline::joined()
            .child(open_original.into())
            .child(dismiss_offer.into()),
    )
    .build(t)
}

/// Asks the daemon to open the fork's original in the column after the fork, and gives that
/// tile the keyboard unless the person moved it meanwhile.
pub(crate) fn open(state: &mut State, fork: &str, tx: &mut Tx) {
    if state.window.tile.is_some() {
        let fork = fork.to_owned();
        let update = move |state: &mut State, tx: &mut Tx| open(state, &fork, tx);
        crate::desktop::to_main(state, Box::new(update));
        crate::frame::raise_main();
        return;
    }
    let before = tx.focused().map(str::to_owned);
    let inputs_before = tx.inputs();
    state.act(
        |request_id| ForkOfferOpenMessage {
            request_id,
            session_id: fork.to_owned(),
            ..ForkOfferOpenMessage::new()
        },
        Refused::Prefixed("Could not open the original"),
        move |state, reply, tx| {
            let Some(result) = reply.parse::<ProfileActionResultMessage>() else {
                return;
            };
            let Some(pane) = result.pane_id.filter(|id| !id.is_empty()) else {
                return;
            };
            let Some(desktop) = result
                .desktops
                .iter()
                .flatten()
                .find(|d| d.panes.iter().any(|p| p.pane_id == pane))
                .map(|d| d.id.clone())
            else {
                return;
            };
            let moved = tx.inputs() != inputs_before
                && tx.focused().is_some_and(|f| Some(f) != before.as_deref());
            crate::desktop::reveal_leaf(state, &desktop, &pane, !moved, tx);
        },
        tx,
    );
}

/// Ends the fork's offer for every window.
pub(crate) fn dismiss(state: &State, fork: &str) {
    let _ = state.daemon.send(&ForkOfferDismissMessage {
        session_id: fork.to_owned(),
        ..ForkOfferDismissMessage::new()
    });
}

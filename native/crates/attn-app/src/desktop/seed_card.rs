use attn_protocol::{Seed, Session};
use attn_ui::Tokens;
use rune::tok::{space, type_};
use rune::{El, Key, KeyEvent, Tx, col, row, text};
use rune_kit::{Button, ButtonVariant};

use crate::State;
use crate::terminal::SeedCard;

const WIDTH: f32 = 300.0;
const FIRST_HEIGHT: f32 = 150.0;
const EXCERPT_CHARS: usize = 260;

pub(crate) fn key(runtime: &str) -> String {
    format!("terminal.{runtime}.seed-card")
}

/// The pointer moved over terminal `runtime`, which shows `session`'s agent, or left it.
pub(super) fn pointer(state: &mut State, runtime: &str, session: &str, inside: bool, tx: &mut Tx) {
    if let Some(due) = state
        .terminals
        .seed_pointer(runtime, session, inside, tx.clock())
    {
        tx.wake_at(due);
    }
}

fn hovered(state: &mut State, inside: bool, tx: &mut Tx) {
    if let Some(due) = state.terminals.seed_card_hovered(inside, tx.clock()) {
        tx.wake_at(due);
    }
}

/// The agent the card goes to: the seed's tender, or else the last session that tended it,
/// while that session is open and is not the one whose terminal shows the card.
fn agent<'a>(seed: &Seed, sessions: &'a [Session], host: &str) -> Option<&'a Session> {
    [
        seed.tender.as_ref().and_then(|t| t.session_id.as_deref()),
        seed.last_execution_id.as_deref(),
    ]
    .into_iter()
    .flatten()
    .find_map(|id| sessions.iter().find(|s| s.id == id))
    .filter(|s| s.id != host)
}

/// The card of the seed id the pointer rests on, over the terminal's slot of `slot` points.
pub(super) fn card(
    card: &SeedCard,
    facts: &attn_client::Facts,
    slot: (f32, f32),
    now_ms: i64,
    t: &Tokens,
) -> Option<El> {
    let seed = facts
        .garden
        .of(&facts.profiles.selected)
        .iter()
        .find(|s| s.id == card.seed)?;
    let kit = attn_ui::kit(t);
    let names = crate::garden::model::session_names(&facts.sessions);
    let tender = crate::garden::model::tender_of(seed, &names);
    let updated = crate::garden::model::ago(&seed.updated_at, now_ms);
    let excerpt = super::seed_chip::excerpt(&seed.body, EXCERPT_CHARS);
    let surface = key(&card.runtime);
    let go = agent(seed, &facts.sessions, &card.session).map(|session| {
        let name = crate::sidebar::model::display_name(session);
        let id = session.id.clone();
        Button::new(format!("Go to {name}"))
            .variant(ButtonVariant::GhostMuted)
            .on_press_tx(move |state: &mut State, tx: &mut Tx| {
                state.terminals.close_seed_card();
                crate::sidebar::open_agent(state, &id, tx);
            })
            .build(kit)
            .key(format!("{surface}.agent"))
    });
    let (seed_id, session) = (seed.id.clone(), card.session.clone());
    let open = Button::new("Open as tile")
        .variant(ButtonVariant::GhostMuted)
        .on_press_tx(move |state: &mut State, tx: &mut Tx| open_tile(state, &seed_id, &session, tx))
        .build(kit)
        .key(format!("{surface}.open"));
    let meta = |label: &str, value: String| {
        (!value.is_empty()).then(|| {
            row()
                .gap(space::S1)
                .child(
                    text(label.to_owned())
                        .font(type_::LABEL)
                        .color(t.text_tertiary),
                )
                .child(text(value).font(type_::LABEL).color(t.text_secondary))
        })
    };
    let at = card.at;
    let size = card.size.unwrap_or((WIDTH, FIRST_HEIGHT));
    let (left, top) = super::annotating::place((at[0] + at[2] * 4.0, at[1]), size, slot);
    let body = col()
        .key(surface.clone())
        .role("dialog")
        .label(seed.title.clone())
        .absolute(left, top)
        .w(WIDTH)
        .padding(space::S2)
        .gap(space::S1)
        .radius(6.0)
        .fill(t.bg_elevated)
        .border(1.0, t.border)
        .focusable(true)
        .on_hover_change(hovered)
        .on_pointer_move_local(|state: &mut State, _, tx: &mut Tx| {
            hovered(state, true, tx);
            tx.live_only();
        })
        .on_layout_tx(|state: &mut State, [_, _, w, h], tx: &mut Tx| {
            if !state.terminals.seed_card_laid_out((w, h)) {
                tx.live_only();
            }
        })
        .on_key(|state: &mut State, ev: &KeyEvent, _: &mut Tx| {
            let closes = ev.key == Key::Escape;
            if closes {
                state.terminals.close_seed_card();
            }
            closes
        })
        .child(
            row()
                .w_full()
                .gap(space::S1)
                .child(
                    text(super::seed_chip::state_label(&seed.status))
                        .font(type_::LABEL)
                        .color(super::seed_chip::state_color(&seed.status, t)),
                )
                .child(rune::el().grow(1.0))
                .children(go)
                .child(open),
        )
        .child(
            text(seed.title.clone())
                .role("heading")
                .label(seed.title.clone())
                .font(type_::TITLE)
                .color(t.text_primary)
                .wrap(),
        )
        .children((!excerpt.is_empty()).then(|| {
            text(excerpt)
                .font(type_::BODY)
                .color(t.text_secondary)
                .wrap()
        }))
        .children(meta("Tender", tender))
        .children(meta("Updated", updated));
    Some(body)
}

fn open_tile(state: &mut State, seed: &str, session: &str, tx: &mut Tx) {
    state.terminals.close_seed_card();
    crate::garden::open_tile_beside(state, seed, Some(session.to_owned()), tx);
}

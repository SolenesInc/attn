use attn_protocol::{Seed, Session};
use attn_ui::blocks::{Card, Inline, Prose};
use attn_ui::system::{Button, Look, Size, Type, words};
use attn_ui::{Frame, Tokens};
use rune::{El, Key, KeyEvent, Tx};

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
    let names = crate::garden::model::session_names(&facts.sessions);
    let tender = crate::garden::model::tender_of(seed, &names);
    let updated = crate::garden::model::ago(&seed.updated_at, now_ms);
    let excerpt = super::seed_chip::excerpt(&seed.body, EXCERPT_CHARS);
    let surface = key(&card.runtime);
    let go = agent(seed, &facts.sessions, &card.session).map(|session| {
        let name = crate::sidebar::model::display_name(session);
        let id = session.id.clone();
        El::from(
            Button::new(format!("Go to {name}"))
                .look(Look::Quiet)
                .size(Size::Compact)
                .on_press_tx(move |state: &mut State, tx: &mut Tx| {
                    state.terminals.close_seed_card();
                    crate::sidebar::open_agent(state, &id, tx);
                })
                .build(t)
                .key(format!("{surface}.agent")),
        )
    });
    let (seed_id, session) = (seed.id.clone(), card.session.clone());
    let open: El = Button::new("Open as tile")
        .look(Look::Plain)
        .size(Size::Compact)
        .on_press_tx(move |state: &mut State, tx: &mut Tx| open_tile(state, &seed_id, &session, tx))
        .build(t)
        .key(format!("{surface}.open"))
        .into();
    let meta = |label: &str, value: String| {
        (!value.is_empty()).then(|| {
            Inline::joined()
                .child(words(label.to_owned(), Type::Label, t.text_tertiary))
                .child(words(value, Type::Small, t.text_secondary))
        })
    };
    let state = words(
        super::seed_chip::state_label(&seed.status),
        Type::Label,
        super::seed_chip::state_color(&seed.status, t),
    );
    let title = words(seed.title.clone(), Type::Name, t.text_primary)
        .key(format!("{surface}.heading"))
        .role("heading")
        .wrap();
    let excerpt =
        (!excerpt.is_empty()).then(|| Prose::lead(format!("{surface}.excerpt"), excerpt).el(t));
    let at = card.at;
    let size = card.size.unwrap_or((WIDTH, FIRST_HEIGHT));
    let (left, top) = super::annotating::place((at[0] + at[2] * 4.0, at[1]), size, slot);
    let actions = Inline::loose().children(go).child(open);
    let body = Card::new(surface.clone())
        .push(state)
        .push(title)
        .push_all(excerpt)
        .push_all(meta("Tender", tender))
        .push_all(meta("Updated", updated))
        .push(actions)
        .build(t);
    let body = Frame::panel(t)
        .draw(body)
        .role("dialog")
        .label(seed.title.clone())
        .focusable(true)
        .absolute(left, top)
        .w(WIDTH)
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
        });
    Some(body)
}

fn open_tile(state: &mut State, seed: &str, session: &str, tx: &mut Tx) {
    state.terminals.close_seed_card();
    crate::garden::open_tile_beside(state, seed, Some(session.to_owned()), tx);
}

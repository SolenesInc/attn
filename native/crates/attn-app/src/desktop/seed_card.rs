use attn_protocol::{Seed, Session};
use attn_ui::blocks::{Card, Inline, Prose};
use attn_ui::system::{Button, Look, Size, Type, words};
use attn_ui::{Frame, Tokens};
use rune::{El, Key, KeyEvent, Tx};
use rune_kit::Placement;

use super::header_pop::{self, Pop};
use crate::State;
use crate::terminal::Subject;

const WIDTH: f32 = 300.0;
const EXCERPT_CHARS: usize = 260;

/// The agent the card goes to: the seed's tender, or else the last session that tended it,
/// while that session is open and is not the one whose terminal shows the card.
fn agent<'a>(seed: &Seed, sessions: &'a [Session], host: Option<&str>) -> Option<&'a Session> {
    [
        seed.tender.as_ref().and_then(|t| t.session_id.as_deref()),
        seed.last_execution_id.as_deref(),
    ]
    .into_iter()
    .flatten()
    .find_map(|id| sessions.iter().find(|s| s.id == id))
    .filter(|s| Some(s.id.as_str()) != host)
}

/// The card of the seed whose id a terminal or words show.
pub(super) fn popover(state: &State, pop: &Pop, t: &Tokens) -> Option<El> {
    let Subject::Seed(id) = pop.marking()?.target() else {
        return None;
    };
    let (facts, now_ms) = (&state.facts, state.window.now_ms);
    let seed = crate::garden::preview_seed(state, id)?;
    let names = crate::garden::model::session_names(&facts.sessions, &facts.harnesses);
    let tender = crate::garden::model::tender_of(seed, &names);
    let updated = crate::garden::model::ago(&seed.updated_at, now_ms);
    let excerpt = super::seed_chip::excerpt(&seed.body, EXCERPT_CHARS);
    let surface = pop.surface();
    let go = agent(seed, &facts.sessions, pop.beside()).map(|session| {
        let name = crate::sidebar::model::display_name(session, &facts.harnesses);
        let id = session.id.clone();
        El::from(
            Button::new(format!("Go to {name}"))
                .look(Look::Quiet)
                .size(Size::Compact)
                .on_press_tx(move |state: &mut State, tx: &mut Tx| {
                    super::agent_card::select(state, &id, tx)
                })
                .build(t)
                .key(format!("{surface}.agent")),
        )
    });
    let (seed_id, beside) = (seed.id.clone(), pop.beside().map(str::to_owned));
    let open: El = Button::new("Open as tile")
        .look(Look::Plain)
        .size(Size::Compact)
        .on_press_tx(move |state: &mut State, tx: &mut Tx| {
            super::agent_card::open_seed(state, &seed_id, beside.as_deref(), tx)
        })
        .build(t)
        .key(format!("{surface}.open"))
        .into();
    let meta = |part: &str, label: &str, value: String| {
        (!value.is_empty()).then(|| {
            Inline::joined()
                .child(words(label.to_owned(), Type::Label, t.text_tertiary))
                .child(
                    words(value, Type::Small, t.text_secondary)
                        .key(format!("{surface}.{part}"))
                        .truncate()
                        .shrink()
                        .min_w(0.0),
                )
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
    let actions = Inline::loose().children(go).child(open);
    let body = Card::new(surface.clone())
        .push(state)
        .push(title)
        .push_all(excerpt)
        .push_all(meta("tender", "Tender", tender))
        .push_all(meta("updated", "Updated", updated))
        .push(actions)
        .build(t);
    let frame = match pop.pinned {
        true => Frame::panel(t).edged(t.accent),
        false => Frame::panel(t),
    };
    let card = frame
        .draw(body)
        .role("dialog")
        .label(seed.title.clone())
        .focusable(true)
        .no_focus_ring()
        .w(WIDTH)
        .on_hover_change(|state: &mut State, inside, tx: &mut Tx| {
            header_pop::hover_popover(state, inside, tx)
        });
    let card = if pop.pinned {
        card.on_key(pinned_key)
            .modal_tx(true, |state: &mut State, _: &mut Tx| {
                header_pop::close(state)
            })
            .modal_press_through(true)
            .modal_wheel_closes(true)
    } else {
        card
    };
    Some(rune_kit::popover::anchored(
        attn_ui::kit(t),
        card,
        &pop.anchor(),
        Placement::BottomStart,
        4.0,
    ))
}

fn pinned_key(state: &mut State, ev: &KeyEvent, tx: &mut Tx) -> bool {
    if ev.key != Key::Enter || ev.mods != rune::Mods::default() {
        return false;
    }
    let open = &state.window.desk.header_pop.open;
    let Some((seed, beside)) = open.as_ref().and_then(|pop| match pop.marking()?.target() {
        Subject::Seed(seed) => Some((seed.clone(), pop.beside().map(str::to_owned))),
        Subject::Session(_) => None,
    }) else {
        return false;
    };
    super::agent_card::open_seed(state, &seed, beside.as_deref(), tx);
    true
}

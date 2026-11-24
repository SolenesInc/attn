use std::collections::HashMap;
use std::rc::Rc;

use attn_protocol::{Ask, AskAnswer, AskQuestion, AskSeedMention, AskThread, Session};
use attn_ui::blocks::{Block, Disclosure, Inline, List, Popover, Section, Sheet, Stack};
use attn_ui::system::{
    AskChip, Button, Choice, DotShape, DotSize, FieldState, IconSize, ListRow, Look, Reading, Size,
    Tone, Type, answers, area_box, ask_mark, checks, count_badge, dot, drop_overlay, file_chip,
    file_kind, file_size, icon_button, image_tile, keycaps, keys_hint, line_box, read_as, tag,
    words,
};
use attn_ui::{Backdrop, Frame, Tokens, glyph};
use rune::{El, Image, Key, KeyEvent, Side, SurfaceFit, Tx, col, row};
use rune_kit::{Markdown, TextArea, TextInput};

use super::inline::Line;
use super::{Chip, Item, Kind, Lists, Stop, attach, mentions, questions};
use crate::State;
use crate::desktop::header_pop::{self, Pop, What};
use crate::markdown::{self, images, links};
use crate::sidebar::queue;

pub(super) const SNOOZE: &str = "asks.snooze";
const OPEN_SEED: &str = "asks.open-seed";
const LIST: &str = "asks.list";

/// The sheet's greatest width, its list rail's width, its reading column's measure and the
/// widest its pictures run, as the person chose them from the asks sheet's third prototype. At
/// `Reading::Text` the measure holds about 90 characters, as the document reader's 640 holds 91
/// to 97. The rail, the widest picture and the body's insets need 1276 of the sheet's width.
const SHEET_W: f32 = 1440.0;
const RAIL_W: f32 = 264.0;
const MEASURE: f32 = 620.0;
const PICTURES: f32 = MEASURE + 360.0;
/// Under its greatest size the sheet stays 96 points narrower than the window and 120 points
/// shorter, centred under the title bar. Past the least size below, its margins give way to
/// it, down to the tightest.
const SHEET_SIDE: f32 = 48.0;
const SHEET_ABOVE: f32 = 44.0;
const SHEET_TIGHTEST: f32 = 16.0;
/// The narrowest sheet that sets its list beside the ask: the rail and 420 points of ask, the
/// least the drawer it replaced read an ask in. A narrower sheet lists the asks over the ask.
const BESIDE_LEAST: f32 = RAIL_W + 420.0;
/// The height the sheet keeps before its margins above and below give way: at 480 the ask
/// shows 360 points of itself beside the list, measured in a 1000 by 600 window.
const TALL_LEAST: f32 = 480.0;
/// How many asks the list over the ask shows before it scrolls: three, and half of the next, so
/// it reads as a list that goes on and leaves the sheet to the ask it shows.
const SHOWN_ASKS: f32 = 3.5;

pub(super) fn choice_key(question: &str, i: usize) -> String {
    format!("asks.q.{question}.choice.{i}")
}

pub(super) fn other_key(question: &str) -> String {
    format!("asks.q.{question}.other.field")
}

pub(super) fn condition_key(question: &str) -> String {
    format!("asks.q.{question}.condition.field")
}

pub(super) fn add_condition_key(question: &str) -> String {
    format!("asks.q.{question}.condition")
}

pub(super) fn suggestion_key(question: &str) -> String {
    format!("asks.q.{question}.suggestion")
}

pub(super) fn drop_key(question: &str) -> String {
    format!("asks.q.{question}.drop")
}

pub(super) fn text_key(question: &str) -> String {
    format!("asks.q.{question}.text.field")
}

pub(super) const NOTE: &str = "asks.note.field";
pub(super) const ADD_NOTE: &str = "asks.note.add";
pub(super) const ATTACH: &str = "asks.attach";

pub(super) fn remove_file_key(file: &str) -> String {
    format!("asks.file.{file}.remove")
}
const QUOTE: &str = "asks.quote";
pub(super) const FOLLOW_UP: &str = "asks.follow-up.field";

fn send_chord() -> &'static str {
    if rune::Platform::current() == rune::Platform::Mac {
        "⌘↩"
    } else {
        "Ctrl+↩"
    }
}

/// The sheet over the window: the rail of the asks it lists beside the one it shows, with the
/// questions to answer.
pub(crate) fn sheet(state: &State, t: &Tokens) -> El {
    let lists = Lists::of(&state.facts, state.window.now_ms);
    let live = lists.live.len();
    let replied = lists.replied.len();
    let asks = &state.window.asks;
    let items = lists.shown(asks.show_replied);
    let shown = state.shown_ask().map(|(id, _)| id);
    let quoting = shown.as_ref().is_some_and(|id| {
        items
            .iter()
            .any(|i| &i.ask.id == id && i.ask.reply.is_none())
    });
    let close = icon_button("asks.close", "Close asks", glyph::TIMES, IconSize::Large)
        .on_press_tx(super::close)
        .build(t);
    let mut rail = Sheet::new("asks.rail", "Asks");
    if live > 0 {
        rail = rail.badge(
            count_badge(live, t.accent, attn_ui::ink_on(t.accent, t))
                .key("asks.count")
                .role("status")
                .label(format!("{live} open")),
        );
    }
    let rail = rail
        .aside(replied_toggle(replied, asks.show_replied, t))
        .close(close);
    let [window_w, window_h] = state.pictures.window;
    let under_title_bar = window_h - crate::shell::TITLE_BAR_HEIGHT;
    let side = ((window_w - BESIDE_LEAST) / 2.0).clamp(SHEET_TIGHTEST, SHEET_SIDE);
    let above = ((under_title_bar - TALL_LEAST) / 2.0).clamp(SHEET_TIGHTEST, SHEET_ABOVE);
    let beside = window_w - 2.0 * side >= BESIDE_LEAST;
    let panel = col()
        .key(super::SHEET)
        .role("dialog")
        .label("Asks")
        .focusable(true)
        .tab_index(-1)
        .no_focus_ring()
        .on_key(sheet_key)
        .on_selection_change(|state: &mut State, change, _: &mut Tx| {
            state.window.asks.quotable = change
                .selection()
                .filter(|_| !change.dragging())
                .and_then(|s| s.texts().iter().find(|t| t.is_selected()))
                .map(|t| keyed_path(t.path()));
        })
        .on_drop_files(
            |state: &mut State, paths: &[std::path::PathBuf], tx: &mut Tx| {
                state.window.asks.dropping = false;
                super::drop_on_sheet(state, paths, tx);
            },
        )
        .on_paste_content(super::paste_on_sheet)
        .on_drop_hover(|state: &mut State, over: bool, _: &mut Tx| {
            state.window.asks.dropping = over && matches!(state.shown_ask(), Some((_, false)));
        })
        .modal_tx(true, super::close)
        .w_full()
        .max_w(SHEET_W);
    let now_ms = state.window.now_ms;
    let panel = match shown.and_then(|id| items.iter().position(|i| i.ask.id == id)) {
        None => panel.child(
            rail.push(empty(replied, t))
                .fits(under_title_bar - 2.0 * above)
                .build(t),
        ),
        Some(at) if beside => {
            let item = &items[at];
            let rail = rail
                .fill(col().child(list(&items, &item.ask.id, now_ms, t).scrolling(t)))
                .build(t)
                .grow(0.0)
                .w(RAIL_W)
                .min_w(RAIL_W)
                .border_sides([0.0, 1.0, 0.0, 0.0], t.border_subtle);
            let reading = Sheet::new("asks.reading", Line::plain(&item.ask.subject))
                .head(header(state, item, t))
                .article("asks.ask", Line::plain(&item.ask.subject))
                .measure(MEASURE, PICTURES)
                .push_all(article(state, item, Some(MEASURE), t))
                .footer(footer(state, item, items.len() > 1, t))
                .build(t)
                .grow_from(1.0, 0.0);
            panel
                .h_full()
                .child(row().w_full().h_full().child(rail).child(reading))
        }
        Some(at) => {
            let item = &items[at];
            let rows = ListRow::two_line_height() * SHOWN_ASKS;
            let list = list(&items, &item.ask.id, now_ms, t)
                .scrolling_up_to(rows, t)
                .shrink()
                .min_h(ListRow::two_line_height());
            let stacked = rail
                .top(list)
                .keeps_room(rows)
                .article("asks.ask", Line::plain(&item.ask.subject))
                .push(Block::custom("asks.header.block", header(state, item, t)))
                .push_all(article(state, item, None, t))
                .footer(footer(state, item, items.len() > 1, t))
                .build(t);
            panel.h_full().child(stacked)
        }
    };
    let panel = match asks.quotable.as_deref().filter(|_| quoting) {
        Some(text) => panel.children(quote_button(text, t)),
        None => panel,
    };
    let placed = col()
        .pin(
            Some(side),
            Some(crate::shell::TITLE_BAR_HEIGHT + above),
            Some(side),
            Some(above),
        )
        .center()
        .child(
            Frame::dialog(t).draw_clipped(
                panel.children(
                    asks.dropping
                        .then(|| drop_overlay("asks.drop", drop_said(state), t)),
                ),
            ),
        );
    rune::el()
        .key("asks.layer")
        .absolute(0.0, 0.0)
        .w_full()
        .h_full()
        .child(Backdrop::finder(t).draw("asks.scrim"))
        .child(placed)
}

/// Shows or hides the asks replied to in the last day, reading as pressed while they show.
fn replied_toggle(replied: usize, on: bool, t: &Tokens) -> El {
    let help = if on {
        "Hide the asks you replied to in the last day (r)"
    } else {
        "Show the asks you replied to in the last day (r)"
    };
    let said = if replied > 0 {
        format!("Replied {replied}")
    } else {
        "Replied".to_owned()
    };
    Button::new(said)
        .size(Size::Compact)
        .look(Look::Quiet)
        .chosen(on)
        .help(help)
        .on_press_tx(toggle_replied)
        .build(t)
        .key("asks.replied")
        .into()
}

fn empty(replied: usize, t: &Tokens) -> Block {
    let replies = match replied {
        0 => None,
        1 => Some("r shows the one you replied to in the last day".to_owned()),
        n => Some(format!("r shows the {n} you replied to in the last day")),
    };
    Block::custom(
        "asks.empty.block",
        Stack::tight()
            .key("asks.empty")
            .w_full()
            .child(words("No open asks.", Type::Body, t.text_tertiary).wrap())
            .child(
                words(
                    "When an agent needs a decision from you, its ask lands here.",
                    Type::Small,
                    t.text_tertiary,
                )
                .wrap(),
            )
            .children(replies.map(|r| words(r, Type::Small, t.text_tertiary).wrap())),
    )
}

fn list(items: &[Item<'_>], shown: &str, now_ms: i64, t: &Tokens) -> List {
    let replied = items.iter().any(|i| i.ask.reply.is_some());
    let mut rows = Vec::new();
    for (at, item) in items.iter().enumerate() {
        let before = at.checked_sub(1).map(|b| &items[b]);
        if item.snoozed_until.is_some() && before.is_none_or(|b| b.snoozed_until.is_none()) {
            rows.push(divider("asks.list.snoozed", "Snoozed", t));
        }
        if item.ask.reply.is_some() && before.is_none_or(|b| b.ask.reply.is_none()) {
            rows.push(divider("asks.list.replied", "Replied in the last day", t));
        }
        rows.push(list_row(item, item.ask.id == shown, now_ms, t));
    }
    let label = if replied {
        "Open and replied asks"
    } else {
        "Open asks"
    };
    List::new(LIST, label).listbox().rows(rows)
}

fn divider(key: &str, said: &'static str, t: &Tokens) -> El {
    ListRow::note(key, said, Type::Label, t.text_tertiary, None, t).role("listitem")
}

fn list_row(item: &Item<'_>, selected: bool, now_ms: i64, t: &Tokens) -> El {
    let ask = item.ask;
    let mut timed = Vec::new();
    if let Some(due) = super::default_of(ask).filter(|_| ask.reply.is_none()) {
        timed.push(format!(
            "default {}",
            queue::wake_time_short(Some(&due.due_at), now_ms)
        ));
    }
    if let Some(until) = item.snoozed_until {
        timed.push(format!(
            "until {}",
            queue::wake_time_short(Some(until), now_ms)
        ));
    }
    let mut about = vec![item.from.clone()];
    if item.questions > 1 {
        about.push(format!("{} questions", item.questions));
    }
    about.extend(timed.iter().cloned());
    let waiting = ask.reply.is_none()
        && item.snoozed_until.is_none()
        && item.holder.is_some_and(|s| s.turn_owed == Some(true));
    if ask.reply.is_some() {
        about.push("replied".into());
    }
    let age = queue::turn_age(Some(super::age_from(ask)), now_ms);
    let subject = Line::plain(&ask.subject);
    let mut said = vec![item.from.clone(), subject.clone()];
    said.extend(about[1..].iter().cloned());
    if waiting {
        said.push("Waiting on you".into());
    }
    let id = ask.id.clone();
    let row = ListRow::new(
        format!("asks.row.{}", ask.id),
        format!("{} {age}", said.join(" · ")),
    );
    let row = if waiting {
        row.aside(dot(t.accent, DotShape::Filled, DotSize::Small))
    } else {
        row
    };
    let row = row.role("option").unfocusable().title(subject);
    let row = if timed.is_empty() {
        row
    } else {
        row.subtitle(timed.join(" · "), t)
    };
    row.aside(words(age, Type::Mono, t.text_tertiary))
        .selected(selected)
        .dimmed(ask.reply.is_some() || item.snoozed_until.is_some())
        .build(t)
        .a11y_value(if selected { "current" } else { "" })
        .on_press_tx(move |state: &mut State, tx: &mut Tx| {
            state.window.asks.selected = Some(id.clone());
            state.window.asks.error = None;
            super::follow(state, tx);
        })
}

/// How an ask's words show: the seeds they name as links that open them, and its pictures,
/// read from the copy of them the home keeps.
struct Prose<'a> {
    names: &'a [AskSeedMention],
    measure: Option<f32>,
    pictures: Rc<HashMap<String, Option<Image>>>,
    widths: &'a HashMap<String, u32>,
    document: String,
    t: &'a Tokens,
}

impl<'a> Prose<'a> {
    fn of(state: &'a State, ask: &Ask, measure: Option<f32>, t: &'a Tokens) -> Prose<'a> {
        Prose {
            names: mentions::of(state, &ask.id),
            measure,
            pictures: state.pictures.of_ask(&ask.id),
            widths: &state.window.asks.widths,
            document: images::ask_document(ask),
            t,
        }
    }

    fn document(&self) -> links::Document {
        links::Document {
            path: self.document.clone(),
            session: None,
            outpost: None,
            view: "asks.ask".into(),
            opens_seed: None,
            seed: None,
            part: None,
        }
    }

    /// The words of `source`, a line of inline markdown, and what draws its marks on the words
    /// that show them, its links opening as the ask's prose opens them.
    fn line(&self, source: &str) -> (String, bool, impl FnOnce(El) -> El + 'static) {
        let line = Line::of(source, self.names);
        let (link, code) = (self.t.accent, self.t.bg_elevated);
        let document = self.document();
        let (words, linked) = (line.text.clone(), line.has_links());
        (words, linked, move |el: El| {
            let el = line.drawn(el, link, code);
            if !linked {
                return el;
            }
            el.on_link(move |state: &mut State, href: &str, tx: &mut Tx| {
                follow_link(state, &document, href, tx)
            })
        })
    }

    /// `source` read as the ask's markdown at `place`, which names its scroll boxes apart from
    /// the ask's other markdown.
    fn markdown(&self, place: &str, source: &str) -> El {
        let document = self.document();
        let bars = format!("{place}.");
        let read = Markdown::new(source)
            .id(bars.clone())
            .selectable(true)
            .scroll_wide(true);
        let read = markdown::diagrams(read, bars, self.t);
        let style = attn_ui::system::read_alone_style(self.t.text_secondary, self.measure, self.t);
        let style = match self.widths.get(place) {
            Some(&width) => style.text_width(width as f32),
            None => style,
        };
        let place = place.to_owned();
        let read = markdown::pictured(read, &document, self.pictures.clone(), self.t)
            .text_links(mentions::seed_links(self.names))
            .style(style)
            .on_link_tx(move |state: &mut State, href: &str, tx: &mut Tx| {
                follow_link(state, &document, href, tx)
            })
            .build(attn_ui::kit(self.t));
        col()
            .w_full()
            .on_layout_tx(move |state: &mut State, [_, _, w, _], tx: &mut Tx| {
                let width = w.round() as u32;
                let widths = &mut state.window.asks.widths;
                if widths.get(&place) == Some(&width) {
                    tx.live_only();
                } else {
                    widths.insert(place.clone(), width);
                }
            })
            .child(read)
    }
}

fn follow_link(state: &mut State, document: &links::Document, href: &str, tx: &mut Tx) {
    match mentions::seed_of(href) {
        Some(seed) => super::open_seed(state, seed, tx),
        None => links::follow(state, document, href, tx),
    }
}

/// The ask's header, which stays while the ask scrolls: who asked and what it is doing, with
/// what to do about the ask besides answering it, and the seed it is about.
fn header(state: &State, item: &Item<'_>, t: &Tokens) -> El {
    let ask = item.ask;
    let snoozable = item
        .holder
        .filter(|s| !crate::outpost::offline(s, &state.facts.outposts));
    let snoozed = snoozable
        .is_some_and(|s| queue::is_snoozed(s.turn_snoozed_until.as_deref(), state.window.now_ms));
    let seed = ask.seed_id.as_ref().map(|seed| {
        let title = ask
            .seed_title
            .clone()
            .filter(|title| !title.is_empty() && *title != item.from);
        Inline::loose()
            .key("asks.seed")
            .min_w(0.0)
            .shrink()
            .child(words(
                ask.seed_slug.clone().unwrap_or_else(|| seed.clone()),
                Type::Mono,
                t.text_tertiary,
            ))
            .children(title.map(|title| {
                words(title, Type::Small, t.text_tertiary)
                    .min_w(0.0)
                    .shrink()
                    .truncate()
            }))
    });
    let open_seed = ask.seed_id.clone().map(|id| {
        ask_button(
            OPEN_SEED,
            "Open in Garden",
            "Open the seed in the garden (g)",
            &attn_ui::icon::GARDEN,
            t,
            move |state: &mut State, tx: &mut Tx| super::open_seed(state, &id, tx),
        )
    });
    Inline::loose()
        .key("asks.header")
        .w_full()
        .children(item.holder.map(|s| {
            crate::sidebar::lead(
                state,
                "asks.from",
                &crate::sidebar::AgentRow::of(
                    s,
                    &state.facts.harnesses,
                    &state.facts.outposts,
                    false,
                ),
                crate::sidebar::logos(state),
                t,
            )
        }))
        .child(
            words(
                super::stranded_line(ask)
                    .map(str::to_owned)
                    .unwrap_or_else(|| item.from.clone()),
                Type::Small,
                t.text_secondary,
            )
            .key("asks.from")
            .min_w(0.0)
            .shrink()
            .truncate(),
        )
        .children(asker_state(state, item).map(|said| {
            words(said, Type::Small, t.text_tertiary)
                .key("asks.asker-state")
                .min_w(0.0)
                .shrink()
                .truncate()
        }))
        .children(seed)
        .child(rune::el().grow(1.0))
        .child(ask_button(
            "asks.copy",
            "Copy ask",
            "Copy the ask as markdown (c)",
            glyph::COPY,
            t,
            super::copy,
        ))
        .children(item.holder.map(|s| open_agent(s, &item.from, t)))
        .children(snoozable.map(|_| snooze_button(snoozed, &item.from, t)))
        .children(open_seed)
}

/// What the asker is doing, as the ask's header says it beside its name.
fn asker_state(state: &State, item: &Item<'_>) -> Option<String> {
    let holder = item.holder?;
    if crate::outpost::offline(holder, &state.facts.outposts) {
        return Some("offline".into());
    }
    if let Some(until) = item.snoozed_until {
        return Some(format!(
            "snoozed until {}",
            queue::wake_time_short(Some(until), state.window.now_ms)
        ));
    }
    if holder.turn_owed == Some(true) {
        return Some("waiting on you".into());
    }
    Some(
        crate::sidebar::model::AgentState::parse(&holder.state)
            .spoken()
            .to_owned(),
    )
}

/// The ask's blocks, its prose kept to `measure` where the ask reads in a centred column.
fn article(state: &State, item: &Item<'_>, measure: Option<f32>, t: &Tokens) -> Vec<Block> {
    let ask = item.ask;
    let prose = Prose::of(state, ask, measure, t);
    let mut blocks = Vec::new();
    if let Some(standby) = item
        .standby
        .as_ref()
        .filter(|_| ask.reply.is_none() && !item.to_chief)
    {
        blocks.push(Block::custom(
            "asks.resumes.block",
            words(standby.answer.clone(), Type::Small, t.text_secondary).key("asks.resumes"),
        ));
    }
    if let Some(re) = &ask.re {
        blocks.push(follows(state, &ask.id, re, &prose, t));
    }
    let (subject, _, marked) = prose.line(&ask.subject);
    blocks.push(Block::custom(
        "asks.subject.block",
        marked(
            read_as(
                words(subject, Type::PageTitle, t.text_primary),
                Reading::Title,
            )
            .key("asks.subject")
            .role("heading")
            .selectable(true)
            .wrap(),
        ),
    ));
    if let Some(due) = super::default_of(ask).filter(|_| ask.reply.is_none()) {
        let said = format!(
            "No answer by {}, and {} goes with {}",
            queue::wake_time_long(Some(&due.due_at), state.window.now_ms),
            item.from,
            Line::plain(&due.choice)
        );
        blocks.push(Block::custom(
            "asks.default.block",
            words(said, Type::Small, t.text_secondary)
                .key("asks.default")
                .selectable(true)
                .wrap(),
        ));
    }
    let draft = state.window.asks.drafts.get(&ask.id);
    let reply_answers = ask.reply.as_ref().map(|r| r.answers.as_slice());
    let mut index = 0;
    for (at, part) in ask.parts.iter().enumerate() {
        blocks.push(match &part.question {
            Some(question) => {
                index += 1;
                let answer = match reply_answers {
                    Some(answers) => Shown::Replied(answers.iter().find(|a| a.key == question.key)),
                    None => Shown::Draft(draft.and_then(|d| d.answers.get(&question.key))),
                };
                let stop = Stop::Question(question.key.clone());
                let active = ask.reply.is_none() && super::active(state, ask) == stop;
                let files = match &ask.reply {
                    Some(reply) => Files::Sent(sent_on(reply, Some(&question.key))),
                    None => Files::Drafted(
                        draft.into_iter().flat_map(|d| d.files_on(&stop)).collect(),
                        state.window.asks.dropping_on.as_ref() == Some(&question.key),
                    ),
                };
                question_section(&ask.id, index, question, answer, files, active, &prose, t)
            }
            None => Block::custom(
                format!("asks.part.{at}.block"),
                col()
                    .key(format!("asks.part.{at}"))
                    .w_full()
                    .children(part.amended_at.as_deref().map(|amended| {
                        let age = queue::turn_age(Some(amended), state.window.now_ms);
                        let said = if age == "now" {
                            "Updated just now".to_owned()
                        } else {
                            format!("Updated {age} ago")
                        };
                        let said = words(said.clone(), Type::Small, t.text_tertiary)
                            .key(format!("asks.part.{at}.amended"))
                            .label(said);
                        match prose.measure {
                            Some(points) => said.w_full().max_w(points).self_center(),
                            None => said,
                        }
                    }))
                    .child(prose.markdown(
                        &format!("asks.part.{at}"),
                        part.markdown.as_deref().unwrap_or_default(),
                    )),
            )
            .wide(),
        });
    }
    let note_placeholder = match (item.holder, &item.standby, &ask.seed_id) {
        _ if item.to_chief => "Anything else for the Chief?".to_owned(),
        (None, Some(standby), _) => format!(
            "Anything else for {}?",
            crate::desktop::chain::dormant_name(standby.tender, &state.facts.harnesses)
        ),
        (None, None, Some(_)) => "Anything else for its next tender?".to_owned(),
        _ => format!("Anything else for {}?", item.from),
    };
    match &ask.reply {
        Some(reply) => {
            blocks.extend(reply.note.as_ref().filter(|n| !n.is_empty()).map(|n| {
                Block::custom(
                    "asks.note.block",
                    replied_text("asks.note".into(), "Note", n, t),
                )
            }));
            let on_note = sent_on(reply, None);
            if !on_note.is_empty() {
                blocks.push(Block::custom(
                    "asks.files.block",
                    sent_files("asks.files", &on_note, t),
                ));
            }
            if let Some(edit) = state.window.asks.follow_ups.get(&ask.id) {
                let id = ask.id.clone();
                blocks.push(Block::custom(
                    "asks.follow-up.block",
                    written_field(
                        Some(edit),
                        FOLLOW_UP,
                        "Follow up",
                        &if item.to_chief {
                            "What else should the Chief know?".to_owned()
                        } else {
                            format!("What else should {} know?", item.from)
                        },
                        move |state: &mut State| super::follow_up_edit(state, &id),
                        t,
                    ),
                ));
            }
        }
        None => {
            let on_note: Vec<&attach::File> = draft
                .into_iter()
                .flat_map(|d| d.files_on(&Stop::Note))
                .collect();
            if !super::note_open(state, ask) {
                blocks.push(Block::custom("asks.note.block", note_closed(&ask.id, t)));
                return with_errors(state, ask, draft, blocks, t);
            }
            let (id, pressed) = (ask.id.clone(), ask.id.clone());
            blocks.push(Block::custom(
                "asks.note.block",
                written_field(
                    draft.map(|d| &d.note),
                    NOTE,
                    "Note",
                    &note_placeholder,
                    move |state: &mut State| state.note(&id),
                    t,
                )
                .on_press_within(move |state: &mut State, _: &mut Tx| {
                    super::press_note(state, &pressed)
                }),
            ));
            blocks.push(Block::custom(
                "asks.files.block",
                drafted_files(&ask.id, &on_note, t),
            ));
        }
    }
    with_errors(state, ask, draft, blocks, t)
}

/// The ask's blocks, then why the daemon refused its answer or its draft's save, if it did.
fn with_errors(
    state: &State,
    ask: &Ask,
    draft: Option<&super::Draft>,
    mut blocks: Vec<Block>,
    t: &Tokens,
) -> Vec<Block> {
    let refused = state
        .window
        .asks
        .error
        .as_ref()
        .filter(|(id, _)| *id == ask.id)
        .map(|(_, why)| ("asks.error", why.clone()));
    let unsaved = draft.and_then(|d| d.refusal()).map(|why| {
        (
            "asks.draft-error",
            format!("Your draft was not saved: {why}"),
        )
    });
    blocks.extend(refused.into_iter().chain(unsaved).map(|(key, why)| {
        Block::custom(
            format!("{key}.block"),
            words(why.clone(), Type::Small, t.error)
                .key(key)
                .role("alert")
                .label(why)
                .wrap(),
        )
    }));
    blocks
}

fn open_agent(session: &Session, from: &str, t: &Tokens) -> El {
    let id = session.id.clone();
    ask_button(
        "asks.open-agent",
        "Open agent",
        &format!("Open {from} (o)"),
        glyph::POP_OUT,
        t,
        move |state: &mut State, tx: &mut Tx| super::open_agent(state, &id, tx),
    )
}

fn snooze_button(snoozed: bool, from: &str, t: &Tokens) -> El {
    let (said, help, mark) = if snoozed {
        ("Wake", format!("Wake {from} (s)"), glyph::SUNRISE)
    } else {
        (
            "Snooze",
            format!("Snooze {from} and its asks (s)"),
            glyph::MOON,
        )
    };
    ask_button(SNOOZE, said, &help, mark, t, super::toggle_snooze)
}

/// What a follow-up ask follows: the earlier ask's subject and when the person answered it, which
/// unfolds to that answer; the earlier ask's id alone when it was gone before this one opened.
fn follows(state: &State, ask_id: &str, re: &AskThread, prose: &Prose, t: &Tokens) -> Block {
    let Some(subject) = &re.subject else {
        return Block::custom(
            "asks.follows.block",
            words(
                format!("Follows {}", re.ask_id),
                Type::Small,
                t.text_secondary,
            )
            .key("asks.follows")
            .selectable(true)
            .wrap(),
        );
    };
    let answered = match &re.answered_at {
        Some(at) => format!(
            "you answered {}",
            crate::garden::model::ago(at, state.window.now_ms)
        ),
        None => "not answered".to_owned(),
    };
    let open = state.window.asks.follows_open.as_deref() == Some(ask_id);
    let id = ask_id.to_owned();
    let fold = Disclosure::new(
        "asks.follows",
        format!("Follows {} · {answered}", Line::plain(subject)),
        open,
        move |state: &mut State, _: &mut Tx| {
            let folds = &mut state.window.asks.follows_open;
            *folds = if folds.as_deref() == Some(id.as_str()) {
                None
            } else {
                Some(id.clone())
            };
        },
    )
    .body_key("asks.follows.answer");
    match &re.answer {
        Some(answer) => fold
            .push(Block::custom(
                "asks.follows.answer.block",
                col()
                    .w_full()
                    .child(prose.markdown("asks.follows.answer", answer)),
            ))
            .build(t),
        None => fold.disabled(true).build(t),
    }
}

/// Copy ask, Open agent, Snooze and Open in Garden: what to do with the ask besides answering it,
/// each an icon named `said` whose help gives its key.
fn ask_button(
    key: &str,
    said: &'static str,
    help: &str,
    mark: impl Into<attn_ui::Glyph>,
    t: &Tokens,
    press: impl Fn(&mut State, &mut Tx) + 'static,
) -> El {
    icon_button(key.to_owned(), said, mark, IconSize::Regular)
        .help(help)
        .on_press_tx(press)
        .build(t)
        .into()
}

enum Shown<'a> {
    Draft(Option<&'a super::Answer>),
    Replied(Option<&'a AskAnswer>),
}

impl Shown<'_> {
    fn picked(&self, label: &str) -> bool {
        match self {
            Shown::Draft(a) => a.is_some_and(|a| a.picked.iter().any(|p| p == label)),
            Shown::Replied(a) => a.is_some_and(|a| a.picked.iter().any(|p| p == label)),
        }
    }
}

/// A question, numbered, its words linking the seeds they name, then what it says about itself
/// and the choices or the field that answer it.
#[allow(clippy::too_many_arguments)]
fn question_section(
    ask: &str,
    index: usize,
    question: &AskQuestion,
    answer: Shown<'_>,
    files: Files<'_>,
    active: bool,
    prose: &Prose<'_>,
    t: &Tokens,
) -> Block {
    let kind = Kind::of(question);
    let number_ink = if active { t.accent } else { t.text_tertiary };
    let (prompt, _, marked) = prose.line(&question.prompt);
    let heading = Inline::top()
        .key(format!("asks.q.{}.prompt", question.key))
        .child(read_as(
            words(index.to_string(), Type::Name, number_ink),
            Reading::Heading,
        ))
        .child(marked(
            read_as(
                words(prompt, Type::Heading, t.text_primary),
                Reading::Heading,
            )
            .selectable(true)
            .wrap()
            .grow(1.0)
            .min_w(0.0),
        ));
    let context = question
        .context
        .as_ref()
        .filter(|c| !c.is_empty())
        .map(|c| {
            Block::custom(
                format!("asks.q.{}.context", question.key),
                prose
                    .markdown(&format!("asks.q.{}.context", question.key), c)
                    .w_full(),
            )
            .wide()
        });
    let body = match kind {
        Kind::Text => written(ask, question, &answer, t),
        Kind::One | Kind::Any => choice_list(ask, question, kind, &answer, active, prose, t),
        Kind::File => match files {
            Files::Drafted(files, dropping) => drop_zone(ask, &question.key, &files, dropping, t),
            Files::Sent(files) if files.is_empty() => {
                words("Not answered", Type::Small, t.text_tertiary)
                    .key(format!("asks.q.{}.files", question.key))
            }
            Files::Sent(files) => sent_files(&format!("asks.q.{}.files", question.key), &files, t),
        },
        Kind::Unknown => words(
            "This kind of question is new to this app. Update attn to answer it.",
            Type::Small,
            t.text_tertiary,
        )
        .key(format!("asks.q.{}.unknown", question.key))
        .wrap(),
    };
    let (ask_id, key) = (ask.to_owned(), question.key.clone());
    let section = Section::new(
        format!("asks.q.{}", question.key),
        Line::plain(&question.prompt),
    );
    let section = match prose.measure {
        Some(points) => section.measure(points),
        None => section,
    };
    section
        .heading(heading)
        .push_all(context)
        .push(Block::custom(
            format!("asks.q.{}.answer", question.key),
            body,
        ))
        .build(t)
        .map(|section| {
            section
                .a11y_value(if active { "active" } else { "" })
                .on_press_within(move |state: &mut State, _: &mut Tx| {
                    super::activate(state, &ask_id, Stop::Question(key.clone()))
                })
        })
        .wide()
}

/// The key that picks choice `n` of a question, drawn in the active question alone; the
/// others keep its room so nothing moves as the active question changes.
fn number_cap(n: usize, active: bool, t: &Tokens) -> Option<El> {
    (n <= 9).then(|| {
        let cap = attn_ui::keycap::Keys::ledged(&[n.to_string()])
            .build(t)
            .a11y_hidden();
        if active { cap } else { cap.opacity(0.0) }
    })
}

fn written(ask: &str, question: &AskQuestion, answer: &Shown<'_>, t: &Tokens) -> El {
    let key = question.key.clone();
    match answer {
        Shown::Replied(answer) => replied_text(
            format!("asks.q.{key}.text"),
            &Line::plain(&question.prompt),
            answer.and_then(|a| a.text.as_deref()).unwrap_or_default(),
            t,
        ),
        Shown::Draft(draft) => {
            let edit = draft.map(|a| &a.text);
            let empty = edit.is_none_or(|e| e.text.trim().is_empty());
            let placeholder = match &question.recommended {
                Some(r) if !r.is_empty() => format!("Suggested: {r}"),
                _ => "Your answer…".to_owned(),
            };
            let (ask_id, q) = (ask.to_owned(), key.clone());
            let field = written_field(
                edit,
                &text_key(&key),
                &Line::plain(&question.prompt),
                &placeholder,
                move |state: &mut State| &mut state.answer(&ask_id, &q).text,
                t,
            );
            let suggestion = question
                .recommended
                .clone()
                .filter(|r| !r.is_empty() && empty)
                .map(|r| {
                    let (ask_id, q) = (ask.to_owned(), key.clone());
                    El::from(
                        Button::new("Use the suggestion")
                            .size(Size::Compact)
                            .look(Look::Plain)
                            .on_press(move |state: &mut State| {
                                let answer = state.answer(&ask_id, &q);
                                answer.text = super::rewritten(&answer.text, r.clone());
                            })
                            .build(t)
                            .key(suggestion_key(&key)),
                    )
                    .self_start()
                });
            Stack::tight().w_full().child(field).children(suggestion)
        }
    }
}

fn choice_list(
    ask: &str,
    question: &AskQuestion,
    kind: Kind,
    answer: &Shown<'_>,
    active: bool,
    prose: &Prose<'_>,
    t: &Tokens,
) -> El {
    let replied = matches!(answer, Shown::Replied(_));
    let many = kind == Kind::Any;
    let n = question.choices.len();
    let options = question.choices.iter().enumerate().map(|(i, choice)| {
        let recommended = question.recommended.as_deref() == Some(choice.label.as_str());
        let (ask_id, q, label) = (ask.to_owned(), question.key.clone(), choice.label.clone());
        let (name, linked, marked) = prose.line(&choice.label);
        let mut shown = Choice::new(choice_key(&question.key, i), name)
            .name_marks(marked, linked)
            .many(many)
            .chosen(answer.picked(&choice.label))
            .disabled(replied)
            .selectable(true)
            .reads();
        if let Some(note) = choice.description.as_deref().filter(|d| !d.is_empty()) {
            let (note, linked, marked) = prose.line(note);
            shown = shown.note(note).note_marks(marked, linked);
        }
        if let Some(cap) = number_cap(i + 1, active, t).filter(|_| !replied) {
            shown = shown.trailing(cap);
        }
        if recommended {
            shown = shown.tag(
                words("Recommended", Type::Small, t.accent)
                    .key(format!("{}.recommended", choice_key(&question.key, i))),
            );
        }
        El::from(
            shown.build((i, n), t, move |state: &mut State, tx: &mut Tx| {
                super::pick(state, &ask_id, &q, kind, &label, tx)
            }),
        )
    });
    let condition = question
        .choices
        .iter()
        .any(|choice| answer.picked(&choice.label))
        .then(|| condition(ask, question, answer, t))
        .flatten();
    let group_key = format!("asks.q.{}.choices", question.key);
    let group = if many {
        checks(group_key, &Line::plain(&question.prompt))
    } else {
        answers(group_key, &Line::plain(&question.prompt))
    };
    let label = format!("Other answer to {}", Line::plain(&question.prompt));
    let other = match answer {
        Shown::Replied(a) => a
            .and_then(|a| a.other.as_deref())
            .filter(|o| !o.is_empty())
            .map(|o| replied_text(format!("asks.q.{}.other", question.key), &label, o, t)),
        Shown::Draft(draft) => {
            let (ask_id, q) = (ask.to_owned(), question.key.clone());
            let edit_of = (ask_id.clone(), q.clone());
            let empty = rune::TextEdit::default();
            let input = TextInput::new(draft.map(|a| &a.other).unwrap_or(&empty))
                .key(other_key(&question.key))
                .label(label)
                .placeholder("Other…")
                .on_edit(move |state: &mut State| &mut state.answer(&edit_of.0, &edit_of.1).other)
                .on_change(move |state: &mut State, _: &mut Tx| {
                    super::other_edited(state, &ask_id, &q, kind)
                })
                .on_key(field_key)
                .on_paste(super::paste_into_field);
            let field = El::from(line_box(input, FieldState::Ready, t))
                .grow(1.0)
                .min_w(0.0);
            Some(
                Inline::loose()
                    .w_full()
                    .child(field)
                    .children(number_cap(n + 1, active, t).map(attn_ui::system::key_under_choices)),
            )
        }
    };
    group.children(options).children(condition).children(other)
}

/// The condition under the picks: the user's, once replied; while answering, the link that opens
/// its field, then the field, labelled with what it qualifies.
fn condition(ask: &str, question: &AskQuestion, answer: &Shown<'_>, t: &Tokens) -> Option<El> {
    let picked = question
        .choices
        .iter()
        .filter(|choice| answer.picked(&choice.label))
        .map(|choice| Line::plain(&choice.label))
        .collect::<Vec<_>>()
        .join(", ");
    let label = format!("Condition on {picked}");
    let key = add_condition_key(&question.key);
    match answer {
        Shown::Replied(a) => a
            .and_then(|a| a.condition.as_deref())
            .map(|condition| replied_text(key, &label, &format!("Condition: {condition}"), t)),
        Shown::Draft(draft) => {
            let (ask_id, q) = (ask.to_owned(), question.key.clone());
            Some(match draft.and_then(|a| a.condition.as_ref()) {
                None => El::from(
                    Button::new("Add a condition")
                        .size(Size::Compact)
                        .look(Look::Plain)
                        .on_press_tx(move |state: &mut State, tx: &mut Tx| {
                            super::add_condition(state, &ask_id, &q, tx)
                        })
                        .build(t)
                        .key(key),
                )
                .self_start(),
                Some(edit) => {
                    let input = TextInput::new(edit)
                        .key(condition_key(&question.key))
                        .label(label)
                        .placeholder(format!("Your condition on {picked}…"))
                        .on_edit(move |state: &mut State| {
                            state
                                .answer(&ask_id, &q)
                                .condition
                                .get_or_insert_with(rune::TextEdit::default)
                        })
                        .on_key(field_key)
                        .on_paste(super::paste_into_field);
                    line_box(input, FieldState::Ready, t).into()
                }
            })
        }
    }
}

fn replied_text(key: String, label: &str, value: &str, t: &Tokens) -> El {
    let shown = if value.is_empty() {
        "Not answered"
    } else {
        value
    };
    row()
        .key(key)
        .role("group")
        .label(label.to_owned())
        .a11y_value(shown.to_owned())
        .w_full()
        .child(
            words(
                shown.to_owned(),
                Type::Body,
                if value.is_empty() {
                    t.text_tertiary
                } else {
                    t.text_secondary
                },
            )
            .selectable(true)
            .wrap(),
        )
}

/// A field of several lines that starts three lines high and that its grip drags taller. A
/// file or image pasted into it attaches to the open ask.
fn written_field(
    edit: Option<&rune::TextEdit>,
    key: &str,
    label: &str,
    placeholder: &str,
    lens: impl for<'a> Fn(&'a mut State) -> &'a mut rune::TextEdit + 'static,
    t: &Tokens,
) -> El {
    let empty = rune::TextEdit::default();
    let area = TextArea::new(edit.unwrap_or(&empty))
        .key(key.to_owned())
        .label(label.to_owned())
        .placeholder(placeholder.to_owned())
        .resizable()
        .on_edit(lens)
        .on_key(field_key)
        .on_paste(super::paste_into_field);
    area_box(area, FieldState::Ready, t).into()
}

/// The files at a stop of an answer, while it is written, or once it was sent.
enum Files<'a> {
    /// The files attached so far, and whether a drag is over the stop's drop zone.
    Drafted(Vec<&'a attach::File>, bool),
    Sent(Vec<&'a attn_protocol::Attachment>),
}

/// The files a reply carried on a question, or with no question on its note.
fn sent_on<'a>(
    reply: &'a attn_protocol::AskReply,
    question: Option<&str>,
) -> Vec<&'a attn_protocol::Attachment> {
    let on = |id: &str| {
        reply
            .answers
            .iter()
            .find(|a| a.attachments.iter().flatten().any(|f| f == id))
            .map(|a| a.key.as_str())
    };
    reply
        .attachments
        .iter()
        .flatten()
        .filter(|f| on(&f.id) == question)
        .collect()
}

/// What a drop on the sheet attaches to, as its overlay says.
fn drop_said(state: &State) -> String {
    let question =
        super::shown_open(state).and_then(|ask| match super::attach_target(state, &ask) {
            Stop::Question(key) => questions(&ask).position(|q| q.key == key),
            Stop::Note => None,
        });
    match question {
        Some(at) => format!("Drop to attach to question {}", at + 1),
        None => "Drop to attach to your answer".to_owned(),
    }
}

/// A file question's answer: the files attached to it, then Attach file, in a zone that takes
/// files dropped on it.
fn drop_zone(ask: &str, question: &str, files: &[&attach::File], dropping: bool, t: &Tokens) -> El {
    let (ask_id, key) = (ask.to_owned(), question.to_owned());
    let pick = El::from(
        Button::new("Attach file")
            .size(Size::Compact)
            .look(Look::Plain)
            .on_press_tx(move |state: &mut State, tx: &mut Tx| {
                super::activate(state, &ask_id, Stop::Question(key.clone()));
                attach::pick(&ask_id, Stop::Question(key.clone()), tx)
            })
            .build(t)
            .key(drop_key(question)),
    );
    let paste = if rune::Platform::current() == rune::Platform::Mac {
        "Drop a file here, or ⌘V pastes a screenshot"
    } else {
        "Drop a file here, or Ctrl+V pastes a screenshot"
    };
    let (ask_id, key) = (ask.to_owned(), question.to_owned());
    let hovered = question.to_owned();
    Inline::loose()
        .key(format!("asks.q.{question}.files"))
        .role("list")
        .label("Attached files")
        .w_full()
        .children(file_tray(ask, files, t))
        .child(pick)
        .child(words(paste, Type::Small, t.text_tertiary))
        .children(dropping.then(|| {
            drop_overlay(
                format!("asks.q.{question}.dropping"),
                "Drop to attach to this question",
                t,
            )
        }))
        .on_drop_files(
            move |state: &mut State, paths: &[std::path::PathBuf], tx: &mut Tx| {
                state.window.asks.dropping_on = None;
                let to = Stop::Question(key.clone());
                super::activate(state, &ask_id, to.clone());
                attach::dropped(state, &ask_id, &to, paths, tx);
            },
        )
        .on_drop_hover(move |state: &mut State, over: bool, _: &mut Tx| {
            state.window.asks.dropping_on = over.then(|| hovered.clone());
        })
}

/// The note while nothing is written or attached on it and the keyboard is elsewhere: Add a
/// note, which opens it, and Attach file.
fn note_closed(ask: &str, t: &Tokens) -> El {
    let add = El::from(
        Button::new("Add a note")
            .size(Size::Compact)
            .look(Look::Quiet)
            .trailing(keycaps(["n"], t))
            .help("Add a note to your answer (n)")
            .on_press_tx(|state: &mut State, tx: &mut Tx| {
                super::open_note(state, tx);
            })
            .build(t)
            .key(ADD_NOTE),
    )
    .label("Add a note n");
    Inline::loose()
        .key("asks.files")
        .w_full()
        .child(add)
        .child(attach_button(ask, t))
}

fn attach_button(ask: &str, t: &Tokens) -> El {
    let id = ask.to_owned();
    El::from(
        Button::new("Attach file")
            .size(Size::Compact)
            .look(Look::Plain)
            .on_press_tx(move |_: &mut State, tx: &mut Tx| attach::pick(&id, Stop::Note, tx))
            .build(t)
            .key(ATTACH),
    )
}

/// The files an answer will carry on its note, each with its remove button, then Attach file.
fn drafted_files(ask: &str, files: &[&attach::File], t: &Tokens) -> El {
    let pick = attach_button(ask, t);
    let paste = if rune::Platform::current() == rune::Platform::Mac {
        "⌘V pastes a screenshot"
    } else {
        "Ctrl+V pastes a screenshot"
    };
    Inline::loose()
        .key("asks.files")
        .role("list")
        .label("Attached files")
        .w_full()
        .children(file_tray(ask, files, t))
        .child(pick)
        .child(words(paste, Type::Small, t.text_tertiary))
}

/// Each file of a draft as a tile or a chip, with its remove button.
fn file_tray<'a>(
    ask: &'a str,
    files: &'a [&'a attach::File],
    t: &'a Tokens,
) -> impl Iterator<Item = El> + 'a {
    files.iter().map(move |file| {
        let (ask_id, file_id) = (ask.to_owned(), file.id.clone());
        let remove = icon_button(
            remove_file_key(&file.id),
            format!("Remove {}", file.name),
            glyph::TIMES,
            IconSize::Small,
        )
        .on_press_tx(move |state: &mut State, tx: &mut Tx| {
            attach::remove(state, &ask_id, &file_id, tx)
        })
        .build(t)
        .into();
        let status = match (file.failed(), file.uploading()) {
            (Some(_), _) => "failed",
            (None, true) => "uploading",
            (None, false) => "ready",
        };
        let key = format!("asks.file.{}", file.id);
        if let Some(picture) = file.picture.as_ref().filter(|p| p.is_ready()) {
            let frame = rune::el()
                .role("image")
                .size(attach::TILE.0, attach::TILE.1)
                .label(file.name.clone())
                .pixels_fit(picture.pixels(), SurfaceFit::Fill);
            return image_tile(key, &file.name, Some(frame), attach::TILE, Some(remove), t)
                .a11y_value(status);
        }
        let detail = match status {
            "failed" => "Failed".to_owned(),
            "uploading" => "Attaching…".to_owned(),
            _ => file_size(file.size),
        };
        file_chip(
            key,
            &file.name,
            file_kind(&file.name, file.picture.is_some()),
            &detail,
            Some(remove),
            t,
        )
        .a11y_value(status)
    })
}

/// The files a replied answer carried, by name and size.
fn sent_files(key: &str, files: &[&attn_protocol::Attachment], t: &Tokens) -> El {
    Inline::loose()
        .key(key.to_owned())
        .role("list")
        .label("Attached files")
        .w_full()
        .children(files.iter().map(|file| {
            file_chip(
                format!("asks.file.{}", file.id),
                &file.name,
                file_kind(&file.name, file.media_type.starts_with("image/")),
                file_size(file.bytes.max(0) as u64),
                None,
                t,
            )
        }))
}

/// A field's keys it does not take as text: ⌘↩ sends, Escape closes the sheet and ⇥ moves the
/// active stop.
fn field_key(state: &mut State, ev: &KeyEvent, tx: &mut Tx) -> bool {
    let m = ev.mods;
    match ev.key {
        Key::Enter if m.cmd || m.ctrl => super::send(state, tx),
        Key::Escape => super::close(state, tx),
        Key::Tab if !(m.cmd || m.ctrl || m.alt) => {
            return super::tab(state, if m.shift { -1 } else { 1 }, tx);
        }
        _ => return false,
    }
    true
}

fn footer(state: &State, item: &Item<'_>, several: bool, t: &Tokens) -> Block {
    let ask = item.ask;
    let snoozable = item
        .holder
        .filter(|s| !crate::outpost::offline(s, &state.facts.outposts));
    let snoozed = snoozable
        .is_some_and(|s| queue::is_snoozed(s.turn_snoozed_until.as_deref(), state.window.now_ms));
    let mut hints: Vec<(&[&str], &str)> = Vec::new();
    if several {
        hints.push((&["j", "k"], "next · previous"));
    }
    if item.holder.is_some() {
        hints.push((&["o"], "open agent"));
    }
    let picks: Option<[String; 1]> = match (ask.reply.is_none(), super::active(state, ask)) {
        (true, Stop::Question(key)) => questions(ask)
            .find(|q| q.key == key && Kind::of(q) != Kind::Text && !q.choices.is_empty())
            .map(|q| [format!("1–{}", q.choices.len().min(9))]),
        _ => None,
    };
    let picks_said: Vec<&str> = picks.iter().flatten().map(String::as_str).collect();
    if !picks_said.is_empty() {
        hints.push((&picks_said, "pick"));
        hints.push((&["↑", "↓", "space"], "move · pick"));
    }
    if ask.reply.is_none() && super::questions(ask).count() > 1 {
        hints.push((&["⇥"], "next question"));
    }
    if ask.reply.is_none() {
        hints.push((&["q"], "quote"));
    }
    if snoozable.is_some() {
        hints.push((&["s"], if snoozed { "wake" } else { "snooze" }));
    }
    hints.push((&["c"], "copy"));
    hints.push((&["r"], "replied"));
    hints.push((&["esc"], "close"));
    let sending = state.window.asks.sending.is_some();
    let accept =
        (ask.reply.is_none() && super::recommended(ask).len() > 1 && !sending).then(|| {
            let id = ask.id.clone();
            El::from(
                Button::new("Accept all recommended")
                    .size(Size::Compact)
                    .look(Look::Plain)
                    .trailing(keycaps(["a"], t))
                    .on_press(move |state: &mut State| super::accept_recommended(state, &id))
                    .build(t)
                    .key("asks.accept"),
            )
            .label("Accept all recommended a")
        });
    let action = match &ask.reply {
        Some(reply) => {
            let age = queue::turn_age(Some(&reply.answered_at), state.window.now_ms);
            let said = if age == "now" {
                "Replied just now".to_owned()
            } else {
                format!("Replied {age} ago")
            };
            let when = words(said.clone(), Type::Small, t.text_tertiary)
                .key("asks.replied-when")
                .label(said);
            Inline::loose()
                .key("asks.replied-actions")
                .child(when)
                .child(follow_up_button(state, item, t))
        }
        None => {
            let sendable = !sending
                && state
                    .window
                    .asks
                    .drafts
                    .get(&ask.id)
                    .is_some_and(|d| d.sendable(ask));
            let waiting = state
                .window
                .asks
                .drafts
                .get(&ask.id)
                .is_some_and(|d| d.sends_after_upload.is_some());
            let sendable = sendable && !waiting;
            let said = if sending || waiting {
                "Sending…"
            } else if item.to_chief {
                "Send to Chief"
            } else {
                "Send"
            };
            El::from(
                Button::new(said)
                    .look(Look::Primary)
                    .disabled(!sendable)
                    .trailing(keycaps([send_chord()], t))
                    .on_press_tx(super::send)
                    .build(t)
                    .key("asks.send"),
            )
            .label(format!("{said} {}", send_chord()))
        }
    };
    let showing_keys = state.window.asks.keys_shown;
    let keys = El::from(
        Button::new("Keys")
            .size(Size::Compact)
            .look(Look::Quiet)
            .chosen(showing_keys)
            .trailing(keycaps(["?"], t))
            .help(if showing_keys {
                "Hide the sheet's keys (?)"
            } else {
                "Show the sheet's keys (?)"
            })
            .on_press_tx(toggle_keys)
            .build(t)
            .key("asks.keys"),
    )
    .label("Keys ?");
    let hints_said = hints
        .iter()
        .map(|(keys, said)| format!("{} {said}", keys.join(" ")))
        .collect::<Vec<_>>()
        .join(" · ");
    let hints = showing_keys.then(|| {
        Inline::loose()
            .key("asks.hints")
            .label(hints_said)
            .min_w(0.0)
            .children(
                hints
                    .into_iter()
                    .map(|(keys, said)| keys_hint(keys.iter().copied(), said, t).a11y_hidden()),
            )
    });
    Block::custom(
        "asks.footer.block",
        Inline::loose()
            .key("asks.footer")
            .w_full()
            .child(
                Inline::loose()
                    .grow(1.0)
                    .min_w(0.0)
                    .child(keys)
                    .children(hints),
            )
            .children(accept)
            .child(action),
    )
}

/// The key path of the element at `path`: its keyed ancestors and itself, joined as layout reads
/// name an element, which leaves out the unkeyed ones a path counts as `#n`.
fn keyed_path(path: &str) -> String {
    path.split('/')
        .filter(|part| !part.starts_with('#'))
        .collect::<Vec<_>>()
        .join("/")
}

/// How far above the selection's first line the Quote button floats, and the least room it
/// keeps from the sheet's edges.
const QUOTE_ABOVE: f32 = 6.0;
const QUOTE_EDGE: f32 = 8.0;

/// The Quote button over the text the selection starts in, inside the sheet so the sheet's modal
/// lets it take the press; a press on it leaves the selection for it to read.
fn quote_button(text: &str, t: &Tokens) -> Option<El> {
    let first = rune::memo::window_layout(text)?;
    let sheet = rune::memo::window_layout(super::SHEET)?;
    let [w, h] = rune::memo::layout_size(QUOTE).unwrap_or([96.0, 28.0]);
    let left = (first[0] - sheet[0]).clamp(QUOTE_EDGE, (sheet[2] - w - QUOTE_EDGE).max(QUOTE_EDGE));
    let above = first[1] - sheet[1] - h - QUOTE_ABOVE;
    let top = if above >= QUOTE_EDGE {
        above
    } else {
        first[1] - sheet[1] + first[3] + QUOTE_ABOVE
    };
    if top + h > sheet[3] - QUOTE_EDGE {
        return None;
    }
    Some(
        El::from(
            Button::new("Quote")
                .size(Size::Compact)
                .look(Look::Primary)
                .trailing(keycaps(["q"], t))
                .help("Quote the selection in your note (q)")
                .on_press_tx(|state: &mut State, tx: &mut Tx| {
                    super::quote(state, tx);
                })
                .build(t)
                .key(QUOTE),
        )
        .label("Quote q")
        .keeps_text_selection()
        .absolute(left, top),
    )
}

/// A replied ask's Follow up, which opens its field, then sends what the field holds.
fn follow_up_button(state: &State, item: &Item<'_>, t: &Tokens) -> El {
    let ask = item.ask;
    let Some(edit) = state.window.asks.follow_ups.get(&ask.id) else {
        return El::from(
            Button::new("Follow up")
                .size(Size::Compact)
                .look(Look::Plain)
                .trailing(keycaps(["f"], t))
                .on_press_tx(super::follow_up)
                .build(t)
                .key("asks.follow-up"),
        )
        .label("Follow up f");
    };
    let sending = state.window.asks.sending.is_some();
    let said = if sending {
        "Sending…"
    } else if item.to_chief {
        "Send to Chief"
    } else {
        "Send follow-up"
    };
    El::from(
        Button::new(said)
            .look(Look::Primary)
            .disabled(sending || edit.text.trim().is_empty())
            .trailing(keycaps([send_chord()], t))
            .on_press_tx(super::send)
            .build(t)
            .key("asks.follow-up.send"),
    )
    .label(format!("{said} {}", send_chord()))
}

fn toggle_keys(state: &mut State, _: &mut Tx) {
    state.window.asks.keys_shown = !state.window.asks.keys_shown;
}

fn toggle_replied(state: &mut State, tx: &mut Tx) {
    state.window.asks.show_replied = !state.window.asks.show_replied;
    state.tick(tx);
    super::follow(state, tx);
}

/// The sheet's keys while it or a control in it holds focus: ⌘↩ sends from anywhere in it;
/// the arrows and the letters act only outside a text field.
fn sheet_key(state: &mut State, ev: &KeyEvent, tx: &mut Tx) -> bool {
    let m = ev.mods;
    if ev.key == Key::Enter && (m.cmd || m.ctrl) {
        super::send(state, tx);
        return true;
    }
    if ev.key == Key::Escape {
        super::close(state, tx);
        return true;
    }
    if ev.key == Key::Tab && !(m.cmd || m.ctrl || m.alt) {
        return super::tab(state, if m.shift { -1 } else { 1 }, tx);
    }
    if tx.in_text_field() || m.cmd || m.ctrl || m.alt {
        return false;
    }
    match ev.key {
        Key::Down if !m.shift => return super::arrow(state, 1, tx),
        Key::Up if !m.shift => return super::arrow(state, -1, tx),
        _ => {}
    }
    let Key::Text(typed) = &ev.key else {
        return false;
    };
    if let Ok(n) = typed.parse::<usize>() {
        return super::number(state, n, tx);
    }
    let shown = state.shown_ask();
    let ask = shown
        .as_ref()
        .and_then(|(id, _)| state.facts.asks.iter().find(|a| a.id == *id))
        .cloned();
    let holder = ask
        .as_ref()
        .and_then(|a| super::holder(a, &state.facts))
        .map(|s| s.id.clone());
    match typed.as_str() {
        "j" if ask.is_some() => super::step(state, 1, tx),
        "k" if ask.is_some() => super::step(state, -1, tx),
        "r" => toggle_replied(state, tx),
        "c" if ask.is_some() => super::copy(state, tx),
        "q" => return super::quote(state, tx),
        "n" => return super::open_note(state, tx),
        "?" => toggle_keys(state, tx),
        "f" if ask.as_ref().is_some_and(|a| a.reply.is_some()) => super::follow_up(state, tx),
        "a" => match &ask {
            Some(ask) if ask.reply.is_none() && super::recommended(ask).len() > 1 => {
                super::accept_recommended(state, &ask.id)
            }
            _ => return false,
        },
        "o" => match holder {
            Some(session) => super::open_agent(state, &session, tx),
            None => return false,
        },
        "s" if holder.is_some() => super::toggle_snooze(state, tx),
        "g" => match ask.as_ref().and_then(|a| a.seed_id.clone()) {
            Some(seed) => super::open_seed(state, &seed, tx),
            None => return false,
        },
        _ => return false,
    }
    true
}

/// The `?` an agent's row carries for its open asks, with their number past one.
pub(crate) fn marker(row_key: &str, count: usize, t: &Tokens) -> Option<El> {
    if count == 0 {
        return None;
    }
    let words = format!("{count} open {}", if count == 1 { "ask" } else { "asks" });
    Some(
        ask_mark(count, t)
            .key(format!("{row_key}.ask"))
            .role("img")
            .label(words),
    )
}

/// The agent's open-ask chip in its tile header: the oldest ask's subject and how many more, at
/// the level the tile's width leaves it. Resting the pointer on it previews the asks; a press
/// opens the sheet on the oldest.
pub(crate) fn chip(leaf: &str, session: &str, chip: &Chip, level: ChipLevel, t: &Tokens) -> El {
    let id = chip.ask.clone();
    let el = ask_chip(chip, level)
        .build(t)
        .key(chip_key(leaf))
        .on_press_tx(move |state: &mut State, tx: &mut Tx| {
            header_pop::close(state);
            super::show(state, Some(&id), tx)
        });
    header_pop::hovers(el, leaf, session, What::Ask)
}

fn ask_chip(chip: &Chip, level: ChipLevel) -> AskChip {
    let label = if chip.more > 0 {
        format!("Open ask: {}, and {} more", chip.subject, chip.more)
    } else {
        format!("Open ask: {}", chip.subject)
    };
    let up_to = match level {
        ChipLevel::Full => None,
        ChipLevel::Short => Some(CHIP_SHORT),
        ChipLevel::Icon => return AskChip::new(label),
    };
    AskChip::new(label)
        .subject(chip.subject.clone(), up_to)
        .more(chip.more)
}

/// The chip's widths in full, with its subject cut, and as its mark alone.
pub(crate) fn chip_widths(chip: &Chip) -> crate::desktop::Widths {
    let full = ask_chip(chip, ChipLevel::Full).width();
    let short = ask_chip(chip, ChipLevel::Short).width();
    crate::desktop::Widths {
        full,
        short: (short < full).then_some(short),
        icon: Some(ask_chip(chip, ChipLevel::Icon).width()),
    }
}

pub(crate) fn chip_key(leaf: &str) -> String {
    header_pop::anchor(leaf, What::Ask)
}

#[derive(Clone, Copy, Debug, PartialEq, Eq, Hash)]
pub(crate) enum ChipLevel {
    Full,
    Short,
    Icon,
}

pub(crate) const CHIP_SHORT: f32 = 120.0;

/// The preview of an agent's open asks under its chip: each ask's questions and choices, and
/// Answer, which opens the sheet on it.
pub(crate) fn preview(state: &State, pop: &Pop, t: &Tokens) -> Option<El> {
    let session = state.facts.sessions.iter().find(|s| s.id == pop.session)?;
    let asks = super::of_session(&state.facts, session);
    if asks.is_empty() {
        return None;
    }
    let previews = asks.iter().map(|ask| {
        let key = format!("asks.preview.{}", ask.id);
        Block::custom(format!("{key}.slot"), previewed(ask, &key, t))
    });
    Some(
        Popover::new(pop.surface(), "Open asks from this agent")
            .push_all(previews)
            .shown_only()
            .build(t)
            .on_hover_change(|state: &mut State, inside, tx: &mut Tx| {
                header_pop::hover_popover(state, inside, tx)
            })
            .anchored_fit(pop.anchor(), Side::Bottom, rune::Align::End, 4.0),
    )
}

/// One ask as its preview shows it: its subject, each question with its choices, and Answer.
fn previewed(ask: &Ask, key: &str, t: &Tokens) -> El {
    let id = ask.id.clone();
    let questions = questions(ask).enumerate().map(|(n, q)| {
        let lines: Vec<El> = match Kind::of(q) {
            Kind::Text => vec![words("Written answer", Type::Small, t.text_tertiary)],
            Kind::File => vec![words("A file or screenshot", Type::Small, t.text_tertiary)],
            Kind::Unknown => vec![words("Update attn to answer", Type::Small, t.text_tertiary)],
            _ => q
                .choices
                .iter()
                .enumerate()
                .map(|(i, c)| {
                    let recommended = q.recommended.as_deref() == Some(c.label.as_str());
                    let choice = format!("{key}.q.{n}.choice.{i}");
                    Inline::tight()
                        .child(
                            words(Line::plain(&c.label), Type::Small, t.text_secondary)
                                .key(format!("{choice}.name"))
                                .wrap()
                                .min_w(0.0)
                                .shrink(),
                        )
                        .children(c.description.as_deref().map(|d| {
                            words(Line::plain(d), Type::Small, t.text_tertiary)
                                .wrap()
                                .min_w(0.0)
                                .shrink()
                        }))
                        .children(recommended.then(|| {
                            El::from(tag(
                                format!("{choice}.recommended"),
                                "Recommended",
                                Tone::Accent,
                                t,
                            ))
                        }))
                })
                .collect(),
        };
        Stack::tight()
            .child(words(Line::plain(&q.prompt), Type::Body, t.text_primary).wrap())
            .children(lines)
    });
    let answer = Button::new("Answer")
        .size(Size::Compact)
        .look(Look::Plain)
        .on_press_tx(move |state: &mut State, tx: &mut Tx| {
            header_pop::close(state);
            super::show(state, Some(&id), tx)
        })
        .build(t);
    Stack::tight()
        .key(key.to_owned())
        .role("group")
        .label(Line::plain(&ask.subject))
        .child(words(Line::plain(&ask.subject), Type::Name, t.text_primary).wrap())
        .children(questions)
        .child(El::from(answer).key(format!("{key}.answer")).self_start())
}

/// The title bar's asks button, while asks are on or any exists: it counts the open asks, but a
/// snoozed agent's, and opens or closes the sheet.
pub(crate) fn title_button(state: &State, t: &Tokens) -> Option<El> {
    if !super::available(&state.facts) {
        return None;
    }
    let count = super::open_count(&state.facts, state.window.now_ms);
    let keys = state
        .keymap
        .binding(&crate::Action::DockAsks)
        .map(|b| format!(" ({})", state.keymap.label(b)))
        .unwrap_or_default();
    let label = match count {
        0 => format!("Asks{keys}"),
        1 => format!("1 open ask{keys}"),
        n => format!("{n} open asks{keys}"),
    };
    let open = super::is_open(state);
    let button = El::from(
        icon_button(
            "titlebar.asks",
            label,
            &attn_ui::icon::ASKS,
            IconSize::Large,
        )
        .count((count > 0).then(|| count.to_string()))
        .selected(open)
        .on_press_tx(|state: &mut State, tx: &mut Tx| super::toggle(state, tx))
        .build(t),
    );
    Some(crate::title_bar::opens(button, open).a11y_value(count.to_string()))
}

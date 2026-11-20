use std::collections::HashMap;
use std::rc::Rc;

use attn_protocol::{Ask, AskAnswer, AskQuestion, AskSeedMention, Session};
use attn_ui::blocks::{Block, Inline, List, Section, Sheet, Stack};
use attn_ui::system::{
    Button, Choice, DotShape, DotSize, FieldState, IconSize, ListRow, Look, Size, Tone, Type,
    answers, area_box, checks, count_badge, dot, icon_button, keycaps, keys_hint, line_box, tag,
    words,
};
use attn_ui::type_;
use attn_ui::{Tokens, glyph};
use rune::tok::space;
use rune::{El, Image, Key, KeyEvent, Side, Tx, col, row, text};
use rune_kit::{ImageFrame, Markdown, TextArea, TextInput};

use super::{Chip, Item, Kind, Lists, mentions, questions};
use crate::State;
use crate::desktop::header_pop::{self, Pop, What};
use crate::markdown::{images, links};
use crate::sidebar::queue;

pub(super) const SNOOZE: &str = "asks.snooze";
const OPEN_SEED: &str = "asks.open-seed";
const LIST: &str = "asks.list";

/// How many asks the list shows before it scrolls: three, and half of the next, so it reads as
/// a list that goes on and leaves the drawer to the ask it shows.
const SHOWN_ASKS: f32 = 3.5;

pub(super) fn choice_key(question: &str, i: usize) -> String {
    format!("asks.q.{question}.choice.{i}")
}

pub(super) fn other_key(question: &str) -> String {
    format!("asks.q.{question}.other.field")
}

pub(super) fn text_key(question: &str) -> String {
    format!("asks.q.{question}.text.field")
}

const NOTE: &str = "asks.note.field";

fn send_chord() -> &'static str {
    if rune::KeyPlatform::current() == rune::KeyPlatform::Mac {
        "⌘↩"
    } else {
        "Ctrl+↩"
    }
}

/// The drawer: the asks it lists, and the one it shows, with the questions to answer.
pub(crate) fn drawer(state: &State, t: &Tokens) -> El {
    let lists = Lists::of(&state.facts, state.window.now_ms);
    let live = lists.live.len();
    let replied = lists.replied.len();
    let asks = &state.window.asks;
    let items = lists.shown(asks.show_replied);
    let shown = state.shown_ask().map(|(id, _)| id);
    let close = icon_button("asks.close", "Close asks", glyph::TIMES, IconSize::Large)
        .on_press_tx(super::close)
        .build(t);
    let mut sheet = Sheet::new("asks.sheet", "Asks");
    if live > 0 {
        sheet = sheet.badge(
            count_badge(live, t.accent, t.bg_app)
                .key("asks.count")
                .role("status")
                .label(format!("{live} open")),
        );
    }
    let sheet = sheet
        .aside(replied_toggle(replied, asks.show_replied, t))
        .close(close);
    let sheet = match shown.and_then(|id| items.iter().position(|i| i.ask.id == id)) {
        None => sheet.push(empty(replied, t)),
        Some(at) => {
            let item = &items[at];
            sheet
                .top(list(&items, &item.ask.id, state.window.now_ms, t))
                .keeps_room(ListRow::two_line_height() * SHOWN_ASKS)
                .article("asks.ask", item.ask.subject.clone())
                .push_all(article(state, item, t))
                .footer(footer(state, item, items.len() > 1, t))
        }
    };
    col()
        .key(super::DRAWER)
        .role("region")
        .label("Asks")
        .focusable(true)
        .no_focus_ring()
        .on_key(drawer_key)
        .h_full()
        .min_h(0.0)
        .child(sheet.build(t))
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

fn list(items: &[Item<'_>], shown: &str, now_ms: i64, t: &Tokens) -> El {
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
    List::new(LIST, label)
        .listbox()
        .rows(rows)
        .scrolling_up_to(ListRow::two_line_height() * SHOWN_ASKS, t)
        .shrink()
        .min_h(ListRow::two_line_height())
}

fn divider(key: &str, said: &'static str, t: &Tokens) -> El {
    ListRow::note(key, said, Type::Label, t.text_tertiary).role("listitem")
}

fn list_row(item: &Item<'_>, selected: bool, now_ms: i64, t: &Tokens) -> El {
    let ask = item.ask;
    let mut about = vec![item.from.clone()];
    if item.questions > 1 {
        about.push(format!("{} questions", item.questions));
    }
    if let Some(until) = item.snoozed_until {
        about.push(format!(
            "until {}",
            queue::wake_time_short(Some(until), now_ms)
        ));
    }
    let waiting = ask.reply.is_none()
        && item.snoozed_until.is_none()
        && item.holder.is_some_and(|s| s.turn_owed == Some(true));
    if ask.reply.is_some() {
        about.push("replied".into());
    }
    let age = queue::turn_age(Some(super::age_from(ask)), now_ms);
    let mut said = vec![item.from.clone(), ask.subject.clone()];
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
    row.role("option")
        .unfocusable()
        .title(ask.subject.clone())
        .subtitle(about.join(" · "), t)
        .aside(words(age, Type::Mono, t.text_tertiary))
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
    pictures: Rc<HashMap<String, Image>>,
    document: String,
    t: &'a Tokens,
}

impl<'a> Prose<'a> {
    fn of(state: &'a State, ask: &Ask, t: &'a Tokens) -> Prose<'a> {
        Prose {
            names: mentions::of(state, &ask.id),
            pictures: state.pictures.of_ask(&ask.id),
            document: images::ask_document(ask),
            t,
        }
    }

    fn text(&self, el: El, content: &str) -> El {
        mentions::linked(el, content, self.names, self.t.accent)
    }

    fn markdown(&self, source: &str) -> El {
        let document = links::Document {
            path: self.document.clone(),
            session: None,
            outpost: None,
            view: "asks.ask".into(),
            opens_seed: None,
            seed: None,
            part: None,
        };
        let path = self.document.clone();
        let pictures = self.pictures.clone();
        Markdown::new(source)
            .text_links(mentions::seed_links(self.names))
            .style(crate::markdown::look(self.t))
            .on_link_tx(move |state: &mut State, href: &str, tx: &mut Tx| {
                match mentions::seed_of(href) {
                    Some(seed) => super::open_seed(state, seed, tx),
                    None => links::follow(state, &document, href, tx),
                }
            })
            .images(move |src| match links::resolve(&path, src) {
                Some(links::Target::Local(file)) => pictures.get(&file).cloned(),
                _ => None,
            })
            .image_frame(|shown: El, frame: &ImageFrame<'_>| {
                let (image, alt) = (frame.image.clone(), frame.alt.to_owned());
                shown.on_press_tx(move |state: &mut State, tx: &mut Tx| {
                    images::show(state, image.clone(), &alt, tx)
                })
            })
            .build(attn_ui::kit(self.t))
    }
}

fn article(state: &State, item: &Item<'_>, t: &Tokens) -> Vec<Block> {
    let ask = item.ask;
    let prose = Prose::of(state, ask, t);
    let snoozable = item
        .holder
        .filter(|s| !crate::outpost::offline(s, &state.facts.outposts));
    let snoozed = snoozable
        .is_some_and(|s| queue::is_snoozed(s.turn_snoozed_until.as_deref(), state.window.now_ms));
    let from = Inline::loose()
        .w_full()
        .children(item.holder.map(|s| {
            crate::sidebar::lead(
                "asks.from",
                &crate::sidebar::AgentRow::of(s, &state.facts.outposts, false),
                crate::sidebar::logos(state),
                t,
            )
        }))
        .child(
            words(item.from.clone(), Type::Name, t.text_primary)
                .key("asks.from")
                .grow(1.0)
                .min_w(0.0)
                .truncate(),
        )
        .children(item.holder.map(|s| open_agent(s, &item.from, t)))
        .children(snoozable.map(|_| snooze_button(snoozed, &item.from, t)));
    let mut blocks = vec![Block::custom("asks.from.block", from)];
    if let Some(seed) = &ask.seed_id {
        let id = seed.clone();
        blocks.push(Block::custom(
            "asks.seed.block",
            Inline::loose()
                .key("asks.seed")
                .w_full()
                .child(words(
                    ask.seed_slug.clone().unwrap_or_else(|| seed.clone()),
                    Type::Mono,
                    t.text_tertiary,
                ))
                .child(
                    words(
                        ask.seed_title.clone().unwrap_or_default(),
                        Type::Small,
                        t.text_secondary,
                    )
                    .grow(1.0)
                    .min_w(0.0)
                    .truncate(),
                )
                .child(ask_button(
                    OPEN_SEED,
                    "Open in Garden",
                    "Open the seed in the garden (g)",
                    t,
                    move |state: &mut State, tx: &mut Tx| super::open_seed(state, &id, tx),
                )),
        ));
    }
    blocks.push(Block::custom(
        "asks.subject.block",
        prose.text(
            words(ask.subject.clone(), Type::Heading, t.text_primary)
                .role("heading")
                .wrap(),
            &ask.subject,
        ),
    ));
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
                question_section(&ask.id, index, question, answer, &prose, t)
            }
            None => Block::custom(
                format!("asks.part.{at}.block"),
                col()
                    .key(format!("asks.part.{at}"))
                    .w_full()
                    .child(prose.markdown(part.markdown.as_deref().unwrap_or_default())),
            ),
        });
    }
    let note_placeholder = if item.holder.is_some() || ask.seed_id.is_none() {
        format!("Anything else for {}?", item.from)
    } else {
        "Anything else for its next tender?".to_owned()
    };
    match &ask.reply {
        Some(reply) => blocks.extend(reply.note.as_ref().filter(|n| !n.is_empty()).map(|n| {
            Block::custom(
                "asks.note.block",
                replied_text("asks.note".into(), "Note", n, t),
            )
        })),
        None => {
            let id = ask.id.clone();
            blocks.push(Block::custom(
                "asks.note.block",
                written_field(
                    draft.map(|d| &d.note),
                    NOTE,
                    "Note",
                    &note_placeholder,
                    move |state: &mut State| state.note(&id),
                    t,
                ),
            ));
        }
    }
    blocks.extend(
        state
            .window
            .asks
            .error
            .as_ref()
            .filter(|(id, _)| *id == ask.id)
            .map(|(_, why)| {
                Block::custom(
                    "asks.error.block",
                    words(why.clone(), Type::Small, t.error)
                        .key("asks.error")
                        .role("alert")
                        .label(why.clone())
                        .wrap(),
                )
            }),
    );
    blocks
}

fn open_agent(session: &Session, from: &str, t: &Tokens) -> El {
    let id = session.id.clone();
    ask_button(
        "asks.open-agent",
        "Open agent",
        &format!("Open {from} (o)"),
        t,
        move |state: &mut State, tx: &mut Tx| super::open_agent(state, &id, tx),
    )
}

fn snooze_button(snoozed: bool, from: &str, t: &Tokens) -> El {
    let (said, help) = if snoozed {
        ("Wake", format!("Wake {from} (s)"))
    } else {
        ("Snooze", format!("Snooze {from} and its asks (s)"))
    };
    ask_button(SNOOZE, said, &help, t, super::toggle_snooze)
}

/// Open agent, Snooze and Open in Garden: what to do with the ask besides answering it.
fn ask_button(
    key: &str,
    said: &'static str,
    help: &str,
    t: &Tokens,
    press: impl Fn(&mut State, &mut Tx) + 'static,
) -> El {
    Button::new(said)
        .size(Size::Compact)
        .look(Look::Plain)
        .help(help)
        .on_press_tx(press)
        .build(t)
        .key(key.to_owned())
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
fn question_section(
    ask: &str,
    index: usize,
    question: &AskQuestion,
    answer: Shown<'_>,
    prose: &Prose<'_>,
    t: &Tokens,
) -> Block {
    let kind = Kind::of(question);
    let heading = Inline::joined()
        .key(format!("asks.q.{}.prompt", question.key))
        .items_start()
        .child(words(index.to_string(), Type::Mono, t.text_tertiary))
        .child(
            prose.text(
                words(question.prompt.clone(), Type::Name, t.text_primary)
                    .wrap()
                    .grow(1.0)
                    .min_w(0.0),
                &question.prompt,
            ),
        );
    let context = question
        .context
        .as_ref()
        .filter(|c| !c.is_empty())
        .map(|c| {
            Block::custom(
                format!("asks.q.{}.context", question.key),
                prose.markdown(c).w_full(),
            )
        });
    let body = match kind {
        Kind::Text => written(ask, question, &answer, t),
        Kind::One | Kind::Any => choice_list(ask, question, kind, &answer, t),
    };
    Section::new(format!("asks.q.{}", question.key), question.prompt.clone())
        .heading(heading)
        .push_all(context)
        .push(Block::custom(
            format!("asks.q.{}.answer", question.key),
            body,
        ))
        .build(t)
}

fn written(ask: &str, question: &AskQuestion, answer: &Shown<'_>, t: &Tokens) -> El {
    let key = question.key.clone();
    match answer {
        Shown::Replied(answer) => replied_text(
            format!("asks.q.{key}.text"),
            &question.prompt,
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
                &question.prompt,
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
                            .key(format!("asks.q.{key}.suggestion")),
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
    t: &Tokens,
) -> El {
    let replied = matches!(answer, Shown::Replied(_));
    let many = kind == Kind::Any;
    let n = question.choices.len();
    let options = question.choices.iter().enumerate().map(|(i, choice)| {
        let recommended = question.recommended.as_deref() == Some(choice.label.as_str());
        let (ask_id, q, label) = (ask.to_owned(), question.key.clone(), choice.label.clone());
        let mut shown = Choice::new(choice_key(&question.key, i), choice.label.clone())
            .many(many)
            .chosen(answer.picked(&choice.label))
            .disabled(replied);
        if let Some(note) = choice.description.clone().filter(|d| !d.is_empty()) {
            shown = shown.note(note);
        }
        if recommended {
            shown = shown.tag(tag(
                format!("{}.recommended", choice_key(&question.key, i)),
                "Recommended",
                Tone::Accent,
                t,
            ));
        }
        El::from(
            shown.build((i, n), t, move |state: &mut State, _: &mut Tx| {
                super::pick(state, &ask_id, &q, kind, &label)
            }),
        )
    });
    let group_key = format!("asks.q.{}.choices", question.key);
    let group = if many {
        checks(group_key, &question.prompt)
    } else {
        answers(group_key, &question.prompt)
    };
    let label = format!("Other answer to {}", question.prompt);
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
                .on_key(field_key);
            Some(El::from(line_box(input, FieldState::Ready, t)))
        }
    };
    Stack::tight()
        .w_full()
        .child(group.children(options))
        .children(other)
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
            .wrap(),
        )
}

/// A field of several lines that starts three lines high and that its grip drags taller.
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
        .on_key(field_key);
    area_box(area, FieldState::Ready, t).into()
}

/// A drawer field's keys it does not take as text: ⌘↩ sends and Escape closes.
fn field_key(state: &mut State, ev: &KeyEvent, tx: &mut Tx) -> bool {
    match ev.key {
        Key::Enter if ev.mods.cmd || ev.mods.ctrl => super::send(state, tx),
        Key::Escape => {
            crate::shell::escape_drawers(state, tx);
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
    if snoozable.is_some() {
        hints.push((&["s"], if snoozed { "wake" } else { "snooze" }));
    }
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
            words(said.clone(), Type::Small, t.text_tertiary)
                .key("asks.replied-when")
                .label(said)
        }
        None => {
            let sendable = !sending
                && state
                    .window
                    .asks
                    .drafts
                    .get(&ask.id)
                    .is_some_and(|d| d.sendable(ask));
            let said = if sending { "Sending…" } else { "Send" };
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
    let hints_said = hints
        .iter()
        .map(|(keys, said)| format!("{} {said}", keys.join(" ")))
        .collect::<Vec<_>>()
        .join(" · ");
    Block::custom(
        "asks.footer.block",
        Inline::loose()
            .key("asks.footer")
            .w_full()
            .child(
                Inline::loose()
                    .key("asks.hints")
                    .label(hints_said)
                    .grow(1.0)
                    .min_w(0.0)
                    .children(hints.into_iter().map(|(keys, said)| {
                        keys_hint(keys.iter().copied(), said, t).a11y_hidden()
                    })),
            )
            .children(accept)
            .child(action),
    )
}

fn toggle_replied(state: &mut State, tx: &mut Tx) {
    state.window.asks.show_replied = !state.window.asks.show_replied;
    state.tick(tx);
    super::follow(state, tx);
}

/// The drawer's keys while it or a control in it holds focus: ⌘↩ sends from anywhere in it;
/// the letters act only outside a text field.
fn drawer_key(state: &mut State, ev: &KeyEvent, tx: &mut Tx) -> bool {
    let m = ev.mods;
    if ev.key == Key::Enter && (m.cmd || m.ctrl) {
        super::send(state, tx);
        return true;
    }
    if ev.key == Key::Escape {
        crate::shell::escape_drawers(state, tx);
        return true;
    }
    if tx.in_text_field() || m.cmd || m.ctrl || m.alt {
        return false;
    }
    let Key::Text(typed) = &ev.key else {
        return false;
    };
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
    let glyph = if count > 1 {
        format!("?{count}")
    } else {
        "?".to_owned()
    };
    Some(
        ask_mark(glyph, t)
            .key(format!("{row_key}.ask"))
            .role("img")
            .label(words),
    )
}

/// The Tauri app's `.ask-marker`: `glyph` in bold monospace on the accent pill.
fn ask_mark(glyph: String, t: &Tokens) -> El {
    row()
        .h(16.0)
        .min_w(MARK_W)
        .px(6.0)
        .center()
        .radius(10.0)
        .fill(t.accent)
        .child(text(glyph).font(MARKER_TYPE).color(t.bg_app))
}

/// The ask mark's width: its 6-point insets round "?", which measures 7.2 in `MARKER_TYPE`.
const MARK_W: f32 = 19.2;

/// The Tauri app's `.ask-marker`: bold monospace on the accent pill.
const MARKER_TYPE: rune::TextStyle = rune::TextStyle::new(12.0, 700.0).mono();

/// The agent's open-ask chip in its tile header: the oldest ask's subject and how many more, at
/// the level the tile's width leaves it. Resting the pointer on it previews the asks; a press
/// opens the drawer on the oldest.
pub(crate) fn chip(leaf: &str, session: &str, chip: &Chip, level: ChipLevel, t: &Tokens) -> El {
    let label = if chip.more > 0 {
        format!("Open ask: {}, and {} more", chip.subject, chip.more)
    } else {
        format!("Open ask: {}", chip.subject)
    };
    let id = chip.ask.clone();
    let subject = (level != ChipLevel::Icon).then(|| {
        let el = text(chip.subject.clone())
            .font(CHIP_TYPE)
            .color(t.text_primary)
            .single_line()
            .truncate()
            .min_w(0.0);
        if level == ChipLevel::Short {
            el.max_w(CHIP_SHORT)
        } else {
            el
        }
    });
    let tertiary = t.text_tertiary;
    let more = (chip.more > 0 && level != ChipLevel::Icon).then(|| format!("+{}", chip.more));
    let mark = ask_mark("?".to_owned(), t);
    let el = rune_kit::Button::new(label)
        .label_with(move |_, _| {
            row()
                .items_center()
                .gap(CHIP_GAP)
                .min_w(0.0)
                .child(mark)
                .children(subject)
                .children(more.map(|more| text(more).font(CHIP_MORE_TYPE).color(tertiary)))
        })
        .on_press_tx(move |state: &mut State, tx: &mut Tx| {
            header_pop::close(state);
            super::show(state, Some(&id), tx)
        })
        .build(attn_ui::kit(t))
        .key(chip_key(leaf))
        .h(CHIP_H)
        .pl(CHIP_START)
        .pr(CHIP_END)
        .min_w(0.0)
        .radius(CHIP_H / 2.0)
        .fill(t.accent.alpha(0.12))
        .border(1.0, t.accent.alpha(0.45));
    header_pop::hovers(el, leaf, session, What::Ask)
}

/// `.pane-ask-chip`: a pill tinted in the accent, its `?` mark close to its start.
const CHIP_H: f32 = 22.0;
const CHIP_GAP: f32 = 6.0;
const CHIP_START: f32 = 3.0;
const CHIP_END: f32 = 9.0;
const CHIP_TYPE: rune::TextStyle = rune::TextStyle {
    weight: 600.0,
    ..type_::LABEL
};
const CHIP_MORE_TYPE: rune::TextStyle = rune::TextStyle {
    weight: 500.0,
    ..type_::LABEL
};

/// The chip's widths in full, with its subject cut, and as its mark alone.
pub(crate) fn chip_widths(chip: &Chip) -> crate::desktop::Widths {
    let w = |s: &str| crate::desktop::text_w(s, CHIP_TYPE);
    let frame = CHIP_START + CHIP_END + 2.0 + MARK_W;
    let more = if chip.more > 0 {
        CHIP_GAP + crate::desktop::text_w(&format!("+{}", chip.more), CHIP_MORE_TYPE)
    } else {
        0.0
    };
    let subject = w(&chip.subject);
    crate::desktop::Widths {
        full: frame + CHIP_GAP + subject + more,
        short: (subject > CHIP_SHORT).then_some(frame + CHIP_GAP + CHIP_SHORT + more),
        icon: Some(frame),
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
/// Answer, which opens the drawer on it.
pub(crate) fn preview(state: &State, pop: &Pop, t: &Tokens) -> Option<El> {
    let session = state.facts.sessions.iter().find(|s| s.id == pop.session)?;
    let asks = super::of_session(&state.facts, session);
    if asks.is_empty() {
        return None;
    }
    let sections = asks.iter().map(|ask| {
        let id = ask.id.clone();
        let questions = questions(ask).map(|q| {
            let lines: Vec<El> = match Kind::of(q) {
                Kind::Text => vec![
                    text("Written answer")
                        .font(type_::LABEL)
                        .color(t.text_tertiary),
                ],
                _ => {
                    q.choices
                        .iter()
                        .map(|c| {
                            let recommended = q.recommended.as_deref() == Some(c.label.as_str());
                            row()
                                .gap(space::S1)
                                .child(
                                    text(c.label.clone())
                                        .font(type_::LABEL)
                                        .color(t.text_secondary),
                                )
                                .children(
                                    c.description
                                        .clone()
                                        .map(|d| text(d).font(type_::LABEL).color(t.text_tertiary)),
                                )
                                .children(recommended.then(|| {
                                    text("recommended").font(type_::LABEL).color(t.accent)
                                }))
                        })
                        .collect()
                }
            };
            col()
                .gap(2.0)
                .child(
                    text(q.prompt.clone())
                        .font(type_::BODY)
                        .color(t.text_primary)
                        .wrap(),
                )
                .children(lines)
        });
        col()
            .key(format!("asks.preview.{}", ask.id))
            .role("group")
            .label(ask.subject.clone())
            .gap(space::S1)
            .child(
                text(ask.subject.clone())
                    .font(type_::LABEL)
                    .color(t.text_primary),
            )
            .children(questions)
            .child(
                El::from(
                    Button::new("Answer")
                        .size(Size::Compact)
                        .look(Look::Plain)
                        .on_press_tx(move |state: &mut State, tx: &mut Tx| {
                            header_pop::close(state);
                            super::show(state, Some(&id), tx)
                        })
                        .build(t)
                        .key(format!("asks.preview.{}.answer", ask.id)),
                )
                .self_start(),
            )
    });
    Some(
        col()
            .key(pop.surface())
            .role("dialog")
            .label("Open asks from this agent")
            .w(300.0)
            .padding(space::S2)
            .gap(space::S2)
            .radius(6.0)
            .fill(t.bg_elevated)
            .border(1.0, t.border)
            .on_hover_change(|state: &mut State, inside, tx: &mut Tx| {
                header_pop::hover_popover(state, inside, tx)
            })
            .children(sections)
            .anchored_fit(pop.anchor(), Side::Bottom, rune::Align::End, 4.0),
    )
}

/// The title bar's asks button, while asks are on or any exists: it counts the open asks, but a
/// snoozed agent's, and opens or closes the drawer.
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
    let open = state.window.shows(crate::shell::Drawer::Asks);
    let button = El::from(
        icon_button(
            "titlebar.asks",
            label,
            &attn_ui::icon::ASKS,
            IconSize::Large,
        )
        .count((count > 0).then(|| count.to_string()))
        .on_press_tx(|state: &mut State, tx: &mut Tx| super::toggle(state, tx))
        .build(t),
    );
    Some(crate::sidebar::opens(button, open, t).a11y_value(count.to_string()))
}

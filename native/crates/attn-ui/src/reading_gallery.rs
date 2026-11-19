//! The gallery's stories for the reading blocks: the parts the garden, the review reader and
//! the markdown reader are built from, a variant per state.

use rune::{Cx, El, Playground, Story, TextEdit, Tx, col, el, row};

use crate::Tokens;
use crate::blocks::{
    Actions, Bar, Block, Byline, Card, Entry, Field, Form, Head, Lane, ListRow, Menu, Note, Reader,
    Room, Section, Standing, Trail, board, entries, lanes, rail,
};
use crate::colors::scheme_for;
use crate::system::{
    Button, FieldState, Link, Look, Size, Tone, Type, area_box, dot, marked, meter, tag, words,
};

/// What a reading story shows: the state its variant sets.
#[derive(Default)]
pub struct Reading {
    pub rings: bool,
    pub long: bool,
    pub width: Width,
    pub lit: bool,
    pub faded: bool,
    pub area: TextEdit,
}

/// How wide the story's reader is.
#[derive(Default, Clone, Copy)]
pub enum Width {
    /// A reader column of a window given over to the garden.
    #[default]
    Wide,
    /// The garden docked beside the work: its narrowest, 380.
    Narrow,
    /// One column of the garden's walk through a tree.
    Smallest,
}

impl Width {
    fn points(self) -> f32 {
        match self {
            Width::Wide => 760.0,
            Width::Narrow => 380.0,
            Width::Smallest => 300.0,
        }
    }
}

/// Every reading story, by name, with the keys of what it draws and the variants it adds to
/// the common ones: what the state sheets and the rule tests read.
pub const READING_SUBJECTS: &[(&str, &[&str], &[&str])] = &[
    ("Blocks/Bar", &["bar"], &[]),
    ("Blocks/List row", &["rows"], &[]),
    ("Blocks/Byline", &["byline"], &[]),
    ("Blocks/Entry", &["entries"], &[]),
    ("Blocks/Reader", &["reader"], &[]),
    ("Blocks/Card", &["cards"], &[]),
    ("Blocks/Lane", &["board"], &["Lit", "Faded"]),
    ("Blocks/Menu", &["menu"], &[]),
    ("Blocks/Form", &["form"], &[]),
];

const LONG_TITLE: &str = "Move the reading surfaces onto blocks so the garden, the review reader and the markdown reader share one system";
const LONG_SAID: &str = "Every agent that reads this seed's log sees this note before its first turn, so a long note here reaches all of them and wraps across the reader.";

pub fn stories<S: 'static>(playground: Playground<S>) -> Playground<S> {
    READING_SUBJECTS
        .iter()
        .fold(playground, |playground, &(name, _, extra)| {
            let view = view_of(name);
            let story = common(Story::new(name, Reading::default, view));
            let story = extra.iter().fold(story, |story, &variant| match variant {
                "Lit" => story.variant("Lit", |s| s.lit = true),
                "Faded" => story.variant("Faded", |s| s.faded = true),
                _ => story,
            });
            playground.story(story)
        })
}

type View = fn(&Reading, &mut Cx<Reading>) -> El;

fn view_of(name: &str) -> View {
    match name {
        "Blocks/Bar" => bar_story,
        "Blocks/List row" => list_rows,
        "Blocks/Byline" => facts_story,
        "Blocks/Entry" => entries_story,
        "Blocks/Reader" => reader_story,
        "Blocks/Card" => cards,
        "Blocks/Lane" => board_story,
        "Blocks/Menu" => menu_story,
        _ => form_story,
    }
}

fn common(story: Story<Reading>) -> Story<Reading> {
    story
        .variant("Rest", |_| {})
        .variant("Focus", |s| s.rings = true)
        .variant("Long", |s| s.long = true)
        .variant("Narrow", |s| s.width = Width::Narrow)
        .variant("Narrow long", |s| {
            s.width = Width::Narrow;
            s.long = true;
        })
        .variant("Smallest long", |s| {
            s.width = Width::Smallest;
            s.long = true;
        })
}

fn tokens(cx: &Cx<Reading>) -> Tokens {
    scheme_for(None).colors(cx.system_dark()).ui
}

fn stage(s: &Reading, cx: &mut Cx<Reading>, t: &Tokens, content: El) -> El {
    if s.rings {
        cx.force_focus_rings();
    }
    el().w_full().h_full().fill(t.bg_panel).child(
        col()
            .w_full()
            .h_full()
            .items_center()
            .child(col().w(s.width.points()).h_full().child(content)),
    )
}

fn noop(_: &mut Reading, _: &mut Tx) {}

fn title(s: &Reading, short: &'static str) -> &'static str {
    if s.long { LONG_TITLE } else { short }
}

fn said(s: &Reading, short: &'static str) -> &'static str {
    if s.long { LONG_SAID } else { short }
}

fn link(key: &str, shown: &str, t: &Tokens) -> El {
    let link: El = Link::new(key, shown).build(t).into();
    link.on_press_tx(noop)
}

fn quiet(key: &str, shown: &str, t: &Tokens) -> El {
    Button::new(shown)
        .look(Look::Quiet)
        .size(Size::Compact)
        .on_press_tx(noop)
        .build(t)
        .key(key)
        .into()
}

fn bar_story(s: &Reading, cx: &mut Cx<Reading>) -> El {
    let t = tokens(cx);
    let trail = Trail::new("bar.trail", "The way back")
        .step(link("bar.trail.0", "Garden", &t))
        .step(link("bar.trail.1", title(s, "Design system"), &t))
        .here("bar.trail.here", title(s, "Reading surfaces"), &t)
        .build(&t);
    let bar = Bar::new("bar")
        .lead(trail)
        .push(quiet("bar.closed", "3 closed", &t))
        .push(quiet("bar.close", "Close", &t))
        .build(&t);
    stage(s, cx, &t, bar)
}

fn seed_row(key: &str, s: &Reading, t: &Tokens, selected: bool, ink: rune::Color) -> El {
    ListRow::new(
        key,
        title(s, "Reading surfaces"),
        words(title(s, "Reading surfaces"), Type::Body, t.text_primary),
    )
    .mark(ink)
    .signal(words("parked", Type::Small, t.warning))
    .facts(
        Byline::new()
            .id("s-c72nmn", t.text_tertiary)
            .said("Builder", t)
            .said("2h ago", t),
    )
    .end(words("›", Type::Small, t.text_tertiary))
    .selected(selected)
    .build(t)
    .on_press_tx(noop)
}

fn list_rows(s: &Reading, cx: &mut Cx<Reading>) -> El {
    let t = tokens(cx);
    let query = "blocks";
    let found = said(s, "Move the reading surfaces onto blocks.");
    let marks: Vec<(usize, usize)> = found
        .find(query)
        .map(|at| vec![(at, at + query.len())])
        .unwrap_or_default();
    let searched = ListRow::new(
        "rows.search",
        "Garden search",
        marked(
            title(s, "Reading surfaces"),
            &[(0, 7)],
            Type::Body,
            t.text_primary,
            t.accent,
        ),
    )
    .mark(t.accent)
    .facts(Byline::new().id("s-c72nmn", t.text_tertiary))
    .snippet(marked(
        found,
        &marks,
        Type::Small,
        t.text_tertiary,
        t.accent,
    ))
    .build(&t)
    .on_press_tx(noop);
    let rows = col()
        .key("rows")
        .w_full()
        .child(seed_row("rows.plain", s, &t, false, t.accent))
        .child(seed_row("rows.selected", s, &t, true, t.success))
        .child(searched);
    stage(
        s,
        cx,
        &t,
        Reader::new()
            .push(Block::custom(rows))
            .build("rows.page", Room::Narrow, &t),
    )
}

fn facts_story(s: &Reading, cx: &mut Cx<Reading>) -> El {
    let t = tokens(cx);
    let facts = Byline::new()
        .key("byline")
        .wrap()
        .push(row().items_center().child(dot(t.accent)).child(words(
            " growing",
            Type::Small,
            t.text_secondary,
        )))
        .push(link("facts.tended", title(s, "tended by Builder"), &t))
        .said("3 of 5 checks", &t)
        .said("2h ago", &t)
        .id("s-c72nmn", t.text_tertiary)
        .build(&t);
    let meter = meter([(3.0, t.success), (1.0, t.warning), (1.0, t.border_subtle)]);
    stage(
        s,
        cx,
        &t,
        Reader::new()
            .push(Block::custom(facts))
            .push(Block::custom(meter))
            .build("facts.page", Room::Narrow, &t),
    )
}

fn entries_story(s: &Reading, cx: &mut Cx<Reading>) -> El {
    let t = tokens(cx);
    let note = Entry::new("entries.note", "Builder", "2h ago")
        .kind("note")
        .body(
            words(
                said(s, "Garden moved onto blocks."),
                Type::Body,
                t.text_primary,
            )
            .wrap(),
        )
        .build(&t);
    let handoff = Entry::new("entries.handoff", "Orchestrator", "1h ago")
        .kind("handoff")
        .accent(true)
        .body(
            words(
                said(s, "Ring me after the first surface."),
                Type::Body,
                t.text_primary,
            )
            .wrap(),
        )
        .build(&t);
    let quoted = rail(
        words(
            said(s, "Land on main once the checks pass."),
            Type::Body,
            t.text_primary,
        )
        .wrap(),
        true,
        &t,
    );
    let list = entries([note, handoff]).key("entries");
    stage(
        s,
        cx,
        &t,
        Reader::new()
            .push(Block::custom(list))
            .push(Block::custom(quoted))
            .build("entries.page", Room::Narrow, &t),
    )
}

fn reader_story(s: &Reading, cx: &mut Cx<Reading>) -> El {
    let t = tokens(cx);
    let head = Head::new("reader.title", title(s, "Reading surfaces"))
        .note(
            Byline::new()
                .wrap()
                .push(tag("reader.state", "growing", Tone::Good, &t))
                .said("tended by Builder", &t)
                .id("s-c72nmn", t.text_tertiary)
                .build(&t),
        )
        .aside(
            Button::new("Open as tile")
                .size(Size::Compact)
                .on_press_tx(noop)
                .build(&t)
                .key("reader.open"),
        );
    let related = Section::new("reader.related", "Related")
        .region()
        .push(Block::custom(link(
            "reader.related.0",
            title(s, "Design system"),
            &t,
        )))
        .push(Note::new("reader.related.note", said(s, "Nothing else links here.")).build(&t));
    let reader = Reader::new()
        .above(link("reader.back", "‹ Back to Design system", &t))
        .head(head)
        .push(
            Note::new(
                "reader.reason",
                said(s, "Harvested: the outcome is complete."),
            )
            .build(&t),
        )
        .push(related.build(&t))
        .build("reader", Room::Narrow, &t);
    stage(s, cx, &t, reader)
}

fn card(key: &str, s: &Reading, t: &Tokens, standing: Standing) -> El {
    Card::new(key)
        .control("button", title(s, "Reading surfaces"))
        .standing(standing)
        .push(words(title(s, "Reading surfaces"), Type::Body, t.text_primary).wrap())
        .push(
            Byline::new()
                .wrap()
                .push(words("ready", Type::Small, t.accent))
                .id("s-c72nmn", t.text_tertiary)
                .said("2h", t)
                .build(t),
        )
        .build(t)
        .on_press_tx(noop)
}

fn cards(s: &Reading, cx: &mut Cx<Reading>) -> El {
    let t = tokens(cx);
    let cards = col()
        .key("cards")
        .w_full()
        .child(card("cards.resting", s, &t, Standing::Resting))
        .child(card("cards.picked", s, &t, Standing::Picked))
        .child(
            Card::new("cards.lifted")
                .standing(Standing::Lifted)
                .push(words("park", Type::Small, t.accent))
                .push(words(said(s, "Why it waits"), Type::Body, t.text_tertiary).wrap())
                .build(&t),
        );
    stage(
        s,
        cx,
        &t,
        Reader::new()
            .push(Block::custom(cards))
            .build("cards.page", Room::Narrow, &t),
    )
}

fn board_story(s: &Reading, cx: &mut Cx<Reading>) -> El {
    let t = tokens(cx);
    let lane = |n: usize, name: &'static str| {
        let lane = Lane::new(
            format!("board.lane.{n}"),
            format!("board.body.{n}"),
            name,
            2,
        );
        let lane = if n == 2 {
            lane.aside(
                Button::new("show")
                    .look(Look::Quiet)
                    .size(Size::Compact)
                    .on_press_tx(noop)
                    .build(&t),
            )
        } else {
            lane
        };
        lane.lit(s.lit && n == 1)
            .faded(s.faded && n == 0)
            .push(card(&format!("board.card.{n}.0"), s, &t, Standing::Resting))
            .push(card(&format!("board.card.{n}.1"), s, &t, Standing::Picked))
            .build(&t)
    };
    let shown = board(
        "board",
        [lanes(
            "board.lanes",
            [lane(0, "Ready"), lane(1, "In progress"), lane(2, "Parked")],
        )],
    );
    stage(s, cx, &t, shown)
}

fn menu_story(s: &Reading, cx: &mut Cx<Reading>) -> El {
    let t = tokens(cx);
    let menu = Menu::new("menu", "Move Reading surfaces")
        .push(Menu::item("menu.park", "Park…", &t).on_press_tx(noop))
        .push(Menu::item("menu.wither", title(s, "Wither…"), &t).on_press_tx(noop))
        .build(&t);
    stage(
        s,
        cx,
        &t,
        Reader::new()
            .push(Block::custom(menu))
            .build("menu.page", Room::Narrow, &t),
    )
}

fn form_story(s: &Reading, cx: &mut Cx<Reading>) -> El {
    let t = tokens(cx);
    let area = rune_kit::TextArea::new(&s.area)
        .key("form.note.input")
        .label("What should the new agent know?")
        .placeholder(said(s, "Optional"))
        .rows(2)
        .on_edit(|s: &mut Reading| &mut s.area);
    let form = Form::new("form", "Handover")
        .push(
            Field::new(
                "form.note",
                title(s, "What should the new agent know? (optional)"),
            )
            .control(area_box(area, FieldState::Ready, &t))
            .build(&t),
        )
        .push(
            Actions::new()
                .push(
                    Button::new("Handover")
                        .look(Look::Primary)
                        .size(Size::Compact)
                        .on_press_tx(noop)
                        .build(&t)
                        .key("form.send"),
                )
                .push(quiet("form.cancel", "Cancel", &t))
                .build(),
        )
        .build(&t);
    stage(
        s,
        cx,
        &t,
        Reader::new()
            .push(form)
            .build("form.page", Room::Narrow, &t),
    )
}

//! The gallery's stories for the reading blocks: the parts the garden, the review reader and
//! the markdown reader are built from, a variant per state.

use rune::{Cx, El, Playground, Story, Tx, col, el, row};

use crate::Tokens;
use crate::blocks::{
    Bar, Block, Byline, Card, Entry, Figure, Head, Inline, KeyFit, KeyLine, Lane, Pane, Prose,
    Reader, Remark, Room, Section, Stack, Standing, Surface, Toolbar, Trail, Tray, Voice, board,
    entries,
    key_tab, lanes, place, rail,
};
use crate::colors::scheme_for;
use crate::system::{
    Button, IconSize, Join, Link, Look, Size, Space, Tone, Type, door, dot_on_line, icon_button,
    keys_hint, meter, split, tag, words,
};

/// What a reading story shows: the state its variant sets.
#[derive(Default)]
pub struct Reading {
    pub rings: bool,
    pub long: bool,
    pub width: Width,
    pub lit: bool,
    pub faded: bool,
    pub off: Off,
}

/// Which of a split control's halves can't act.
#[derive(Default, Clone, Copy, PartialEq, Eq)]
pub enum Off {
    #[default]
    Neither,
    /// The action and its menu.
    Both,
    /// Only the menu.
    Menu,
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
    ("Blocks/Byline", &["byline"], &[]),
    ("Blocks/Entry", &["entries"], &[]),
    ("Blocks/Reader", &["reader"], &[]),
    ("Blocks/Card", &["cards"], &[]),
    ("Blocks/Lane", &["board"], &["Lit", "Faded"]),
    ("Blocks/Surface", &["surfaces"], &["Lit"]),
    ("Blocks/Pane", &["panes"], &[]),
    ("Blocks/Remark", &["remarks"], &[]),
    ("Blocks/KeyLine", &["keys"], &[]),
    ("Blocks/Figure", &["figure"], &[]),
    ("Blocks/Toolbar", &["toolbar"], &[]),
    ("Blocks/Split", &["split"], &["Disabled", "Disabled menu"]),
    ("Blocks/Tray", &["tray"], &[]),
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
                "Disabled" => story.variant("Disabled", |s| s.off = Off::Both),
                "Disabled menu" => story.variant("Disabled menu", |s| s.off = Off::Menu),
                _ => story,
            });
            playground.story(story)
        })
}

type View = fn(&Reading, &mut Cx<Reading>) -> El;

fn view_of(name: &str) -> View {
    match name {
        "Blocks/Bar" => bar_story,
        "Blocks/Byline" => facts_story,
        "Blocks/Entry" => entries_story,
        "Blocks/Reader" => reader_story,
        "Blocks/Card" => cards,
        "Blocks/Surface" => surfaces_story,
        "Blocks/Pane" => panes_story,
        "Blocks/Remark" => remarks_story,
        "Blocks/KeyLine" => keys_story,
        "Blocks/Figure" => figure_story,
        "Blocks/Toolbar" => toolbar_story,
        "Blocks/Split" => split_story,
        "Blocks/Tray" => tray_story,
        _ => board_story,
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
    Button::new(shown.to_owned())
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

fn facts_story(s: &Reading, cx: &mut Cx<Reading>) -> El {
    let t = tokens(cx);
    let facts = Byline::new()
        .key("byline")
        .wrap()
        .push(
            row()
                .items_center()
                .child(dot_on_line(t.accent, Type::Small))
                .child(words(" growing", Type::Small, t.text_secondary)),
        )
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
            .push(Block::custom("facts.facts", facts))
            .push(Block::custom("facts.meter", meter))
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
            .push(Block::custom("entries.list", list))
            .push(Block::custom("entries.quoted", quoted))
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
        .region("Related")
        .push(Block::custom(
            "cards.link",
            link("reader.related.0", title(s, "Design system"), &t),
        ))
        .push(Prose::empty("reader.related.note", said(s, "Nothing else links here.")).build(&t));
    let reader = Reader::new()
        .above(link("reader.back", "‹ Back to Design system", &t))
        .head(head)
        .push(
            Prose::empty(
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
        .push(quiet(&format!("{key}.open"), "Open the plot", t))
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
            .push(Block::custom("cards.cards", cards))
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
            lane.aside(quiet("board.lane.2.show", "show", &t))
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

fn surfaces_story(s: &Reading, cx: &mut Cx<Reading>) -> El {
    let t = tokens(cx);
    let row_of = |n: usize, shown: &'static str, current: bool| {
        place(
            words(title(s, shown), Type::Mono, t.text_primary)
                .key(format!("surfaces.place.{n}"))
                .wrap(),
            current,
            &t,
        )
    };
    let listing = Surface::listing(
        col()
            .w_full()
            .child(row_of(0, "src/garden/view.rs", false))
            .child(row_of(1, "src/garden/board.rs", true))
            .child(row_of(2, "src/review/keys.rs", false))
            .child(crate::blocks::edged(
                words(
                    said(s, "A block the change rewrote."),
                    Type::Body,
                    t.text_primary,
                )
                .key("surfaces.marked.0")
                .wrap(),
                t.success,
                false,
                &t,
            ))
            .child(crate::blocks::edged(
                words("A block the cursor is on.", Type::Body, t.text_primary)
                    .key("surfaces.marked.1")
                    .wrap(),
                t.success,
                s.lit,
                &t,
            )),
        &t,
    );
    let plain = |key: &'static str, said: &'static str| {
        words(said, Type::Body, t.text_secondary).key(key).wrap()
    };
    let card = Surface::card(
        crate::system::door::inset(
            col()
                .w_full()
                .child(plain("surfaces.card", "A card among others")),
            crate::system::Space::S12,
            crate::system::Space::S8,
        ),
        &t,
    );
    let document = Surface::document(
        plain("surfaces.document", said(s, "A document on its card.")),
        crate::blocks::document_inset(s.width.points()),
        &t,
    );
    let figure = Surface::figure(
        plain("surfaces.figure", "A figure on its own ground"),
        t.bg_app,
        t.border,
    );
    let raised = Surface::raised(
        crate::system::door::inset(
            col()
                .w_full()
                .child(plain("surfaces.raised", "A menu raised over its page")),
            crate::system::Space::S12,
            crate::system::Space::S8,
        ),
        &t,
    );
    let page = Reader::new()
        .push(Block::custom("surfaces.listing", listing))
        .push(Block::custom("surfaces.raised", raised))
        .push(Block::custom("surfaces.card", card))
        .push(Block::custom("surfaces.document", document))
        .push(Block::custom("surfaces.figure", figure))
        .build("surfaces.page", Room::Narrow, &t)
        .key("surfaces");
    stage(s, cx, &t, page)
}

fn panes_story(s: &Reading, cx: &mut Cx<Reading>) -> El {
    let t = tokens(cx);
    let column = |key: &'static str, said: &'static str| {
        words(title(s, said), Type::Body, t.text_primary)
            .key(key)
            .wrap()
    };
    let panes = row()
        .key("panes")
        .w_full()
        .h(240.0)
        .child(
            Pane::new("panes.fixed", column("panes.fixed.words", "Design system"))
                .width(120.0)
                .build(&t),
        )
        .child(
            Pane::new(
                "panes.share",
                column("panes.share.words", "Reading surfaces"),
            )
            .share(1.0, 80.0)
            .ruled()
            .build(&t),
        )
        .child(Pane::new("panes.rest", column("panes.rest.words", "Garden step")).build(&t));
    stage(s, cx, &t, panes)
}

fn tray_story(s: &Reading, cx: &mut Cx<Reading>) -> El {
    let t = tokens(cx);
    let remark = |n: usize, voice: Voice, who: &'static str, said_: &'static str| {
        Remark::new(who, "L12", said(s, said_))
            .voice(voice)
            .key(format!("tray.{n}"))
            .build(&t)
    };
    let panel = col()
        .key("tray.panel")
        .w_full()
        .child(
            Bar::new("tray.head")
                .lead(words("totals.rs", Type::Mono, t.text_primary).key("tray.path"))
                .push(quiet("tray.close", "close", &t))
                .build(&t),
        )
        .child(el().w_full().h(120.0))
        .child(
            Tray::new("tray", "Marks on this line")
                .push(remark(0, Voice::Look, "Builder", "Look at this line."))
                .push(remark(1, Voice::Yours, "You", "Why here?"))
                .build(&t),
        );
    stage(s, cx, &t, panel)
}

fn remarks_story(s: &Reading, cx: &mut Cx<Reading>) -> El {
    let t = tokens(cx);
    let remark = |n: usize, voice: Voice, who: &'static str, said_: &'static str| {
        Remark::new(who, "L12–14", said(s, said_))
            .voice(voice)
            .key(format!("remarks.{n}"))
            .control(quiet(&format!("remarks.{n}.reply"), "Reply", &t))
            .build(&t)
    };
    let remarks = col()
        .key("remarks")
        .w_full()
        .child(remark(0, Voice::Agent, "Builder", "Moved onto blocks."))
        .child(remark(1, Voice::Look, "Builder", "Look at this line."))
        .child(remark(2, Voice::Yours, "You", "Why here?"))
        .child(remark(3, Voice::Edited, "You", "Renamed the row."));
    stage(
        s,
        cx,
        &t,
        Reader::new()
            .push(Block::custom("remarks.remarks", remarks))
            .build("remarks.page", Room::Narrow, &t),
    )
}

/// The KeyLine story's frame tabs, by their keys and names.
pub const KEY_TABS: &[(&str, &str)] = &[("1", "Diff"), ("2", "Files")];

/// The KeyLine story's hints, by their keys and what they do: the first [`SHORT_HINTS`] in
/// every state, all of them in its long one.
pub const KEY_HINTS: &[(&[&str], &str)] = &[
    (&["j", "k"], "move"),
    (&["n"], "note"),
    (&["Enter"], "open"),
    (&["⌘", "Enter"], "send"),
    (&["["], "previous file"),
    (&["]"], "next file"),
    (&["/"], "search"),
    (&["Esc"], "close"),
];

const SHORT_HINTS: usize = 3;

fn keys_story(s: &Reading, cx: &mut Cx<Reading>) -> El {
    let t = tokens(cx);
    let hints = if s.long {
        KEY_HINTS
    } else {
        &KEY_HINTS[..SHORT_HINTS]
    };
    let line =
        KeyLine::new("keys", "Keys").tabs(KEY_TABS.iter().enumerate().map(|(at, (keys, name))| {
            key_tab(keys, name, at == 0, &t).key(format!("keys.tab.{keys}"))
        }));
    let line = hints.iter().fold(line, |line, (keys, what)| {
        line.push(
            keys_hint(keys.iter().copied(), what.to_owned(), &t).key(format!("keys.hint.{what}")),
        )
    });
    let fit = match s.width {
        Width::Wide => KeyFit::Line,
        Width::Narrow => KeyFit::Under,
        Width::Smallest => KeyFit::Tabs,
    };
    let status = door::gap(row().items_center(), Space::S8)
        .key("keys.status")
        .child(words("go no server", Type::Small, t.warning))
        .child(
            Button::new("set up")
                .size(Size::Compact)
                .on_press_tx(noop)
                .build(&t)
                .key("keys.status.setup")
                .into(),
        );
    let line = line
        .fit(fit)
        .status(status)
        .end(keys_hint(["?"], "all keys", &t))
        .build(&t);
    stage(s, cx, &t, col().w_full().child(line))
}

/// A figure's parts as a sequence diagram draws them: a group's box, a participant's node and
/// a person's, and a message's band at rest and current.
fn figure_story(s: &Reading, cx: &mut Cx<Reading>) -> El {
    let t = tokens(cx);
    let node = |n: usize, person: bool, name: &'static str| {
        Figure::node(120.0, 30.0, person, &t)
            .key(format!("figure.node.{n}"))
            .child(words(name, Type::Control, t.text_primary).key(format!("figure.node.{n}.name")))
    };
    let band = |n: usize, current: bool, said: &'static str| {
        Figure::band(260.0, 30.0, current, &t)
            .key(format!("figure.band.{n}"))
            .role("row")
            .label(said)
            .items_center()
            .child(words(said, Type::Small, t.text_secondary))
    };
    let figure = door::gap(col(), Space::S8)
        .key("figure")
        .child(
            door::gap(row(), Space::S16)
                .child(node(0, false, "Calc"))
                .child(node(1, true, "Reader")),
        )
        .child(Figure::group(260.0, 48.0, &t).key("figure.group"))
        .child(band(0, false, "Add(a, b)"))
        .child(band(1, true, "helper()"));
    stage(s, cx, &t, figure)
}

fn toolbar_story(s: &Reading, cx: &mut Cx<Reading>) -> El {
    let t = tokens(cx);
    let tool = |key: &str, glyph: &'static str, label: &str, ink| {
        let button: El = icon_button(key, label, glyph, IconSize::Regular)
            .size(Size::Regular)
            .ink(ink)
            .expanded(s.long && key == "toolbar.label")
            .on_press_tx(noop)
            .build(&t)
            .into();
        button
    };
    let under = s.long.then(|| {
        Stack::tight()
            .key("toolbar.picker")
            .child(quiet("toolbar.pick.0", "👍 I agree", &t))
            .child(quiet("toolbar.pick.1", "Cut this", &t))
    });
    let toolbar = Toolbar::new("toolbar", "Annotate selection")
        .group([
            tool("toolbar.delete", crate::glyph::BAN, "Delete", t.error),
            tool(
                "toolbar.comment",
                crate::glyph::COMMENT,
                "Comment",
                t.warning,
            ),
            tool(
                "toolbar.label",
                crate::glyph::BOLT,
                "Quick label",
                t.warning,
            ),
            tool("toolbar.agree", "👍", "I agree", t.text_secondary),
        ])
        .group([tool(
            "toolbar.cancel",
            crate::glyph::TIMES,
            "Cancel",
            t.text_secondary,
        )])
        .under(under)
        .build(&t);
    stage(s, cx, &t, row().child(toolbar))
}

fn split_story(s: &Reading, cx: &mut Cx<Reading>) -> El {
    let t = tokens(cx);
    let more = |key: &str| -> El {
        Button::mark(crate::glyph::CARET_DOWN, IconSize::Small)
            .size(Size::Compact)
            .joined(Join::Previous)
            .disabled(s.off != Off::Neither)
            .on_press_tx(noop)
            .build(&t)
            .key(key)
            .label("Change annotation destination")
            .into()
    };
    let send: El = Button::new("Send 3")
        .size(Size::Compact)
        .then_quiet(if s.long {
            "to the session with the longest name"
        } else {
            "to Alder"
        })
        .joined(Join::Next)
        .disabled(s.off == Off::Both)
        .on_press_tx(noop)
        .build(&t)
        .key("split.send")
        .into();
    let failed: El = Button::new("Send failed")
        .size(Size::Compact)
        .leading(words("!", Type::Control, t.warning))
        .then_quiet("to Alder")
        .joined(Join::Next)
        .on_press_tx(noop)
        .build(&t)
        .key("split.failed")
        .into();
    let sent: El = Button::new("Sent")
        .size(Size::Compact)
        .leading(words("✓", Type::Control, t.success))
        .on_press_tx(noop)
        .build(&t)
        .key("split.sent")
        .into();
    let line = |key: &str, parts: Vec<El>| Inline::line().key(key.to_owned()).children(parts);
    let content = Stack::tight()
        .key("split")
        .child(line(
            "split.ready",
            vec![
                quiet("split.notes", "Notes 3", &t),
                split(send, more("split.send.menu")),
            ],
        ))
        .child(line(
            "split.problem",
            vec![split(failed, more("split.failed.menu"))],
        ))
        .child(line("split.done", vec![sent]));
    stage(s, cx, &t, content)
}

//! A markdown tile's body: the document the daemon reads from disk, rendered.

mod anchoring;
pub(crate) mod anchors;
pub(crate) mod annotating;
pub(crate) mod drafts;
pub(crate) mod find;
pub(crate) mod keys;
pub(crate) use keys::{on_key as reading_key, open_find};
mod front_matter;
pub(crate) use front_matter::{Value as FrontMatterValue, entries as front_matter_entries};
pub(crate) mod ids;
pub(crate) mod images;
pub(crate) mod links;
mod marks;
mod passages;
pub(crate) mod sending;

use std::collections::HashMap;
use std::rc::Rc;

use attn_ui::blocks::{DOCUMENT_EDGE, DOCUMENT_GUTTER, Inline, Stack, Surface, document_inset};
use attn_ui::system::{
    Button as AttnButton, Reading, Size, Type, dot_on_line, read_as as set_in, read_aside,
    words as attn_words,
};
use attn_ui::{GlyphButton, Tokens, glyph};
use rune::{Color, El, Image, LinkPaint, ScrollSpec, Span, Tx, col, memo, text};
use rune_kit::ScrollBar;
use rune_kit::{CodeFrame, ImageFrame, Markdown, MarkdownStyle, MissingImage};

use crate::State;

pub use annotating::Readings;
pub use drafts::Drafts;
pub use images::Pictures;
pub(crate) use images::{follow as follow_images, over as with_image_preview};
pub(crate) use links::Document;

/// How far a code block's Copy button sits in from the block's top right corner.
const COPY_INSET: f32 = 8.0;

/// The widest paragraphs, lists, quotes and headings run; tables, code, diagrams and front
/// matter take the card.
const PROSE_MEASURE: f32 = attn_ui::blocks::DOCUMENT;
/// The reader's card around `reading`, `padding` inside its border.
pub(crate) fn card(padding: f32, reading: El, t: &Tokens) -> El {
    Surface::document(reading, padding, t)
}

/// The card's padding and the text column inside it, in a document view `area` points across,
/// for a card at most `widest` points wide.
fn card_fit(area: Option<u32>, widest: f32) -> (f32, Option<f32>) {
    let Some(area) = area else {
        return (document_inset(0.0), None);
    };
    let room = area as f32 - 2.0 * DOCUMENT_GUTTER;
    let padding = document_inset(room);
    let text = room.min(widest) - 2.0 * (padding + DOCUMENT_EDGE);
    (padding, Some(text.max(0.0)))
}

/// The body of the document tile `tile`, opened for `session`, from what the daemon last sent
/// for it. `outposts` names the outpost a document is on.
pub(crate) fn body(
    (tile, held): (&str, bool),
    title: &str,
    session: Option<&str>,
    content: Option<&attn_client::TileContent>,
    (outposts, servers): (&[attn_protocol::OutpostInfo], crate::review::setup::Servers),
    (pictures, drafts, readings): (&Pictures, &Drafts, &Readings),
    t: &Tokens,
) -> El {
    let Some(attn_client::TileContent {
        revision,
        message: content,
    }) = content
    else {
        return message(t, "Loading…", false);
    };
    if let Some(error) = content.error.as_deref().filter(|e| !e.is_empty()) {
        return message(t, error, true);
    }
    if content.content.trim().is_empty() {
        return message(t, "This file is empty.", false);
    }
    let view = format!("tile.{tile}.document");
    let gutter = format!("{view}.gutter");
    let outpost = (content.host.as_deref().filter(|h| !h.is_empty())).map(|host| links::Outpost {
        host: host.to_owned(),
        name: crate::desktop::outpost_name(host, outposts),
    });
    let uri = drafts::Source::File {
        path: content.path.clone(),
        host: outpost.as_ref().map(|o| o.host.clone()),
    }
    .uri();
    let mut marks = annotating::highlights(drafts.get(&uri), readings.tiles.get(tile), tile, t);
    let finding = (content.review.is_none())
        .then(|| find::finding(readings, &view))
        .flatten();
    let found = find::highlights(finding, &content.content, &view);
    let matches = found.len();
    marks.extend(found);
    let kept = readings.kept(&view);
    let (widest, measure) = if content.review.is_some() {
        (crate::review::CARD_MAX_WIDTH, PROSE_MEASURE)
    } else {
        (f32::INFINITY, PROSE_MEASURE)
    };
    let (padding, text_width) = card_fit(kept.width, widest);
    let version = (
        *revision,
        title,
        session,
        outpost.as_ref().map(|o| o.name.clone()),
        pictures.revision,
        (marks.clone(), kept.clone()),
        *t,
    );
    let scroll = |card: El| {
        let document = col()
            .key(view.clone())
            .role("document")
            .label(title)
            .holds_keys(held)
            .w_full()
            .grow(1.0)
            .min_h(0.0)
            .scroll(ScrollSpec {
                stick_bottom: false,
            })
            .items_center()
            .child(card)
            .scroll_bar_styled(attn_ui::document_scroll_bar(t));
        Surface::gutter(col().key(gutter.clone()).w_full().grow(1.0).min_h(0.0))
            .on_layout_tx({
                let view = view.clone();
                move |state: &mut State, [_, _, w, _], tx: &mut Tx| {
                    state.readings.measured(&view, w, tx)
                }
            })
            .child(document)
    };
    let card = |reading: El| card(padding, reading, t);
    let document = links::Document {
        path: content.path.clone(),
        session: session.map(str::to_owned),
        outpost: outpost.clone(),
        view: view.clone(),
        opens_seed: None,
        seed: None,
        part: None,
        ids: kept.ids.clone(),
    };
    let read_source = |source: &str, part: Option<usize>, marking: Marking| {
        let pictures = pictures.of(outpost.as_ref().map(|o| o.host.as_str()));
        reader(
            source,
            links::Document {
                part,
                ..document.clone()
            },
            pictures,
            kept.clone(),
            Some(marking),
            match text_width {
                Some(width) => look(t).prose_measure(measure).text_width(width),
                None => look(t).prose_measure(measure),
            },
            t,
        )
    };
    if let Some(bundle) = content.review.as_ref() {
        let read_part = |part: std::ops::Range<usize>| {
            let marking = Marking {
                by: Some((tile.to_owned(), uri.clone())),
                highlights: within(&marks, &part),
            };
            read_source(&content.content[part.clone()], Some(part.start), marking)
        };
        let card = crate::review::card_key(tile);
        let seen = format!("tile.{tile}.document");
        let marking = annotating::in_document(readings, tile, (&card, &seen), padding, t);
        return crate::review::body(
            (tile, &view, title, padding, held),
            (&content.content, bundle, *revision),
            readings,
            (
                &document,
                &pictures.of(outpost.as_ref().map(|o| o.host.as_str())),
                pictures.revision,
            ),
            (&read_part, {
                let mut h = std::hash::DefaultHasher::new();
                std::hash::Hash::hash(&(session, &version.3, version.4, &kept, &marks), &mut h);
                std::hash::Hasher::finish(&h)
            }),
            marking,
            drafts.get(&uri).map_or(&[][..], |d| &d.marks[..]),
            (content.host.as_deref().filter(|h| !h.is_empty()), servers),
            t,
        );
    }
    let root = annotating::root_key(tile);
    let reading = memo(root.clone(), version, || {
        read_source(
            &content.content,
            None,
            Marking {
                by: Some((tile.to_owned(), uri)),
                highlights: marks,
            },
        )
    });
    let seen = format!("tile.{tile}.document");
    let marking = annotating::in_document(readings, tile, (&root, &seen), padding, t);
    let bar = finding.map(|finding| find::bar(&view, finding, matches, keys::source(tile), t));
    scroll(card(reading).children(marking)).children(bar)
}

/// The document `content` holds, read `width` points across as its tile reads it, with no marks and
/// none of its tile's tools. `view` names it apart from its tile. None when there is nothing to
/// read.
pub(crate) fn still(
    view: String,
    width: f32,
    content: &attn_client::TileContent,
    (pictures, ids): (&Pictures, &crate::terminal::Known),
    t: &Tokens,
) -> Option<El> {
    let attn_client::TileContent { revision, message } = content;
    let failed = message.error.as_deref().is_some_and(|e| !e.is_empty());
    if failed || message.content.trim().is_empty() {
        return None;
    }
    let widest = if message.review.is_some() {
        crate::review::CARD_MAX_WIDTH
    } else {
        f32::INFINITY
    };
    let (padding, text_width) = card_fit(Some(width as u32), widest);
    let version = (
        *revision,
        width.to_bits(),
        pictures.revision,
        ids.clone(),
        *t,
    );
    let reading = memo(format!("{view}.reading"), version, || {
        let style = look(t).prose_measure(PROSE_MEASURE);
        let host = message.host.as_deref().filter(|h| !h.is_empty());
        reader(
            &message.content,
            links::Document {
                path: message.path.clone(),
                session: None,
                outpost: None,
                view: view.clone(),
                opens_seed: None,
                seed: None,
                part: None,
                ids: ids.clone(),
            },
            pictures.of(host),
            annotating::Kept::default(),
            None,
            match text_width {
                Some(width) => style.text_width(width),
                None => style,
            },
            t,
        )
    });
    let page = col().key(view).w_full().items_center();
    Some(Surface::gutter(page).child(card(padding, reading, t)))
}

/// Washes over a document's rendered text: each over its ranges of the source.
pub(crate) type Highlights = Vec<(Vec<std::ops::Range<usize>>, rune_kit::Highlight)>;

/// The marks a reader paints over its document, and, in a tile, where its selections go.
/// Reading a part of the document ([`links::Document::part`]), the reader paints marks by
/// where they sit in it.
pub(crate) struct Marking {
    /// The tile a selection in the reader opens the toolbar of, and the document its marks are
    /// on: none for a reader that only washes `highlights`, as a find bar does.
    by: Option<(String, String)>,
    highlights: Highlights,
}

impl Marking {
    /// The washes of `highlights` alone, on a reader nothing is marked in.
    pub(crate) fn washes(highlights: Highlights) -> Self {
        Marking {
            by: None,
            highlights,
        }
    }
}

/// The marks `highlights` paints on the document, as a reader of its bytes `part` alone
/// paints them.
fn within(
    highlights: &[(Vec<std::ops::Range<usize>>, rune_kit::Highlight)],
    part: &std::ops::Range<usize>,
) -> Highlights {
    highlights
        .iter()
        .filter_map(|(ranges, highlight)| {
            let inside: Vec<_> = ranges
                .iter()
                .filter(|r| r.start < part.end && part.start < r.end)
                .map(|r| r.start.max(part.start) - part.start..r.end.min(part.end) - part.start)
                .collect();
            (!inside.is_empty()).then(|| (inside, highlight.clone()))
        })
        .collect()
}

/// The reader of seed `seed`'s body in tile `tile`, with the marks `drafts` hold on it and the
/// washes of its find bar's matches, `found`, where people mark it as they mark a file: the
/// reader, keyed so its marks' toolbar and comment box find their passage, then those, placed
/// over it.
pub(crate) fn seed_reader(
    (drafts, readings): (&Drafts, &Readings),
    tile: &str,
    (seed, found): (&str, Highlights),
    version: impl std::hash::Hash,
    read: impl FnOnce(Option<Marking>) -> El,
    t: &Tokens,
) -> (El, Vec<El>) {
    let uri = drafts::Source::Seed(seed.to_owned()).uri();
    let mut highlights =
        annotating::highlights(drafts.get(&uri), readings.tiles.get(tile), tile, t);
    highlights.extend(found);
    let reading = memo(
        annotating::root_key(tile),
        (version, highlights.clone()),
        || {
            read(Some(Marking {
                by: Some((tile.to_owned(), uri)),
                highlights,
            }))
        },
    );
    let (root, seen) = (
        annotating::root_key(tile),
        format!("seed-tile.{tile}.viewport"),
    );
    let marking = annotating::in_document(readings, tile, (&root, &seen), 0.0, t);
    (reading, marking)
}

/// `source` drawn in `style` as a document at `document.path` reads: its links go where the reader sends them,
/// its headings are keyed by their slugs for `#fragment` links, and its local images show the
/// ready `pictures` by the paths they resolve to, opening larger on a press. Its `<details>`
/// blocks open as their tags say, or as `folds` says people left them, by their order in the
/// document, and a press on a summary opens or closes its block. With `marking`, it paints
/// marks, a press on one picks it, and a selection let go opens the toolbar; a reader of a part
/// of the document sits in a column of its own, which whoever lays out the parts may key.
pub(crate) fn reader(
    source: &str,
    document: Document,
    pictures: Rc<HashMap<String, Option<Image>>>,
    kept: annotating::Kept,
    marking: Option<Marking>,
    style: MarkdownStyle,
    t: &Tokens,
) -> El {
    let mut document = document;
    let folded = document.view.clone();
    let copied_in = document.view.clone();
    let nth = details_order(source);
    let part = document.part;
    let root = marking
        .as_ref()
        .and_then(|m| m.by.as_ref())
        .map(|(tile, _)| match part {
            None => annotating::root_key(tile),
            Some(start) => annotating::part_key(tile, start),
        });
    if let Some(root) = &root {
        document.view = format!("{}/{root}", document.view);
    }
    let bars = scroll_bars_id(&document.view, part);
    let markdown = read_as(source)
        .id(bars.clone())
        .details(
            {
                let nth = nth.clone();
                let folds = kept.folds;
                move |at, tag| folds.get(&nth(at)).copied().unwrap_or(tag)
            },
            move |state: &mut State, at, open, _: &mut Tx| {
                state.readings.fold(&folded, nth(at), open)
            },
        )
        .code_frame({
            let (t, view, copied) = (*t, copied_in, kept.copied);
            move |card, frame| with_copy(card, frame, &view, copied, &t)
        })
        .front_matter_frame({
            let t = *t;
            move |_, yaml| front_matter::card(yaml, &t)
        });
    let markdown = diagrams(markdown, bars, t);
    let markdown = linked(markdown, document, pictures, t);
    let markdown = markdown.style(style);
    let Some(Marking { by, highlights }) = marking else {
        return ids::pointed(markdown.build(attn_ui::kit(t)), ids::Cards::All);
    };
    let markdown = markdown.highlights(highlights);
    let (Some((tile, uri)), Some(root)) = (by, root) else {
        return ids::pointed(markdown.build(attn_ui::kit(t)), ids::Cards::All);
    };
    let pressed = tile.clone();
    let in_part = part.is_some();
    let part = part.map(|start| (start, source.to_owned()));
    let reading = markdown
        .on_highlight(move |state: &mut State, key: &str, _: &mut Tx| {
            annotating::mark_pressed(state, &pressed, key)
        })
        .build(attn_ui::kit(t))
        .key(root)
        .on_selection_change(move |state: &mut State, change, tx: &mut Tx| {
            let part = part
                .as_ref()
                .map(|(start, source)| (*start, source.as_str()));
            annotating::selection_changed(state, &tile, &uri, part, change, tx)
        });
    ids::pointed(
        if in_part {
            col().w_full().child(reading)
        } else {
            reading
        },
        ids::Cards::All,
    )
}

/// `markdown` read as a document at `document.path`: its links go where the reader sends them
/// and read as text where it does not follow them, and its local images show the ready
/// `pictures` by the paths they resolve to, opening larger on a press.
pub(crate) fn linked(
    markdown: Markdown,
    document: Document,
    pictures: Rc<HashMap<String, Option<Image>>>,
    t: &Tokens,
) -> Markdown {
    let help = document.clone();
    let plain = document.clone();
    let text_color = t.text_primary;
    let ids = document.ids.clone();
    pictured(markdown, &document, pictures, t)
        .text_links(move |run| ids::links(&ids, run))
        .on_link_tx(move |state: &mut State, href: &str, tx: &mut Tx| {
            links::follow(state, &document, href, tx)
        })
        .link_help(move |href| help.verdict(href).help)
        .link_style(move |href| {
            (!plain.verdict(href).follows).then(|| {
                (
                    Span::new(0, 0).color(text_color),
                    LinkPaint::new().color(text_color),
                )
            })
        })
}

/// `markdown`'s local images, read as a document at `document.path`, showing the ready
/// `pictures` by the paths they resolve to and opening larger on a press, and saying what an
/// image with no picture is waiting on.
pub(crate) fn pictured(
    markdown: Markdown,
    document: &Document,
    pictures: Rc<HashMap<String, Option<Image>>>,
    t: &Tokens,
) -> Markdown {
    let (shows, named) = (document.clone(), document.clone());
    markdown
        .missing_image_frame({
            let (pictures, t) = (pictures.clone(), *t);
            move |_, image| {
                let failed = named
                    .picture(image.src)
                    .map(|file| matches!(pictures.get(&file), Some(None)));
                missing(image, failed, &t)
            }
        })
        .images(move |src| pictures.get(&shows.picture(src)?).cloned().flatten())
        .image_frame(|shown: El, frame: &ImageFrame<'_>| {
            let (image, alt) = (frame.image.clone(), frame.alt.to_owned());
            shown.on_press_tx(move |state: &mut State, tx: &mut Tx| {
                images::show(state, image.clone(), &alt, tx)
            })
        })
}

/// `markdown`'s Mermaid diagrams as a document frames them: one too wide to read fitted
/// scrolls at its natural size and offers to focus it, one drawn in part says what it leaves
/// out, and one that cannot draw shows its source under a note. `bars` names the reading's
/// scroll boxes apart from every other reading's in the window.
pub(crate) fn diagrams(markdown: Markdown, bars: String, t: &Tokens) -> Markdown {
    let t = *t;
    markdown
        .diagram_frame(move |card, frame| leaves_out(framed(card, frame, &bars, &t), frame, &t))
        .diagram_source_frame(move |source, _| not_drawn(source, &t))
}

/// Which of `source`'s `<details>` blocks the one whose tag starts at a source byte is, counted
/// from 0, so a block keeps its open state when the text above it changes.
fn details_order(source: &str) -> Rc<dyn Fn(usize) -> usize> {
    let starts: Vec<usize> = source
        .to_ascii_lowercase()
        .match_indices("<details")
        .map(|(at, _)| at)
        .collect();
    Rc::new(move |at| starts.partition_point(|&start| start < at))
}

/// How the reader draws a document.
pub(crate) fn look(t: &Tokens) -> MarkdownStyle {
    attn_ui::system::document_style(t)
}

/// What an image with no picture to show says: for a document's local file, missing when its
/// read `failed` and loading until then; blocked for anything else.
fn missing(image: &MissingImage<'_>, failed: Option<bool>, t: &Tokens) -> El {
    let named = if image.alt.is_empty() {
        image.src.rsplit('/').next().unwrap_or(image.src)
    } else {
        image.alt
    };
    let words = match failed {
        Some(true) => format!("[missing image: {named}]"),
        Some(false) => format!("[loading image: {named}]"),
        None => {
            let named = [image.alt, image.src]
                .into_iter()
                .find(|s| !s.is_empty())
                .unwrap_or("unknown source");
            format!("[blocked image: {named}]")
        }
    };
    read_aside(text(words), Reading::Text)
        .color(t.text_tertiary)
        .wrap()
}

/// What names the scroll bars of a reading in `view`, of the document's `part` when it reads
/// one, apart from every other reading's in the window. Scroll bars are found by path, so
/// the name leaves out the path's `/`.
fn scroll_bars_id(view: &str, part: Option<usize>) -> String {
    let view = view.replace('/', ".");
    match part {
        Some(start) => format!("{view}.part{start}."),
        None => format!("{view}."),
    }
}

/// `source` as the reader reads it, which a mark's passage is found in.
fn read_as(source: &str) -> Markdown {
    Markdown::new(source.to_owned())
        .selectable(true)
        .smart_punctuation(true)
        .block_keys(anchors::heading_keys(source))
        .scroll_wide(true)
}

/// A code block's card with a button that copies its code, shown while the pointer is over the
/// card, and saying "Copied!" a while after a copy.
fn with_copy(card: El, frame: &CodeFrame<'_>, view: &str, copied: Option<usize>, t: &Tokens) -> El {
    if frame.open {
        return card;
    }
    let done = copied == Some(frame.index);
    let (glyph, label) = if done {
        (glyph::CHECK, "Copied!")
    } else {
        (glyph::COPY, "Copy code")
    };
    let (code, view, index) = (frame.source.to_owned(), view.to_owned(), frame.index);
    let button = GlyphButton::new(glyph, label)
        .help(label)
        .on_press_tx(move |state: &mut State, tx: &mut Tx| {
            tx.copy(code.clone());
            state.readings.copied(&view, index, tx);
        })
        .build(attn_ui::kit(t))
        .key("copy")
        .pin(None, Some(COPY_INSET), Some(COPY_INSET), None);
    let button = if done {
        button.color(t.success)
    } else {
        button.group_opacity(0.0, 1.0)
    };
    col().w_full().hover_group().child(card).child(button)
}

/// A diagram that cannot draw, shown as its source under a note that says so.
fn not_drawn(source: El, t: &Tokens) -> El {
    Stack::tight()
        .w_full()
        .child(attn_words("Diagram failed to render", Type::Small, t.error))
        .child(source)
}

/// The widest a diagram's card draws in a document, the kit's default.
const DIAGRAM_ROOM: f32 = 680.0;
/// A diagram whose card would draw it smaller than this share of its natural size shows at its
/// natural size instead, scrolling.
const LEAST_FIT: f32 = 0.8;
/// `source` at its natural size, laid out for the room a document gives it, so a diagram that
/// turns top-down to fit that room turns here too.
pub(crate) fn natural(source: &str, t: &Tokens) -> Option<rune_kit::mermaid::Drawing> {
    rune_kit::mermaid::drawing(source, &look(t).diagram_min_scale(1.0), DIAGRAM_ROOM)
}

/// The tallest a large diagram's scroll box grows.
const LARGE_HEIGHT: f32 = 410.0;

/// A diagram framed: a `diagram` bar over its card, and a diagram too
/// wide to read fitted shows at its natural size in a box that scrolls, its bar saying so and
/// offering to focus it, which opens it over the window to zoom and pan.
fn framed(card: El, frame: &rune_kit::DiagramFrame<'_>, bars: &str, t: &Tokens) -> El {
    let style = look(t);
    let large = (!frame.open)
        .then(|| rune_kit::mermaid::drawing(frame.source, &style, DIAGRAM_ROOM))
        .flatten()
        .filter(|fitted| fitted.scale < LEAST_FIT)
        .and_then(|_| natural(frame.source, t));
    let label = attn_words("diagram", Type::Mono, t.text_tertiary);
    let Some(drawing) = large else {
        return Stack::tight().w_full().child(label).child(card);
    };
    let (source, size) = (frame.source.to_owned(), drawing.size);
    let bar = Inline::top()
        .items_center()
        .child(label.grow(1.0))
        .child(
            Inline::joined()
                .child(dot_on_line(t.warning, Type::Small))
                .child(attn_words(
                    "Large diagram · 100%",
                    Type::Small,
                    t.text_tertiary,
                )),
        )
        .child(
            AttnButton::new("Focus diagram")
                .size(Size::Compact)
                .on_press_tx(move |state: &mut State, tx: &mut Tx| {
                    images::show_diagram(state, &source, size, tx)
                })
                .build(t)
                .key("focus")
                .into(),
        );
    let scrolled = Surface::figure(
        col()
            .key(format!("{bars}diagram-{}", frame.index))
            .w_full()
            .max_h(LARGE_HEIGHT)
            .scroll_xy()
            .child(drawing.el),
        drawing.surface,
        drawing.surface_border,
    )
    .scroll_bar_styled(attn_ui::document_scroll_bar(t).both_ways());
    Stack::tight().w_full().child(bar).child(scrolled)
}

/// A drawn diagram, with what it leaves out because it cannot read it said under it.
fn leaves_out(card: El, frame: &rune_kit::DiagramFrame<'_>, t: &Tokens) -> El {
    let skipped = if frame.open {
        Vec::new()
    } else {
        rune_kit::mermaid::skipped(frame.source)
    };
    if skipped.is_empty() {
        return card;
    }
    Stack::tight()
        .w_full()
        .child(card)
        .child(attn_words(
            "This diagram leaves out what it cannot read:",
            Type::Small,
            t.error,
        ))
        .children(
            skipped
                .into_iter()
                .map(|statement| attn_words(statement, Type::Mono, t.error)),
        )
}

/// What the tile body says in place of a document, where the document's first line would
/// start: an italic quiet note, or its error in a softened red.
fn message(t: &Tokens, message: &str, error: bool) -> El {
    let said = if error {
        set_in(text(message), Reading::Text).color(attn_ui::colors::mix_srgb(
            t.error,
            Color::WHITE,
            0.24,
        ))
    } else {
        read_aside(text(message), Reading::Text).color(t.text_tertiary)
    };
    Surface::gutter(col().w_full().h_full().child(said.wrap()))
}

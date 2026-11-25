use rune::{Color, El, Shadow, col, el, row, text};

use crate::colors::{MARK_READS, READS, mix_srgb, moved_until_it_reads, reads_furthest};
use crate::system::{
    DotShape, DotSize, IconSize, Radius, Space, Type, door, dot, glyph, width_of, words,
};
use crate::{Frame, Glyph, Tokens};

#[derive(Clone)]
pub enum IdMark {
    Glyph(Glyph),
    Hollow,
}

#[derive(Clone)]
pub struct IdLabel<'a> {
    pub keys: &'a str,
    pub typed: usize,
    pub mark: IdMark,
    pub id: &'a str,
    pub name: &'a str,
    pub state: &'a str,
    pub ink: Color,
}

#[derive(Clone, Copy, Debug, PartialEq)]
pub struct IdPlace {
    pub at: [f32; 4],
    pub room: f32,
    pub pane: (f32, f32),
}

const TALL: f32 = 20.0;
const CAP_TALL: f32 = 16.0;
const NAME_MIN: f32 = 64.0;
const NAME_MAX: f32 = 260.0;
const EDGE: f32 = 1.0;

impl IdPlace {
    fn over(&self, width: f32) -> (f32, f32) {
        let [x, y, w, h] = self.at;
        self.inside(width, x + (w - width).min(0.0) / 2.0, y + (h - TALL) / 2.0)
    }

    fn at_start(&self, width: f32) -> (f32, f32) {
        let [x, y, _, h] = self.at;
        self.inside(width, x, y + (h - TALL) / 2.0)
    }

    fn inside(&self, width: f32, x: f32, y: f32) -> (f32, f32) {
        let within = |at: f32, size: f32, room: f32| at.min(room - size).max(0.0);
        (within(x, width, self.pane.0), within(y, TALL, self.pane.1))
    }
}

fn ground(ink: Color, t: &Tokens) -> Color {
    mix_srgb(t.bg_panel, ink, 0.18)
}

fn readable(ink: Color, ground: Color, t: &Tokens) -> Color {
    moved_until_it_reads(ink, t.text_primary, ground, READS)
}

fn cap_width(letters: usize) -> f32 {
    (width_of("O".repeat(letters), Type::Control) + 2.0 * Space::S4.pt()).ceil()
}

fn cap(label: &IdLabel, radius: Radius, t: &Tokens) -> El {
    let fill = moved_until_it_reads(label.ink, t.text_primary, t.bg_panel, MARK_READS);
    let ink = reads_furthest(fill, [t.bg_app, t.text_primary]);
    let keys = label.keys.to_uppercase();
    let (typed, rest) = keys.split_at(label.typed.min(keys.len()));
    let face = |said: &str, ink: Color| door::font(text(said.to_owned()), Type::Control).color(ink);
    let cap = row()
        .center()
        .w(cap_width(keys.len()))
        .h(CAP_TALL)
        .fill(fill)
        .children((!typed.is_empty()).then(|| face(typed, ink.alpha(0.45))))
        .child(face(rest, ink));
    door::corner(cap, radius)
}

fn mark(label: &IdLabel, ink: Color) -> El {
    match label.mark.clone() {
        IdMark::Glyph(mark) => glyph(mark, IconSize::Small, ink),
        IdMark::Hollow => row()
            .size(IconSize::Small.pt(), IconSize::Small.pt())
            .center()
            .child(dot(ink, DotShape::Hollow, DotSize::Regular)),
    }
}

fn said(label: &IdLabel) -> String {
    format!(
        "{} {}, {}",
        label.keys.to_uppercase(),
        label.name,
        label.state
    )
}

fn chip(key: String, label: &IdLabel, (x, y): (f32, f32), width: f32, t: &Tokens) -> El {
    let ground = ground(label.ink, t);
    let chip = row()
        .key(key)
        .role("note")
        .label(said(label))
        .absolute(x, y)
        .w(width)
        .h(TALL)
        .items_center()
        .fill(ground)
        .border(EDGE, label.ink.alpha(0.7))
        .box_shadow(Shadow {
            blur: 12.0,
            color: label.ink.alpha(0.35),
            ..Shadow::default()
        });
    door::gap(door::inset_start(chip, Space::S2.pt()), Space::S4)
}

pub fn lantern(key: impl Into<String>, label: &IdLabel, place: IdPlace, t: &Tokens) -> El {
    let ink = readable(label.ink, ground(label.ink, t), t);
    let width = Space::S2.pt()
        + cap_width(label.keys.len())
        + Space::S4.pt()
        + IconSize::Small.pt()
        + Space::S4.pt()
        + width_of(label.id.to_owned(), Type::Mono)
        + Space::S6.pt();
    let chip = chip(key.into(), label, place.over(width), width, t)
        .child(cap(label, Radius::Tag, t))
        .child(mark(label, ink))
        .child(words(label.id.to_owned(), Type::Mono, t.text_primary).single_line());
    door::corner(chip, Radius::Control)
}

pub fn name_in_place(key: impl Into<String>, label: &IdLabel, place: IdPlace, t: &Tokens) -> El {
    let ink = readable(label.ink, ground(label.ink, t), t);
    let head = Space::S2.pt()
        + cap_width(label.keys.len())
        + Space::S4.pt()
        + IconSize::Small.pt()
        + Space::S4.pt();
    let whole = head + width_of(label.name.to_owned(), Type::Name).ceil() + Space::S8.pt();
    let width = whole.min(NAME_MAX).min(place.room.max(NAME_MIN));
    let chip = chip(key.into(), label, place.at_start(width), width, t)
        .child(cap(label, Radius::Round, t))
        .child(mark(label, ink))
        .child(
            words(label.name.to_owned(), Type::Name, t.text_primary)
                .single_line()
                .truncate()
                .w(width - head - Space::S8.pt())
                .shrink(),
        );
    door::corner(chip, Radius::Round)
}

const LANTERN_DIM: f32 = 0.68;
const NAMES_DIM: f32 = 0.4;

pub fn id_scrim(ground: Color, named: bool) -> El {
    let dim = if named { NAMES_DIM } else { LANTERN_DIM };
    el().absolute(0.0, 0.0)
        .w_full()
        .h_full()
        .fill(ground.alpha(dim))
        .a11y_hidden()
}

const LIST_ROW: f32 = 22.0;
const LIST_W: f32 = 340.0;
const LIST_INSET: f32 = 12.0;

pub fn id_list(key: impl Into<String>, labels: &[IdLabel], pane: (f32, f32), t: &Tokens) -> El {
    let chrome = 2.0 * (Space::S8.pt() + EDGE);
    let room = |size: f32| (size - 2.0 * LIST_INSET - chrome).max(0.0);
    let width = LIST_W.min(room(pane.0));
    let rows = ((room(pane.1) / LIST_ROW) as usize).max(1);
    let columns = ((room(pane.0) + Space::S16.pt()) / (width + Space::S16.pt())) as usize;
    let fits = rows * columns.max(1);
    let shown = if labels.len() > fits {
        fits - 1
    } else {
        labels.len()
    };
    let mut lines: Vec<El> = labels[..shown].iter().map(|l| line(l, t)).collect();
    if shown < labels.len() {
        lines.push(quiet(format!("and {} more", labels.len() - shown), t));
    }
    if labels.is_empty() {
        lines.push(quiet("No ids on screen".to_owned(), t));
    }
    let width = if labels.is_empty() { None } else { Some(width) };
    let mut columns: Vec<El> = Vec::new();
    let mut lines = lines.into_iter().peekable();
    while lines.peek().is_some() {
        let column = col().children(lines.by_ref().take(rows));
        columns.push(match width {
            Some(width) => column.w(width),
            None => column,
        });
    }
    let list = door::gap(row(), Space::S16)
        .key(key.into())
        .role("group")
        .label("Labels")
        .children(columns);
    let list = Frame::menu(t).draw(door::inset(list, Space::S8, Space::S8));
    let corner = col()
        .absolute(0.0, 0.0)
        .w_full()
        .h_full()
        .justify_end()
        .items_end();
    door::inset(corner, Space::S12, Space::S12).child(list)
}

fn line(label: &IdLabel, t: &Tokens) -> El {
    let ink = readable(label.ink, t.bg_panel, t);
    let line = row()
        .role("note")
        .label(said(label))
        .h(LIST_ROW)
        .w_full()
        .items_center()
        .child(cap(label, Radius::Tag, t))
        .child(mark(label, ink))
        .child(
            words(label.name.to_owned(), Type::Body, t.text_primary)
                .single_line()
                .truncate()
                .grow(1.0)
                .min_w(0.0),
        )
        .child(words(label.state.to_owned(), Type::Small, ink).single_line());
    door::gap(line, Space::S6)
}

fn quiet(said: String, t: &Tokens) -> El {
    row()
        .h(LIST_ROW)
        .items_center()
        .child(words(said, Type::Small, t.text_secondary).single_line())
}

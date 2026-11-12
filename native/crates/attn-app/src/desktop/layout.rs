//! A desktop's tiles as the daemon arranges them in `tree_json`: a row of columns, each a
//! split tree of leaves, and which run of that row fits on screen.

use serde_json::Value;

/// The narrowest a tile gets, in points, whatever the narrowest width in columns says.
pub const TILE_MIN_WIDTH: f32 = 480.0;
/// The shortest a stacked tile gets, in points.
pub const TILE_MIN_HEIGHT: f32 = 320.0;
/// The narrowest tile widths, in terminal columns, that the shelf steps through.
pub const NARROWEST_STEPS: [u32; 7] = [57, 66, 76, 86, 100, 120, 150];
pub const DEFAULT_NARROWEST_COLS: u32 = 66;
const ZOOM_PATH_RATIO: f32 = 0.76;
const FIT_TOLERANCE_PX: f32 = 0.5;

#[derive(Clone, Copy, Debug, PartialEq, Eq, Hash)]
pub enum Direction {
    /// Side by side: the children are columns.
    Vertical,
    /// One above the other.
    Horizontal,
}

#[derive(Clone, Copy, Debug, PartialEq)]
pub struct Frame {
    pub x: f64,
    pub y: f64,
    pub width: f64,
    pub height: f64,
}

#[derive(Clone, Debug, PartialEq)]
pub enum LeafKind {
    /// A terminal pane; its title and terminal are the desktop's `panes` entry of this id.
    Pane,
    Tile {
        kind: String,
        params: Option<String>,
        session: Option<String>,
    },
}

#[derive(Clone, Debug, PartialEq)]
pub struct Leaf {
    pub id: String,
    pub kind: LeafKind,
    pub wanted_cols: Option<u32>,
    pub wanted_area_cols: Option<u32>,
    /// Where the leaf's own window sits while it is popped out.
    pub window: Option<Frame>,
}

#[derive(Clone, Debug, PartialEq)]
pub struct Split {
    pub id: String,
    pub direction: Direction,
    /// The first child's share, in (0, 1).
    pub ratio: f32,
    pub children: Box<[Node; 2]>,
}

#[derive(Clone, Debug, PartialEq)]
pub enum Node {
    Leaf(Leaf),
    Split(Split),
}

impl Node {
    /// The tree in a desktop's `tree_json`; None for an empty desktop, or a tree this app
    /// cannot read, as the Tauri app reads it.
    pub fn parse(tree_json: &str) -> Option<Node> {
        if tree_json.trim().is_empty() {
            return None;
        }
        serde_json::from_str::<Value>(tree_json)
            .ok()
            .and_then(|v| parse_node(&v))
    }

    pub fn leaves(&self) -> Vec<&Leaf> {
        let mut out = Vec::new();
        self.collect(&mut out);
        out
    }

    fn collect<'a>(&'a self, out: &mut Vec<&'a Leaf>) {
        match self {
            Node::Leaf(leaf) => out.push(leaf),
            Node::Split(split) => split.children.iter().for_each(|c| c.collect(out)),
        }
    }

    pub fn has_leaf(&self, id: &str) -> bool {
        self.find_leaf(id).is_some()
    }

    pub fn find_leaf(&self, id: &str) -> Option<&Node> {
        match self {
            Node::Leaf(leaf) => (leaf.id == id).then_some(self),
            Node::Split(split) => split.children.iter().find_map(|c| c.find_leaf(id)),
        }
    }

    /// The tree as the main window shows it: popped-out leaves leave their columns, and a
    /// column keeps its first leaf's dragged width.
    pub fn main(&self) -> Option<Node> {
        match self {
            Node::Split(split) if split.direction == Direction::Vertical => {
                let [a, b] = &*split.children;
                match (a.main(), b.main()) {
                    (Some(a), Some(b)) => Some(Node::Split(Split {
                        children: Box::new([a, b]),
                        ..split.clone()
                    })),
                    (a, b) => a.or(b),
                }
            }
            column => {
                let kept = column.without_popped()?;
                let first = column.leaves()[0];
                Some(kept.with_first_width(first.wanted_cols, first.wanted_area_cols))
            }
        }
    }

    pub fn popped(&self) -> Vec<&Leaf> {
        self.leaves()
            .into_iter()
            .filter(|l| l.window.is_some())
            .collect()
    }

    fn without_popped(&self) -> Option<Node> {
        match self {
            Node::Leaf(leaf) => leaf.window.is_none().then(|| self.clone()),
            Node::Split(split) => {
                let [a, b] = &*split.children;
                match (a.without_popped(), b.without_popped()) {
                    (Some(a), Some(b)) => Some(Node::Split(Split {
                        children: Box::new([a, b]),
                        ..split.clone()
                    })),
                    (a, b) => a.or(b),
                }
            }
        }
    }

    fn with_first_width(mut self, cols: Option<u32>, area: Option<u32>) -> Node {
        let mut node = &mut self;
        loop {
            match node {
                Node::Leaf(leaf) => {
                    leaf.wanted_cols = cols;
                    leaf.wanted_area_cols = area;
                    return self;
                }
                Node::Split(split) => node = &mut split.children[0],
            }
        }
    }

    /// Each leaf's rectangle as fractions of the tree's.
    pub fn bounds(&self) -> Vec<(&str, Rect)> {
        let mut out = Vec::new();
        self.collect_bounds(Rect::UNIT, &mut out);
        out
    }

    fn collect_bounds<'a>(&'a self, r: Rect, out: &mut Vec<(&'a str, Rect)>) {
        match self {
            Node::Leaf(leaf) => out.push((&leaf.id, r)),
            Node::Split(split) => {
                let (a, b) = r.split(split.direction, split.ratio);
                split.children[0].collect_bounds(a, out);
                split.children[1].collect_bounds(b, out);
            }
        }
    }

    /// The tree with every split on the way to `leaf` giving it most of the room.
    pub fn zoomed_toward(&self, leaf: &str) -> Node {
        match self {
            Node::Leaf(_) => self.clone(),
            Node::Split(split) => {
                let first = split.children[0].has_leaf(leaf);
                let second = split.children[1].has_leaf(leaf);
                let ratio = match (first, second) {
                    (true, _) => ZOOM_PATH_RATIO,
                    (_, true) => 1.0 - ZOOM_PATH_RATIO,
                    _ => split.ratio,
                };
                Node::Split(Split {
                    ratio,
                    children: Box::new(
                        split
                            .children
                            .clone()
                            .map(|child| child.zoomed_toward(leaf)),
                    ),
                    ..split.clone()
                })
            }
        }
    }

    /// The leaf nearest `from` in `direction`: the closest edge first, then the closest
    /// center across, then the id.
    pub fn leaf_toward(&self, from: &str, direction: Toward) -> Option<String> {
        let rects = self.bounds();
        let current = rects.iter().find(|(id, _)| *id == from)?.1;
        rects
            .iter()
            .filter(|(id, _)| *id != from)
            .filter_map(|(id, c)| {
                let (primary, overlap, secondary) = match direction {
                    Toward::Left => (
                        current.left - c.right(),
                        overlap(current.top, current.bottom(), c.top, c.bottom()),
                        (current.center_y() - c.center_y()).abs(),
                    ),
                    Toward::Right => (
                        c.left - current.right(),
                        overlap(current.top, current.bottom(), c.top, c.bottom()),
                        (current.center_y() - c.center_y()).abs(),
                    ),
                    Toward::Up => (
                        current.top - c.bottom(),
                        overlap(current.left, current.right(), c.left, c.right()),
                        (current.center_x() - c.center_x()).abs(),
                    ),
                    Toward::Down => (
                        c.top - current.bottom(),
                        overlap(current.left, current.right(), c.left, c.right()),
                        (current.center_x() - c.center_x()).abs(),
                    ),
                };
                (primary >= -1e-6 && overlap > 0.0).then_some((primary, secondary, *id))
            })
            .min_by(|a, b| {
                a.0.total_cmp(&b.0)
                    .then(a.1.total_cmp(&b.1))
                    .then(a.2.cmp(b.2))
            })
            .map(|(_, _, id)| id.to_owned())
    }
}

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Toward {
    Left,
    Right,
    Up,
    Down,
}

fn overlap(a0: f32, a1: f32, b0: f32, b1: f32) -> f32 {
    (a1.min(b1) - a0.max(b0)).max(0.0)
}

#[derive(Clone, Copy, Debug, PartialEq)]
pub struct Rect {
    pub left: f32,
    pub top: f32,
    pub width: f32,
    pub height: f32,
}

impl Rect {
    const UNIT: Rect = Rect {
        left: 0.0,
        top: 0.0,
        width: 1.0,
        height: 1.0,
    };

    fn right(&self) -> f32 {
        self.left + self.width
    }
    fn bottom(&self) -> f32 {
        self.top + self.height
    }
    fn center_x(&self) -> f32 {
        self.left + self.width / 2.0
    }
    fn center_y(&self) -> f32 {
        self.top + self.height / 2.0
    }

    fn split(self, direction: Direction, ratio: f32) -> (Rect, Rect) {
        match direction {
            Direction::Vertical => {
                let w = self.width * ratio;
                (
                    Rect { width: w, ..self },
                    Rect {
                        left: self.left + w,
                        width: self.width - w,
                        ..self
                    },
                )
            }
            Direction::Horizontal => {
                let h = self.height * ratio;
                (
                    Rect { height: h, ..self },
                    Rect {
                        top: self.top + h,
                        height: self.height - h,
                        ..self
                    },
                )
            }
        }
    }
}

fn positive_u32(v: Option<&Value>) -> Option<u32> {
    v.and_then(Value::as_f64)
        .filter(|n| *n > 0.0)
        .map(|n| n as u32)
}

fn parse_leaf(v: &Value, id: &str, kind: LeafKind) -> Leaf {
    let wanted_cols = positive_u32(v.get("wanted_cols"));
    let window = v.get("window").and_then(|w| {
        let n = |k| w.get(k).and_then(Value::as_f64);
        let frame = Frame {
            x: n("x")?,
            y: n("y")?,
            width: n("width")?,
            height: n("height")?,
        };
        (frame.width > 0.0 && frame.height > 0.0).then_some(frame)
    });
    Leaf {
        id: id.to_owned(),
        kind,
        wanted_cols,
        wanted_area_cols: wanted_cols.and(positive_u32(v.get("wanted_area_cols"))),
        window,
    }
}

fn parse_node(v: &Value) -> Option<Node> {
    let s = |k| v.get(k).and_then(Value::as_str);
    match s("type")? {
        "pane" => Some(Node::Leaf(parse_leaf(v, s("pane_id")?, LeafKind::Pane))),
        "tile" => {
            let id = s("tile_id").filter(|id| !id.is_empty())?;
            let kind = s("tile_kind").filter(|k| !k.is_empty())?.to_owned();
            let kind = LeafKind::Tile {
                kind,
                params: s("tile_params").map(str::to_owned),
                session: s("tile_session_id")
                    .filter(|id| !id.is_empty())
                    .map(str::to_owned),
            };
            Some(Node::Leaf(parse_leaf(v, id, kind)))
        }
        "split" => {
            let direction = match s("direction")? {
                "vertical" => Direction::Vertical,
                "horizontal" => Direction::Horizontal,
                _ => return None,
            };
            let children = v.get("children")?.as_array()?;
            let [a, b, ..] = children.as_slice() else {
                return None;
            };
            let ratio = v
                .get("ratio")
                .and_then(Value::as_f64)
                .filter(|r| *r > 0.0 && *r < 1.0)
                .unwrap_or(0.5) as f32;
            Some(Node::Split(Split {
                id: s("split_id")?.to_owned(),
                direction,
                ratio,
                children: Box::new([parse_node(a)?, parse_node(b)?]),
            }))
        }
        _ => None,
    }
}

/// One column of the row: the leaves stacked in it, top to bottom, and the width a divider
/// drag asked for, which its first leaf carries.
#[derive(Clone, Debug, PartialEq)]
pub struct Column {
    pub leaf_ids: Vec<String>,
    pub wanted_cols: Option<u32>,
    pub wanted_area_cols: Option<u32>,
    pub node: Node,
}

impl Column {
    pub fn first(&self) -> &str {
        &self.leaf_ids[0]
    }

    pub fn holds(&self, leaf: &str) -> bool {
        self.leaf_ids.iter().any(|id| id == leaf)
    }

    /// The leaf of this column used last, by the window's focus order, newest first.
    pub fn last_used<'a>(&'a self, focus_order: &[String]) -> &'a str {
        focus_order
            .iter()
            .find_map(|id| self.leaf_ids.iter().find(|l| *l == id))
            .unwrap_or(&self.leaf_ids[0])
    }
}

/// The row: top-level side-by-side splits are columns, anything else one column.
pub fn read_row(tree: &Node) -> Vec<Column> {
    match tree {
        Node::Split(split) if split.direction == Direction::Vertical => {
            let mut row = read_row(&split.children[0]);
            row.extend(read_row(&split.children[1]));
            row
        }
        column => {
            let leaves = column.leaves();
            vec![Column {
                leaf_ids: leaves.iter().map(|l| l.id.clone()).collect(),
                wanted_cols: leaves[0].wanted_cols,
                wanted_area_cols: leaves[0].wanted_area_cols,
                node: column.clone(),
            }]
        }
    }
}

pub fn column_index(row: &[Column], leaf: &str) -> Option<usize> {
    row.iter().position(|c| c.holds(leaf))
}

/// The first column on screen, by its first leaf, and where it was in case that leaf goes.
#[derive(Clone, Debug, PartialEq, Eq, Hash)]
pub struct FirstColumn {
    pub leaf: String,
    pub index: usize,
}

/// How the active tile shows.
#[derive(Clone, Debug, PartialEq, Eq, Hash, Default)]
pub enum View {
    #[default]
    Normal,
    /// The active tile alone on the desktop.
    Focused(String),
    /// Every split on the way to the active tile gives it most of the room.
    Zoomed,
}

#[derive(Clone, Copy, Debug, PartialEq)]
pub struct Measures {
    pub area_px: f32,
    pub narrowest_px: f32,
    pub px_per_col: f32,
}

impl Measures {
    pub fn new(area_px: f32, narrowest_cols: u32, px_per_col: f32) -> Measures {
        Measures {
            area_px,
            narrowest_px: narrowest_width_px(narrowest_cols, px_per_col),
            px_per_col,
        }
    }

    fn fits(&self, px: f32) -> bool {
        px <= self.area_px + FIT_TOLERANCE_PX
    }

    fn area_cols(&self) -> u32 {
        (self.area_px / self.px_per_col).floor() as u32
    }

    fn wanted_px(&self, column: &Column) -> Option<f32> {
        let px = column.wanted_cols.unwrap_or(0) as f32 * self.px_per_col;
        let area_holds = self.area_cols() >= column.wanted_area_cols.unwrap_or(0);
        (px > 0.0 && area_holds && self.fits(px)).then_some(px)
    }

    fn need_px(&self, column: &Column) -> f32 {
        self.narrowest_px.max(self.wanted_px(column).unwrap_or(0.0))
    }
}

pub fn narrowest_width_px(cols: u32, px_per_col: f32) -> f32 {
    TILE_MIN_WIDTH.max(cols as f32 * px_per_col)
}

#[derive(Clone, Copy)]
pub struct RowInput<'a> {
    pub tree: &'a Node,
    pub active: &'a str,
    pub first_column: Option<&'a FirstColumn>,
    /// Leaves sent to the shelf while they would fit, kept off screen.
    pub held: &'a [String],
    /// Columns, by first leaf, laid out as if a divider drag had never widened them.
    pub cleared: &'a [String],
    pub measures: Measures,
}

impl RowInput<'_> {
    /// The row as the screen places it: held leaves left out and cleared widths forgotten.
    fn shown(&self) -> Vec<Column> {
        shown_row(
            without_widths(read_row(self.tree), self.cleared),
            self.held,
            self.active,
        )
    }
}

/// `row` with the dragged widths of the columns whose first leaf is in `cleared` forgotten.
pub fn without_widths(mut row: Vec<Column>, cleared: &[String]) -> Vec<Column> {
    for column in &mut row {
        if cleared.iter().any(|id| id == column.first()) {
            column.wanted_cols = None;
            column.wanted_area_cols = None;
        }
    }
    row
}

pub fn columns_fit_together(a: &Column, b: &Column, m: &Measures) -> bool {
    m.fits(m.need_px(a) + m.need_px(b))
}

/// What the screen shows of the row: the run of columns that fits around the active tile,
/// their widths in points, and the columns left on the shelf on either side.
#[derive(Clone, Debug, PartialEq)]
pub struct RowPlan {
    pub screen: Vec<Column>,
    pub before: Vec<Column>,
    pub after: Vec<Column>,
    pub first_column: FirstColumn,
    pub widths: Vec<f32>,
}

fn shown_row(row: Vec<Column>, held: &[String], active: &str) -> Vec<Column> {
    if held.is_empty() {
        return row;
    }
    row.into_iter()
        .filter(|c| c.holds(active) || !c.leaf_ids.iter().any(|id| held.contains(id)))
        .collect()
}

fn fill_from(row: &[Column], start: usize, m: &Measures) -> (usize, f32) {
    let mut end = start + 1;
    let mut sum = m.need_px(&row[start]);
    while end < row.len() && m.fits(sum + m.need_px(&row[end])) {
        sum += m.need_px(&row[end]);
        end += 1;
    }
    (end, sum)
}

fn place_screen(
    row: &[Column],
    active: &str,
    first: Option<&FirstColumn>,
    m: &Measures,
) -> (usize, usize, f32) {
    let active_at = column_index(row, active);
    let from = match first {
        None => 0,
        Some(f) => column_index(row, &f.leaf).unwrap_or(f.index),
    };
    let mut start = from.min(active_at.unwrap_or(row.len() - 1));
    let (mut end, mut sum) = fill_from(row, start, m);
    while active_at.is_some_and(|a| end <= a) {
        start += 1;
        (end, sum) = fill_from(row, start, m);
    }
    while start > 0 && m.fits(sum + m.need_px(&row[start - 1])) {
        start -= 1;
        (end, sum) = fill_from(row, start, m);
    }
    (start, end, sum)
}

fn widths(screen: &[Column], m: &Measures) -> Vec<f32> {
    let wanted = |c: &Column| m.wanted_px(c).map(|_| m.need_px(c));
    let wanted_sum: f32 = screen.iter().filter_map(wanted).sum();
    let shared = screen.iter().filter(|c| wanted(c).is_none()).count();
    if shared == 0 {
        return screen
            .iter()
            .map(|c| wanted(c).unwrap_or(0.0) * m.area_px / wanted_sum)
            .collect();
    }
    let each = (m.area_px - wanted_sum) / shared as f32;
    screen.iter().map(|c| wanted(c).unwrap_or(each)).collect()
}

/// Which run of an empty-free tree's row shows. The tree has at least one leaf.
pub fn resolve_row(input: &RowInput) -> RowPlan {
    let row = without_widths(read_row(input.tree), input.cleared);
    let shown = shown_row(row.clone(), input.held, input.active);
    let (start, end, _) = place_screen(&shown, input.active, input.first_column, &input.measures);
    let screen = shown[start..end].to_vec();
    let first_at = row
        .iter()
        .position(|c| c.first() == screen[0].first())
        .unwrap_or(0);
    let on_screen = |c: &Column| screen.iter().any(|s| s.first() == c.first());
    RowPlan {
        widths: widths(&screen, &input.measures),
        before: row[..first_at].to_vec(),
        after: row[first_at..]
            .iter()
            .filter(|c| !on_screen(c))
            .cloned()
            .collect(),
        first_column: FirstColumn {
            leaf: screen[0].first().to_owned(),
            index: start,
        },
        screen,
    }
}

impl RowPlan {
    /// The split tree the screen draws: its columns side by side at their widths, as the
    /// divider between each column and the next, `row:<first leaf>`, splits them.
    pub fn screen_tree(&self) -> Node {
        let n = self.screen.len();
        let mut tree = self.screen[n - 1].node.clone();
        let mut span = self.widths[n - 1];
        for i in (0..n - 1).rev() {
            span += self.widths[i];
            let ratio = if span > 0.0 {
                self.widths[i] / span
            } else {
                1.0 / (n - i) as f32
            };
            tree = Node::Split(Split {
                id: format!("row:{}", self.screen[i].first()),
                direction: Direction::Vertical,
                ratio,
                children: Box::new([self.screen[i].node.clone(), tree]),
            });
        }
        tree
    }

    /// What the screen draws in `view`, with `active` the active tile.
    pub fn rendered(&self, tree: &Node, view: &View, active: &str) -> Node {
        match view {
            View::Focused(leaf) => tree.find_leaf(leaf).unwrap_or(tree).clone(),
            View::Zoomed => self.screen_tree().zoomed_toward(active),
            View::Normal => self.screen_tree(),
        }
    }
}

/// Which side of an anchor a moved leaf lands on.
#[derive(Clone, Copy, Debug, PartialEq, Eq, Hash)]
pub enum Edge {
    Left,
    Right,
}

impl Edge {
    pub fn as_str(self) -> &'static str {
        match self {
            Edge::Left => "left",
            Edge::Right => "right",
        }
    }
}

/// What sending the active tile to the shelf does.
#[derive(Clone, Debug, PartialEq)]
pub enum ShelfSend {
    /// Nothing else would show in its place.
    Alone,
    /// The tile leaves its stack for the row's end.
    Unstack {
        leaf: String,
        anchor: String,
        next: String,
    },
    /// The row is short enough that the tile would fit: the window holds it off screen.
    Hold { leaf: String, next: String },
    /// The tile's column moves to the far end of the row, past the screen.
    Move {
        leaf: String,
        anchor: String,
        edge: Edge,
        next: String,
    },
}

/// What sending `input.active` to the shelf does, with `screen` the columns on screen.
pub fn plan_send_to_shelf(
    input: &RowInput,
    screen: &[Column],
    focus_order: &[String],
) -> ShelfSend {
    let leaf = input.active.to_owned();
    let row = read_row(input.tree);
    let Some(at) = column_index(&row, &leaf) else {
        return ShelfSend::Alone;
    };
    let column = &row[at];
    if column.leaf_ids.len() > 1 {
        let rest: Vec<&String> = column.leaf_ids.iter().filter(|id| **id != leaf).collect();
        let last: Vec<&String> = row[row.len() - 1]
            .leaf_ids
            .iter()
            .filter(|id| **id != leaf)
            .collect();
        let index = column
            .leaf_ids
            .iter()
            .position(|id| *id == leaf)
            .unwrap_or(0);
        return ShelfSend::Unstack {
            anchor: last[last.len() - 1].clone(),
            next: rest[index.min(rest.len() - 1)].clone(),
            leaf,
        };
    }
    let shown = input.shown();
    let Some(shown_at) = column_index(&shown, &leaf) else {
        return ShelfSend::Alone;
    };
    let slot = column_index(screen, &leaf).unwrap_or(0);
    let in_slot = screen
        .get(slot + 1)
        .or_else(|| slot.checked_sub(1).and_then(|i| screen.get(i)))
        .or_else(|| shown.get(shown_at + 1))
        .or_else(|| shown_at.checked_sub(1).and_then(|i| shown.get(i)));
    let Some(in_slot) = in_slot else {
        return ShelfSend::Alone;
    };
    let rest: Vec<Column> = shown
        .iter()
        .filter(|c| c.first() != leaf)
        .cloned()
        .collect();
    let (start, end, sum) =
        place_screen(&rest, in_slot.first(), input.first_column, &input.measures);
    let next_column = &rest[(start + slot).min(end - 1)];
    let next = next_column.last_used(focus_order).to_owned();
    if input.measures.fits(sum + input.measures.need_px(column)) {
        return ShelfSend::Hold { leaf, next };
    }
    let others: Vec<&Column> = row.iter().filter(|c| c.first() != leaf).collect();
    let rest_right = screen[screen.len() - 1].first() != shown[shown.len() - 1].first();
    let (anchor, edge) = if rest_right {
        (others[others.len() - 1].first(), Edge::Right)
    } else {
        (others[0].first(), Edge::Left)
    };
    ShelfSend::Move {
        anchor: anchor.to_owned(),
        edge,
        next,
        leaf,
    }
}

/// The first column nearest `current_start` from which the screen holds both `leaf` and the
/// active tile, or None when no screen holds both.
pub fn reveal_first_column(
    input: &RowInput,
    leaf: &str,
    current_start: usize,
) -> Option<FirstColumn> {
    let shown = input.shown();
    let leaf_at = column_index(&shown, leaf)?;
    let active_at = column_index(&shown, input.active)?;
    let mut best: Option<usize> = None;
    for start in 0..=leaf_at.min(active_at) {
        let (end, _) = fill_from(&shown, start, &input.measures);
        let holds_both = leaf_at < end && active_at < end;
        if holds_both
            && best.is_none_or(|b| start.abs_diff(current_start) < b.abs_diff(current_start))
        {
            best = Some(start);
        }
    }
    best.map(|index| FirstColumn {
        leaf: shown[index].first().to_owned(),
        index,
    })
}

/// A leaf brought beside an anchor.
#[derive(Clone, Debug, PartialEq, Eq, Hash)]
pub struct ShelfDrop {
    pub leaf: String,
    pub anchor: String,
    pub edge: Edge,
    /// The leaf brought its whole column rather than coming alone.
    pub whole_column: bool,
}

/// Where the screen starts so a dropped leaf shows beside its anchor, and the dragged widths
/// it has to forget for both to fit, the fewest first.
pub fn reveal_drop(
    input: &RowInput,
    drop: &ShelfDrop,
    current_start: usize,
) -> Option<(FirstColumn, Vec<String>)> {
    let row = without_widths(read_row(input.tree), input.cleared);
    let widened: Vec<String> = [&drop.anchor, &drop.leaf]
        .into_iter()
        .filter_map(|leaf| row.get(column_index(&row, leaf)?))
        .filter(|c| input.measures.wanted_px(c).is_some())
        .map(|c| c.first().to_owned())
        .collect();
    let mut attempts: Vec<Vec<String>> = vec![vec![]];
    attempts.extend(widened.iter().map(|w| vec![w.clone()]));
    if widened.len() > 1 {
        attempts.push(widened.clone());
    }
    attempts.into_iter().find_map(|clear| {
        let cleared: Vec<String> = input
            .cleared
            .iter()
            .cloned()
            .chain(clear.iter().cloned())
            .collect();
        let first = reveal_first_column(
            &RowInput {
                cleared: &cleared,
                ..*input
            },
            &drop.anchor,
            current_start,
        )?;
        Some((first, clear))
    })
}

/// What one press of fewer or more tiles does to the narrowest tile width.
#[derive(Clone, Debug, PartialEq)]
pub enum NarrowestStep {
    Step(u32),
    /// Dragged widths keep more tiles off screen: forget them, then step.
    ClearThenStep {
        cols: u32,
        clear: Vec<String>,
        shown: usize,
    },
    Limit(String),
}

fn tiles_shown_at(input: &RowInput, cols: u32) -> usize {
    let measures = Measures::new(input.measures.area_px, cols, input.measures.px_per_col);
    resolve_row(&RowInput { measures, ..*input }).screen.len()
}

/// The tile counts the steps reach from `current`, fewest first.
pub fn reachable_tile_counts(input: &RowInput, current: u32) -> Vec<usize> {
    let mut counts: Vec<usize> = std::iter::once(current)
        .chain(NARROWEST_STEPS)
        .map(|cols| tiles_shown_at(input, cols))
        .collect();
    if let NarrowestStep::ClearThenStep { shown, .. } =
        step_narrowest_cols(input, current, false, 0.0)
    {
        counts.push(shown);
    }
    counts.sort_unstable();
    counts.dedup();
    counts
}

/// The next narrowest width that shows fewer tiles, or more, or why there is none.
/// `window_px` is the window's width, to say when closing the sidebar would make room.
pub fn step_narrowest_cols(
    input: &RowInput,
    current: u32,
    fewer: bool,
    window_px: f32,
) -> NarrowestStep {
    let shown = tiles_shown_at(input, current);
    let total = input.shown().len();
    let candidates: Vec<u32> = if fewer {
        NARROWEST_STEPS
            .into_iter()
            .filter(|c| *c > current)
            .collect()
    } else {
        NARROWEST_STEPS
            .into_iter()
            .rev()
            .filter(|c| *c < current)
            .collect()
    };
    let found = candidates.iter().copied().find(|step| {
        let at = tiles_shown_at(input, *step);
        if fewer { at < shown } else { at > shown }
    });
    if let Some(cols) = found {
        return NarrowestStep::Step(cols);
    }
    let m = &input.measures;
    if !fewer {
        let row = without_widths(read_row(input.tree), input.cleared);
        let all: Vec<String> = row.iter().map(|c| c.first().to_owned()).collect();
        let screen_without = |cols: u32| {
            let measures = Measures::new(m.area_px, cols, m.px_per_col);
            resolve_row(&RowInput {
                measures,
                cleared: &all,
                ..*input
            })
            .screen
        };
        let unblocked = std::iter::once(current)
            .chain(candidates.iter().copied())
            .find(|step| screen_without(*step).len() > shown);
        if let Some(cols) = unblocked {
            let screen = screen_without(cols);
            let clear = row
                .iter()
                .filter(|c| {
                    m.wanted_px(c).is_some() && screen.iter().any(|s| s.first() == c.first())
                })
                .map(|c| c.first().to_owned())
                .collect();
            return NarrowestStep::ClearThenStep {
                cols,
                clear,
                shown: screen.len(),
            };
        }
    }
    if fewer {
        let widest = NARROWEST_STEPS[NARROWEST_STEPS.len() - 1].max(current);
        return NarrowestStep::Limit(if shown <= 1 {
            "Already showing one tile.".into()
        } else {
            format!(
                "{shown} tiles still fit at the widest width ({widest} cols, {}px). Tiles have {}px here.",
                narrowest_width_px(widest, m.px_per_col),
                m.area_px
            )
        });
    }
    if shown >= total {
        return NarrowestStep::Limit("Every tile on this desktop is already on screen.".into());
    }
    let narrowest = NARROWEST_STEPS[0].min(current);
    let narrowest_px = narrowest_width_px(narrowest, m.px_per_col);
    let asked = shown + 1;
    let asked_px = asked as f32 * narrowest_px;
    let sidebar = m.area_px < window_px && asked_px <= window_px;
    let narrower = narrowest_px > TILE_MIN_WIDTH && asked as f32 * TILE_MIN_WIDTH <= m.area_px;
    NarrowestStep::Limit(format!(
        "{asked} tiles at the narrowest width ({narrowest} cols, {narrowest_px}px) need {asked_px}px. Tiles have {}px here.{}{}",
        m.area_px,
        if sidebar {
            " Closing the sidebar would make room."
        } else {
            ""
        },
        if narrower {
            " A narrower width can be set in Settings → Appearance."
        } else {
            ""
        },
    ))
}

/// The message when a column cannot take one more stacked tile at the minimum height, or
/// None when it can.
pub fn stack_limit_message(tree: &Node, anchor: &str, tile_height_px: f32) -> Option<String> {
    let row = read_row(tree);
    let column = &row[column_index(&row, anchor)?];
    let asked = column.leaf_ids.len() + 1;
    let needed = asked as f32 * TILE_MIN_HEIGHT;
    (needed > tile_height_px).then(|| {
        format!(
            "{asked} stacked tiles at the {}px minimum need {}px. Tiles have {}px here, so it opened beside instead.",
            TILE_MIN_HEIGHT, needed, tile_height_px
        )
    })
}

#[cfg(test)]
mod tests {
    use super::*;

    const PX_PER_COL: f32 = 10.0;

    fn pane(id: &str) -> Node {
        Node::Leaf(Leaf {
            id: id.into(),
            kind: LeafKind::Pane,
            wanted_cols: None,
            wanted_area_cols: None,
            window: None,
        })
    }

    fn chain(ids: &[&str]) -> Node {
        match ids {
            [one] => pane(one),
            [first, rest @ ..] => Node::Split(Split {
                id: format!("s-{}", ids.len()),
                direction: Direction::Vertical,
                ratio: 0.5,
                children: Box::new([pane(first), chain(rest)]),
            }),
            [] => unreachable!(),
        }
    }

    fn firsts(columns: &[Column]) -> Vec<&str> {
        columns.iter().map(Column::first).collect()
    }

    fn resolve(ids: &[&str], active: &str, first: Option<&FirstColumn>) -> RowPlan {
        let tree = chain(ids);
        resolve_row(&RowInput {
            tree: &tree,
            active,
            first_column: first,
            held: &[],
            cleared: &[],
            measures: Measures {
                area_px: 1500.0,
                narrowest_px: 500.0,
                px_per_col: PX_PER_COL,
            },
        })
    }

    #[test]
    fn the_screen_shows_the_run_that_fits_and_keeps_the_active_column_on_it() {
        let plan = resolve(&["a", "b", "c", "d", "e"], "a", None);
        assert_eq!(firsts(&plan.screen), ["a", "b", "c"]);
        assert_eq!(firsts(&plan.after), ["d", "e"]);

        let plan = resolve(&["a", "b", "c", "d", "e"], "e", None);
        assert_eq!(firsts(&plan.screen), ["c", "d", "e"]);
        assert_eq!(firsts(&plan.before), ["a", "b"]);
    }

    #[test]
    fn the_screen_keeps_its_first_column_while_the_active_tile_stays_on_it() {
        let first = FirstColumn {
            leaf: "b".into(),
            index: 1,
        };
        let plan = resolve(&["a", "b", "c", "d", "e"], "c", Some(&first));
        assert_eq!(firsts(&plan.screen), ["b", "c", "d"]);
    }

    #[test]
    fn a_tree_parses_from_the_daemons_json_and_reads_as_a_row() {
        let json = r#"{"type":"split","split_id":"s1","direction":"vertical","ratio":0.5,
            "children":[{"type":"pane","pane_id":"a","wanted_cols":80,"wanted_area_cols":200},
            {"type":"split","split_id":"s2","direction":"horizontal","ratio":0.3,
             "children":[{"type":"tile","tile_id":"m","tile_kind":"markdown"},{"type":"pane","pane_id":"b"}]}]}"#;
        let tree = Node::parse(json).unwrap();
        let row = read_row(&tree);
        assert_eq!(
            row.iter().map(|c| c.leaf_ids.clone()).collect::<Vec<_>>(),
            [vec!["a".to_owned()], vec!["m".into(), "b".into()]]
        );
        assert_eq!(row[0].wanted_cols, Some(80));
        assert_eq!(Node::parse(""), None);
        assert_eq!(Node::parse("{\"type\":\"tile\",\"tile_id\":\"x\"}"), None);
    }

    #[test]
    fn arrows_find_the_nearest_leaf_that_overlaps() {
        let json = r#"{"type":"split","split_id":"s1","direction":"vertical","ratio":0.5,
            "children":[{"type":"pane","pane_id":"a"},
            {"type":"split","split_id":"s2","direction":"horizontal","ratio":0.5,
             "children":[{"type":"pane","pane_id":"b"},{"type":"pane","pane_id":"c"}]}]}"#;
        let tree = Node::parse(json).unwrap();
        assert_eq!(tree.leaf_toward("a", Toward::Right).as_deref(), Some("b"));
        assert_eq!(tree.leaf_toward("c", Toward::Up).as_deref(), Some("b"));
        assert_eq!(tree.leaf_toward("c", Toward::Left).as_deref(), Some("a"));
        assert_eq!(tree.leaf_toward("a", Toward::Left), None);
    }
}

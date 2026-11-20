use std::rc::Rc;

use rune::{El, Tx, col, row};

use super::actions::Actions;
use super::block::{Block, Kind, Press, inner_gap, spaced};
use super::measure::{FIELD, FLEX_FLOOR, READING};
use crate::Tokens;
use crate::system::{
    Button, Control, IconSize, Look, Radius, Said, Size, Space, Tone, Type, door, hover_layer,
    press_area, tag, words,
};

/// The columns of a [`Table`]: the name column, which takes the width the others leave, and
/// fixed columns after it at widths the caller measured. A table also has a column for the
/// rows' marks when a row has one, and one for their end buttons when a row has one.
pub struct TableColumns {
    name: Said,
    identifiers: bool,
    fixed: Vec<(Said, f32, bool)>,
}

/// A fixed column of the [`TableColumns`] that made it, where a row places its controls.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub struct Column(usize);

impl TableColumns {
    pub fn new(name: impl Into<Said>) -> TableColumns {
        TableColumns {
            name: name.into(),
            fixed: Vec::new(),
            identifiers: false,
        }
    }

    /// Names that are identifiers, such as `session.state.changed`: drawn in Mono and, where one
    /// must wrap, broken after its dots, never inside a part.
    pub fn identifiers(mut self) -> Self {
        self.identifiers = true;
        self
    }

    /// A column `width` wide after the ones before it, titled `title`.
    pub fn fixed(&mut self, title: impl Into<Said>, width: f32) -> Column {
        self.fixed.push((title.into(), width, false));
        Column(self.fixed.len() - 1)
    }

    /// [`Self::fixed`] whose content and title end on the column's trailing edge, as numbers
    /// do so that numbers of any size share a right edge.
    pub fn fixed_end(&mut self, title: impl Into<Said>, width: f32) -> Column {
        self.fixed.push((title.into(), width, true));
        Column(self.fixed.len() - 1)
    }
}

const ROW_INSET: Space = Space::S16;
const COLUMN_GAP: Space = Space::S12;

/// How the name grows against the last column's cell, which grows by 1: on a line they share
/// the cell takes a ten-thousandth of the spare room, under 0.1 point at the widest dialog, and
/// on a line of its own it takes the whole line, so the row's button ends at its trailing edge.
const NAME_GROW: f32 = 1e4;

/// The most lines a closed sub-row gives its note, the one place it shows what the person wrote.
const NOTE_LINES: usize = 2;

/// A table of rows that open: each row's name opens it, and an open row shows its details, its
/// sub-rows and its actions on the table's columns. A narrow table wraps its fixed columns under
/// the name, and its head then names only the columns still on its line.
pub struct Table<S> {
    key: String,
    columns: TableColumns,
    rows: Vec<TableRow<S>>,
    empty: Option<Empty>,
    footer: Option<(String, Actions)>,
    head_key: Option<String>,
    dimmed: bool,
    named: Option<String>,
    compact: bool,
}

impl<S: 'static> Table<S> {
    pub fn new(key: impl Into<String>, columns: TableColumns) -> Table<S> {
        Table {
            key: key.into(),
            columns,
            rows: Vec::new(),
            empty: None,
            footer: None,
            head_key: None,
            dimmed: false,
            named: None,
            compact: false,
        }
    }

    /// The head's key in place of `{key}.head`, for a page whose head had its own key before it
    /// stood in a table.
    pub fn head_key(mut self, key: impl Into<String>) -> Self {
        self.head_key = Some(key.into());
        self
    }

    /// Rows as tall as their text, not a Regular control, for a table of many rows read at a
    /// glance, such as a diagnostics list.
    pub fn compact(mut self) -> Self {
        self.compact = true;
        self
    }

    /// Assistive tools find the table as a table read as `label`, as one whose rows only show
    /// their numbers is read, its head a row read as "Columns".
    pub fn named(mut self, label: impl Into<String>) -> Self {
        self.named = Some(label.into());
        self
    }

    pub fn row(mut self, row: TableRow<S>) -> Self {
        self.rows.push(row);
        self
    }

    pub fn rows(mut self, rows: impl IntoIterator<Item = TableRow<S>>) -> Self {
        self.rows.extend(rows);
        self
    }

    /// What the table says in place of rows it does not have yet.
    pub fn empty(mut self, empty: Empty) -> Self {
        self.empty = Some(empty);
        self
    }

    /// The quiet buttons under the last row that add to it, on a line keyed `key`, their words
    /// on the name column.
    pub fn footer(mut self, key: impl Into<String>, actions: Actions) -> Self {
        self.footer = Some((key.into(), actions));
        self
    }

    /// While what the table configures is off, its head and what its rows say fade. Every control
    /// keeps its colour, since each still works.
    pub fn dimmed(mut self, dimmed: bool) -> Self {
        self.dimmed = dimmed;
        self
    }

    pub fn build(self, t: &Tokens) -> Block {
        let grid = Grid {
            name: self.columns.name,
            identifiers: self.columns.identifiers,
            fixed: self.columns.fixed,
            lead: self.rows.iter().any(|r| r.leading.is_some()),
            action: self.rows.iter().any(TableRow::has_end),
            dimmed: self.dimmed,
            compact: self.compact,
        };
        let head_key = self
            .head_key
            .unwrap_or_else(|| format!("{}.head", self.key));
        let mut head = grid.dim(grid.head(&head_key, t).key(head_key));
        if self.named.is_some() {
            head = head.role("row").label("Columns");
        }
        let n = self.rows.len();
        let footed = self.footer.is_some();
        let rows = self
            .rows
            .into_iter()
            .enumerate()
            .map(|(i, r)| r.build(&grid, i + 1 < n || footed, t));
        let footer = self.footer.map(|(key, actions)| {
            let quiet = door::hang_left(actions.build().el, Size::Regular.inset());
            grid.padded(grid.name_column(quiet), Space::S8).key(key)
        });
        let mut table = col().key(self.key);
        if let Some(label) = self.named {
            table = table.role("table").label(label);
        }
        let table = table
            .w_full()
            .fill(t.bg_panel)
            .border(1.0, t.border)
            .child(head)
            .children(self.empty.map(|e| e.build(t)))
            .children(rows)
            .children(footer);
        Block::new(
            Kind::Table,
            door::clip_corner(door::corner(table, Radius::Card), Radius::Card),
        )
    }
}

struct Grid {
    name: Said,
    identifiers: bool,
    fixed: Vec<(Said, f32, bool)>,
    compact: bool,
    lead: bool,
    action: bool,
    dimmed: bool,
}

impl Grid {
    /// How tall a row's first line stands: a Regular control, or in a compact table its text.
    fn band(&self) -> f32 {
        if self.compact {
            0.0
        } else {
            Size::Regular.height()
        }
    }

    fn dim(&self, e: El) -> El {
        if self.dimmed { door::faded(e) } else { e }
    }

    /// The mark and the name, as every line lays them: below the mark and the name's floor, the
    /// name takes a line of its own under the mark. `between` stands between those lines.
    fn who(&self, lead: Option<El>, name: El, between: Space) -> El {
        let name = name.min_w(0.0);
        if !self.lead {
            return name.w_full();
        }
        let side = Size::Regular.height();
        door::gaps(
            row().items_start().w_full().min_w(0.0).flex_wrap(),
            between,
            COLUMN_GAP,
        )
        .child(
            row()
                .w(side)
                .min_w(side)
                .children(lead.map(|l| band(l, side))),
        )
        .child(name.grow_from(1.0, FLEX_FLOOR))
    }

    /// One line on the columns: `who` over the mark and name columns, `fixed` giving each fixed
    /// column its content, and `end` in the action column. Below the name's floor the fixed
    /// columns wrap under the name, starting on the names' edge, and the end follows the line.
    fn line(&self, who: El, mut fixed: impl FnMut(Column) -> El, end: Option<El>) -> El {
        let side = Size::Regular.height();
        let mark = self.mark_column();
        let who = door::drawn_back(row(), mark)
            .grow_from(NAME_GROW, self.name_floor())
            .child(who.grow(1.0));
        let end_room = if self.action {
            COLUMN_GAP.pt() + side
        } else {
            0.0
        };
        let mut at_end = end.map(|end| row().grow_from(1.0, side).justify_end().child(end));
        let last = self.fixed.len().checked_sub(1);
        let cells: Vec<El> = self
            .fixed
            .iter()
            .enumerate()
            .map(|(i, (_, w, end))| {
                let w = *w;
                let content = fixed(Column(i)).shrink().max_w(w);
                let content = if *end {
                    row().w(w).justify_end().child(content)
                } else {
                    content
                };
                if Some(i) == last {
                    door::gaps(
                        row().items_start().flex_wrap().grow_from(1.0, w + end_room),
                        Space::S4,
                        COLUMN_GAP,
                    )
                    .child(content)
                    .children(at_end.take())
                } else {
                    row().grow_from(0.0, w).child(content)
                }
            })
            .collect();
        let columns_width = self.fixed.iter().map(|(_, w, _)| *w).sum::<f32>()
            + COLUMN_GAP.pt() * self.fixed.len().saturating_sub(1) as f32
            + end_room;
        let columns = door::gaps(
            row().items_start().flex_wrap().grow_from(1.0, FLEX_FLOOR),
            Space::S0,
            COLUMN_GAP,
        )
        .children(cells)
        .children(at_end);
        let columns = door::gaps(
            door::drawn_back(row(), mark)
                .items_start()
                .flex_wrap()
                .grow_from(1.0, mark + columns_width),
            Space::S0,
            COLUMN_GAP,
        )
        .children(self.lead.then(|| row().w(side).min_w(side)))
        .child(columns);
        let line = door::gaps(
            door::inset_start(row().items_start().flex_wrap().w_full(), mark),
            Space::S0,
            COLUMN_GAP,
        )
        .child(who)
        .child(columns);
        door::sides(row().w_full().child(line), ROW_INSET, ROW_INSET)
    }

    /// How far the names start from the row's start: past the mark column, when there is one.
    fn mark_column(&self) -> f32 {
        if self.lead {
            Size::Regular.height() + COLUMN_GAP.pt()
        } else {
            0.0
        }
    }

    /// The width the mark and the name column need on one line.
    fn name_floor(&self) -> f32 {
        self.mark_column() + FLEX_FLOOR
    }

    /// The head on the columns, one line high, so a narrow table names only the columns on it.
    fn head(&self, head: &str, t: &Tokens) -> El {
        let label = |title: &Said| words(title.clone(), Type::Label, t.text_tertiary);
        let line = self.line(
            self.who(None, label(&self.name), Space::S0),
            |column| label(&self.fixed[column.0].0).key(format!("{head}.{}", column.0)),
            None,
        );
        let line = door::clip(line.max_h(door::line_height(Type::Label)));
        self.padded(line, Space::S8)
            .fill(t.bg_elevated)
            .border_sides([0.0, 0.0, 1.0, 0.0], t.border_subtle)
    }

    /// Each fixed column's content, in the order it was placed.
    fn place(&self, content: impl IntoIterator<Item = (Column, El)>) -> Vec<Vec<El>> {
        let mut placed: Vec<Vec<El>> = self.fixed.iter().map(|_| Vec::new()).collect();
        for (column, e) in content {
            placed
                .get_mut(column.0)
                .unwrap_or_else(|| {
                    panic!(
                        "a row placed content in column {} of another table, which has {} fixed columns",
                        column.0,
                        self.fixed.len()
                    )
                })
                .push(e);
        }
        placed
    }

    /// `line` with `space` above and below it.
    fn padded(&self, line: El, space: Space) -> El {
        door::inset(col().w_full().child(line), Space::S0, space)
    }

    /// `content` in the name column of a line of its own, so it ends where the names end.
    fn name_column(&self, content: El) -> El {
        self.line(self.who(None, content, Space::S0), |_| row(), None)
    }
}

/// `name` in Mono, its parts after each dot free to start a line, a part with no dot wrapping
/// where it must.
fn identifier(key: &str, name: &str, t: &Tokens) -> El {
    let parts = name.split_inclusive('.').enumerate().map(|(i, part)| {
        words(Said::from(part.to_owned()), Type::Mono, t.text_primary)
            .key(format!("{key}.name.{i}"))
            .wrap_anywhere()
            .shrink()
            .min_w(0.0)
    });
    col()
        .label(name.to_owned())
        .shrink()
        .min_w(0.0)
        .child(row().w_full().flex_wrap().children(parts).a11y_hidden())
}

/// A row's part centred on its first line, a band `height` high.
fn band(content: El, height: f32) -> El {
    row().min_h(height).items_center().child(content)
}

/// A fixed column's content: the first on the row's first line, the rest under it.
fn stacked(content: Vec<El>, height: f32) -> El {
    let mut content = content.into_iter();
    door::gap(col().items_start(), Space::S4)
        .children(content.next().map(|c| band(c, height).w_full()))
        .children(content)
}

/// `area` widened by the press area's inset on both sides, so what it holds is as wide as the
/// column it stands in.
fn whole_width(area: El) -> El {
    door::grown_past_end(row().grow(1.0).min_w(0.0), 2.0 * Size::Compact.inset().pt()).child(area)
}

/// A control in a column, its box out to the left by its own inset, so its words sit on the
/// column line.
fn hung(control: Control) -> El {
    let size = control.size();
    door::hang_left(El::from(control).shrink(), size.inset())
}

/// What a table with no rows says, and what to do about it.
pub struct Empty {
    key: String,
    heading: Said,
    copy: Said,
    actions: Vec<El>,
}

impl Empty {
    pub fn new(
        key: impl Into<String>,
        heading: impl Into<Said>,
        copy: impl Into<Said>,
        actions: impl IntoIterator<Item = El>,
    ) -> Empty {
        Empty {
            key: key.into(),
            heading: heading.into(),
            copy: copy.into(),
            actions: actions.into_iter().collect(),
        }
    }

    fn build(self, t: &Tokens) -> El {
        let said = door::gap(col().items_center(), Space::S4)
            .child(
                words(self.heading.clone(), Type::Name, t.text_primary)
                    .key(format!("{}.heading", self.key))
                    .role("heading")
                    .label(self.heading.text()),
            )
            .child(
                words(self.copy, Type::Small, t.text_secondary)
                    .wrap()
                    .max_w(READING)
                    .align(rune::Align::Center),
            );
        let actions = door::gaps(
            row().w_full().items_center().justify_center().flex_wrap(),
            Space::S8,
            Space::S8,
        )
        .children(self.actions);
        let empty = door::gap(col().key(self.key).w_full().items_center(), Space::S12)
            .child(said)
            .child(actions);
        door::inset(empty, Space::S24, Space::S32)
            .border_sides([0.0, 0.0, 1.0, 0.0], t.border_subtle)
    }
}

/// A tag after a row's name: what it says, its tone, its tooltip and, when the caller names it,
/// its key.
struct RowTag {
    said: Said,
    tone: Tone,
    help: Option<String>,
    key: Option<String>,
}

/// One row of a [`Table`]: a mark, a name that opens the row, words or controls in the fixed
/// columns, and a button at its end.
pub struct TableRow<S> {
    key: String,
    leading: Option<Control>,
    title: Said,
    tags: Vec<RowTag>,
    subtitle: Said,
    controls: Vec<(Column, El)>,
    cells: Vec<(Column, Said)>,
    still: bool,
    more: Option<String>,
    off: bool,
    open: bool,
    toggle: Press<S>,
    details: Vec<Block>,
    sub_rows: Vec<SubRow<S>>,
    sub_footer: Option<(Column, Control)>,
    actions: Option<Actions>,
}

impl<S: 'static> TableRow<S> {
    pub fn new(
        key: impl Into<String>,
        title: impl Into<Said>,
        toggle: impl Fn(&mut S, &mut Tx) + 'static,
    ) -> TableRow<S> {
        TableRow {
            key: key.into(),
            leading: None,
            title: title.into(),
            tags: Vec::new(),
            subtitle: Said::default(),
            controls: Vec::new(),
            cells: Vec::new(),
            still: false,
            more: None,
            off: false,
            open: false,
            toggle: Rc::new(toggle),
            details: Vec::new(),
            sub_rows: Vec::new(),
            sub_footer: None,
            actions: None,
        }
    }

    /// A row that only shows its thing and does not open: its name is words, and the row is
    /// read as its name, its tags and its cells.
    pub fn still(key: impl Into<String>, title: impl Into<Said>) -> TableRow<S> {
        let mut row = TableRow::new(key, title, |_: &mut S, _: &mut Tx| {});
        row.still = true;
        row
    }

    /// Words in the fixed column `column`, such as a count; text built at run time is drawn as
    /// written.
    pub fn cell(mut self, column: Column, said: impl Into<Said>) -> Self {
        self.cells.push((column, said.into()));
        self
    }

    /// A tag after the name keyed `key`, explained by `help` under the pointer and to assistive
    /// tools when there is help, for a tag whose words change, such as a count.
    pub fn tag_keyed(
        mut self,
        key: impl Into<String>,
        said: impl Into<Said>,
        tone: Tone,
        help: Option<String>,
    ) -> Self {
        self.tags.push(RowTag {
            said: said.into(),
            tone,
            help,
            key: Some(key.into()),
        });
        self
    }

    /// The row's mark in the icon column.
    pub fn leading(mut self, mark: Control) -> Self {
        self.leading = Some(mark);
        self
    }

    pub fn tag(mut self, said: impl Into<Said>, tone: Tone) -> Self {
        self.tags.push(RowTag {
            said: said.into(),
            tone,
            help: None,
            key: None,
        });
        self
    }

    pub fn subtitle(mut self, said: impl Into<Said>) -> Self {
        self.subtitle = said.into();
        self
    }

    /// A control in the fixed column `column`; several in one column stack one under another.
    pub fn control(mut self, column: Column, control: Control) -> Self {
        self.controls.push((column, hung(control)));
        self
    }

    /// The button at the row's end that opens it too, named `label`.
    pub fn more(mut self, label: impl Into<String>) -> Self {
        self.more = Some(label.into());
        self
    }

    pub fn off(mut self, off: bool) -> Self {
        self.off = off;
        self
    }

    pub fn open(mut self, open: bool) -> Self {
        self.open = open;
        self
    }

    pub fn details(mut self, block: Block) -> Self {
        self.details.push(block);
        self
    }

    pub fn sub_row(mut self, sub: SubRow<S>) -> Self {
        self.sub_rows.push(sub);
        self
    }

    /// A control under the sub-rows in the fixed column `column`, such as one that adds another.
    pub fn sub_footer(mut self, column: Column, control: Control) -> Self {
        self.sub_footer = Some((column, control));
        self
    }

    /// What can be done to the row, last in its open state.
    pub fn actions(mut self, actions: Actions) -> Self {
        self.actions = Some(actions);
        self
    }

    fn has_end(&self) -> bool {
        self.more.is_some() || self.sub_rows.iter().any(|s| s.trailing.is_some())
    }

    fn build(mut self, grid: &Grid, ruled: bool, t: &Tokens) -> El {
        let toggle = self.toggle.clone();
        let quiet = self.off || grid.dimmed;
        let fade = |e: El| if quiet { door::faded(e) } else { e };
        let title_line = door::gaps(
            row().items_center().w_full().min_w(0.0).flex_wrap(),
            Space::S4,
            Space::S8,
        )
        .child(if grid.identifiers {
            identifier(&self.key, &self.title.text(), t)
        } else {
            words(&self.title, Type::Name, t.text_primary)
                .wrap()
                .shrink()
        })
        .children(self.tags.iter().map(
            |RowTag {
                 said,
                 tone,
                 help,
                 key,
             }| {
                let tag = El::from(tag(
                    key.clone()
                        .unwrap_or_else(|| format!("{}.tag.{}", self.key, said.text())),
                    said,
                    *tone,
                    t,
                ));
                let tag = if self.still { tag.role("status") } else { tag };
                match help {
                    Some(help) => tag.help(help.clone()),
                    None => tag,
                }
            },
        ));
        let description: Vec<String> = self
            .tags
            .iter()
            .map(|tag| tag.said.text())
            .chain((!self.subtitle.text().is_empty()).then(|| self.subtitle.text()))
            .collect();
        let said = door::gap(col().w_full().min_w(0.0).items_start(), Space::S2)
            .child(title_line)
            .children((!self.subtitle.text().is_empty()).then(|| {
                words(self.subtitle.clone(), Type::Small, t.text_secondary)
                    .w_full()
                    .min_w(0.0)
                    .wrap()
            }));
        let said = if self.still { said } else { said.a11y_hidden() };
        let said = if grid.compact {
            said
        } else {
            door::on_band(said, Type::Name, Size::Regular)
        };
        let said = fade(grid.who(self.leading.map(El::from), said, Space::S4));
        let mut cells = std::mem::take(&mut self.cells);
        cells.sort_by_key(|(column, _)| column.0);
        let read: Vec<String> = std::iter::once(self.title.text())
            .chain(self.tags.iter().map(|tag| tag.said.text()))
            .chain(cells.iter().map(|(_, said)| said.text()))
            .collect();
        let key = self.key.clone();
        let cell_els = cells.into_iter().map(|(column, said)| {
            (
                column,
                fade(
                    words(said, Type::Mono, t.text_secondary)
                        .wrap_anywhere()
                        .shrink()
                        .key(format!("{key}.cell.{}", column.0)),
                ),
            )
        });
        if self.still {
            let mut placed = grid.place(cell_els.chain(self.controls));
            let fixed =
                |column: Column| stacked(std::mem::take(&mut placed[column.0]), grid.band());
            let main = grid.padded(grid.line(said, fixed, None), Space::S8);
            let out = col()
                .key(self.key)
                .role("row")
                .label(read.join(", "))
                .w_full()
                .child(main);
            return if ruled {
                out.border_sides([0.0, 0.0, 1.0, 0.0], t.border_subtle)
            } else {
                out
            };
        }
        self.controls.extend(cell_els);
        let who = press_area(
            self.key.clone(),
            &self.title.text(),
            self.open,
            false,
            said,
            t,
            move |s: &mut S, tx: &mut Tx| toggle(s, tx),
        );
        let who = whole_width(El::from(who).description(description.join(" · ")));
        let mut placed = grid.place(self.controls);
        let fixed = |column: Column| stacked(std::mem::take(&mut placed[column.0]), grid.band());
        let more = self.more.map(|label| {
            let toggle = self.toggle.clone();
            band(
                Button::mark("···", IconSize::Large)
                    .look(Look::Quiet)
                    .size(Size::Compact)
                    .expanded(self.open)
                    .on_press_tx(move |s: &mut S, tx: &mut Tx| toggle(s, tx))
                    .build(t)
                    .key(format!("{}.more", self.key))
                    .label(label)
                    .into(),
                grid.band(),
            )
        });
        let main = grid.padded(grid.line(who, fixed, more), Space::S8);
        let mut out = col().key(format!("{}.row", self.key)).w_full().child(main);
        if self.open {
            out = out.fill(t.bg_elevated);
            if !self.details.is_empty() {
                out = out.child(door::below(
                    grid.name_column(spaced(self.details, inner_gap, None).max_w(FIELD)),
                    Space::S16,
                ));
            }
            out = out.children(self.sub_rows.into_iter().map(|s| s.build(grid, t)));
            if let Some((at, footer)) = self.sub_footer {
                let mut placed = grid.place([(at, hung(footer))]);
                let line = grid.line(
                    grid.who(None, row(), Space::S0),
                    |column| stacked(std::mem::take(&mut placed[column.0]), grid.band()),
                    None,
                );
                out = out.child(door::below(line, Space::S12));
            }
            if let Some(actions) = self.actions {
                out = out.child(door::below(
                    grid.name_column(actions.build().el.max_w(FIELD)),
                    Space::S16,
                ));
            }
        } else {
            out = out.hover(hover_layer(t.bg_panel, t));
        }
        if ruled {
            out = out.border_sides([0.0, 0.0, 1.0, 0.0], t.border_subtle);
        }
        out
    }
}

/// A row inside an open row, on a rail in the name column: an alternative to the row's own
/// choice, with its control in the fixed column.
pub struct SubRow<S> {
    key: String,
    title: Said,
    note_label: Said,
    note: Said,
    note_missing: bool,
    controls: Vec<(Column, El)>,
    trailing: Option<Control>,
    open: bool,
    toggle: Press<S>,
    details: Vec<Block>,
}

impl<S: 'static> SubRow<S> {
    pub fn new(
        key: impl Into<String>,
        title: impl Into<Said>,
        toggle: impl Fn(&mut S, &mut Tx) + 'static,
    ) -> SubRow<S> {
        SubRow {
            key: key.into(),
            title: title.into(),
            note_label: Said::default(),
            note: Said::default(),
            note_missing: false,
            controls: Vec::new(),
            trailing: None,
            open: false,
            toggle: Rc::new(toggle),
            details: Vec::new(),
        }
    }

    /// The line under the title: `label` then `said`, quiet when `missing`.
    pub fn note(mut self, label: impl Into<Said>, said: impl Into<Said>, missing: bool) -> Self {
        self.note_label = label.into();
        self.note = said.into();
        self.note_missing = missing;
        self
    }

    /// A control in the fixed column `column`, as its row's are.
    pub fn control(mut self, column: Column, control: Control) -> Self {
        self.controls.push((column, hung(control)));
        self
    }

    /// A button at the sub-row's end, in the action column.
    pub fn trailing(mut self, control: Control) -> Self {
        self.trailing = Some(control);
        self
    }

    pub fn open(mut self, open: bool) -> Self {
        self.open = open;
        self
    }

    pub fn details(mut self, block: Block) -> Self {
        self.details.push(block);
        self
    }

    fn build(self, grid: &Grid, t: &Tokens) -> El {
        let toggle = self.toggle.clone();
        let note_ink = if self.note_missing {
            t.text_tertiary
        } else {
            t.text_secondary
        };
        let note = door::gap(row().w_full().min_w(0.0).items_start(), Space::S4)
            .child(
                words(self.note_label, Type::Small, t.text_tertiary)
                    .key(format!("{}.note.label", self.key)),
            )
            .child(
                words(self.note.clone(), Type::Small, note_ink)
                    .key(format!("{}.note", self.key))
                    .max_lines(NOTE_LINES)
                    .shrink()
                    .min_w(0.0),
            );
        let said = door::gap(col().w_full().min_w(0.0).items_start(), Space::S2)
            .child(
                words(self.title.clone(), Type::Name, t.text_primary)
                    .truncate()
                    .min_w(0.0),
            )
            .child(note)
            .a11y_hidden();
        let who = El::from(press_area(
            self.key.clone(),
            &self.title.text(),
            self.open,
            false,
            grid.dim(door::on_band(said, Type::Name, Size::Regular)),
            t,
            move |s: &mut S, tx: &mut Tx| toggle(s, tx),
        ))
        .description(self.note.text());
        let rail = door::on_rail(row().w_full().min_w(0.0), t.border_hover).child(who);
        let mut placed = grid.place(self.controls);
        let mut out = col()
            .key(format!("{}.row", self.key))
            .w_full()
            .child(grid.line(
                grid.who(None, rail, Space::S0),
                |column| stacked(std::mem::take(&mut placed[column.0]), grid.band()),
                self.trailing.map(|c| band(c.into(), grid.band())),
            ));
        if self.open && !self.details.is_empty() {
            let details = spaced(self.details, inner_gap, None).max_w(FIELD);
            out = out.child(door::below(
                grid.name_column(door::on_rail(row().w_full(), t.border_hover).child(details)),
                Space::S16,
            ));
        }
        door::below(out, Space::S4)
    }
}

use super::shelf::{CountSide, Place, Shelf, ShelfCount, ShelfEntry, ShelfMember};

/// An entry of a [`ShelfRow`]: what it stands for to its caller, how it draws, how wide its name
/// stands, and whether it is folded into its end's count.
pub struct Laid<T> {
    pub of: T,
    pub drawn: ShelfEntry,
    pub name_px: f32,
    pub folded: bool,
}

impl<T> Laid<T> {
    fn new((of, drawn): (T, ShelfEntry)) -> Laid<T> {
        Laid {
            of,
            name_px: drawn.name_width(),
            drawn,
            folded: false,
        }
    }

    pub fn needs_user(&self) -> bool {
        self.drawn.members().iter().any(ShelfMember::needs_user)
    }

    /// The entries on screen, and the current one wherever it stands.
    fn never_folds(&self) -> bool {
        self.drawn.place() != Place::OffScreen
    }

    pub fn width(&self) -> f32 {
        self.drawn.width(self.name_px)
    }

    fn least_width(&self) -> f32 {
        self.drawn.width(0.0)
    }
}

/// A shelf's entries before its screen, on it and after it, fitted to the room it has: the
/// entries off screen that the room cannot hold fold into a count at their end.
pub struct ShelfRow<T> {
    pub before: Vec<Laid<T>>,
    pub screen: Vec<Laid<T>>,
    pub after: Vec<Laid<T>>,
    /// How many marks a count shows at most.
    marks: usize,
}

/// The count on `side` of the entries `held` in it, with at most `marks` of the members that need
/// the user.
fn counted<T>(side: CountSide, held: &[&Laid<T>], marks: usize) -> Option<ShelfCount> {
    let needing: Vec<ShelfMember> = held
        .iter()
        .flat_map(|e| e.drawn.members())
        .filter(|m| m.needs_user())
        .cloned()
        .collect();
    (!held.is_empty()).then(|| ShelfCount {
        side,
        columns: held.len(),
        needs_user: !needing.is_empty(),
        marks: needing.into_iter().take(marks).collect(),
    })
}

/// The room `count` takes and the gap to its neighbour.
fn count_width(count: Option<&ShelfCount>) -> f32 {
    count.map_or(0.0, |c| c.width() + Shelf::gap())
}

/// The room `items` and the shelf's ends take, each item after the first with its gap.
fn shelf_width(items: impl Iterator<Item = f32>) -> f32 {
    items.map(|w| w + Shelf::gap()).sum::<f32>() - Shelf::gap() + Shelf::START + Shelf::END
}

/// The width each name is cut to so that all of them and the current one fit `room`: the others
/// evenly, the current one only once they have none.
fn cut_names<T>(shown: &mut [&mut Laid<T>], room: f32) {
    let least: f32 = shown.iter().map(|e| e.least_width()).sum();
    let gaps = Shelf::gap() * shown.len().saturating_sub(1) as f32;
    let spare = room - least - gaps;
    let wanted = |e: &Laid<T>| e.width() - e.least_width();
    let current = |e: &Laid<T>| e.drawn.place() == Place::Current;
    let kept: f32 = shown.iter().filter(|e| current(e)).map(|e| wanted(e)).sum();
    let mut others: Vec<f32> = shown
        .iter()
        .filter(|e| !current(e))
        .map(|e| wanted(e))
        .collect();
    others.sort_by(f32::total_cmp);
    let mut left = spare - kept;
    let mut share = f32::INFINITY;
    for (i, want) in others.iter().enumerate() {
        let even = left / (others.len() - i) as f32;
        if *want > even {
            share = even.max(0.0);
            break;
        }
        left -= want;
    }
    for e in shown.iter_mut().filter(|e| !current(e)) {
        if wanted(e) > share {
            e.name_px = e.drawn.name_within(share);
        }
    }
    let taken: f32 = shown
        .iter()
        .filter(|e| !current(e))
        .map(|e| wanted(e))
        .sum();
    for e in shown.iter_mut().filter(|e| current(e)) {
        if wanted(e) > spare - taken {
            e.name_px = e.drawn.name_within((spare - taken).max(0.0));
        }
    }
}

impl<T> ShelfRow<T> {
    /// The entries `before` the screen, on the `screen` and `after` it, fitted to `room`.
    pub fn fit(
        before: Vec<(T, ShelfEntry)>,
        screen: Vec<(T, ShelfEntry)>,
        after: Vec<(T, ShelfEntry)>,
        room: f32,
    ) -> ShelfRow<T> {
        let laid = |entries: Vec<(T, ShelfEntry)>| entries.into_iter().map(Laid::new).collect();
        let mut row = ShelfRow {
            before: laid(before),
            screen: laid(screen),
            after: laid(after),
            marks: usize::MAX,
        };
        row.fold(room);
        row
    }

    /// The width of the entries that never fold without their names and of both counts without
    /// marks, under which the shelf overflows its slot.
    pub fn least_px(&self) -> f32 {
        let bare = |side: CountSide, columns: usize| {
            (columns > 0).then(|| ShelfCount {
                side,
                columns,
                needs_user: true,
                marks: vec![],
            })
        };
        let least = shelf_width(
            self.entries()
                .filter(|e| e.never_folds())
                .map(Laid::least_width),
        ) + count_width(bare(CountSide::Before, self.before.len()).as_ref())
            + count_width(bare(CountSide::After, self.after.len()).as_ref());
        least.ceil()
    }

    /// Folds the entries off screen that `room` cannot hold, the farthest from the screen first
    /// and the ones that need the user last. A row that still does not fit gives up the counts'
    /// marks, then the names on screen.
    fn fold(&mut self, room: f32) {
        while self.width() > room {
            let Some(next) = self.next_to_fold() else {
                break;
            };
            next.folded = true;
        }
        self.marks = [CountSide::Before, CountSide::After]
            .into_iter()
            .filter_map(|side| self.count(side))
            .map(|count| count.marks.len())
            .max()
            .unwrap_or(0);
        while self.marks > 0 && self.width() > room {
            self.marks -= 1;
        }
        let over = self.width() - room;
        if over > 0.0 {
            let mut kept: Vec<&mut Laid<T>> = self
                .before
                .iter_mut()
                .chain(&mut self.screen)
                .chain(&mut self.after)
                .filter(|e| e.never_folds())
                .collect();
            let shown = shelf_width(kept.iter().map(|e| e.width()));
            cut_names(&mut kept, shown - Shelf::START - Shelf::END - over);
        }
    }

    /// The count of the entries folded at `side`, when any are.
    pub fn count(&self, side: CountSide) -> Option<ShelfCount> {
        counted(side, &self.folded(side), self.marks)
    }

    fn next_to_fold(&mut self) -> Option<&mut Laid<T>> {
        let distance = self.before.len();
        let before = self
            .before
            .iter_mut()
            .enumerate()
            .map(|(i, e)| (distance - i, e));
        let after = self.after.iter_mut().enumerate().map(|(i, e)| (i + 1, e));
        before
            .chain(after)
            .filter(|(_, e)| !e.folded && !e.never_folds())
            .max_by(|(a, x), (b, y)| {
                (!x.needs_user())
                    .cmp(&!y.needs_user())
                    .then(a.cmp(b))
                    .then(x.width().total_cmp(&y.width()))
            })
            .map(|(_, e)| e)
    }

    fn entries(&self) -> impl Iterator<Item = &Laid<T>> {
        self.before.iter().chain(&self.screen).chain(&self.after)
    }

    /// The entries the row draws, in its order.
    pub fn shown(&self) -> impl Iterator<Item = &Laid<T>> {
        self.entries().filter(|e| !e.folded)
    }

    fn width(&self) -> f32 {
        shelf_width(self.shown().map(Laid::width))
            + count_width(self.count(CountSide::Before).as_ref())
            + count_width(self.count(CountSide::After).as_ref())
    }

    /// Where the frame starts and how wide it stands: around the entries on screen.
    pub fn framed(&self) -> (f32, f32) {
        let gap = Shelf::gap();
        let start = Shelf::START
            + count_width(self.count(CountSide::Before).as_ref())
            + self
                .before
                .iter()
                .filter(|e| !e.folded)
                .map(|e| e.width() + gap)
                .sum::<f32>();
        let width = self.screen.iter().map(|e| e.width() + gap).sum::<f32>() - gap;
        let reach = Shelf::FRAME_REACH;
        (start - reach, width + 2.0 * reach)
    }

    /// The entries folded at `side`.
    pub fn folded(&self, side: CountSide) -> Vec<&Laid<T>> {
        let entries = match side {
            CountSide::Before => &self.before,
            CountSide::After => &self.after,
        };
        entries.iter().filter(|e| e.folded).collect()
    }
}

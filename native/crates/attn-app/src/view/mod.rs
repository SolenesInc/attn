//! A view drawn in its markdown tile: the parts the daemon checked, each by its draw function,
//! from the values of the view's sources.

mod answers;
mod commands;
mod draw;
mod keys;
mod sounds;
mod start;
mod values;

use std::collections::{BTreeMap, BTreeSet, HashMap};
use std::rc::Rc;

use attn_client::TileKey;
use attn_protocol::{ViewDocument, ViewPart, ViewParts};
use attn_ui::Tokens;
use attn_ui::blocks::{Block, Notice, Reader, Surface};
use rune::{El, Image, ScrollSpec, col, memo};
use rune_kit::ScrollBar;

use crate::State;
use crate::markdown::Document;

pub(crate) use answers::{Answers, keep_paused as keep_writing};
pub(crate) use commands::{Commands, confirmation};
pub(crate) use keys::{on_key, send_key};
pub(crate) use sounds::follow as follow_sounds;
pub(crate) use values::{Data, Scope};

/// What the window keeps for the views it shows, for as long as it lives: each view tile's tile
/// state, by tile, what is written and refused there, and the clips the views play.
#[derive(Default)]
pub struct Tiles {
    /// Rises with every change, so the tiles a view draws in build again.
    pub(crate) revision: u64,
    tiles: HashMap<String, TileState>,
    /// The text being written into each tile's questions.
    pub(crate) writing: answers::Writing,
    /// The files being uploaded into each tile's file questions.
    pub(crate) choosing: answers::Choosing,
    /// Why the home last refused an answer, a send or a command, by tile, until it takes one.
    refusals: HashMap<String, String>,
    pub(crate) sounds: sounds::Sounds,
    /// The text being typed into each entry, by the entry's key.
    pub(crate) entries: HashMap<String, rune::TextEdit>,
    /// The command waiting for the user's yes before it runs.
    pub(crate) confirming: Option<commands::Confirming>,
}

impl Tiles {
    fn of(&self, tile: &str) -> TileState {
        self.tiles.get(tile).cloned().unwrap_or_default()
    }

    fn change(&mut self, tile: &str, change: impl FnOnce(&mut TileState)) {
        change(self.tiles.entry(tile.to_owned()).or_default());
        self.revision += 1;
    }

    /// Forgets the tile state of tiles that closed.
    pub(crate) fn keep_only(&mut self, shown: &std::collections::HashSet<String>) {
        self.tiles.retain(|tile, _| shown.contains(tile));
        self.refusals.retain(|tile, _| shown.contains(tile));
        self.entries.retain(|key, _| {
            shown
                .iter()
                .any(|tile| key.starts_with(&format!("tile.{tile}.")))
        });
        self.writing.keep_only(shown);
        self.choosing.keep_only(shown);
    }

    /// Keeps why the home refused the tile's last answer or send, or forgets it once one is taken.
    fn refused(&mut self, tile: &str, why: Option<String>) {
        let changed = match why {
            Some(why) => self.refusals.insert(tile.to_owned(), why.clone()) != Some(why),
            None => self.refusals.remove(tile).is_some(),
        };
        if changed {
            self.revision += 1;
        }
    }
}

/// What a tile remembers about the view it shows: the highlight, the folded lists and the place
/// in each `one-by-one`, each by the key of what it is on.
#[derive(Clone, Default, Hash, PartialEq)]
pub(crate) struct TileState {
    highlight: Option<String>,
    folded: BTreeSet<String>,
    steps: BTreeMap<String, Steps>,
}

/// Where the user moved a `one-by-one` to, once they move it. Until then it rests on the first
/// item they have not kept or skipped. What they did with each item is an answer.
#[derive(Clone, Default, Hash, PartialEq)]
pub(crate) struct Steps {
    at: Option<usize>,
}

/// A part's way to draw itself: from its five doors and nothing else.
type Draw = fn(&ViewPart, &Doors<'_>) -> Block;

/// The one table from part to draw function. A part the catalog gains has no field here until
/// it has a draw function, so the app does not build without one.
const DRAWN: ViewParts<Draw> = ViewParts {
    markdown: draw::markdown,
    media: draw::media,
    tasks: draw::tasks,
    question: draw::question,
    question_multi: draw::question,
    question_text: draw::question,
    question_file: draw::question,
    compare: draw::compare,
    data: draw::data,
    status: draw::status,
    facts: draw::facts,
    list: draw::list,
    table: draw::table,
    chart: draw::chart,
    each: draw::each,
    one_by_one: draw::one_by_one,
    entry: draw::entry,
    controls: draw::controls,
    commands: draw::commands,
};

/// Everything a part reaches the rest of attn through.
pub(crate) struct Doors<'a> {
    /// Values in: the view's data around the part, and how each source stands.
    pub(crate) values: Scope<'a>,
    /// View commands: which run, how their runs stand, and the calls out.
    pub(crate) commands: Commands<'a>,
    /// Answers: what the user answered, and the drafts out.
    pub(crate) answers: Answers<'a>,
    /// Tile state: what the tile remembers about the part, and its changes.
    pub(crate) tile: TileDoor<'a>,
    /// Its box: where it stands, the theme, and what it draws pictures and links with.
    pub(crate) room: Room<'a>,
}

/// A part's tile state, and the handlers that change it.
pub(crate) struct TileDoor<'a> {
    tile: &'a str,
    state: &'a TileState,
}

impl TileDoor<'_> {
    pub(crate) fn highlighted(&self, key: &str) -> bool {
        self.state.highlight.as_deref() == Some(key)
    }

    /// Whether the keys reach the part keyed `key`: the highlight is on it or on a part it is in.
    pub(crate) fn reaches(&self, key: &str) -> bool {
        self.state.highlight.as_deref().is_some_and(|h| {
            key == h
                || key
                    .strip_prefix(h)
                    .is_some_and(|rest| rest.starts_with('.'))
        })
    }

    pub(crate) fn folded(&self, key: &str) -> bool {
        self.state.folded.contains(key)
    }

    pub(crate) fn steps(&self, key: &str) -> Steps {
        self.state.steps.get(key).cloned().unwrap_or_default()
    }

    /// Folds the list keyed `key`, or unfolds it, and puts the highlight on its heading.
    pub(crate) fn fold(&self, key: &str) -> impl Fn(&mut State) + 'static {
        let (tile, key) = (self.tile.to_owned(), key.to_owned());
        move |state: &mut State| keys::toggle_fold(state, &tile, &key)
    }
}

/// Where a part stands and how it draws.
pub(crate) struct Room<'a> {
    /// The part's key: unique in the window, and the same each time the view draws.
    pub(crate) key: String,
    /// The heading the view wrote just above a list, which the list draws as its own.
    pub(crate) heading: Option<String>,
    pub(crate) t: &'a Tokens,
    pub(crate) document: &'a Document,
    pub(crate) pictures: &'a Rc<HashMap<String, Option<Image>>>,
    pub(crate) heard: Heard<'a>,
}

/// The clips a view plays, on the machine its file is on.
#[derive(Clone, Copy)]
pub(crate) struct Heard<'a> {
    sounds: &'a sounds::Sounds,
    host: Option<&'a str>,
}

impl Heard<'_> {
    /// The clip `file` names in the view at `document`, when it names a file.
    pub(crate) fn clip(&self, document: &str, file: &str) -> Option<sounds::Clip> {
        clip(self.host, document, file)
    }

    /// How `clip` stands: whether it plays, how far it has played, and its length once known.
    pub(crate) fn of(&self, clip: &sounds::Clip) -> (attn_ui::blocks::Playing, f64, Option<f64>) {
        self.sounds.of(clip)
    }

    /// Plays `clip`, or pauses it while it plays.
    pub(crate) fn toggle(
        &self,
        clip: sounds::Clip,
    ) -> impl Fn(&mut State, &mut rune::Tx) + 'static {
        move |state: &mut State, _: &mut rune::Tx| sounds::toggle(state, &clip)
    }
}

/// The clip `file` names in the view at `document` on `host`, when it names a file.
fn clip(host: Option<&str>, document: &str, file: &str) -> Option<sounds::Clip> {
    match crate::markdown::links::resolve(document, file)? {
        crate::markdown::links::Target::Local(path) => Some(sounds::Clip {
            host: host.map(str::to_owned),
            path,
        }),
        _ => None,
    }
}

/// Each view tile on the current desktop, by the revision of its content and a hash of its
/// document, with the clips it draws now: the current item's alone in a `one-by-one`.
fn drawn_clips(state: &State) -> Vec<(TileKey, (u64, u64), Vec<sounds::Clip>)> {
    let Some(desktop) = state.facts.current_desktop() else {
        return Vec::new();
    };
    let mut drawn = Vec::new();
    for (place, content) in &state.facts.tile_contents.tiles {
        let message = &content.message;
        let Some(view) = &message.view else { continue };
        if place.desktop != desktop.id {
            continue;
        }
        let host = message.host.as_deref().filter(|h| !h.is_empty());
        let mut found = Vec::new();
        keys::clips(
            &view.parts,
            &Data::of(view).scope(0),
            &format!("tile.{}.view", place.tile),
            &|key, rows| keys::steps_at(state, place, key, rows),
            &mut found,
        );
        let clips = (found.into_iter())
            .filter_map(|(_, file)| clip(host, &message.path, &file))
            .collect();
        let document = sounds::document(&message.path, host, &message.content);
        drawn.push((place.clone(), (content.revision, document), clips));
    }
    drawn
}

impl<'a> Doors<'a> {
    /// The doors of the part `n` inside this one, reading inside `scope`.
    fn inner(
        &self,
        n: impl std::fmt::Display,
        scope: Scope<'a>,
        heading: Option<String>,
    ) -> Doors<'a> {
        Doors {
            values: scope,
            commands: self.commands.clone(),
            answers: self.answers.clone(),
            tile: TileDoor { ..self.tile },
            room: Room {
                key: format!("{}.{n}", self.room.key),
                heading,
                ..self.room
            },
        }
    }
}

/// `part`, drawn by its draw function, or its problem in its place.
fn part(part: &ViewPart, doors: &Doors<'_>) -> Block {
    if let Some(problem) = &part.problem {
        return draw::problem(&doors.room.key, problem, doors.room.t);
    }
    match DRAWN.get(&part.name) {
        Some(draw) => draw(part, doors),
        None => draw::notice(
            &doors.room.key,
            &format!("attn cannot draw \"{}\" yet", part.name),
            doors.room.t,
        ),
    }
}

/// `parts`, one under another, each keyed by its place. A heading written just above a list
/// moves into the list, which folds and counts under it and hides with it.
fn parts(parts: &[ViewPart], doors: &Doors<'_>) -> Vec<Block> {
    let mut drawn = Vec::new();
    let mut heading = None;
    for (i, shown) in parts.iter().enumerate() {
        let next_is_list = parts
            .get(i + 1)
            .is_some_and(|p| p.list.is_some() && p.problem.is_none());
        let mut inner = doors.inner(i, doors.values.clone(), heading.take());
        inner.commands = inner.commands.at(shown.line);
        if let (true, Some(prose)) = (next_is_list, shown.markdown.as_ref()) {
            let text = inner.values.text(&prose.text);
            if let Some((above, last)) = draw::last_heading(&text) {
                heading = Some(last);
                if !above.trim().is_empty() {
                    drawn.push(draw::prose(&above, &inner));
                }
                continue;
            }
        }
        drawn.push(part(shown, &inner));
    }
    drawn
}

/// The problems of the view that no part shows, such as a language line attn cannot read.
fn unplaced(view: &ViewDocument) -> Vec<&attn_protocol::ViewProblem> {
    let mut placed = Vec::new();
    keys::each_part(&view.parts, &mut |part| {
        placed.extend(part.problem.as_ref().map(|p| p.line))
    });
    view.problems
        .iter()
        .filter(|p| !placed.contains(&p.line))
        .collect()
}

/// The minute the views shown draw their ages at: none when no view draws an age.
pub(crate) fn ticks(contents: &attn_client::TileContents, now_ms: i64) -> Option<i64> {
    let ages = (contents.tiles.values()).any(|c| {
        c.message
            .view
            .as_ref()
            .is_some_and(|v| keys::ages(&v.parts))
    });
    ages.then_some(now_ms / 60_000)
}

/// When the ages the shown views draw next read differently: at the next minute.
pub(crate) fn next_change(contents: &attn_client::TileContents, now_ms: i64) -> Option<i64> {
    ticks(contents, now_ms).map(|minute| (minute + 1) * 60_000)
}

/// The body of the markdown tile `tile` showing the view `view`, which `content` carries.
pub(crate) fn body(
    (tile, held, title): (&str, bool, &str),
    (session, send_key): (Option<&str>, Option<&str>),
    (content, view): (&attn_client::TileContent, &ViewDocument),
    machines: crate::machine::Machines,
    (tiles, pictures): (&Tiles, &crate::markdown::Pictures),
    now_ms: i64,
    t: &Tokens,
) -> El {
    let scroll_key = crate::markdown::keys::view(tile);
    let host = content.message.host.as_deref().filter(|h| !h.is_empty());
    let away = crate::markdown::Away::of(&machines, host);
    let document = Document {
        path: content.message.path.clone(),
        session: session.map(str::to_owned),
        away: away.clone(),
        view: scroll_key.clone(),
        opens_seed: None,
        seed: None,
        part: None,
        ids: Default::default(),
        placed: Vec::new(),
    };
    let state = tiles.of(tile);
    let highlight = state.highlight.clone();
    let ticks = keys::ages(&view.parts).then_some(now_ms / 60_000);
    let place = TileKey {
        desktop: content.message.desktop_id.clone(),
        tile: tile.to_owned(),
    };
    let refusal = tiles.refusals.get(tile).cloned();
    let version = (
        (
            content.revision,
            tiles.revision,
            session.is_some(),
            send_key,
        ),
        away,
        state.clone(),
        pictures.revision,
        ticks,
        *t,
    );
    let pictures = pictures.of(host);
    let machine = machines.name(host);
    let heard = Heard {
        sounds: &tiles.sounds,
        host,
    };
    let drawn = memo(format!("{scroll_key}.view"), version, || {
        let data = Data::of(view);
        let doors = Doors {
            values: data.scope(now_ms),
            commands: Commands::of(
                &place,
                view,
                content.message.view_commands.as_deref().unwrap_or_default(),
                &tiles.entries,
            ),
            answers: Answers::of(&place, content.message.answers.as_ref(), tiles),
            tile: TileDoor {
                tile,
                state: &state,
            },
            room: Room {
                key: format!("tile.{tile}.view"),
                heading: None,
                t,
                document: &document,
                pictures: &pictures,
                heard,
            },
        };
        let problems = unplaced(view)
            .into_iter()
            .map(|p| draw::problem(&format!("tile.{tile}.view.problem.{}", p.line), p, t));
        let refused = refusal
            .as_deref()
            .map(|why| draw::notice(&format!("tile.{tile}.view.refused"), why, t));
        let sending = (keys::asks(&view.parts) && session.is_some())
            .then(|| draw::sending(&format!("tile.{tile}.view.send"), send_key, &doors));
        let waiting = start::notice(view, (&content.message.desktop_id, tile), &machine, t);
        let stopped = start::stopped(view);
        let drawn = parts(&view.parts, &doors)
            .into_iter()
            .map(|part| if stopped { part.faded() } else { part });
        col()
            .key(format!("tile.{tile}.view"))
            .role("group")
            .label(title.to_owned())
            .w_full()
            .child(
                Reader::new()
                    .push_all(waiting)
                    .push_all(problems)
                    .push_all(drawn)
                    .push_all(refused)
                    .push_all(sending)
                    .bare(t)
                    .max_w(crate::markdown::PROSE_MEASURE)
                    .self_center(),
            )
    });
    let (padding, _) = crate::markdown::card_fit(None, f32::INFINITY);
    let document = col()
        .key(scroll_key)
        .role("document")
        .label(title)
        .holds_keys(held);
    let document = match highlight {
        Some(key) => document.active_descendant(key),
        None => document,
    };
    let document = document
        .w_full()
        .grow(1.0)
        .min_h(0.0)
        .scroll(ScrollSpec {
            stick_bottom: false,
        })
        .items_center()
        .child(crate::markdown::card(padding, drawn, t))
        .scroll_bar_styled(attn_ui::document_scroll_bar(t));
    Surface::gutter(col().w_full().grow(1.0).min_h(0.0)).child(document)
}

/// The markdown a view shows, its prose and pictures with their values in place, so the
/// pictures it names load as a document's do.
pub(crate) fn shown_markdown(view: &ViewDocument) -> String {
    let data = Data::of(view);
    let mut shown = String::new();
    keys::each_text(&view.parts, &data.scope(0), &mut |text| {
        shown.push_str(&text);
        shown.push_str("\n\n");
    });
    shown
}

/// A notice that says `what` is not available yet.
pub(crate) fn not_yet(key: &str, what: &str, t: &Tokens) -> Block {
    Notice::info(key.to_owned(), format!("{what} is not available yet")).build(t)
}

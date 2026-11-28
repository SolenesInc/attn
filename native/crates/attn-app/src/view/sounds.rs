//! The clips the shown views play. A clip is read through the daemon (`tile_asset_read`), as a
//! view's pictures are, so a clip on an outpost plays as one on this machine does. Rune's player
//! opens a file, so the clip's sound is written to this app's own temporary folder, and goes
//! when no view draws the clip any more.

use std::collections::{HashMap, HashSet};
use std::hash::{Hash, Hasher};
use std::path::PathBuf;
use std::sync::atomic::{AtomicU64, Ordering};

use attn_client::TileKey;

use attn_protocol::{TileAssetReadMessage, TileAssetReadResultMessage};
use attn_ui::blocks::Playing;
use base64::Engine;
use rune::{Player, PlayerState, Tx};

use crate::State;
use crate::requests::Answer;

/// How far Shift with ← or → moves a clip, as far as a video player's arrow keys move one. The
/// clips of the transcript task this was built for run 10 to 43 s.
pub(crate) const SEEK_SECONDS: f64 = 5.0;

/// What a clip says where it cannot play, on a platform Rune has no player for.
const NO_PLAYER: Option<&str> = if cfg!(target_os = "linux") {
    Some("audio does not play on Linux yet")
} else {
    None
};

/// A clip a view draws: its file, on this machine or on outpost `host`.
#[derive(Clone, Debug, PartialEq, Eq, Hash)]
pub(crate) struct Clip {
    pub(crate) host: Option<String>,
    pub(crate) path: String,
}

enum Load {
    Asked,
    Ready(Sound),
    Failed(String),
}

/// A clip's sound, written where its player opens it. The file goes with the player.
struct Sound {
    player: Player,
    file: PathBuf,
    /// How the clip stood when the views last drew it.
    drawn: Drawn,
}

/// What a clip's player shows: whether it plays, the second it is in, its length once known, and
/// whether it failed.
type Drawn = (bool, u64, Option<u64>, bool);

impl Sound {
    fn drawn(&self) -> Drawn {
        let player = &self.player;
        (
            player.is_playing(),
            player.time().max(0.0).floor() as u64,
            player.duration().map(f64::to_bits),
            matches!(player.state(), PlayerState::Failed(_)),
        )
    }
}

impl Drop for Sound {
    fn drop(&mut self) {
        self.player.pause();
        let _ = std::fs::remove_file(&self.file);
    }
}

/// The window's clips.
#[derive(Default)]
pub(crate) struct Sounds {
    loads: HashMap<Clip, Load>,
    wake: Option<rune::Waker>,
    /// What the clips were last followed for: the tile contents, the view tiles and the desktop.
    followed: Option<u64>,
    /// Each shown view's tile, by the revision of its content and a hash of its document, as the
    /// clips were last read for it.
    documents: HashMap<TileKey, (u64, u64)>,
}

/// How many clips this process has written, so each window's clips have files of their own.
static WRITTEN: AtomicU64 = AtomicU64::new(0);

impl Sounds {
    /// Wakes the window when a clip loads, ends or fails.
    pub(crate) fn start(&mut self, wake: rune::Waker) {
        self.wake = Some(wake);
    }

    /// How `clip` stands: whether it plays, how far it has played, and its length once known.
    pub(crate) fn of(&self, clip: &Clip) -> (Playing, f64, Option<f64>) {
        if let Some(why) = NO_PLAYER {
            return (Playing::Unplayable(why.to_owned()), 0.0, None);
        }
        match self.loads.get(clip) {
            None | Some(Load::Asked) => (Playing::Loading, 0.0, None),
            Some(Load::Failed(why)) => (Playing::Unplayable(why.clone()), 0.0, None),
            Some(Load::Ready(sound)) => {
                let player = &sound.player;
                let playing = match player.state() {
                    PlayerState::Failed(why) => Playing::Unplayable(why),
                    PlayerState::Loading if !player.is_playing() => Playing::Loading,
                    _ if player.is_playing() => Playing::Playing,
                    _ => Playing::Paused,
                };
                (playing, player.time(), player.duration())
            }
        }
    }

    fn player(&self, clip: &Clip) -> Option<&Player> {
        match self.loads.get(clip)? {
            Load::Ready(sound) => Some(&sound.player),
            _ => None,
        }
    }

    fn folder(&self) -> PathBuf {
        std::env::temp_dir().join(format!("attn-sounds-{}", std::process::id()))
    }
}

/// Plays `clip` and pauses every other, or pauses it while it plays.
pub(crate) fn toggle(state: &mut State, clip: &Clip) {
    let sounds = &state.views.sounds;
    let Some(player) = sounds.player(clip) else {
        return;
    };
    if player.is_playing() {
        player.pause();
    } else {
        for (other, load) in &sounds.loads {
            if let (true, Load::Ready(sound)) = (other != clip, load) {
                sound.player.pause();
            }
        }
        player.play();
    }
    state.views.revision += 1;
}

/// Moves `clip` `by` seconds on, or back when `by` is below zero, within the clip.
pub(crate) fn seek(state: &mut State, clip: &Clip, by: f64) {
    let Some(player) = state.views.sounds.player(clip) else {
        return;
    };
    let end = player.duration().unwrap_or(f64::INFINITY);
    player.seek((player.time() + by).clamp(0.0, end));
    state.views.revision += 1;
}

/// Reads each clip the shown views draw that the window has not read, and forgets the clips no
/// view draws any more. While a clip plays, wakes the window when it plays into its next second.
/// Returns whether a clip changed how it draws.
pub(crate) fn follow(state: &mut State, tx: &mut Tx) -> bool {
    let followed = hashed(&(
        state.facts.tile_contents.revision,
        state.views.revision,
        state.facts.current_desktop().map(|d| &d.id),
    ));
    if NO_PLAYER.is_none() && state.views.sounds.followed != Some(followed) {
        state.views.sounds.followed = Some(followed);
        let shown = super::drawn_clips(state);
        let sounds = &mut state.views.sounds;
        let mut wanted = HashSet::new();
        let mut documents = HashMap::new();
        for (place, (revision, document), clips) in shown {
            let before = sounds.documents.get(&place).copied();
            for clip in &clips {
                let stale = match (before, sounds.loads.get(clip)) {
                    (Some((_, read)), Some(_)) if read != document => true,
                    (Some((seen, _)), Some(Load::Failed(_))) => seen != revision,
                    _ => false,
                };
                if stale {
                    sounds.loads.remove(clip);
                }
            }
            documents.insert(place, (revision, document));
            wanted.extend(clips);
        }
        sounds.documents = documents;
        sounds.loads.retain(|clip, _| wanted.contains(clip));
        for clip in wanted {
            if !state.views.sounds.loads.contains_key(&clip) {
                read(state, clip, tx);
            }
        }
    }
    let mut changed = false;
    let mut next: Option<f64> = None;
    for load in state.views.sounds.loads.values_mut() {
        let Load::Ready(sound) = load else { continue };
        let now = sound.drawn();
        if now != sound.drawn {
            sound.drawn = now;
            changed = true;
        }
        if now.0 {
            let time = sound.player.time();
            let rate = f64::from(sound.player.rate()).max(f64::MIN_POSITIVE);
            let due = (time.floor() + 1.0 - time) / rate;
            next = Some(next.map_or(due, |n| n.min(due)));
        }
    }
    if let Some(due) = next {
        tx.wake_at(tx.clock() + due);
    }
    if changed {
        state.views.revision += 1;
    }
    changed
}

fn read(state: &mut State, clip: Clip, tx: &mut Tx) {
    state.views.sounds.loads.insert(clip.clone(), Load::Asked);
    let (host, path) = (clip.host.clone(), clip.path.clone());
    state.ask(
        |id| TileAssetReadMessage {
            host,
            path,
            request_id: Some(id),
            ..TileAssetReadMessage::new()
        },
        move |state, answer: Answer, _| {
            let bytes = answer.outcome().and_then(|reply| {
                let result = reply
                    .parse::<TileAssetReadResultMessage>()
                    .ok_or("the daemon's answer could not be read")?;
                let uri = result.data_uri.ok_or("the daemon sent no sound")?;
                decoded(&uri).ok_or_else(|| "the daemon's sound could not be read".to_owned())
            });
            arrived(state, clip, bytes);
        },
        tx,
    );
}

fn arrived(state: &mut State, clip: Clip, bytes: Result<Vec<u8>, String>) {
    let sounds = &mut state.views.sounds;
    if !matches!(sounds.loads.get(&clip), Some(Load::Asked)) {
        return;
    }
    let load = bytes
        .and_then(|bytes| written(sounds, &clip, &bytes))
        .map_or_else(Load::Failed, |file| {
            let player = Player::audio(file.as_path());
            if let Some(wake) = &sounds.wake {
                player.set_waker(wake.clone());
            }
            Load::Ready(Sound {
                player,
                file,
                drawn: (false, 0, None, false),
            })
        });
    sounds.loads.insert(clip, load);
    state.views.revision += 1;
}

/// Writes `bytes`, the sound of `clip`, where its player opens it, named for the clip's own kind.
fn written(sounds: &Sounds, clip: &Clip, bytes: &[u8]) -> Result<PathBuf, String> {
    let folder = sounds.folder();
    std::fs::create_dir_all(&folder).map_err(|e| e.to_string())?;
    let n = WRITTEN.fetch_add(1, Ordering::Relaxed) + 1;
    let kind = std::path::Path::new(&clip.path)
        .extension()
        .and_then(|e| e.to_str())
        .unwrap_or("wav");
    let file = folder.join(format!("{n}.{kind}"));
    std::fs::write(&file, bytes).map_err(|e| e.to_string())?;
    Ok(file)
}

fn hashed(value: &impl Hash) -> u64 {
    let mut hasher = std::collections::hash_map::DefaultHasher::new();
    value.hash(&mut hasher);
    hasher.finish()
}

/// A hash of what a view tile shows: the document, its machine and its text. Its clips are read
/// again when it changes.
pub(super) fn document(path: &str, host: Option<&str>, content: &str) -> u64 {
    hashed(&(path, host, content))
}

fn decoded(data_uri: &str) -> Option<Vec<u8>> {
    let (_, data) = data_uri.split_once(";base64,")?;
    base64::engine::general_purpose::STANDARD.decode(data).ok()
}

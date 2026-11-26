//! The profile switcher under the title bar's profile pill: the live profiles, most recently
//! used first, to switch to, name, rename or delete. The daemon refuses a name another live
//! profile holds and a profile that is not empty; the switcher shows its refusal in place.

use attn_protocol::{
    Profile, ProfileActionResultMessage, ProfileCreateMessage, ProfileDeleteMessage,
    ProfileRenameMessage, ProfileSelectMessage,
};
use attn_ui::Tokens;
use attn_ui::blocks::{Actions, Block, Field, Menu, Notice, Prose};
use attn_ui::system::{
    Button, FieldState, IconSize, ListRow, Look, Reveal, Size, Trigger, TriggerLook, Type, glyph,
    icon_button, keycaps, line_box, words,
};
use rune::{El, Key, KeyEvent, TextEdit, Tx};
use rune_kit::{Placement, TextInput, popover};

use crate::requests::{Answer, Refused};
use crate::shell::Layer;
use crate::{Action, State};

pub(crate) const MENU: &str = "profile-switcher";
pub(crate) const PILL: &str = "titlebar.profile";
const PILL_SLOT: &str = "titlebar.profile.slot";
const NAME_FIELD: &str = "profile-switcher.name";
const CHOOSE: &str = "profile-switcher.choose.";

/// The switcher's highlighted profile, what it is doing and what the daemon last refused. They
/// start over each time it opens.
#[derive(Default)]
pub(crate) struct Switcher {
    highlighted: Option<String>,
    mode: Mode,
    refusal: Option<String>,
    busy: bool,
}

#[derive(Default)]
enum Mode {
    #[default]
    List,
    /// Naming a new profile (`profile` none) or renaming one.
    Name {
        profile: Option<String>,
        draft: Box<TextEdit>,
    },
    Delete {
        profile: String,
    },
}

fn is_open(state: &State) -> bool {
    state.window.layer == Some(Layer::ProfileSwitcher)
}

/// Opens the switcher on the current profile, or closes it.
pub(crate) fn toggle(state: &mut State, tx: &mut Tx) {
    if is_open(state) {
        close(state, tx);
        return;
    }
    state.window.profile_switcher = Switcher {
        highlighted: Some(state.facts.profiles.selected.clone()),
        ..Switcher::default()
    };
    state.window.layer = Some(Layer::ProfileSwitcher);
    tx.focus(MENU, false);
}

fn close(state: &mut State, tx: &mut Tx) {
    state.window.profile_switcher = Switcher::default();
    state.window.layer = None;
    crate::picker::refocus_inline(state, tx);
}

/// Escape backs out of naming or deleting, and closes the list; a press outside closes it.
fn dismiss(state: &mut State, tx: &mut Tx) {
    if tx.by_keys() && !matches!(state.window.profile_switcher.mode, Mode::List) {
        back_to_list(state, tx);
    } else {
        close(state, tx);
    }
}

fn back_to_list(state: &mut State, tx: &mut Tx) {
    let switcher = &mut state.window.profile_switcher;
    switcher.mode = Mode::List;
    switcher.refusal = None;
    tx.focus(MENU, false);
}

fn enter(state: &mut State, mode: Mode, tx: &mut Tx) {
    let naming = matches!(mode, Mode::Name { .. });
    let switcher = &mut state.window.profile_switcher;
    switcher.mode = mode;
    switcher.refusal = None;
    tx.focus(if naming { NAME_FIELD } else { MENU }, false);
}

/// The live profiles, most recently used first.
fn ordered(state: &State) -> Vec<&Profile> {
    let mut profiles: Vec<&Profile> = state.facts.profiles.all.iter().collect();
    profiles.sort_by(|a, b| {
        let used = |p: &Profile| p.last_used_at.clone().unwrap_or_default();
        used(b).cmp(&used(a))
    });
    profiles
}

/// The highlighted profile, or the first one once the highlighted one is gone.
fn highlighted(state: &State) -> Option<&Profile> {
    let profiles = ordered(state);
    let wanted = state.window.profile_switcher.highlighted.as_deref();
    profiles
        .iter()
        .find(|p| Some(p.id.as_str()) == wanted)
        .or(profiles.first())
        .copied()
}

fn name_of(state: &State, profile: &str) -> Option<String> {
    state
        .facts
        .profiles
        .all
        .iter()
        .find(|p| p.id == profile)
        .map(|p| p.name.clone())
}

fn step(state: &mut State, by: i64) {
    let profiles = ordered(state);
    let Some(at) = highlighted(state).and_then(|h| profiles.iter().position(|p| p.id == h.id))
    else {
        return;
    };
    let next = (at as i64 + by).rem_euclid(profiles.len() as i64) as usize;
    let id = profiles[next].id.clone();
    state.window.profile_switcher.highlighted = Some(id);
}

/// Shows `profile` in the window. Choosing the profile it shows changes nothing.
pub(crate) fn select(state: &mut State, profile: &str, tx: &mut Tx) {
    if profile == state.facts.profiles.selected {
        return;
    }
    let profile = profile.to_owned();
    state.act(
        |id| ProfileSelectMessage {
            request_id: id,
            profile_id: profile,
            ..ProfileSelectMessage::new()
        },
        Refused::Bare,
        |_, _, _| {},
        tx,
    );
}

fn choose(state: &mut State, profile: &str, tx: &mut Tx) {
    close(state, tx);
    state.window.show_desktop(tx);
    select(state, profile, tx);
}

fn start_new(state: &mut State, tx: &mut Tx) {
    let draft = Box::default();
    enter(
        state,
        Mode::Name {
            profile: None,
            draft,
        },
        tx,
    );
}

fn start_rename(state: &mut State, profile: &str, tx: &mut Tx) {
    let Some(name) = name_of(state, profile) else {
        return;
    };
    state.window.profile_switcher.highlighted = Some(profile.to_owned());
    let mode = Mode::Name {
        profile: Some(profile.to_owned()),
        draft: Box::new(TextEdit::new(name)),
    };
    enter(state, mode, tx);
}

fn start_delete(state: &mut State, profile: &str, tx: &mut Tx) {
    let Some(name) = name_of(state, profile) else {
        return;
    };
    state.window.profile_switcher.highlighted = Some(profile.to_owned());
    if state.facts.profiles.all.len() == 1 {
        state.window.profile_switcher.refusal = Some(format!(
            "{name} is the last profile, and attn always keeps one."
        ));
        return;
    }
    let mode = Mode::Delete {
        profile: profile.to_owned(),
    };
    enter(state, mode, tx);
}

/// What the daemon said when it did not do what was asked.
fn refused(state: &mut State, answer: Answer) -> Option<ProfileActionResultMessage> {
    let switcher = &mut state.window.profile_switcher;
    switcher.busy = false;
    let result = answer.parse::<ProfileActionResultMessage>();
    if let Err(reason) = answer.outcome() {
        switcher.refusal = Some(reason);
        return None;
    }
    result
}

fn running(state: &mut State) {
    let switcher = &mut state.window.profile_switcher;
    switcher.busy = true;
    switcher.refusal = None;
}

fn submit_name(state: &mut State, tx: &mut Tx) {
    let switcher = &state.window.profile_switcher;
    let Mode::Name { profile, draft } = &switcher.mode else {
        return;
    };
    if switcher.busy || draft.text.trim().is_empty() {
        return;
    }
    let name = draft.text.clone();
    match profile.clone() {
        None => create(state, name, tx),
        Some(profile) => rename(state, profile, name, tx),
    }
}

/// Creates the profile, then switches to it and closes.
fn create(state: &mut State, name: String, tx: &mut Tx) {
    running(state);
    state.ask(
        |id| ProfileCreateMessage {
            request_id: id,
            name,
            ..ProfileCreateMessage::new()
        },
        |state, answer, tx| {
            let Some(created) = refused(state, answer).and_then(|r| r.profile) else {
                return;
            };
            running(state);
            state.ask(
                |id| ProfileSelectMessage {
                    request_id: id,
                    profile_id: created.id,
                    ..ProfileSelectMessage::new()
                },
                |state, answer, tx| {
                    if refused(state, answer).is_some() && is_open(state) {
                        close(state, tx);
                        state.window.show_desktop(tx);
                    }
                },
                tx,
            );
        },
        tx,
    );
}

fn rename(state: &mut State, profile: String, name: String, tx: &mut Tx) {
    let Some(revision) = revision_of(state, &profile) else {
        return;
    };
    running(state);
    state.ask(
        |id| ProfileRenameMessage {
            request_id: id,
            profile_id: profile,
            name,
            expected_revision: revision,
            ..ProfileRenameMessage::new()
        },
        |state, answer, tx| {
            if refused(state, answer).is_some() && is_open(state) {
                back_to_list(state, tx);
            }
        },
        tx,
    );
}

fn delete(state: &mut State, tx: &mut Tx) {
    let switcher = &state.window.profile_switcher;
    let Mode::Delete { profile } = &switcher.mode else {
        return;
    };
    if switcher.busy {
        return;
    }
    let profile = profile.clone();
    let Some(revision) = revision_of(state, &profile) else {
        return;
    };
    running(state);
    state.ask(
        |id| ProfileDeleteMessage {
            request_id: id,
            profile_id: profile,
            expected_revision: revision,
            ..ProfileDeleteMessage::new()
        },
        |state, answer, tx| {
            if refused(state, answer).is_some() && is_open(state) {
                back_to_list(state, tx);
            }
        },
        tx,
    );
}

fn revision_of(state: &State, profile: &str) -> Option<i64> {
    state
        .facts
        .profiles
        .all
        .iter()
        .find(|p| p.id == profile)
        .map(|p| p.revision)
}

/// ↑ and ↓ move, Return on the list switches to the highlighted profile, N names a new
/// profile, R renames, ⌫ asks to delete, and Return while asking deletes. Return on a row Tab
/// reached switches to that row's profile.
fn key(state: &mut State, ev: &KeyEvent, tx: &mut Tx) -> bool {
    if ev.mods.cmd || ev.mods.ctrl || ev.mods.alt {
        return false;
    }
    let on_menu = tx.focused() == Some(MENU);
    match state.window.profile_switcher.mode {
        Mode::List => {}
        Mode::Delete { .. } if ev.key == Key::Enter && on_menu => {
            delete(state, tx);
            return true;
        }
        _ => return false,
    }
    if ev.key == Key::Enter
        && let Some(row) = tx.focused().and_then(|f| f.strip_prefix(CHOOSE))
    {
        let row = row.to_owned();
        choose(state, &row, tx);
        return true;
    }
    let Some(focused) = highlighted(state).map(|p| p.id.clone()) else {
        return false;
    };
    match &ev.key {
        Key::Down => step(state, 1),
        Key::Up => step(state, -1),
        Key::Enter if on_menu => choose(state, &focused, tx),
        Key::Backspace | Key::Delete => start_delete(state, &focused, tx),
        Key::Text(t) if t.eq_ignore_ascii_case("n") => start_new(state, tx),
        Key::Text(t) if t.eq_ignore_ascii_case("r") => start_rename(state, &focused, tx),
        _ => return false,
    }
    true
}

/// The pill that names the profile the window shows and opens the switcher.
pub(crate) fn pill(state: &State, t: &Tokens) -> El {
    let facts = &state.facts;
    let name = name_of(state, &facts.profiles.selected).unwrap_or_else(|| "Profile".into());
    let keys = state
        .keymap
        .binding(&Action::ProfileSwitch)
        .map(|b| state.keymap.label(b));
    let help = match keys {
        Some(keys) => format!("Switch profile  {keys}"),
        None => "Switch profile".to_owned(),
    };
    let pill = Trigger::new(
        PILL,
        name.clone(),
        words(name.clone(), Type::Control, t.text_primary)
            .single_line()
            .truncate()
            .shrink(),
    )
    .size(Size::Compact)
    .look(TriggerLook::Quiet)
    .open(is_open(state))
    .build(t, toggle);
    let pill = El::from(pill)
        .role("button")
        .description(help.clone())
        .help(help)
        .max_w(
            if crate::sidebar::crowding::given_up(state)
                >= crate::sidebar::crowding::GivenUp::ProfileName
            {
                crate::sidebar::crowding::PROFILE_SHORT_W
            } else {
                PILL_MAX_W
            },
        );
    crate::sidebar::crowding::part(
        crate::shell::title_bar_slot(PILL_SLOT, pill),
        crate::sidebar::crowding::Part::Profile,
    )
}

/// The pill's widest: a profile's name truncates past it, leaving the title bar its room.
const PILL_MAX_W: f32 = 140.0;

/// The switcher under the pill, while it is open.
pub(crate) fn view(state: &State, t: &Tokens) -> Option<El> {
    if !is_open(state) {
        return None;
    }
    let k = attn_ui::kit(t);
    let switcher = &state.window.profile_switcher;
    let mut menu = Menu::new(MENU, "Switch profile");
    if let (Mode::List, Some(profile)) = (&switcher.mode, highlighted(state)) {
        menu = menu.active(Some(format!("{CHOOSE}{}", profile.id)));
    }
    let refusal = switcher
        .refusal
        .clone()
        .map(|said| Notice::error("profile-switcher.refusal", said).build(t));
    menu = match &switcher.mode {
        Mode::List => menu.rows(list(state, t)).form(refusal).footer(
            Actions::new("profile-switcher.list-actions").push(
                Button::new("New profile")
                    .look(Look::Quiet)
                    .trailing(keycaps(["N"], t))
                    .on_press_tx(start_new)
                    .build(t)
                    .key("profile-switcher.new"),
            ),
        ),
        Mode::Name { profile, draft } => {
            let renaming = profile.as_deref().and_then(|p| name_of(state, p));
            let (form, actions) = name_form(renaming, draft, switcher.busy, t);
            menu.form(std::iter::once(form).chain(refusal))
                .footer(actions)
        }
        Mode::Delete { profile } => {
            let name = name_of(state, profile).unwrap_or_default();
            let (form, actions) = delete_form(&name, switcher.busy, t);
            menu.form(std::iter::once(form).chain(refusal))
                .footer(actions)
        }
    };
    Some(popover::anchored(
        k,
        menu.build(t).on_key(key).modal_tx(true, dismiss),
        PILL_SLOT,
        Placement::BottomEnd,
        4.0,
    ))
}

fn list(state: &State, t: &Tokens) -> Vec<El> {
    let highlighted = highlighted(state).map(|p| p.id.clone());
    ordered(state)
        .into_iter()
        .map(|profile| {
            let current = profile.id == state.facts.profiles.selected;
            let lit = Some(&profile.id) == highlighted.as_ref();
            profile_row(profile, current, lit, t)
        })
        .collect()
}

fn profile_row(profile: &Profile, current: bool, lit: bool, t: &Tokens) -> El {
    let id = profile.id.clone();
    let name = profile.name.clone();
    let rename = icon_button(
        format!("profile-switcher.rename.{id}"),
        format!("Rename {name}"),
        &attn_ui::icon::PENCIL,
        IconSize::Regular,
    )
    .help("Rename  R")
    .on_press_tx({
        let id = id.clone();
        move |state: &mut State, tx: &mut Tx| start_rename(state, &id, tx)
    })
    .build(t);
    let delete = icon_button(
        format!("profile-switcher.delete.{id}"),
        format!("Delete {name}"),
        &attn_ui::icon::TRASH,
        IconSize::Regular,
    )
    .help("Delete  ⌫")
    .on_press_tx({
        let id = id.clone();
        move |state: &mut State, tx: &mut Tx| start_delete(state, &id, tx)
    })
    .build(t);
    ListRow::new(format!("profile-switcher.row.{id}"), name.clone())
        .lead(if current {
            glyph("✓", IconSize::Small, t.brand)
        } else {
            glyph(" ", IconSize::Small, t.brand)
        })
        .title(name.clone())
        .selected(lit)
        .value(if current { "current" } else { "" })
        .controls([El::from(rename), El::from(delete)], Reveal::Always)
        .select_role("menuitem")
        .select(format!("{CHOOSE}{id}"), name, {
            let id = id.clone();
            move |state: &mut State, tx: &mut Tx| choose(state, &id, tx)
        })
        .build(t)
}

fn back(t: &Tokens) -> El {
    Button::new("Back")
        .look(Look::Quiet)
        .trailing(keycaps(["esc"], t))
        .on_press_tx(back_to_list)
        .build(t)
        .key("profile-switcher.back")
        .into()
}

fn name_form(
    renaming: Option<String>,
    draft: &TextEdit,
    busy: bool,
    t: &Tokens,
) -> (Block, Actions) {
    let label = match &renaming {
        None => "New profile name".to_owned(),
        Some(name) => format!("Rename {name}"),
    };
    let submit = if renaming.is_none() {
        "Create and switch"
    } else {
        "Rename"
    };
    let field = TextInput::new(draft)
        .key(NAME_FIELD)
        .label(label.clone())
        .on_edit(
            |state: &mut State| match &mut state.window.profile_switcher.mode {
                Mode::Name { draft, .. } => &mut **draft,
                _ => unreachable!("the name field shows only while naming"),
            },
        )
        .on_submit(submit_name);
    let form = Field::new(NAME_FIELD, label)
        .control(line_box(field, FieldState::disabled_if(busy), t))
        .build(t);
    let actions = Actions::new("profile-switcher.form-actions")
        .push(
            Button::new(submit)
                .look(Look::Primary)
                .trailing(keycaps(["⏎"], t))
                .disabled(busy || draft.text.trim().is_empty())
                .on_press_tx(submit_name)
                .build(t)
                .key("profile-switcher.submit"),
        )
        .push(back(t));
    (form, actions)
}

fn delete_form(name: &str, busy: bool, t: &Tokens) -> (Block, Actions) {
    let ask = format!("Delete {name}? Clean up its agents, crew, automations and tiles first.");
    let form = Prose::lead("profile-switcher.delete.ask", ask).build(t);
    let actions = Actions::new("profile-switcher.delete-actions")
        .push(
            Button::new(format!("Delete {name}"))
                .look(Look::Danger)
                .trailing(keycaps(["⏎"], t))
                .disabled(busy)
                .on_press_tx(delete)
                .build(t)
                .key("profile-switcher.confirm"),
        )
        .push(back(t));
    (form, actions)
}

use attn_ui::finder::{self, Hint};
use attn_ui::system::{IconSize, ListRow, Part, Said, chip_edge, icon};
use attn_ui::{Tokens, icon};
use rune::{El, Tx, col};
use rune_kit::TextInput;

use super::{Row, Scope};
use crate::State;
use crate::launcher::{age, age_column, machine_tag, quiet};

const LABEL: &str = "Open a markdown file";

/// The opener over the window: its field, with the machine it lists as a chip, and the rows its
/// query lists, the highlighted one marked.
pub(crate) fn view(state: &State, t: &Tokens) -> El {
    let (rows, empty) = super::rows(state);
    let at = super::highlighted(&state.opener, &rows);
    let outposts = crate::launcher::outposts(state);
    let field = finder::field(
        TextInput::new(&state.opener.query)
            .key(super::FIELD)
            .plain()
            .width(None)
            .label(LABEL)
            .placeholder("Open a markdown file…")
            .active_descendant(at.map(|i| row_key(&rows[i])))
            .on_edit(|state: &mut State| &mut state.opener.query)
            .on_change(super::edited)
            .on_key(super::on_key),
    )
    .build(attn_ui::kit(t));
    let scope = match &state.opener.scope {
        Scope::Every => None,
        Scope::Machine(host) => Some(chip_edge(
            machine_tag(
                SCOPE.to_owned(),
                &crate::launcher::offered(state, outposts),
                host.as_deref(),
                t,
            ),
            t,
        )),
    };
    let tagged =
        state.opener.scope == Scope::Every && crate::launcher::offered(state, outposts).several();
    let list = finder::list(
        LIST,
        rows.iter()
            .enumerate()
            .map(|(i, r)| option(state, r, at == Some(i), tagged, t))
            .chain(
                rows.is_empty()
                    .then(|| finder::empty(t, "opener.empty", empty.clone())),
            )
            .collect(),
    )
    .label(LABEL);
    let back = state.opener.scope != Scope::Every && state.opener.query.text.is_empty();
    let footer = (!outposts.is_empty()).then(|| {
        let mut hints = vec![Hint::keys("", "Tab", " machine")];
        if back {
            hints.push(Hint::keys("", "⌫", " every machine"));
        }
        finder::footer(t, "opener", hints)
    });
    let panel = col()
        .key("opener.dialog")
        .role("dialog")
        .label(LABEL)
        .modal_tx(true, super::close)
        .max_h(finder::FinderSize::Narrow.max_height())
        .shrink()
        .min_h(0.0);
    attn_ui::finder::layer(
        t,
        "opener",
        finder::panel(
            t,
            panel,
            finder::input_row(scope, field, None),
            list,
            footer,
        ),
        finder::FinderSize::Narrow,
    )
}

pub(super) const LIST: &str = "opener.list";
const SCOPE: &str = "opener.scope";

pub(super) fn row_key(r: &Row) -> String {
    format!("opener.row.{}", r.key)
}

fn option(state: &State, r: &Row, highlighted: bool, tagged: bool, t: &Tokens) -> El {
    let outposts = crate::launcher::outposts(state);
    let key = row_key(r);
    let on = crate::launcher::offered(state, outposts)
        .away(r.host.as_deref())
        .map(|name| format!(" on {name}"))
        .unwrap_or_default();
    let offline = crate::outpost::reach(r.host.as_deref(), outposts).offline();
    let mut line = ListRow::new(key.clone(), format!("{} {}{on}", r.title, r.detail))
        .role("option")
        .unfocusable()
        .cursor(highlighted)
        .selected(highlighted)
        .title(r.title.clone())
        .subtitle(Said::parts([Part::Code(r.detail.clone())]), t)
        .dimmed(offline);
    if tagged {
        line = line.aside(machine_tag(
            format!("{key}.machine"),
            &crate::launcher::offered(state, outposts),
            r.host.as_deref(),
            t,
        ));
    }
    if offline {
        line = line.aside(quiet("offline".to_owned(), t).key(format!("{key}.offline")));
    }
    if let Some(at) = &r.at {
        let (mark, verb) = if r.edited {
            (&icon::PENCIL, "edited")
        } else {
            (&icon::OPENED, "opened")
        };
        let ago = age(at, state.window.now_ms);
        line = line.aside(
            attn_ui::blocks::Inline::joined()
                .key(format!("{key}.touched"))
                .label(format!("{verb} {ago}"))
                .child(icon(mark, IconSize::Small, t.text_tertiary))
                .child(age_column(ago, t)),
        );
    }
    let picked = r.key.clone();
    line.build(t)
        .label(format!("{} {}{on}", r.title, r.detail))
        .on_press_tx(move |state: &mut State, tx: &mut Tx| super::pick_key(state, &picked, tx))
}

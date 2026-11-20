//! What a block's parts belong to: a control under a list row lies inside that row's item, and
//! an opened part shows its control and the start of what it holds.

use attn_ui::blocks::{Block, Disclosure, List};
use attn_ui::system::{Button, ListRow, Type, words};
use rune::{App, AppSpec, Cx, El, Env, Key, KeyEvent, Mods, Runtime, Tx, col};

const SCALE: f32 = 2.0;
const VIEW: (f32, f32) = (600.0, 400.0);

fn tokens<S: 'static>(cx: &mut Cx<S>) -> attn_ui::Tokens {
    attn_ui::colors::scheme_for(None)
        .colors(cx.system_dark())
        .ui
}

fn remove(key: &str, t: &attn_ui::Tokens) -> Block {
    Block::custom(
        format!("{key}.block"),
        Button::new("Remove")
            .on_press_tx(|_: &mut (), _: &mut Tx| {})
            .build(t)
            .key(key.to_owned())
            .into(),
    )
}

fn list_view(_: &(), cx: &mut Cx<()>) -> El {
    let t = tokens(cx);
    let still = ListRow::new("row.alpha", "Alpha").title("Alpha").still();
    let pressed = ListRow::new("row.beta", "Beta")
        .title("Beta")
        .on_press_tx(|_: &mut (), _: &mut Tx| {});
    let list = List::new("rows", "Rows")
        .row_with(still, vec![remove("alpha.remove", &t)])
        .row_with(pressed, vec![remove("beta.remove", &t)])
        .build(&t);
    col()
        .w_full()
        .child(attn_ui::blocks::Page::new().push(list).build("page", &t))
}

fn settled<S: 'static>(mut rt: Runtime<S>) -> Runtime<S> {
    while rt.frame(1.0 / 60.0) {}
    rt
}

#[test]
fn every_control_under_a_list_row_lies_inside_that_rows_item() {
    let rt = settled(
        App::new(AppSpec {
            fonts: attn_ui::FONTS,
            families: attn_ui::FAMILIES,
            ..AppSpec::new("ownership", VIEW, || (), list_view)
        })
        .headless_runtime(VIEW, Env::new(true, SCALE)),
    );
    let all = rt.elements();
    for (control, row) in [("alpha.remove", "Alpha"), ("beta.remove", "Beta")] {
        let at = all
            .iter()
            .position(|e| e.key.as_deref() == Some(control))
            .unwrap_or_else(|| panic!("no {control}"));
        let mut parent = all[at].parent;
        let mut item = None;
        while let Some(p) = parent {
            if all[p].role == "listitem" {
                item = Some(&all[p]);
                break;
            }
            parent = all[p].parent;
        }
        let item = item.unwrap_or_else(|| panic!("{control} lies in no list item"));
        assert_eq!(
            item.label.as_deref(),
            Some(row),
            "{control} lies in the item named {:?}, not its row's",
            item.label
        );
    }
}

#[derive(Default)]
struct Opening {
    open: bool,
}

const LINES: usize = 300;

fn opening_view(state: &Opening, cx: &mut Cx<Opening>) -> El {
    let t = tokens(cx);
    let lines: Vec<String> = (1..=LINES)
        .map(|n| format!("line {n} of what the part holds"))
        .collect();
    let part = Disclosure::new(
        "part",
        "Diagnostic output",
        state.open,
        |s: &mut Opening, _: &mut Tx| {
            s.open = !s.open;
        },
    )
    .region("Diagnostic output")
    .push(Block::custom(
        "held.block",
        col().children(
            lines
                .into_iter()
                .enumerate()
                .map(|(i, line)| words(line, Type::Body, t.text_primary).key(format!("held.{i}"))),
        ),
    ))
    .build(&t);
    attn_ui::blocks::Page::new()
        .push(Block::custom("above", col().h(VIEW.1 - 60.0)))
        .push(part)
        .build("view", &t)
}

fn opened() -> Runtime<Opening> {
    let mut rt = App::new(AppSpec {
        fonts: attn_ui::FONTS,
        families: attn_ui::FAMILIES,
        ..AppSpec::new("opening", VIEW, Opening::default, opening_view)
    })
    .headless_runtime(VIEW, Env::new(true, SCALE));
    rt.frame(1.0);
    rt.set_full_keyboard_access(true);
    for _ in 0..10 {
        if rt
            .elements()
            .iter()
            .any(|e| e.focused && e.key.as_deref() == Some("part"))
        {
            break;
        }
        rt.key(KeyEvent {
            key: Key::Tab,
            mods: Mods::default(),
        });
        rt.frame(1.0);
    }
    assert!(
        rt.elements()
            .iter()
            .any(|e| e.focused && e.key.as_deref() == Some("part")),
        "Tab reaches the part's control"
    );
    rt.key(KeyEvent {
        key: Key::Text(" ".into()),
        mods: Mods::default(),
    });
    settled(rt)
}

#[test]
fn opening_a_part_taller_than_the_view_shows_its_control_ring_and_all_and_where_it_starts() {
    let rt = opened();
    let all = rt.elements();
    let rect = |key: &str| {
        all.iter()
            .find(|e| e.key.as_deref() == Some(key))
            .unwrap_or_else(|| panic!("no {key}"))
            .rect
    };
    let (view, control, first) = (rect("view"), rect("part"), rect("held.0"));
    let held = rect("held.block");
    assert!(
        held[3] > 10.0 * view[3],
        "what the part holds stands ten views tall"
    );
    let ring = 4.0;
    assert!(
        control[1] - ring >= view[1] - 0.5
            && control[1] + control[3] + ring <= view[1] + view[3] + 0.5,
        "the control at {control:?}, ring and all, lies in the view {view:?}"
    );
    assert!(
        first[1] >= control[1] + control[3] && first[1] + first[3] <= view[1] + view[3],
        "the first line at {first:?} shows under the control in the view {view:?}"
    );
}

#[test]
fn an_opened_part_with_a_region_name_is_a_landmark_holding_what_it_holds() {
    let rt = opened();
    let all = rt.elements();
    let region = all
        .iter()
        .position(|e| e.role == "region" && e.label.as_deref() == Some("Diagnostic output"))
        .expect("a region named Diagnostic output");
    let first = all
        .iter()
        .position(|e| e.key.as_deref() == Some("held.0"))
        .expect("the first line");
    let mut parent = all[first].parent;
    while let Some(p) = parent {
        if p == region {
            return;
        }
        parent = all[p].parent;
    }
    panic!("the first line lies outside the region");
}

fn beside_view(_: &(), cx: &mut Cx<()>) -> El {
    use attn_ui::blocks::{Inspector, inspector};
    let t = tokens(cx);
    let list = List::new("list", "Rows")
        .rows((0..3).map(|i| {
            ListRow::new(format!("row{i}"), format!("Row {i}"))
                .still()
                .title(format!("Row {i}"))
                .build(&t)
        }))
        .build(&t);
    let held = (1..=LINES).fold(Inspector::side("inspector", "Selected"), |column, n| {
        column.push(words(
            format!("line {n} of what the row holds"),
            Type::Body,
            t.text_primary,
        ))
    });
    let column = held
        .push(El::from(Button::new("Send").build(&t)).key("send"))
        .in_page(&t);
    attn_ui::blocks::Page::new()
        .push(inspector::beside_list(list, column))
        .build("view", &t)
}

#[test]
fn an_inspector_in_a_page_scrolls_with_the_page_and_tab_shows_its_last_control_whole() {
    let mut rt = App::new(AppSpec {
        fonts: attn_ui::FONTS,
        families: attn_ui::FAMILIES,
        ..AppSpec::new("beside", VIEW, || (), beside_view)
    })
    .headless_runtime(VIEW, Env::new(true, SCALE));
    rt.frame(1.0);
    rt.set_full_keyboard_access(true);
    for _ in 0..20 {
        if rt
            .elements()
            .iter()
            .any(|e| e.focused && e.key.as_deref() == Some("send"))
        {
            break;
        }
        rt.key(KeyEvent {
            key: Key::Tab,
            mods: Mods::default(),
        });
        rt.frame(1.0);
    }
    let rt = settled(rt);
    let all = rt.elements();
    let rect = |key: &str| {
        all.iter()
            .find(|e| e.key.as_deref() == Some(key))
            .unwrap_or_else(|| panic!("no {key}"))
            .rect
    };
    assert!(
        all.iter()
            .any(|e| e.focused && e.key.as_deref() == Some("send")),
        "Tab reaches the inspector's last control"
    );
    let (view, column, send) = (rect("view"), rect("inspector"), rect("send"));
    assert!(
        column[3] > 10.0 * view[3],
        "the inspector stands at its full height, {} against a view of {}, scrolling with the page",
        column[3],
        view[3]
    );
    let ring = 4.0;
    assert!(
        send[1] - ring >= view[1] - 0.5 && send[1] + send[3] + ring <= view[1] + view[3] + 0.5,
        "the focused control at {send:?}, ring and all, lies in the view {view:?}"
    );
}

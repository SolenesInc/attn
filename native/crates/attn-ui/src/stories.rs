use rune::{Cx, El, Playground, Story, TextEdit, col, el};
use rune_kit::{
    Button, ButtonVariant, Dialog, Field, Kit, List, ListItem, Menu, MenuItem, Popover, TextField,
    Toast, ToastRegion, Tooltip,
};

use crate::{kit, tokens};

/// What a component story shows: one component, open where it opens, in the stage's
/// appearance, which the playground's own switch sets.
#[derive(Default)]
pub struct Showcase {
    pub field: TextEdit,
    pub dismissed: bool,
}

type View = fn(&Showcase, &mut Cx<Showcase>) -> El;

/// Every shared component, each in attn's colors, by the name its playground story has.
pub const STORIES: &[(&str, View)] = &[
    ("Components/List row", list_row),
    ("Components/Button", button),
    ("Components/Field", field),
    ("Components/Menu", menu),
    ("Components/Dialog", dialog),
    ("Components/Popover", popover),
    ("Components/Toast", toast),
    ("Components/Tooltip", tooltip),
];

/// The app's playground with a story for each shared component. It has no shortcut and no
/// bar, so no key of attn's keymap opens it; the View menu and `--playground` do.
pub fn playground<S: 'static>() -> Playground<S> {
    STORIES.iter().fold(
        Playground::new().shortcut(None).bar(false),
        |playground, &(name, view)| playground.story(Story::new(name, Showcase::default, view)),
    )
}

fn stage(cx: &Cx<Showcase>, body: impl FnOnce(&Kit, &mut Vec<El>) -> El) -> El {
    let t = tokens(cx.system_dark());
    let k = kit(&t);
    let mut layer = Vec::new();
    let content = body(&k, &mut layer);
    el().w_full()
        .h_full()
        .fill(t.bg_app)
        .child(col().w_full().h_full().center().child(content))
        .children(layer)
}

fn list_row(_: &Showcase, cx: &mut Cx<Showcase>) -> El {
    stage(cx, |k, _| {
        List::new()
            .item(ListItem::new("api-review").selected(true))
            .item(ListItem::new("docs-pass"))
            .item(ListItem::new("build-shell"))
            .build(k)
            .w(240.0)
    })
}

fn button(_: &Showcase, cx: &mut Cx<Showcase>) -> El {
    stage(cx, |k, _| {
        rune::row()
            .gap(8.0)
            .child(
                Button::new("New session")
                    .variant(ButtonVariant::Submit)
                    .build(k),
            )
            .child(Button::new("Cancel").build(k))
    })
}

fn field(s: &Showcase, cx: &mut Cx<Showcase>) -> El {
    stage(cx, |k, _| {
        Field::new()
            .label("Session name")
            .control(
                TextField::new("Session name", &s.field)
                    .placeholder("api-review")
                    .hide_label()
                    .on_edit(|s: &mut Showcase| &mut s.field)
                    .build(k),
            )
            .build(k)
            .w(280.0)
    })
}

fn menu(_: &Showcase, cx: &mut Cx<Showcase>) -> El {
    stage(cx, |k, layer| {
        Menu::new("story.menu")
            .label("Session")
            .trigger(Button::new("Session").build(k))
            .item(MenuItem::new("Rename").shortcut("⌘R"))
            .item(MenuItem::new("Snooze"))
            .separator()
            .item(MenuItem::new("Close").danger(true))
            .open(true)
            .build(k, layer)
    })
}

fn dialog(s: &Showcase, cx: &mut Cx<Showcase>) -> El {
    stage(cx, |k, layer| {
        Dialog::new("story.dialog", "Close this session?")
            .description("The agent stops and its terminal closes.")
            .child(
                rune::row()
                    .gap(8.0)
                    .justify_end()
                    .w_full()
                    .padding(16.0)
                    .child(Button::new("Cancel").build(k))
                    .child(
                        Button::new("Close session")
                            .variant(ButtonVariant::Danger)
                            .build(k),
                    ),
            )
            .fit()
            .open(!s.dismissed)
            .on_close(|s: &mut Showcase| s.dismissed = true)
            .build(k, layer);
        el()
    })
}

fn popover(_: &Showcase, cx: &mut Cx<Showcase>) -> El {
    stage(cx, |k, layer| {
        Popover::new("story.popover")
            .trigger(Button::new("Pull request").build(k))
            .title("#123 Route every key")
            .description("Checks passed · 2 reviews")
            .open(true)
            .build(k, layer)
    })
}

fn toast(_: &Showcase, cx: &mut Cx<Showcase>) -> El {
    stage(cx, |k, _| {
        ToastRegion::new()
            .toast(
                Toast::new("story.toast", "Session closed")
                    .description("api-review stopped.")
                    .build(k),
            )
            .build(k)
    })
}

fn tooltip(_: &Showcase, cx: &mut Cx<Showcase>) -> El {
    stage(cx, |k, layer| {
        Tooltip::new("story.tooltip")
            .trigger(Button::new("Split").build(k))
            .text(k, "New shell beside")
            .open(true)
            .build(k, layer)
    })
}

use attn_app::{Action, Binding, Keymap, PageKey, page_key};
use rune::KeyPlatform;

/// Who takes a key while a web page in a browser tile holds the keyboard.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
enum Owner {
    /// attn runs the key's action.
    Attn,
    /// The page hears the key.
    Page,
    /// attn runs the key on the page, as a browser's own key.
    Browser(PageKey),
}

/// The split a browser makes between itself and its page, by action. Every action not listed
/// here is attn's.
const SPLIT: &[(Action, Owner)] = &[
    (Action::TerminalFind, Owner::Page),
    (Action::SessionToggleSidebar, Owner::Page),
    (Action::UiActionMenu, Owner::Page),
    (Action::TerminalSplitVertical, Owner::Page),
    (Action::SessionPrev, Owner::Page),
    (Action::SessionNext, Owner::Page),
    (Action::TerminalToggleZoom, Owner::Page),
    (Action::MarkdownSendAnnotations, Owner::Page),
    (Action::TerminalSendAnnotations, Owner::Page),
    (Action::SessionRefreshPrs, Owner::Browser(PageKey::Reload)),
    (Action::SessionHistoryBack, Owner::Browser(PageKey::Back)),
    (
        Action::SessionHistoryForward,
        Owner::Browser(PageKey::Forward),
    ),
];

/// The keys the window and app, desktops and queue, tile movement and pop-out use, which a
/// page never takes from attn.
const ATTN: &[Action] = &[
    Action::SessionClose,
    Action::AppQuit,
    Action::SessionNew,
    Action::SessionNewTerminal,
    Action::DesktopSelect1,
    Action::DesktopSelect9,
    Action::SessionJumpToWaiting,
    Action::SessionNextRun,
    Action::DesktopOverview,
    Action::TerminalFocusLeft,
    Action::TerminalFocusRight,
    Action::TerminalFocusUp,
    Action::TerminalFocusDown,
    Action::TilePopOut,
];

fn owner(keymap: &Keymap, action: &Action) -> Owner {
    let binding = keymap.binding(action).expect("the action is bound");
    let first = match binding {
        Binding::Chord(chord) => chord,
        Binding::Sequence { leader, .. } => leader,
    };
    let claimed = keymap
        .claimed_from_pages()
        .any(|chord| chord.same_keys(&first, keymap.platform()));
    match (claimed, page_key(action)) {
        (false, _) => Owner::Page,
        (true, Some(key)) => Owner::Browser(key),
        (true, None) => Owner::Attn,
    }
}

fn expected(action: &Action) -> Owner {
    SPLIT
        .iter()
        .find(|(a, _)| a == action)
        .map_or(Owner::Attn, |&(_, owner)| owner)
}

fn splits_like_a_browser(keymap: &Keymap) {
    let platform = keymap.platform();
    for action in Action::ALL {
        if keymap.binding(action).is_none() {
            continue;
        }
        assert_eq!(
            owner(keymap, action),
            expected(action),
            "{} on {platform:?}",
            action.id()
        );
    }
    for action in ATTN {
        assert_eq!(owner(keymap, action), Owner::Attn, "{}", action.id());
    }
}

#[test]
fn each_default_key_goes_to_attn_or_the_focused_page_as_a_browser_splits_them() {
    for platform in [KeyPlatform::Mac, KeyPlatform::Linux] {
        splits_like_a_browser(&Keymap::from_setting(None, platform));
    }
}

#[test]
fn a_rebound_key_follows_its_action_between_attn_and_the_page() {
    let setting = r#"{"version":1,"overrides":{
        "terminal.find":{"key":"e","meta":true},
        "session.refreshPRs":{"key":"l","meta":true,"alt":true},
        "session.jumpToWaiting":{"key":"y","meta":true}
    }}"#;
    let keymap = Keymap::from_setting(Some(setting), KeyPlatform::Mac);
    splits_like_a_browser(&keymap);
    let claims = |spec: &str| {
        let chord = rune::Chord::parse(spec).unwrap();
        keymap
            .claimed_from_pages()
            .any(|c| c.same_keys(&chord, KeyPlatform::Mac))
    };
    assert!(!claims("mod+e"), "the page keeps find's new key");
    assert!(
        !claims("mod+f"),
        "find's old key is unbound, so the page has it"
    );
    assert!(claims("mod+alt+l"), "reload's new key reloads the page");
    assert!(claims("mod+y"), "jump to waiting's new key is attn's");
}

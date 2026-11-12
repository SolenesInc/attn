use attn_term::Vt;
use attn_term::keys::{Press, press};
use rune::{Key, KeyEvent, KeyPlatform, Mods, PhysicalKeyCode as Code};

const MAC: KeyPlatform = KeyPlatform::Mac;
const LINUX: KeyPlatform = KeyPlatform::Linux;

fn mods(spec: &str, platform: KeyPlatform) -> Mods {
    let mut m = Mods::default();
    for part in spec.split('+').filter(|p| !p.is_empty()) {
        match part {
            "shift" => m.shift = true,
            "alt" => m.alt = true,
            "ctrl" => {
                m.ctrl = true;
                m.cmd |= platform != MAC;
            }
            "cmd" => {
                m.cmd = true;
                m.logo = true;
            }
            "super" => m.logo = true,
            other => panic!("unknown modifier {other}"),
        }
    }
    m
}

fn text(s: &str) -> Key {
    Key::Text(s.into())
}

type Case = (
    &'static str,
    Key,
    &'static str,
    Option<Code>,
    KeyPlatform,
    Option<&'static [u8]>,
);

fn sent(key: Key, held: &str, physical: Option<Code>, platform: KeyPlatform) -> Option<Vec<u8>> {
    let ev = KeyEvent {
        key,
        mods: mods(held, platform),
    };
    match press(&ev, physical, false, platform)? {
        Press::Bytes(b) => Some(b.to_vec()),
        Press::Encode(input) => Some(Vt::new(80, 24).encode_key(&input)),
    }
}

#[test]
fn keys_send_what_the_tauri_terminal_sends() {
    let cases: &[Case] = &[
        ("letter", text("a"), "", Some(Code::KeyA), MAC, Some(b"a")),
        (
            "shifted letter",
            text("A"),
            "shift",
            Some(Code::KeyA),
            MAC,
            Some(b"A"),
        ),
        ("enter", Key::Enter, "", Some(Code::Enter), MAC, Some(b"\r")),
        (
            "shift+enter sends a newline",
            Key::Enter,
            "shift",
            Some(Code::Enter),
            MAC,
            Some(b"\n"),
        ),
        (
            "shift+tab is back-tab",
            Key::Tab,
            "shift",
            Some(Code::Tab),
            LINUX,
            Some(b"\x1b[Z"),
        ),
        (
            "backspace",
            Key::Backspace,
            "",
            Some(Code::Backspace),
            MAC,
            Some(b"\x7f"),
        ),
        (
            "escape",
            Key::Escape,
            "",
            Some(Code::Escape),
            MAC,
            Some(b"\x1b"),
        ),
        ("up", Key::Up, "", Some(Code::ArrowUp), MAC, Some(b"\x1b[A")),
        (
            "ctrl+left",
            Key::Left,
            "ctrl",
            Some(Code::ArrowLeft),
            MAC,
            Some(b"\x1b[1;5D"),
        ),
        (
            "mac ctrl+c",
            text("c"),
            "ctrl",
            Some(Code::KeyC),
            MAC,
            Some(b"\x03"),
        ),
        (
            "linux ctrl+c",
            Key::Command('c'),
            "ctrl",
            Some(Code::KeyC),
            LINUX,
            Some(b"\x03"),
        ),
        (
            "mac ctrl+v is a literal ^V",
            text("v"),
            "ctrl",
            Some(Code::KeyV),
            MAC,
            Some(b"\x16"),
        ),
        (
            "option+b is meta-b",
            text("∫"),
            "alt",
            Some(Code::KeyB),
            MAC,
            Some(b"\x1bb"),
        ),
        (
            "option+. is meta-.",
            text("≥"),
            "alt",
            Some(Code::Period),
            MAC,
            Some(b"\x1b."),
        ),
        (
            "linux alt+f",
            text("f"),
            "alt",
            Some(Code::KeyF),
            LINUX,
            Some(b"\x1bf"),
        ),
        (
            "cmd chords stay with the app",
            text("k"),
            "cmd",
            Some(Code::KeyK),
            MAC,
            None,
        ),
        (
            "no physical key reads the logical key",
            Key::Command('d'),
            "ctrl",
            None,
            LINUX,
            Some(b"\x04"),
        ),
        (
            "a composed character",
            text("é"),
            "",
            None,
            MAC,
            Some("é".as_bytes()),
        ),
        (
            "f5",
            Key::Function(5),
            "",
            Some(Code::F5),
            MAC,
            Some(b"\x1b[15~"),
        ),
    ];
    for (name, key, held, physical, platform, want) in cases {
        let got = sent(key.clone(), held, *physical, *platform);
        assert_eq!(
            got.as_deref(),
            *want,
            "{name}: {:?}",
            got.as_deref().map(String::from_utf8_lossy)
        );
    }
}

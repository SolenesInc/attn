use std::io::{BufRead, BufReader, Write};
use std::net::TcpStream;
use std::sync::{Arc, Mutex, mpsc};
use std::time::Duration;

use attn_app::automation::AppHooks;
use attn_app::{State, Update};
use attn_automation::{Host, Inspection, Server};
use attn_client::{Clock, Link, LinkConfig, Pulse, Store, Subscription};
use attn_protocol::{
    Desktop, DesktopPane, InitialStateMessage, Profile, ProfileArrangementChangedMessage,
};
use attn_testkit::{CLIENT_TOKEN, Daemon, INSTANCE};
use rune::Env;
use serde_json::{Value, json};

const STUCK: Duration = Duration::from_secs(10);

enum Work {
    Update(Update),
    Wake,
    Inspect(Inspection<State>),
}

/// A window the user covered: it runs the link's updates and wakes and automation's
/// inspections in the order they come, handles an inspection's input before the next one, and
/// draws no frame. Nothing beats its `frames` but what `attn_app::connect` delivers.
struct Covered {
    work: Mutex<mpsc::Sender<Work>>,
    frames: Pulse,
    inspected: Pulse,
}

impl Host<State> for Covered {
    fn inspect(&self, f: Inspection<State>) {
        let _ = self.work.lock().unwrap().send(Work::Inspect(f));
    }

    fn frames(&self) -> &Pulse {
        &self.frames
    }
}

struct App {
    host: Arc<Covered>,
    port: u16,
    token: String,
    _link: Link,
    _window: Subscription,
}

impl App {
    fn start(daemon: &Daemon) -> App {
        let (work, inbox) = mpsc::channel();
        let (wake, deliver) = (work.clone(), work.clone());
        let host = Arc::new(Covered {
            work: Mutex::new(work),
            frames: Pulse::default(),
            inspected: Pulse::default(),
        });
        let inspected = host.inspected.clone();
        std::thread::spawn(move || {
            let env = Env {
                system_dark: true,
                scale: 2.0,
            };
            let mut rt = attn_app::app().headless_runtime((1200.0, 800.0), env);
            for work in inbox {
                match work {
                    Work::Update(f) => rt.update(f),
                    Work::Wake => {
                        rt.frame(0.0);
                    }
                    Work::Inspect(f) => {
                        rt.frame(0.0);
                        f(&mut rt);
                        while rt.has_pending_input() {
                            rt.frame(0.0);
                        }
                        inspected.beat();
                    }
                }
            }
        });
        let address = daemon.address().to_string();
        let (link, window) = attn_app::connect(
            &Store::default(),
            |terminals| LinkConfig {
                daemon: address,
                client_token: CLIENT_TOKEN.into(),
                host_token: None,
                instance: INSTANCE.into(),
                profile_id: None,
                client_id: "covered-window".into(),
                app_pid: std::process::id(),
                clock: Clock::system(),
                pulse: Pulse::default(),
                terminals,
            },
            host.frames.clone(),
            move || {
                let _ = wake.send(Work::Wake);
            },
            move |update| {
                let _ = deliver.send(Work::Update(update));
            },
        );
        let manifest = std::env::temp_dir().join(format!(
            "attn-covered-window-{}-{:?}.json",
            std::process::id(),
            std::thread::current().id()
        ));
        let server = Server::start(host.clone(), Arc::new(AppHooks), &manifest)
            .expect("the automation server starts");
        let _ = std::fs::remove_file(&manifest);
        App {
            host,
            port: server.manifest.port,
            token: server.manifest.token,
            _link: link,
            _window: window,
        }
    }

    fn ask(&self, action: &str, payload: Value) -> Value {
        let mut stream = TcpStream::connect(("127.0.0.1", self.port)).expect("the server's port");
        let request = json!({"id": 1, "token": self.token, "action": action, "payload": payload});
        writeln!(stream, "{request}").expect("the request goes out");
        let mut reply = String::new();
        BufReader::new(stream)
            .read_line(&mut reply)
            .expect("a reply");
        serde_json::from_str(&reply).expect("the reply is JSON")
    }
}

fn pane(id: &str) -> DesktopPane {
    DesktopPane {
        desktop_id: "d1".into(),
        pane_id: id.into(),
        runtime_id: format!("r-{id}"),
        title: format!("Agent {id}"),
        kind: "agent".into(),
        ..DesktopPane::default()
    }
}

fn desktop(panes: Vec<DesktopPane>) -> Desktop {
    Desktop {
        id: "d1".into(),
        name: "d1".into(),
        active_pane_id: "t1".into(),
        panes,
        profile_id: "work".into(),
        ..Desktop::default()
    }
}

fn profile() -> Profile {
    Profile {
        id: "work".into(),
        name: "Work".into(),
        current_desktop_id: "d1".into(),
        ..Profile::default()
    }
}

#[test]
fn a_wait_on_a_covered_window_returns_when_the_daemon_s_change_arrives() {
    let daemon = Daemon::scripted().state(InitialStateMessage {
        selected_profile_id: Some("work".into()),
        profiles: Some(vec![profile()]),
        desktops: Some(vec![desktop(vec![pane("t1")])]),
        ..InitialStateMessage::new()
    });
    let app = App::start(&daemon);
    let connected = app.ask("state", json!({"of": "desktops", "includes": "Agent t1"}));
    assert_eq!(connected["ok"], true, "{connected}");

    let attempts = app.host.inspected.count();
    let app = Arc::new(app);
    let asker = app.clone();
    let waiting = std::thread::spawn(move || {
        asker.ask(
            "state",
            json!({"of": "desktops", "includes": "Agent t9", "timeout_ms": 20_000}),
        )
    });
    assert!(
        app.host.inspected.wait_past(attempts, STUCK),
        "the wait made no first attempt"
    );
    daemon.emit(ProfileArrangementChangedMessage {
        profile: profile(),
        desktops: vec![desktop(vec![pane("t1"), pane("t9")])],
        ..ProfileArrangementChangedMessage::new()
    });

    let reply = waiting.join().unwrap();
    assert_eq!(reply["ok"], true, "{reply}");
}

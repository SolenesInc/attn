//! Session cookies outlive the app, as they did in the Tauri app: WebKit keeps persistent
//! cookies in the data store itself and drops session cookies when the app quits, so the jar
//! keeps the session cookies. The jar is the Tauri app's file, in its format, so the first
//! launch on a home the Tauri app used restores what that app kept.
//!
//! The jar is written when a page finishes loading or changes its address, when a page's script
//! or one of its requests changes a cookie, and after the agent changes cookies. The cookie
//! store's change notifications cannot drive it: for a data store opened by identifier, WebKit
//! sends only the first of them. A page's script is heard only where the page has `cookieStore`,
//! which WebKit gives secure pages: on a plain `http` page the jar hears of a script's change at
//! the page's next request, load or address change. The app's quit saves the jar once more and
//! waits for it, so a change heard in the pause between saves still reaches the next launch.

use std::cell::{Cell, RefCell};
use std::collections::HashSet;
use std::ffi::c_void;
use std::path::{Path, PathBuf};
use std::ptr::NonNull;
use std::rc::Rc;

use block2::RcBlock;
use objc2::rc::Retained;
use objc2::runtime::{AnyObject, NSObjectProtocol, ProtocolObject};
use objc2::{DefinedClass, MainThreadMarker, MainThreadOnly, Message, define_class, msg_send, sel};
use objc2_app_kit::NSModalPanelRunLoopMode;
use objc2_foundation::{
    NSArray, NSDate, NSHTTPCookie, NSKeyValueObservingOptions, NSObject,
    NSObjectNSKeyValueObserverRegistration, NSRunLoop, NSString, ns_string,
};
use objc2_web_kit::{
    WKContentWorld, WKHTTPCookieStore, WKScriptMessage, WKScriptMessageHandler,
    WKUserContentController, WKUserScript, WKUserScriptInjectionTime, WKWebView,
};
use serde_json::{Map, Value};

const COOKIE_SIGNALS: &str = "attnSessionCookies";
/// How often a page busy with requests has the jar saved. Each save reads the whole store on the
/// main thread, about 1.4 ms with 300 cookies.
const PAUSE_BETWEEN_SAVES_SECONDS: f64 = 1.0;
/// The longest a quit waits for the store's cookies. A quit's save took 0.4 to 8.9 ms over 54
/// quits of the session-cookies scenario in the macOS VM.
const LONGEST_QUIT_SAVE_SECONDS: f64 = 1.0;

/// Tells the keeper each time a page's cookies may have changed: a response arrived, which can
/// set or delete any cookie, httpOnly ones included, or a script changed one. A site that signs
/// out in place does one of these without loading a page.
fn cookie_signals_js() -> String {
    format!(
        r#"(() => {{
  const changed = () => webkit.messageHandlers.{COOKIE_SIGNALS}.postMessage(null);
  new PerformanceObserver(changed).observe({{ type: 'resource' }});
  globalThis.cookieStore?.addEventListener('change', changed);
}})()"#
    )
}

thread_local! {
    static KEEPER: RefCell<Option<Retained<JarKeeper>>> = const { RefCell::new(None) };
}

/// Puts the jar's session cookies into `store`, then runs `restored`. From then on the jar
/// follows the store's session cookies, told of changes by a script `pages` run in `world`. A
/// cookie the store already holds under the same name, domain and path is newer than the jar's,
/// so it stays.
pub(super) fn restore_then_keep(
    store: Retained<WKHTTPCookieStore>,
    jar: PathBuf,
    pages: &WKUserContentController,
    world: &WKContentWorld,
    mtm: MainThreadMarker,
    restored: impl FnOnce() + 'static,
) {
    let saved = read_session_cookies(&jar);
    let keeper = JarKeeper::new(mtm, jar, store.clone());
    KEEPER.with_borrow_mut(|slot| *slot = Some(keeper.clone()));
    unsafe {
        pages.addScriptMessageHandler_contentWorld_name(
            ProtocolObject::from_ref(&*keeper),
            world,
            &NSString::from_str(COOKIE_SIGNALS),
        );
        pages.addUserScript(
            &WKUserScript::initWithSource_injectionTime_forMainFrameOnly_inContentWorld(
                WKUserScript::alloc(mtm),
                &NSString::from_str(&cookie_signals_js()),
                WKUserScriptInjectionTime::AtDocumentStart,
                false,
                world,
            ),
        );
    }
    let keep = Rc::new(Cell::new(Some(move || {
        keeper.ivars().restored.set(true);
        keeper.save();
        restored();
    })));
    let target = store.clone();
    let present = RcBlock::new(move |all: NonNull<NSArray<NSHTTPCookie>>| {
        let held: HashSet<CookieKey> = unsafe { all.as_ref() }
            .iter()
            .map(|cookie| {
                CookieKey::of(
                    &cookie.name().to_string(),
                    &cookie.domain().to_string(),
                    &cookie.path().to_string(),
                )
            })
            .collect();
        let missing: Vec<_> = saved
            .iter()
            .filter(|entry| !held.contains(&CookieKey::of_entry(entry)))
            .filter_map(super::http_cookie)
            .collect();
        if missing.is_empty() {
            if let Some(keep) = keep.take() {
                keep();
            }
            return;
        }
        let left = Rc::new(Cell::new(missing.len()));
        for cookie in missing {
            let (left, keep) = (left.clone(), keep.clone());
            let set = RcBlock::new(move || {
                left.set(left.get() - 1);
                if left.get() == 0
                    && let Some(keep) = keep.take()
                {
                    keep();
                }
            });
            unsafe { target.setCookie_completionHandler(&cookie, Some(&set)) };
        }
    });
    unsafe { store.getAllCookies(&present) };
}

#[derive(PartialEq, Eq, Hash)]
struct CookieKey(String, String, String);

impl CookieKey {
    fn of(name: &str, domain: &str, path: &str) -> CookieKey {
        let path = if path.is_empty() { "/" } else { path };
        CookieKey(
            name.to_owned(),
            domain.trim_start_matches('.').to_ascii_lowercase(),
            path.to_owned(),
        )
    }

    fn of_entry(entry: &Map<String, Value>) -> CookieKey {
        let text = |key: &str| entry.get(key).and_then(Value::as_str).unwrap_or("");
        CookieKey::of(text("name"), text("domain"), text("path"))
    }
}

/// Saves the jar each time `web` finishes loading a page.
pub(super) fn watch(web: &WKWebView) {
    KEEPER.with_borrow(|keeper| {
        if let Some(keeper) = keeper {
            unsafe {
                web.addObserver_forKeyPath_options_context(
                    keeper,
                    ns_string!("loading"),
                    NSKeyValueObservingOptions::New,
                    std::ptr::null_mut(),
                );
            }
        }
    });
}

pub(super) fn unwatch(web: &WKWebView) {
    KEEPER.with_borrow(|keeper| {
        if let Some(keeper) = keeper {
            unsafe { web.removeObserver_forKeyPath(keeper, ns_string!("loading")) };
        }
    });
}

/// Saves the jar after a change no page script reports: the agent's cookie commands, or a page
/// changing its address, as a script on a plain `http` page that signs out and changes the
/// route does.
pub(super) fn changed() {
    let keeper = KEEPER.with_borrow(Clone::clone);
    if let Some(keeper) = keeper {
        keeper.save();
    }
}

/// Saves the jar and waits until it is written, as the app quits: a change heard in the pause
/// between saves, or while a save reads the store, would otherwise end with the process, and the
/// next launch would bring back a cookie the site deleted.
pub(super) fn save_before_quit() {
    let keeper = KEEPER.with_borrow(Clone::clone);
    if let Some(keeper) = keeper {
        keeper.save_and_wait();
    }
}

/// The jar's session cookies. A jar that is missing or unreadable holds none: the cookies it
/// lost are sign-ins the person redoes, and the next save writes it anew.
fn read_session_cookies(jar: &Path) -> Vec<Map<String, Value>> {
    let entries = match std::fs::read(jar) {
        Ok(data) => {
            serde_json::from_slice::<Vec<Map<String, Value>>>(&data).unwrap_or_else(|error| {
                eprintln!(
                    "attn-app: browser cookie jar {} is unreadable: {error}",
                    jar.display()
                );
                Vec::new()
            })
        }
        Err(error) if error.kind() == std::io::ErrorKind::NotFound => Vec::new(),
        Err(error) => {
            eprintln!(
                "attn-app: reading browser cookie jar {}: {error}",
                jar.display()
            );
            Vec::new()
        }
    };
    entries
        .into_iter()
        .filter(|entry| entry.get("expiry").is_none_or(Value::is_null))
        .collect()
}

pub(super) struct Keeping {
    jar: PathBuf,
    store: Retained<WKHTTPCookieStore>,
    restored: Cell<bool>,
    written: RefCell<Option<Vec<u8>>>,
    saving: Cell<bool>,
    changed_while_saving: Cell<bool>,
}

define_class!(
    #[unsafe(super(NSObject))]
    #[thread_kind = MainThreadOnly]
    #[name = "AttnBrowserJarKeeper"]
    #[ivars = Keeping]
    struct JarKeeper;

    impl JarKeeper {
        #[unsafe(method(observeValueForKeyPath:ofObject:change:context:))]
        fn observe(
            &self,
            _path: Option<&NSString>,
            object: Option<&AnyObject>,
            _change: Option<&AnyObject>,
            _context: *mut c_void,
        ) {
            let Some(web) = object else { return };
            let loading: bool = unsafe { msg_send![web, isLoading] };
            if !loading {
                self.save();
            }
        }
    }

    impl JarKeeper {
        #[unsafe(method(saveAfterPause))]
        fn save_after_pause(&self) {
            let keeping = self.ivars();
            keeping.changed_while_saving.set(false);
            keeping.saving.set(false);
            self.save();
        }
    }

    unsafe impl NSObjectProtocol for JarKeeper {}

    unsafe impl WKScriptMessageHandler for JarKeeper {
        #[unsafe(method(userContentController:didReceiveScriptMessage:))]
        fn heard(&self, _pages: &WKUserContentController, _message: &WKScriptMessage) {
            self.save();
        }
    }
);

impl JarKeeper {
    fn new(
        mtm: MainThreadMarker,
        jar: PathBuf,
        store: Retained<WKHTTPCookieStore>,
    ) -> Retained<Self> {
        let this = Self::alloc(mtm).set_ivars(Keeping {
            jar,
            store,
            restored: Cell::new(false),
            written: RefCell::new(None),
            saving: Cell::new(false),
            changed_while_saving: Cell::new(false),
        });
        unsafe { msg_send![super(this), init] }
    }

    /// Writes the store's session cookies to the jar when they differ from what it last wrote.
    /// Saves asked for while one reads the store run once, a pause after it.
    fn save(&self) {
        let keeping = self.ivars();
        if !keeping.restored.get() {
            return;
        }
        if keeping.saving.replace(true) {
            keeping.changed_while_saving.set(true);
            return;
        }
        let this = self.retain();
        let handler = RcBlock::new(move |all: NonNull<NSArray<NSHTTPCookie>>| {
            this.write(unsafe { all.as_ref() });
            let keeping = this.ivars();
            if keeping.changed_while_saving.replace(false) {
                let _: () = unsafe {
                    msg_send![
                        &*this,
                        performSelector: sel!(saveAfterPause),
                        withObject: std::ptr::null::<AnyObject>(),
                        afterDelay: PAUSE_BETWEEN_SAVES_SECONDS
                    ]
                };
            } else {
                keeping.saving.set(false);
            }
        });
        unsafe { keeping.store.getAllCookies(&handler) };
    }

    /// Reads the store and writes the jar before returning. The store answers on the main run
    /// loop, so it runs until the answer is written, or for at most
    /// `LONGEST_QUIT_SAVE_SECONDS` when the store never answers. It runs in the modal panel mode,
    /// as a modal would: WebKit's answers arrive in every common mode, and winit queues its own
    /// work for the default mode, where it would run the app's handler again inside this one.
    fn save_and_wait(&self) {
        if !self.ivars().restored.get() {
            return;
        }
        let written = Rc::new(Cell::new(false));
        let this = self.retain();
        let handler = RcBlock::new({
            let written = written.clone();
            move |all: NonNull<NSArray<NSHTTPCookie>>| {
                this.write(unsafe { all.as_ref() });
                written.set(true);
            }
        });
        unsafe { self.ivars().store.getAllCookies(&handler) };
        let run_loop = NSRunLoop::currentRunLoop();
        let deadline = NSDate::dateWithTimeIntervalSinceNow(LONGEST_QUIT_SAVE_SECONDS);
        while !written.get() && deadline.timeIntervalSinceNow() > 0.0 {
            unsafe { run_loop.runMode_beforeDate(NSModalPanelRunLoopMode, &deadline) };
        }
        if !written.get() {
            eprintln!(
                "attn-app: the browser cookie store did not answer within {LONGEST_QUIT_SAVE_SECONDS} s of the quit, so the jar keeps its last save"
            );
        }
    }

    /// Writes `all`'s session cookies to the jar when they differ from what it last wrote.
    fn write(&self, all: &NSArray<NSHTTPCookie>) {
        let session: Vec<Value> = all
            .iter()
            .filter(|cookie| cookie.isSessionOnly())
            .map(|cookie| super::stored_cookie(&cookie))
            .collect();
        let keeping = self.ivars();
        let data = serde_json::to_vec_pretty(&session).unwrap_or_default();
        if keeping.written.borrow().as_deref() != Some(&data[..]) {
            match replace_jar(&keeping.jar, &data) {
                Ok(()) => *keeping.written.borrow_mut() = Some(data),
                Err(error) => eprintln!(
                    "attn-app: writing browser cookie jar {}: {error}",
                    keeping.jar.display()
                ),
            }
        }
    }
}

fn replace_jar(jar: &Path, data: &[u8]) -> std::io::Result<()> {
    use std::os::unix::fs::PermissionsExt;
    if let Some(folder) = jar.parent() {
        std::fs::create_dir_all(folder)?;
    }
    let temporary = jar.with_extension("json.tmp");
    std::fs::write(&temporary, data)?;
    std::fs::set_permissions(&temporary, std::fs::Permissions::from_mode(0o600))?;
    std::fs::rename(&temporary, jar)
}

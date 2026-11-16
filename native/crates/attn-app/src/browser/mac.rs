//! Browser tiles' pages on macOS: a `WKWebView` per tile, a subview of the window's content
//! view, clipped to the part of its tile the window shows. AppKit routes the page's input to it
//! directly; the window takes back only the keys attn binds.

use std::cell::{Cell, RefCell};
use std::collections::HashMap;
use std::ffi::c_void;
use std::ptr::NonNull;
use std::rc::Rc;

use attn_client::TileKey;
use base64::Engine;
use block2::RcBlock;
use objc2::rc::Retained;
use objc2::runtime::{AnyObject, Bool, NSObjectProtocol, ProtocolObject};
use objc2::{DefinedClass, MainThreadMarker, MainThreadOnly, Message, define_class, msg_send};
use objc2_app_kit::{
    NSAlert, NSAlertFirstButtonReturn, NSBitmapImageFileType, NSBitmapImageRep, NSEvent,
    NSEventModifierFlags, NSImage, NSResponder, NSTextField, NSView,
};
use objc2_core_foundation::{CGPoint, CGRect, CGSize};
use objc2_foundation::{
    NSArray, NSData, NSDate, NSDictionary, NSError, NSHTTPCookie, NSHTTPCookieDomain,
    NSHTTPCookieExpires, NSHTTPCookieName, NSHTTPCookiePath, NSHTTPCookieSameSitePolicy,
    NSHTTPCookieSecure, NSHTTPCookieValue, NSKeyValueObservingOptions, NSObject,
    NSObjectNSKeyValueObserverRegistration, NSPoint, NSRect, NSSize, NSString, NSURL, NSURLRequest,
    NSUUID, ns_string,
};
use objc2_web_kit::{
    WKContentWorld, WKFrameInfo, WKHTTPCookieStore, WKPDFConfiguration, WKSnapshotConfiguration,
    WKUIDelegate, WKUserScript, WKUserScriptInjectionTime, WKWebView, WKWebViewConfiguration,
    WKWebsiteDataStore,
};
use rune::{Key, KeyEvent, Mods};
use serde_json::{Map, Value, json};

use super::{Alert, AlertKind, BrowserHost, Control, Heard, Placement};

/// The agent's page script, built from `app/src/browser/runtime.ts` for both apps.
const RUNTIME_JS: &str = include_str!("../../../../../app/src-tauri/generated/browser-runtime.js");
const CONTROL_WORLD: &str = "attn-browser-control";
/// The Tauri app's store, so a person signed in there stays signed in here.
const DATA_STORE_ID: [u8; 16] = *b"attn-browser-v1!";
/// Keeps every page in its tile: links, forms and `window.open` that would open another window
/// load in this one.
const SINGLE_TAB_JS: &str = r#"(() => {
  const navigateInPlace = (raw) => {
    try {
      const url = new URL(String(raw), location.href);
      if (url.protocol === 'http:' || url.protocol === 'https:') location.assign(url.href);
    } catch {}
  };
  window.addEventListener('click', (event) => {
    if (event.defaultPrevented || event.button !== 0) return;
    const anchor = event.target?.closest?.('a[href][target]');
    if (!anchor || anchor.download) return;
    const target = anchor.getAttribute('target')?.toLowerCase();
    if (!target || target === '_self') return;
    event.preventDefault();
    navigateInPlace(anchor.href);
  }, true);
  const retargetForm = (form, submitter) => {
    const targetOwner = submitter?.hasAttribute?.('formtarget') ? submitter : form;
    const attribute = targetOwner === form ? 'target' : 'formtarget';
    const target = targetOwner.getAttribute(attribute)?.toLowerCase();
    if (!target || target === '_self') return () => {};
    const original = targetOwner.getAttribute(attribute);
    targetOwner.setAttribute(attribute, '_self');
    return () => {
      if (original === null) targetOwner.removeAttribute(attribute);
      else targetOwner.setAttribute(attribute, original);
    };
  };
  window.addEventListener('submit', (event) => {
    if (!(event.target instanceof HTMLFormElement)) return;
    const restore = retargetForm(event.target, event.submitter);
    window.setTimeout(restore, 0);
  }, true);
  const originalSubmit = HTMLFormElement.prototype.submit;
  HTMLFormElement.prototype.submit = function() {
    const restore = retargetForm(this, null);
    try {
      return originalSubmit.call(this);
    } finally {
      window.setTimeout(restore, 0);
    }
  };
  const originalOpen = window.open.bind(window);
  window.open = (url, target, features) => {
    if (url) {
      navigateInPlace(url);
      return window;
    }
    return originalOpen(url, target, features);
  };
})()"#;
const OFF_SCREEN: &str = "browser screenshot is unavailable while the browser tile is off screen; ask the user to bring it on screen with its chip on the shelf or by selecting its desktop, then retry";

thread_local! {
    static WAKE: RefCell<Option<Box<dyn Fn()>>> = const { RefCell::new(None) };
    /// Set while the window hands the keyboard to a page itself, which it need not hear about.
    static HANDING: Cell<bool> = const { Cell::new(false) };
}

/// Runs browser tiles' pages in `window`'s content view. `wake` brings the window to a frame,
/// where it takes what the pages said. A parked automation window keeps its pages drawing.
pub fn install(window: &rune::WindowHandle, wake: impl Fn() + 'static, automation: bool) {
    let content = window.with_raw_handle(|handle| match handle.as_raw() {
        raw_window_handle::RawWindowHandle::AppKit(appkit) => Some(appkit.ns_view.as_ptr()),
        _ => None,
    });
    let Some(content) = content
        .ok()
        .flatten()
        .and_then(|view| unsafe { Retained::retain(view.cast::<NSView>()) })
    else {
        eprintln!("attn-app: browser tiles are off: the window has no content view");
        return;
    };
    let Some(mtm) = MainThreadMarker::new() else {
        return;
    };
    WAKE.with_borrow_mut(|slot| *slot = Some(Box::new(wake)));
    super::use_host(Box::new(WebKitHost::new(content, mtm, automation)));
}

fn wake() {
    WAKE.with_borrow(|wake| {
        if let Some(wake) = wake {
            wake();
        }
    });
}

fn tell(heard: Heard) {
    super::hear(heard);
    wake();
}

fn reply(request_id: String, result: Result<String, String>) {
    tell(Heard::Answered { request_id, result });
}

struct WebKitHost {
    mtm: MainThreadMarker,
    content: Retained<NSView>,
    config: Retained<WKWebViewConfiguration>,
    world: Retained<WKContentWorld>,
    pages: HashMap<TileKey, Shown>,
    unoccluded: bool,
}

struct Shown {
    clip: Retained<PageClip>,
    web: Retained<PageView>,
    location: Retained<LocationWatch>,
    _dialogs: Retained<PageDialogs>,
}

impl WebKitHost {
    /// Gives the keyboard back to the window when `web` holds it, so the window's keys reach
    /// attn and not the window's own close.
    fn focus_window_from(&self, web: &WKWebView) {
        let Some(window) = self.content.window() else {
            return;
        };
        if window
            .firstResponder()
            .is_some_and(|responder| same(&responder, web))
        {
            HANDING.set(true);
            window.makeFirstResponder(Some(&self.content));
            HANDING.set(false);
        }
    }

    fn new(content: Retained<NSView>, mtm: MainThreadMarker, unoccluded: bool) -> Self {
        let world =
            unsafe { WKContentWorld::worldWithName(&NSString::from_str(CONTROL_WORLD), mtm) };
        let config = unsafe { WKWebViewConfiguration::new(mtm) };
        unsafe {
            let store =
                WKWebsiteDataStore::dataStoreForIdentifier(&NSUUID::from_bytes(DATA_STORE_ID), mtm);
            config.setWebsiteDataStore(&store);
            let scripts = config.userContentController();
            let runtime =
                WKUserScript::initWithSource_injectionTime_forMainFrameOnly_inContentWorld(
                    WKUserScript::alloc(mtm),
                    &NSString::from_str(RUNTIME_JS),
                    WKUserScriptInjectionTime::AtDocumentStart,
                    false,
                    &world,
                );
            scripts.addUserScript(&runtime);
            let single_tab = WKUserScript::initWithSource_injectionTime_forMainFrameOnly(
                WKUserScript::alloc(mtm),
                &NSString::from_str(SINGLE_TAB_JS),
                WKUserScriptInjectionTime::AtDocumentStart,
                true,
            );
            scripts.addUserScript(&single_tab);
        }
        WebKitHost {
            mtm,
            content,
            config,
            world,
            pages: HashMap::new(),
            unoccluded,
        }
    }

    fn web(&self, page: &TileKey) -> Option<Retained<PageView>> {
        self.pages.get(page).map(|shown| shown.web.clone())
    }
}

fn load(web: &WKWebView, url: &str) {
    if let Some(url) = NSURL::URLWithString(&NSString::from_str(url)) {
        unsafe { web.loadRequest(&NSURLRequest::requestWithURL(&url)) };
    }
}

impl BrowserHost for WebKitHost {
    fn open(&mut self, page: &TileKey, url: &str) {
        let mtm = self.mtm;
        let none = NSRect::new(NSPoint::new(0.0, 0.0), NSSize::new(0.0, 0.0));
        let clip = PageClip::new(mtm, none);
        clip.setClipsToBounds(true);
        clip.setHidden(true);
        let web = PageView::new(mtm, page.clone(), none, &self.config);
        if self.unoccluded {
            let _: () = unsafe { msg_send![&*web, _setWindowOcclusionDetectionEnabled: false] };
        }
        let dialogs = PageDialogs::new(mtm, page.clone());
        unsafe { web.setUIDelegate(Some(ProtocolObject::from_ref(&*dialogs))) };
        let location = LocationWatch::new(mtm, page.clone());
        unsafe {
            web.addObserver_forKeyPath_options_context(
                &location,
                ns_string!("URL"),
                NSKeyValueObservingOptions::New,
                std::ptr::null_mut(),
            );
        }
        clip.addSubview(&web);
        self.content.addSubview(&clip);
        load(&web, url);
        self.pages.insert(
            page.clone(),
            Shown {
                clip,
                web,
                location,
                _dialogs: dialogs,
            },
        );
    }

    fn navigate(&mut self, page: &TileKey, url: &str) {
        if let Some(web) = self.web(page) {
            load(&web, url);
        }
    }

    fn reload(&mut self, page: &TileKey) {
        if let Some(web) = self.web(page) {
            unsafe { web.reload() };
        }
    }

    fn back(&mut self, page: &TileKey) {
        if let Some(web) = self.web(page) {
            unsafe { web.goBack() };
        }
    }

    fn forward(&mut self, page: &TileKey) {
        if let Some(web) = self.web(page) {
            unsafe { web.goForward() };
        }
    }

    fn close(&mut self, page: &TileKey) {
        let Some(shown) = self.pages.remove(page) else {
            return;
        };
        self.focus_window_from(&shown.web);
        unsafe {
            shown
                .web
                .removeObserver_forKeyPath(&shown.location, ns_string!("URL"));
            shown.web.stopLoading();
            shown.web.setUIDelegate(None);
        }
        shown.clip.removeFromSuperview();
    }

    fn place(&mut self, page: &TileKey, at: Option<Placement>) {
        let Some(shown) = self.pages.get(page) else {
            return;
        };
        let Some(at) = at else {
            shown.clip.setHidden(true);
            return;
        };
        let rect = |r: [f32; 4]| {
            NSRect::new(
                NSPoint::new(f64::from(r[0]), f64::from(r[1])),
                NSSize::new(f64::from(r[2]), f64::from(r[3])),
            )
        };
        let [vx, vy, _, _] = at.visible;
        let [fx, fy, fw, fh] = at.frame;
        shown.clip.setFrame(rect(at.visible));
        shown.web.setFrame(rect([fx - vx, fy - vy, fw, fh]));
        shown.clip.setHidden(false);
    }

    fn focus(&mut self, page: &TileKey, focused: bool) {
        let Some(shown) = self.pages.get(page) else {
            return;
        };
        if !focused {
            return self.focus_window_from(&shown.web);
        }
        let Some(window) = self.content.window() else {
            return;
        };
        let web: &NSResponder = &shown.web;
        if !window
            .firstResponder()
            .is_some_and(|responder| same(&responder, web))
        {
            HANDING.set(true);
            window.makeFirstResponder(Some(web));
            HANDING.set(false);
        }
    }

    fn control(&mut self, page: &TileKey, request: Control) {
        let Some(web) = self.web(page) else {
            return reply(
                request.request_id,
                Err("browser webview is not mounted".into()),
            );
        };
        let web: Retained<WKWebView> = Retained::into_super(web);
        let Control {
            request_id: id,
            action,
            params,
        } = request;
        let world = self.world.clone();
        match action.as_str() {
            "screenshot" => snapshot(&web, None, id),
            "element_screenshot" => {
                let target = web.clone();
                run_page_action(
                    &web,
                    &world,
                    "get_element_screenshot_rect",
                    &params,
                    move |rect| {
                        let rect = rect.and_then(|raw| {
                            serde_json::from_str::<Value>(&raw).map_err(|why| {
                                format!("decode element screenshot rectangle: {why}")
                            })
                        });
                        match rect {
                            Ok(rect) => snapshot(&target, Some(rect), id),
                            Err(why) => reply(id, Err(why)),
                        }
                    },
                );
            }
            "print_page" => print_page(&web, &params, id),
            "get_all_cookies" | "get_cookie" | "add_cookie" | "delete_cookie"
            | "delete_all_cookies" => cookies(&web, &action, &params, id),
            _ => run_page_action(&web, &world, &action, &params, move |result| {
                reply(id, result)
            }),
        }
    }
}

/// Runs `action` in the agent's script world on the page, with `done` given its JSON answer.
fn run_page_action(
    web: &WKWebView,
    world: &WKContentWorld,
    action: &str,
    params: &Map<String, Value>,
    done: impl FnOnce(Result<String, String>) + 'static,
) {
    let body = format!(
        r#"try {{
  if (!window.__attnBrowser) {{ {runtime} }}
  if (!window.__attnBrowser) throw new Error('attn browser runtime failed to install');
  const value = await window.__attnBrowser.execute({action}, {params});
  return JSON.stringify({{ success: true, value }});
}} catch (error) {{
  return JSON.stringify({{ success: false, error: String(error?.message || error) }});
}}"#,
        runtime = RUNTIME_JS,
        action = Value::String(action.to_owned()),
        params = Value::Object(params.clone()),
    );
    let done = Cell::new(Some(done));
    let handler = RcBlock::new(move |value: *mut AnyObject, error: *mut NSError| {
        let Some(done) = done.take() else { return };
        done(page_answer(value, error));
    });
    unsafe {
        web.callAsyncJavaScript_arguments_inFrame_inContentWorld_completionHandler(
            &NSString::from_str(&body),
            None,
            None,
            world,
            Some(&handler),
        );
    }
}

fn page_answer(value: *mut AnyObject, error: *mut NSError) -> Result<String, String> {
    if let Some(error) = unsafe { error.as_ref() } {
        return Err(error.localizedDescription().to_string());
    }
    let raw = unsafe { value.as_ref() }
        .and_then(|value| value.downcast_ref::<NSString>())
        .map(NSString::to_string)
        .ok_or("browser script returned no result")?;
    let envelope: Value = serde_json::from_str(&raw)
        .map_err(|why| format!("decode browser runtime result: {why}"))?;
    if envelope.get("success").and_then(Value::as_bool) != Some(true) {
        return Err(envelope
            .get("error")
            .and_then(Value::as_str)
            .unwrap_or("browser runtime action failed")
            .to_owned());
    }
    Ok(envelope.get("value").unwrap_or(&Value::Null).to_string())
}

/// A PNG of the page, or of `rect` in it, base64-encoded.
fn snapshot(web: &WKWebView, rect: Option<Value>, id: String) {
    let mtm = MainThreadMarker::from(web);
    let config = rect.map(|rect| {
        let number = |key: &str| rect.get(key).and_then(Value::as_f64).unwrap_or(0.0);
        let config = unsafe { WKSnapshotConfiguration::new(mtm) };
        unsafe {
            config.setRect(CGRect::new(
                CGPoint::new(number("x"), number("y")),
                CGSize::new(number("width"), number("height")),
            ));
        }
        config
    });
    let id = Cell::new(Some(id));
    let handler = RcBlock::new(move |image: *mut NSImage, error: *mut NSError| {
        let Some(id) = id.take() else { return };
        let result = match (unsafe { image.as_ref() }, unsafe { error.as_ref() }) {
            (_, Some(error)) => Err(error.localizedDescription().to_string()),
            (None, None) => Err("browser screenshot returned no image".to_owned()),
            (Some(image), None) => png(image),
        };
        reply(id, result);
    });
    unsafe { web.takeSnapshotWithConfiguration_completionHandler(config.as_deref(), &handler) };
}

fn png(image: &NSImage) -> Result<String, String> {
    let bitmap = image
        .TIFFRepresentation()
        .and_then(|tiff| NSBitmapImageRep::imageRepWithData(&tiff))
        .ok_or("browser screenshot could not be encoded")?;
    if bitmap.pixelsWide() <= 1 || bitmap.pixelsHigh() <= 1 {
        return Err(OFF_SCREEN.to_owned());
    }
    let data = unsafe {
        bitmap.representationUsingType_properties(NSBitmapImageFileType::PNG, &NSDictionary::new())
    }
    .ok_or("browser screenshot could not be encoded")?;
    Ok(base64::engine::general_purpose::STANDARD.encode(data.to_vec()))
}

/// A PDF of the page on the agent's paper, base64-encoded.
fn print_page(web: &WKWebView, params: &Map<String, Value>, id: String) {
    let number = |group: &str, key: &str, fallback: f64| {
        params
            .get(group)
            .and_then(|g| g.get(key))
            .and_then(Value::as_f64)
            .unwrap_or(fallback)
    };
    let (width, height) = (
        number("page", "width", 21.0),
        number("page", "height", 29.7),
    );
    let (width, height) = match params.get("orientation").and_then(Value::as_str) {
        Some("landscape") => (height, width),
        _ => (width, height),
    };
    let [top, right, bottom, left] =
        ["top", "right", "bottom", "left"].map(|side| number("margin", side, 1.0));
    let style = format!(
        r#"(() => {{
  document.getElementById('__attn_print_style')?.remove();
  const style = document.createElement('style');
  style.id = '__attn_print_style';
  style.textContent = `@page {{ size: {width}cm {height}cm; margin: {top}cm {right}cm {bottom}cm {left}cm; }} @media print {{ body {{ -webkit-print-color-adjust: exact; print-color-adjust: exact; }} }}`;
  document.head.appendChild(style);
}})()"#
    );
    let target = web.retain();
    let id = Cell::new(Some(id));
    let styled = RcBlock::new(move |_: *mut AnyObject, _: *mut NSError| {
        let Some(id) = id.take() else { return };
        let mtm = MainThreadMarker::from(&*target);
        let config = unsafe { WKPDFConfiguration::new(mtm) };
        let unstyle = target.clone();
        let id = Cell::new(Some(id));
        let printed = RcBlock::new(move |data: *mut NSData, error: *mut NSError| {
            let Some(id) = id.take() else { return };
            unsafe {
                unstyle.evaluateJavaScript_completionHandler(
                    ns_string!("document.getElementById('__attn_print_style')?.remove()"),
                    None,
                );
            }
            let result = match (unsafe { data.as_ref() }, unsafe { error.as_ref() }) {
                (_, Some(error)) => Err(error.localizedDescription().to_string()),
                (None, None) => Err("browser PDF returned no data".to_owned()),
                (Some(data), None) => {
                    Ok(base64::engine::general_purpose::STANDARD.encode(data.to_vec()))
                }
            };
            reply(id, result);
        });
        unsafe { target.createPDFWithConfiguration_completionHandler(Some(&config), &printed) };
    });
    unsafe { web.evaluateJavaScript_completionHandler(&NSString::from_str(&style), Some(&styled)) };
}

/// The cookie actions, on the cookies the page's address would send.
fn cookies(web: &WKWebView, action: &str, params: &Map<String, Value>, id: String) {
    let Some(url) = (unsafe { web.URL() }).and_then(|url| url.absoluteString()) else {
        return reply(id, Err("read browser URL: the page has no address".into()));
    };
    let url = url.to_string();
    let (host, path, secure) = url_parts(&url);
    let store = unsafe { web.configuration().websiteDataStore().httpCookieStore() };
    if action == "add_cookie" {
        let result = add_cookie(&store, &host, params, id.clone());
        if let Err(why) = result {
            reply(id, Err(why));
        }
        return;
    }
    let action = action.to_owned();
    let params = params.clone();
    let id = Cell::new(Some(id));
    let target = store.clone();
    let handler = RcBlock::new(move |all: NonNull<NSArray<NSHTTPCookie>>| {
        let Some(id) = id.take() else { return };
        let all = unsafe { all.as_ref() };
        let sent: Vec<Retained<NSHTTPCookie>> = all
            .iter()
            .filter(|cookie| {
                domain_matches(&host, &cookie.domain().to_string())
                    && path_matches(&path, &cookie.path().to_string())
                    && (!cookie.isSecure() || secure)
            })
            .collect();
        let name = params
            .get("name")
            .and_then(Value::as_str)
            .filter(|n| !n.is_empty());
        match action.as_str() {
            "get_all_cookies" => {
                reply(
                    id,
                    Ok(
                        Value::from(sent.iter().map(|c| cookie_value(c)).collect::<Vec<_>>())
                            .to_string(),
                    ),
                );
            }
            "get_cookie" => {
                let Some(name) = name else {
                    return reply(id, Err("name must be a non-empty string".into()));
                };
                let found = sent
                    .iter()
                    .find(|c| c.name().to_string() == name)
                    .map_or(Value::Null, |c| cookie_value(c));
                reply(id, Ok(found.to_string()));
            }
            _ => {
                if action == "delete_cookie" && name.is_none() {
                    return reply(id, Err("name must be a non-empty string".into()));
                }
                let doomed: Vec<_> = sent
                    .into_iter()
                    .filter(|c| name.is_none_or(|name| c.name().to_string() == name))
                    .collect();
                delete_cookies(&target, doomed, id);
            }
        }
    });
    unsafe { store.getAllCookies(&handler) };
}

fn delete_cookies(store: &WKHTTPCookieStore, doomed: Vec<Retained<NSHTTPCookie>>, id: String) {
    if doomed.is_empty() {
        return reply(id, Ok("null".into()));
    }
    let left = Rc::new(Cell::new(doomed.len()));
    let id = Rc::new(Cell::new(Some(id)));
    for cookie in doomed {
        let (left, id) = (left.clone(), id.clone());
        let deleted = RcBlock::new(move || {
            left.set(left.get() - 1);
            if left.get() == 0
                && let Some(id) = id.take()
            {
                reply(id, Ok("null".into()));
            }
        });
        unsafe { store.deleteCookie_completionHandler(&cookie, Some(&deleted)) };
    }
}

fn add_cookie(
    store: &WKHTTPCookieStore,
    host: &str,
    params: &Map<String, Value>,
    id: String,
) -> Result<(), String> {
    let source = params
        .get("cookie")
        .and_then(Value::as_object)
        .unwrap_or(params);
    let text = |key: &str| source.get(key).and_then(Value::as_str);
    let name = text("name")
        .filter(|n| !n.is_empty())
        .ok_or("name must be a non-empty string")?;
    if host.is_empty() {
        return Err("current browser URL has no host".into());
    }
    let domain = match text("domain") {
        Some(domain) if !domain_matches(host, domain) => {
            return Err("cookie domain does not match the current browser host".into());
        }
        Some(domain) => domain.to_owned(),
        None => host.to_owned(),
    };
    let mut keys: Vec<&NSString> = vec![
        unsafe { NSHTTPCookieName },
        unsafe { NSHTTPCookieValue },
        unsafe { NSHTTPCookieDomain },
        unsafe { NSHTTPCookiePath },
    ];
    let mut values: Vec<Retained<AnyObject>> = vec![
        object(NSString::from_str(name)),
        object(NSString::from_str(text("value").unwrap_or(""))),
        object(NSString::from_str(&domain)),
        object(NSString::from_str(text("path").unwrap_or("/"))),
    ];
    if source.get("secure").and_then(Value::as_bool) == Some(true) {
        keys.push(unsafe { NSHTTPCookieSecure });
        values.push(object(NSString::from_str("TRUE")));
    }
    if source.get("httpOnly").and_then(Value::as_bool) == Some(true) {
        keys.push(ns_string!("HttpOnly"));
        values.push(object(NSString::from_str("TRUE")));
    }
    if let Some(same_site) = text("sameSite") {
        let policy = match same_site.to_ascii_lowercase().as_str() {
            "strict" => "strict",
            "none" => "none",
            _ => "lax",
        };
        keys.push(unsafe { NSHTTPCookieSameSitePolicy });
        values.push(object(NSString::from_str(policy)));
    }
    if let Some(expiry) = source.get("expiry").and_then(Value::as_i64) {
        keys.push(unsafe { NSHTTPCookieExpires });
        values.push(object(NSDate::dateWithTimeIntervalSince1970(expiry as f64)));
    }
    let properties = NSDictionary::from_retained_objects(&keys, &values);
    let cookie = unsafe { NSHTTPCookie::cookieWithProperties(&properties) }
        .ok_or("set browser cookie: the cookie is not valid")?;
    let id = Cell::new(Some(id));
    let stored = RcBlock::new(move || {
        if let Some(id) = id.take() {
            reply(id, Ok("null".into()));
        }
    });
    unsafe { store.setCookie_completionHandler(&cookie, Some(&stored)) };
    Ok(())
}

fn object<T: Message>(value: Retained<T>) -> Retained<AnyObject> {
    unsafe { Retained::cast_unchecked(value) }
}

fn cookie_value(cookie: &NSHTTPCookie) -> Value {
    let same_site = cookie.sameSitePolicy().map(|policy| {
        match policy.to_string().to_ascii_lowercase().as_str() {
            "strict" => "Strict",
            "none" => "None",
            _ => "Lax",
        }
    });
    json!({
        "name": cookie.name().to_string(),
        "value": cookie.value().to_string(),
        "path": cookie.path().to_string(),
        "domain": cookie.domain().to_string().trim_start_matches('.'),
        "secure": cookie.isSecure(),
        "httpOnly": cookie.isHTTPOnly(),
        "sameSite": same_site,
        "expiry": cookie.expiresDate().map(|date| date.timeIntervalSince1970() as i64),
    })
}

/// The host, path and whether the scheme is https, of an absolute URL.
fn url_parts(url: &str) -> (String, String, bool) {
    let (scheme, rest) = url.split_once("://").unwrap_or(("", url));
    let end = rest.find(['/', '?', '#']).unwrap_or(rest.len());
    let authority = &rest[..end];
    let host = authority
        .rsplit_once('@')
        .map_or(authority, |(_, host)| host);
    let host = match host.strip_prefix('[') {
        Some(inside) => inside.split_once(']').map_or(inside, |(name, _)| name),
        None => host.split_once(':').map_or(host, |(name, _)| name),
    };
    let path = rest[end..].split(['?', '#']).next().unwrap_or("");
    let path = if path.is_empty() { "/" } else { path };
    (
        host.to_ascii_lowercase(),
        path.to_owned(),
        scheme.eq_ignore_ascii_case("https"),
    )
}

fn domain_matches(host: &str, domain: &str) -> bool {
    let host = host.to_ascii_lowercase();
    let domain = domain.trim_start_matches('.').to_ascii_lowercase();
    !domain.is_empty() && (host == domain || host.ends_with(&format!(".{domain}")))
}

fn path_matches(request: &str, cookie: &str) -> bool {
    let cookie = if cookie.is_empty() { "/" } else { cookie };
    request == cookie
        || (request.starts_with(cookie)
            && (cookie.ends_with('/') || request.as_bytes().get(cookie.len()) == Some(&b'/')))
}

fn same<T: Message>(responder: &NSResponder, view: &T) -> bool {
    std::ptr::addr_eq(responder, view)
}

/// The key a page heard, as the window's keymap reads keys.
fn key_event(event: &NSEvent) -> Option<KeyEvent> {
    let flags = event.modifierFlags();
    let command = flags.contains(NSEventModifierFlags::Command);
    let mods = Mods {
        shift: flags.contains(NSEventModifierFlags::Shift),
        cmd: command,
        alt: flags.contains(NSEventModifierFlags::Option),
        ctrl: flags.contains(NSEventModifierFlags::Control),
        logo: command,
    };
    let typed = event.charactersIgnoringModifiers()?.to_string();
    let first = typed.chars().next()?;
    let key = match u32::from(first) {
        0xF700 => Key::Up,
        0xF701 => Key::Down,
        0xF702 => Key::Left,
        0xF703 => Key::Right,
        n @ 0xF704..=0xF71B => Key::Function(u8::try_from(n - 0xF704 + 1).ok()?),
        0xF728 => Key::Delete,
        0xF729 => Key::Home,
        0xF72B => Key::End,
        0xF72C => Key::PageUp,
        0xF72D => Key::PageDown,
        0x1B => Key::Escape,
        0x0D | 0x03 => Key::Enter,
        0x09 | 0x19 => Key::Tab,
        0x7F => Key::Backspace,
        _ if command => Key::Command(first.to_ascii_lowercase()),
        _ => Key::Text(typed),
    };
    Some(KeyEvent { key, mods })
}

define_class!(
    #[unsafe(super(NSView))]
    #[thread_kind = MainThreadOnly]
    #[name = "AttnBrowserPageClip"]
    struct PageClip;

    impl PageClip {
        #[unsafe(method(isFlipped))]
        fn is_flipped(&self) -> bool {
            true
        }
    }
);

impl PageClip {
    fn new(mtm: MainThreadMarker, frame: NSRect) -> Retained<Self> {
        unsafe { msg_send![Self::alloc(mtm), initWithFrame: frame] }
    }
}

define_class!(
    #[unsafe(super(WKWebView))]
    #[thread_kind = MainThreadOnly]
    #[name = "AttnBrowserPageView"]
    #[ivars = TileKey]
    struct PageView;

    impl PageView {
        #[unsafe(method(acceptsFirstMouse:))]
        fn accepts_first_mouse(&self, _event: Option<&NSEvent>) -> bool {
            true
        }

        #[unsafe(method(becomeFirstResponder))]
        fn become_first_responder(&self) -> bool {
            let became: bool = unsafe { msg_send![super(self), becomeFirstResponder] };
            if became && !HANDING.get() {
                tell(Heard::Focused { page: self.ivars().clone() });
            }
            became
        }

        #[unsafe(method(performKeyEquivalent:))]
        fn perform_key_equivalent(&self, event: &NSEvent) -> Bool {
            let window = self.window();
            let holds = window
                .as_ref()
                .and_then(|window| window.firstResponder())
                .is_some_and(|responder| same(&responder, self));
            let content = window.and_then(|window| window.contentView());
            let key = key_event(event).filter(|_| holds);
            if let Some(edit) = key.as_ref().and_then(super::page_edit) {
                self.edit(edit);
                return Bool::YES;
            }
            if let Some(content) = content
                && key.as_ref().is_some_and(super::claims_key)
            {
                content.keyDown(event);
                return Bool::YES;
            }
            unsafe { msg_send![super(self), performKeyEquivalent: event] }
        }
    }
);

impl PageView {
    /// Runs `edit` on the page, as AppKit's Edit menu would for a web view with the keyboard:
    /// undo and redo through the undo manager the page records its edits in, the others as
    /// the page's own commands.
    fn edit(&self, edit: super::PageEdit) {
        use super::PageEdit;
        let command = match edit {
            PageEdit::Undo | PageEdit::Redo => {
                let Some(manager) = self.undoManager() else {
                    return;
                };
                match edit {
                    PageEdit::Undo if manager.canUndo() => manager.undo(),
                    PageEdit::Redo if manager.canRedo() => manager.redo(),
                    _ => {}
                }
                return;
            }
            PageEdit::SelectAll => objc2::sel!(selectAll:),
            PageEdit::Copy => objc2::sel!(copy:),
            PageEdit::Cut => objc2::sel!(cut:),
            PageEdit::Paste => objc2::sel!(paste:),
        };
        let _: Bool = unsafe { msg_send![self, tryToPerform: command, with: self] };
    }

    fn new(
        mtm: MainThreadMarker,
        page: TileKey,
        frame: NSRect,
        config: &WKWebViewConfiguration,
    ) -> Retained<Self> {
        let this = Self::alloc(mtm).set_ivars(page);
        unsafe { msg_send![super(this), initWithFrame: frame, configuration: config] }
    }
}

define_class!(
    #[unsafe(super(NSObject))]
    #[thread_kind = MainThreadOnly]
    #[name = "AttnBrowserLocationWatch"]
    #[ivars = TileKey]
    struct LocationWatch;

    impl LocationWatch {
        #[unsafe(method(observeValueForKeyPath:ofObject:change:context:))]
        fn observe(
            &self,
            _path: Option<&NSString>,
            object: Option<&AnyObject>,
            _change: Option<&AnyObject>,
            _context: *mut c_void,
        ) {
            let Some(web) = object else { return };
            let url: Option<Retained<NSURL>> = unsafe { msg_send![web, URL] };
            let Some(url) = url.and_then(|url| url.absoluteString()) else {
                return;
            };
            let url = url.to_string();
            let scheme = url.split_once("://").map(|(s, _)| s.to_ascii_lowercase());
            if matches!(scheme.as_deref(), Some("http" | "https")) {
                tell(Heard::Located { page: self.ivars().clone(), url });
            }
        }
    }
);

impl LocationWatch {
    fn new(mtm: MainThreadMarker, page: TileKey) -> Retained<Self> {
        let this = Self::alloc(mtm).set_ivars(page);
        unsafe { msg_send![super(this), init] }
    }
}

define_class!(
    #[unsafe(super(NSObject))]
    #[thread_kind = MainThreadOnly]
    #[name = "AttnBrowserPageDialogs"]
    #[ivars = TileKey]
    struct PageDialogs;

    unsafe impl NSObjectProtocol for PageDialogs {}

    #[allow(non_snake_case)]
    unsafe impl WKUIDelegate for PageDialogs {
        #[unsafe(method(webView:runJavaScriptAlertPanelWithMessage:initiatedByFrame:completionHandler:))]
        fn webView_runJavaScriptAlertPanelWithMessage_initiatedByFrame_completionHandler(
            &self,
            _webview: &WKWebView,
            message: &NSString,
            _frame: &WKFrameInfo,
            completion: &block2::DynBlock<dyn Fn()>,
        ) {
            let Some(done) =
                (unsafe { RcBlock::<dyn Fn()>::copy(completion as *const _ as *mut _) })
            else {
                return;
            };
            let alert = Alert {
                kind: AlertKind::Alert,
                message: message.to_string(),
                default_text: None,
                close: Box::new(move |_| done.call(())),
            };
            answer_or_show(MainThreadMarker::from(self), self.ivars(), alert);
        }

        #[unsafe(method(webView:runJavaScriptConfirmPanelWithMessage:initiatedByFrame:completionHandler:))]
        fn webView_runJavaScriptConfirmPanelWithMessage_initiatedByFrame_completionHandler(
            &self,
            _webview: &WKWebView,
            message: &NSString,
            _frame: &WKFrameInfo,
            completion: &block2::DynBlock<dyn Fn(Bool)>,
        ) {
            let Some(done) =
                (unsafe { RcBlock::<dyn Fn(Bool)>::copy(completion as *const _ as *mut _) })
            else {
                return;
            };
            let alert = Alert {
                kind: AlertKind::Confirm,
                message: message.to_string(),
                default_text: None,
                close: Box::new(move |answer: Option<String>| {
                    done.call((Bool::new(answer.is_some()),))
                }),
            };
            answer_or_show(MainThreadMarker::from(self), self.ivars(), alert);
        }

        #[unsafe(method(webView:runJavaScriptTextInputPanelWithPrompt:defaultText:initiatedByFrame:completionHandler:))]
        fn webView_runJavaScriptTextInputPanelWithPrompt_defaultText_initiatedByFrame_completionHandler(
            &self,
            _webview: &WKWebView,
            prompt: &NSString,
            default_text: Option<&NSString>,
            _frame: &WKFrameInfo,
            completion: &block2::DynBlock<dyn Fn(*mut NSString)>,
        ) {
            let Some(done) = (unsafe {
                RcBlock::<dyn Fn(*mut NSString)>::copy(completion as *const _ as *mut _)
            }) else {
                return;
            };
            let alert = Alert {
                kind: AlertKind::Prompt,
                message: prompt.to_string(),
                default_text: default_text.map(NSString::to_string),
                close: Box::new(move |answer: Option<String>| match answer {
                    Some(text) => {
                        let text = NSString::from_str(&text);
                        done.call((Retained::as_ptr(&text).cast_mut(),));
                    }
                    None => done.call((std::ptr::null_mut(),)),
                }),
            };
            answer_or_show(MainThreadMarker::from(self), self.ivars(), alert);
        }
    }
);

/// An agent's action ended in `alert`, which waits for the agent, and the window wakes to tell
/// it; with no action running the person answers it.
fn answer_or_show(mtm: MainThreadMarker, page: &TileKey, alert: Alert) {
    match super::page_alert(page, alert) {
        None => wake(),
        Some(alert) => show(mtm, alert),
    }
}

impl PageDialogs {
    fn new(mtm: MainThreadMarker, page: TileKey) -> Retained<Self> {
        let this = Self::alloc(mtm).set_ivars(page);
        unsafe { msg_send![super(this), init] }
    }
}

/// Shows the person a dialog no agent is answering, and closes it with what they chose.
fn show(mtm: MainThreadMarker, alert: Alert) {
    let panel = NSAlert::new(mtm);
    panel.setMessageText(&NSString::from_str(&alert.message));
    panel.addButtonWithTitle(ns_string!("OK"));
    if alert.kind != AlertKind::Alert {
        panel.addButtonWithTitle(ns_string!("Cancel"));
    }
    let input = (alert.kind == AlertKind::Prompt).then(|| {
        let initial = NSString::from_str(alert.default_text.as_deref().unwrap_or(""));
        let input = NSTextField::textFieldWithString(&initial, mtm);
        input.setFrame(NSRect::new(
            NSPoint::new(0.0, 0.0),
            NSSize::new(320.0, 24.0),
        ));
        panel.setAccessoryView(Some(&input));
        input
    });
    let accepted = panel.runModal() == NSAlertFirstButtonReturn;
    let text = input.map_or_else(String::new, |input| input.stringValue().to_string());
    (alert.close)(accepted.then_some(text));
}

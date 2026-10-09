use objc2::ffi::{objc_setAssociatedObject, OBJC_ASSOCIATION_RETAIN_NONATOMIC};
use objc2::rc::{Retained, Weak};
use objc2::{define_class, msg_send, DefinedClass, MainThreadMarker, MainThreadOnly};
use objc2_app_kit::{NSEvent, NSEventModifierFlags, NSResponder, NSView};
use objc2_foundation::NSObjectProtocol;
use tauri::WebviewWindow;

const RETURN_KEY_CODE: u16 = 36;
static KEYBOARD_RESPONDER_KEY: u8 = 0;

struct KeyboardResponderIvars {
    view: Weak<NSView>,
}

define_class!(
    #[unsafe(super(NSResponder))]
    #[thread_kind = MainThreadOnly]
    #[name = "AttnMainKeyboardResponder"]
    #[ivars = KeyboardResponderIvars]
    struct KeyboardResponder;

    impl KeyboardResponder {
        #[unsafe(method(contextMenuKeyDown:))]
        fn context_menu_key_down(&self, event: &NSEvent) {
            let modifiers = event.modifierFlags()
                & (NSEventModifierFlags::Control
                    | NSEventModifierFlags::Shift
                    | NSEventModifierFlags::Option
                    | NSEventModifierFlags::Command);
            if event.keyCode() == RETURN_KEY_CODE
                && modifiers == NSEventModifierFlags::Control
            {
                if let Some(view) = self.ivars().view.load() {
                    let main_webview_focused = event.window(self.mtm()).is_some_and(|window| {
                        window.firstResponder().is_some_and(|responder| {
                            Retained::as_ptr(&responder).cast::<NSView>() == Retained::as_ptr(&view)
                        })
                    });
                    if main_webview_focused {
                        view.keyDown(event);
                        return;
                    }
                }
            }
            unsafe { msg_send![super(self), contextMenuKeyDown: event] }
        }
    }

    unsafe impl NSObjectProtocol for KeyboardResponder {}
);

pub fn install(window: &WebviewWindow) -> tauri::Result<()> {
    window.with_webview(|platform| unsafe {
        let mtm = MainThreadMarker::new().expect("webview setup runs on the main thread");
        let view: Retained<NSView> =
            Retained::retain(platform.inner().cast()).expect("wry provides a live webview");
        let responder = KeyboardResponder::alloc(mtm).set_ivars(KeyboardResponderIvars {
            view: Weak::from_retained(&view),
        });
        let responder: Retained<KeyboardResponder> = msg_send![super(responder), init];
        responder.setNextResponder(view.nextResponder().as_deref());
        objc_setAssociatedObject(
            Retained::as_ptr(&view).cast_mut().cast(),
            std::ptr::addr_of!(KEYBOARD_RESPONDER_KEY).cast(),
            Retained::as_ptr(&responder).cast_mut().cast(),
            OBJC_ASSOCIATION_RETAIN_NONATOMIC,
        );
        view.setNextResponder(Some(&responder));
    })
}

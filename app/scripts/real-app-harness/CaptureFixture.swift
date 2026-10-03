import AppKit
import Carbon

final class ScreenshotView: NSImageView, NSDraggingSource {
    var fileURL: URL!
    override func mouseDown(with event: NSEvent) {
        let manifest = fileURL.deletingLastPathComponent().appendingPathComponent("drag-files.json")
        let files = FileManager.default.fileExists(atPath: manifest.path)
            ? (try! JSONSerialization.jsonObject(with: Data(contentsOf: manifest)) as! [String]).map { URL(fileURLWithPath: $0) }
            : [fileURL!]
        let items = files.map { url in
            let item = NSDraggingItem(pasteboardWriter: url as NSURL)
            item.setDraggingFrame(bounds, contents: image)
            return item
        }
        beginDraggingSession(with: items, event: event, source: self)
    }
    func draggingSession(_ session: NSDraggingSession, sourceOperationMaskFor context: NSDraggingContext) -> NSDragOperation { .copy }
}
final class Fixture: NSObject, NSApplicationDelegate, NSWindowDelegate {
    var window: NSWindow!
    var image: NSImage!
    var originalClipboard: [NSPasteboardItem] = []
    var conflict: EventHotKeyRef?
    var keyReceipts: [[String: Any]] = []
    var keyMonitor: Any?
    var copiedClipboardCount: Int?
    let directory = URL(fileURLWithPath: ProcessInfo.processInfo.environment["CAPTURE_FIXTURE_DIR"]!)
    func applicationDidFinishLaunching(_ notification: Notification) {
        keyMonitor = NSEvent.addLocalMonitorForEvents(matching: [.keyDown, .keyUp]) { [weak self] event in
            self?.keyReceipts.append(["at": Int64(Date().timeIntervalSince1970 * 1000), "type": event.type.rawValue,
                "keyCode": event.keyCode, "characters": event.characters ?? "", "flags": event.modifierFlags.rawValue])
            return event
        }
        originalClipboard = (NSPasteboard.general.pasteboardItems ?? []).map { original in
            let copy = NSPasteboardItem()
            for type in original.types { if let data = original.data(forType: type) { copy.setData(data, forType: type) } }
            return copy
        }
        if ProcessInfo.processInfo.environment["CAPTURE_FIXTURE_NO_HOTKEY"] != "1" {
            let status = RegisterEventHotKey(UInt32(kVK_ANSI_J), UInt32(controlKey | optionKey), EventHotKeyID(signature: 0x51434150, id: 1), GetApplicationEventTarget(), UInt32(kEventHotKeyExclusive), &conflict)
            if status != noErr { fputs("Fixture conflict registration failed: \(status)\n", stderr); exit(1) }
        }
        var symbolicKeys: Unmanaged<CFArray>?
        let symbolicStatus = CopySymbolicHotKeys(&symbolicKeys)
        guard symbolicStatus == noErr, let keys = symbolicKeys?.takeRetainedValue() as? [[String: Any]] else {
            fatalError("Cannot inspect native system shortcut fixture: \(symbolicStatus)")
        }
        if let shortcut = keys.first(where: {
            ($0[kHISymbolicHotKeyEnabled] as? Bool) == true &&
            ($0[kHISymbolicHotKeyCode] as? NSNumber)?.intValue == Int(kVK_Space)
        }), let modifiers = shortcut[kHISymbolicHotKeyModifiers] as? NSNumber {
            let flags = modifiers.uint32Value
            let names = [(UInt32(controlKey), "Control"), (UInt32(optionKey), "Alt"), (UInt32(cmdKey), "Super"), (UInt32(shiftKey), "Shift")]
            let binding = (names.filter { flags & $0.0 != 0 }.map { $0.1 } + ["Space"]).joined(separator: "+")
            try! Data(binding.utf8).write(to: directory.appendingPathComponent("system-shortcut.txt"))
        }
        image = NSImage(size: NSSize(width: 640, height: 360))
        image.lockFocus()
        NSColor(calibratedRed: 0.15, green: 0.23, blue: 0.2, alpha: 1).setFill()
        NSBezierPath(rect: NSRect(x: 0, y: 0, width: 640, height: 360)).fill()
        ("SYNTHETIC SCREENSHOT" as NSString).draw(at: NSPoint(x: 40, y: 260), withAttributes: [.font: NSFont.monospacedSystemFont(ofSize: 24, weight: .bold), .foregroundColor: NSColor.white])
        ("Track the launch checklist\nNo private data · Quick Capture fixture" as NSString).draw(at: NSPoint(x: 40, y: 150), withAttributes: [.font: NSFont.systemFont(ofSize: 22), .foregroundColor: NSColor.white])
        image.unlockFocus()
        let png = NSBitmapImageRep(data: image.tiffRepresentation!)!.representation(using: .png, properties: [:])!
        let url = directory.appendingPathComponent("synthetic-screenshot.png")
        try! png.write(to: url)
        window = NSWindow(contentRect: NSRect(x: 80, y: 140, width: 700, height: 500), styleMask: [.titled, .closable, .resizable, .miniaturizable], backing: .buffered, defer: false)
        window.delegate = self
        window.title = "Capture Fixture · synthetic only"
        window.collectionBehavior = [.fullScreenPrimary]
        let view = ScreenshotView(frame: NSRect(x: 40, y: 80, width: 620, height: 350))
        view.image = image; view.fileURL = url; view.imageScaling = .scaleProportionallyUpOrDown
        window.contentView!.addSubview(view)
        let label = NSTextField(labelWithString: "⌃⌥Space opens capture · ⌘C copies fixture · drag image into capture")
        label.frame = NSRect(x: 30, y: 20, width: 650, height: 35)
        window.contentView!.addSubview(label)
        let menu = NSMenu()
        let appItem = NSMenuItem(); menu.addItem(appItem)
        let appMenu = NSMenu(); appItem.submenu = appMenu
        appMenu.addItem(withTitle: "Quit Fixture", action: #selector(NSApplication.terminate(_:)), keyEquivalent: "q")
        let editItem = NSMenuItem(); menu.addItem(editItem)
        let edit = NSMenu(title: "Edit"); editItem.submenu = edit
        let copy = NSMenuItem(title: "Copy synthetic screenshot", action: #selector(copyImage), keyEquivalent: "c")
        copy.target = self; edit.addItem(copy)
        let viewItem = NSMenuItem(); menu.addItem(viewItem)
        let viewMenu = NSMenu(title: "View"); viewItem.submenu = viewMenu
        let full = NSMenuItem(title: "Toggle Full Screen", action: #selector(NSWindow.toggleFullScreen(_:)), keyEquivalent: "f")
        full.keyEquivalentModifierMask = [.command, .control]; full.target = window; viewMenu.addItem(full)
        NSApplication.shared.mainMenu = menu
        window.makeKeyAndOrderFront(nil)
        NSApplication.shared.activate(ignoringOtherApps: true)
        let readyFile = ProcessInfo.processInfo.environment["CAPTURE_FIXTURE_READY_FILE"] ?? "fixture-ready"
        try! Data("ready".utf8).write(to: directory.appendingPathComponent(readyFile))
    }
    func windowDidEnterFullScreen(_ notification: Notification) { try! Data("entered".utf8).write(to: directory.appendingPathComponent("fullscreen-entered")) }
    func windowDidExitFullScreen(_ notification: Notification) { try! Data("exited".utf8).write(to: directory.appendingPathComponent("fullscreen-exited")) }
    func applicationWillTerminate(_ notification: Notification) {
        if let keyMonitor { NSEvent.removeMonitor(keyMonitor) }
        let name = ProcessInfo.processInfo.environment["CAPTURE_FIXTURE_READY_FILE"] ?? "fixture-ready"
        try! JSONSerialization.data(withJSONObject: keyReceipts).write(to: directory.appendingPathComponent("key-events-\(name).json"))
        if copiedClipboardCount == NSPasteboard.general.changeCount {
            NSPasteboard.general.clearContents()
            NSPasteboard.general.writeObjects(originalClipboard)
        }
        if let conflict { UnregisterEventHotKey(conflict) }
    }
    @objc func copyImage() { NSPasteboard.general.clearContents(); NSPasteboard.general.writeObjects([image]); copiedClipboardCount = NSPasteboard.general.changeCount }
}
let app = NSApplication.shared
let fixture = Fixture()
app.delegate = fixture
app.setActivationPolicy(.regular)
app.run()

import XCTest

// Drives the spike app through the real iOS keyboard. The app is launched outside the test
// (`xcrun simctl launch --console-pty`) so its console log is captured; the test attaches with activate().
final class SpikeUITests: XCTestCase {
    let app = XCUIApplication(bundleIdentifier: "dev.attn.phonespike")
    var shotDir: String { ProcessInfo.processInfo.environment["SPIKE_SHOT_DIR"] ?? NSTemporaryDirectory() }
    var env: [String: String] { ProcessInfo.processInfo.environment }

    override func setUp() { continueAfterFailure = true; app.activate() }

    func shot(_ name: String) {
        let data = XCUIScreen.main.screenshot().pngRepresentation
        try? data.write(to: URL(fileURLWithPath: "\(shotDir)/\(name).png"))
    }

    func button(_ label: String) -> XCUIElement { app.buttons[label].firstMatch }

    func openSession(input: String, size: String) {
        if button("‹").exists { button("‹").tap() }
        let row = app.staticTexts["fizzbuzz demo"].firstMatch
        XCTAssertTrue(row.waitForExistence(timeout: 10), "session list did not show fizzbuzz demo")
        // WKWebView drops a tap that lands within the double-tap window of the previous one, so space them out.
        button(input).tap(); sleep(1)
        button(size).tap(); sleep(1)
        row.tap()
        let opened = app.staticTexts.containing(NSPredicate(format: "label CONTAINS ' kb '")).firstMatch
        if !opened.waitForExistence(timeout: 5) { row.tap(); _ = opened.waitForExistence(timeout: 5) }
        sleep(2)
    }

    /// Taps one soft-keyboard key per character. Uppercase via shift, space via "space".
    func tapKeys(_ text: String) {
        let kb = app.keyboards.firstMatch
        for ch in text {
            let s = String(ch)
            if s == " " { kb.keys["space"].tap(); continue }
            if s == "⌫" { kb.keys["delete"].tap(); continue }
            if s == "⏎" {
                let ret = kb.buttons.matching(NSPredicate(format: "label IN {'return','Return','send','Send','go'}")).firstMatch
                if ret.exists { ret.tap() } else { kb.keys["Return"].tap() }
                continue
            }
            if s == "." {
                if !kb.keys["."].exists { kb.keys["more"].tap() }
                kb.keys["."].tap(); continue
            }
            // Key identifiers follow the shift state ("A" after autocapitalize, "a" otherwise).
            let candidates = [s, s.uppercased(), s.lowercased()]
            if let k = candidates.map({ kb.keys[$0] }).first(where: { $0.exists }) { k.tap() }
            else { print("SPIKE_TEST key \(s) not found; keys: \(kb.keys.allElementsBoundByIndex.map { $0.identifier })"); XCTFail("key \(s)") }
        }
    }

    /// Long-presses "e" and drags straight up: the first variant above the key is "é" (measured by testAccentProbe).
    func tapAccentE() {
        let kb = app.keyboards.firstMatch
        let e = kb.keys["e"].exists ? kb.keys["e"] : kb.keys["E"]
        let from = e.coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: 0.5))
        from.press(forDuration: 1.0, thenDragTo: from.withOffset(CGVector(dx: 0, dy: -e.frame.height * 1.1)))
    }

    func focusInput(input: String) {
        if input == "(b) composer" {
            app.textViews.firstMatch.tap()
        } else {
            app.textViews["Terminal input"].firstMatch.exists ? app.textViews["Terminal input"].firstMatch.tap() : app.otherElements["Terminal input"].firstMatch.tap()
        }
        _ = app.keyboards.firstMatch.waitForExistence(timeout: 5)
        sleep(1)
    }

    // One scenario per env: SPIKE_INPUT=(a) direct|(b) composer, SPIKE_SIZE=fit PTY|desktop size, SPIKE_TAG=name
    func testSoftKeyboardTyping() {
        let input = env["SPIKE_INPUT"] ?? "(b) composer"
        let size = env["SPIKE_SIZE"] ?? "desktop size"
        let tag = env["SPIKE_TAG"] ?? "run"
        openSession(input: input, size: size)
        shot("\(tag)-1-open")
        if !button("log").isSelected { button("log").tap(); sleep(1) }
        focusInput(input: input)
        shot("\(tag)-2-keyboard")
        print("SPIKE_TEST begin typing \(tag)")
        // "teh" is a typo autocorrect should fix; "fizx⌫z" backspaces mid-word; "café" uses the long-press accent popup.
        tapKeys("add teh docstring to fizx⌫z caf")
        tapAccentE()
        shot("\(tag)-3-typed")
        tapKeys("⏎")
        sleep(2)
        shot("\(tag)-4-sent")
        print("SPIKE_TEST end typing \(tag)")
    }

    // Hardware keyboard: typeText goes through the connected hardware keyboard path.
    func testHardwareKeyboardTyping() {
        let input = env["SPIKE_INPUT"] ?? "(b) composer"
        let size = env["SPIKE_SIZE"] ?? "desktop size"
        let tag = env["SPIKE_TAG"] ?? "hw"
        openSession(input: input, size: size)
        if !button("log").isSelected { button("log").tap(); sleep(1) }
        focusInput(input: input)
        shot("\(tag)-1-focused")
        print("SPIKE_TEST begin hw typing \(tag)")
        let target = input == "(b) composer" ? app.textViews.firstMatch : app.descendants(matching: .any)["Terminal input"].firstMatch
        target.typeText("add teh docstring to fizx")
        target.typeText(XCUIKeyboardKey.delete.rawValue)
        target.typeText("z café")
        shot("\(tag)-2-typed")
        target.typeText("\n")
        sleep(2)
        shot("\(tag)-3-sent")
        print("SPIKE_TEST end hw typing \(tag)")
    }

    // Keys bar: Shift-Tab cycles Claude Code modes; Esc clears.
    func testKeysBar() {
        let input = env["SPIKE_INPUT"] ?? "(b) composer"
        let size = env["SPIKE_SIZE"] ?? "desktop size"
        let tag = env["SPIKE_TAG"] ?? "keys"
        openSession(input: input, size: size)
        if !button("log").isSelected { button("log").tap() }
        shot("\(tag)-0-before")
        let shiftTabs = Int(env["SPIKE_SHIFTTABS"] ?? "0") ?? 0
        for i in 0..<shiftTabs {
            button("⇧Tab").tap(); sleep(1); shot("\(tag)-shifttab-\(i + 1)")
            print("SPIKE_TEST after shift-tab \(i + 1)")
        }
        if let k = env["SPIKE_ANSWER"] { button(k).tap(); sleep(2); shot("\(tag)-answered-\(k)") }
    }

    /// The header line: "<state> · <status> · kb <px> · vv <px>".
    func header() -> String {
        let q = app.staticTexts.containing(NSPredicate(format: "label CONTAINS ' kb '")).firstMatch
        return q.exists ? q.label : "(header not found)"
    }

    // Opens a terminal; optionally opens the keyboard, closes it again, and holds still for idle measurements.
    func testOpenIdle() {
        let input = env["SPIKE_INPUT"] ?? "(b) composer"
        let size = env["SPIKE_SIZE"] ?? "desktop size"
        let tag = env["SPIKE_TAG"] ?? "idle"
        openSession(input: input, size: size)
        sleep(1)
        print("SPIKE_TEST header open: \(header())")
        shot("\(tag)-open")
        if env["SPIKE_FOCUS"] == "1" {
            focusInput(input: input)
            sleep(1)
            print("SPIKE_TEST header keyboard: \(header())")
            shot("\(tag)-keyboard")
            // Try to close the keyboard: first the session title, then the terminal area above the keys bar.
            app.staticTexts["fizzbuzz demo"].firstMatch.tap()
            sleep(2)
            print("SPIKE_TEST header after title tap: \(header())")
            app.coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: 0.25)).tap()
            sleep(2)
            print("SPIKE_TEST header after terminal tap: \(header()) keyboardVisible=\(app.keyboards.firstMatch.exists)")
            button("Hide keyboard").tap()
            sleep(2)
            print("SPIKE_TEST header closed: \(header()) keyboardVisible=\(app.keyboards.firstMatch.exists)")
            shot("\(tag)-closed")
        }
        if env["SPIKE_PINCH"] == "1" {
            // Pinch to zoom in, pan with one finger, then double-tap to fit the width again.
            let area = app.coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: 0.45))
            app.webViews.firstMatch.pinch(withScale: 2.5, velocity: 2)
            sleep(1); shot("\(tag)-pinched")
            area.press(forDuration: 0.1, thenDragTo: area.withOffset(CGVector(dx: -150, dy: 120)))
            sleep(1); shot("\(tag)-panned")
            area.doubleTap()
            sleep(1); shot("\(tag)-reset")
        }
        if let secs = env["SPIKE_HOLD"].flatMap(Double.init) {
            print("SPIKE_TEST hold begin")
            Thread.sleep(forTimeInterval: secs)
            print("SPIKE_TEST hold end")
            shot("\(tag)-held")
        }
    }

    // Opens a terminal, turns on the log and focuses the input, then exits so a real hardware keyboard
    // (Mac keystrokes into the Simulator window) can type. XCUITest disconnects the hardware keyboard while it runs.
    func testOpenFocus() {
        let input = env["SPIKE_INPUT"] ?? "(b) composer"
        openSession(input: input, size: env["SPIKE_SIZE"] ?? "desktop size")
        if !button("log").isSelected { button("log").tap(); sleep(1) }
        focusInput(input: input)
    }

    // Finds where the accented variants sit in the long-press popup of "e". Types into the composer, never sends.
    func testAccentProbe() {
        openSession(input: "(b) composer", size: "desktop size")
        focusInput(input: "(b) composer")
        let tv = app.textViews.firstMatch
        let kb = app.keyboards.firstMatch
        for dxKeys in [-1.0, 0.0, 1.0, 2.0, 3.0, 4.0, 5.0, 6.0] {
            let e = kb.keys["e"].exists ? kb.keys["e"] : kb.keys["E"]
            let w = e.frame.width
            let from = e.coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: 0.5))
            let to = from.withOffset(CGVector(dx: dxKeys * w, dy: -e.frame.height * 1.1))
            from.press(forDuration: 1.0, thenDragTo: to)
            sleep(1)
            print("SPIKE_TEST accent dx=\(dxKeys) value=\(String(describing: tv.value))")
        }
        shot("accent-probe")
        for _ in 0..<12 { kb.keys["delete"].tap() }
    }

    func testDumpTree() { print(app.debugDescription) }
}

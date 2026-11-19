import CoreGraphics
import Foundation

let arguments = CommandLine.arguments
guard arguments.count == 3, let width = Int(arguments[1]), let height = Int(arguments[2]) else {
  FileHandle.standardError.write("usage: ci-macos-display-mode.swift <width> <height>\n".data(using: .utf8)!)
  exit(2)
}

let display = CGMainDisplayID()
let listing = [kCGDisplayShowDuplicateLowResolutionModes: kCFBooleanTrue] as CFDictionary
let modes = CGDisplayCopyAllDisplayModes(display, listing) as? [CGDisplayMode] ?? []
guard let mode = modes.first(where: { $0.width == width && $0.height == height }) else {
  let offered = modes.map { "\($0.width)x\($0.height)" }.joined(separator: " ")
  FileHandle.standardError.write("the main display offers no \(width)x\(height) mode; it offers \(offered)\n".data(using: .utf8)!)
  exit(1)
}

var config: CGDisplayConfigRef?
guard CGBeginDisplayConfiguration(&config) == .success,
      CGConfigureDisplayWithDisplayMode(config, display, mode, nil) == .success,
      CGCompleteDisplayConfiguration(config, .permanently) == .success else {
  FileHandle.standardError.write("the main display refused the \(width)x\(height) mode\n".data(using: .utf8)!)
  exit(1)
}

let bounds = CGDisplayBounds(display)
print("main display: \(Int(bounds.width))x\(Int(bounds.height))")
guard Int(bounds.width) == width, Int(bounds.height) == height else { exit(1) }

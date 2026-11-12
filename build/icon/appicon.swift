// Copyright (C) 2026 Mohsen Mirzakhani
// SPDX-License-Identifier: AGPL-3.0-only

// Draws the hm app icon: "Hm" on an ink squircle with a coral dot.
//
//   swift build/icon/appicon.swift build/appicon-1024.png
//
// The canvas follows the macOS icon grid: a 1024 px image with the
// rounded square inset 100 px, so the Dock shows it at the same size as
// system icons.
import AppKit

let out = CommandLine.arguments.count > 1 ? CommandLine.arguments[1] : "appicon-1024.png"
let size: CGFloat = 1024
let inset: CGFloat = 100
let radius: CGFloat = 185

// Palette.
let inkTop = NSColor(srgbRed: 0x23 / 255, green: 0x27 / 255, blue: 0x36 / 255, alpha: 1) // #232736
let inkBottom = NSColor(srgbRed: 0x12 / 255, green: 0x14 / 255, blue: 0x1C / 255, alpha: 1) // #12141C
let paper = NSColor(srgbRed: 0xF4 / 255, green: 0xEF / 255, blue: 0xE6 / 255, alpha: 1) // #F4EFE6
let coral = NSColor(srgbRed: 0xFF / 255, green: 0x6B / 255, blue: 0x4A / 255, alpha: 1) // #FF6B4A

let rep = NSBitmapImageRep(
    bitmapDataPlanes: nil, pixelsWide: Int(size), pixelsHigh: Int(size),
    bitsPerSample: 8, samplesPerPixel: 4, hasAlpha: true, isPlanar: false,
    colorSpaceName: .deviceRGB, bytesPerRow: 0, bitsPerPixel: 0)!
rep.size = NSSize(width: size, height: size)
NSGraphicsContext.saveGraphicsState()
NSGraphicsContext.current = NSGraphicsContext(bitmapImageRep: rep)

let tile = NSRect(x: inset, y: inset, width: size - 2 * inset, height: size - 2 * inset)
let shape = NSBezierPath(roundedRect: tile, xRadius: radius, yRadius: radius)

// Soft drop shadow under the tile, as on system icons.
NSGraphicsContext.saveGraphicsState()
let shadow = NSShadow()
shadow.shadowColor = NSColor.black.withAlphaComponent(0.35)
shadow.shadowOffset = NSSize(width: 0, height: -12)
shadow.shadowBlurRadius = 28
shadow.set()
inkBottom.setFill()
shape.fill()
NSGraphicsContext.restoreGraphicsState()

NSGradient(starting: inkTop, ending: inkBottom)!.draw(in: shape, angle: -90)

// A faint inner rim keeps the dark tile from melting into dark docks.
NSColor.white.withAlphaComponent(0.08).setStroke()
let rim = NSBezierPath(roundedRect: tile.insetBy(dx: 2, dy: 2), xRadius: radius - 2, yRadius: radius - 2)
rim.lineWidth = 4
rim.stroke()

// "Hm" in paper, set as one line so kerning stays natural. The heavy H
// reads wider than the m, so the m gets a touch of horizontal expansion
// (which also thickens its stems toward the H's) and a little more room.
let font = NSFont.systemFont(ofSize: 360, weight: .heavy)
let text = NSMutableAttributedString(string: "Hm", attributes: [
    .font: font,
    .foregroundColor: paper,
])
text.addAttribute(.kern, value: -4, range: NSRange(location: 0, length: 1))
text.addAttribute(.expansion, value: 0.07, range: NSRange(location: 1, length: 1))
// Ink bounds, not the line box, so the dot can be placed against the
// m's actual right edge.
let ink = text.boundingRect(with: NSSize(width: size, height: size), options: [.usesLineFragmentOrigin, .usesDeviceMetrics])

// The coral dot is drawn by hand rather than set as a period: a period
// tucks tight against the m and sits on the baseline, so at 16-32 px it
// smears into the m's last stem. A clear gap and a slight lift keep it a
// distinct dot at every size.
let dot: CGFloat = 76
let gap: CGFloat = 40
let lift: CGFloat = 6
let width = ink.width + gap + dot

// Center on the cap height rather than the line box so the word sits
// optically in the middle.
let capMid = font.capHeight / 2
let baseline = size / 2 - capMid
let origin = NSPoint(x: (size - width) / 2 - ink.minX, y: baseline + font.descender)
text.draw(at: origin)

coral.setFill()
NSBezierPath(ovalIn: NSRect(
    x: origin.x + ink.maxX + gap, y: baseline + lift,
    width: dot, height: dot)).fill()

NSGraphicsContext.restoreGraphicsState()

guard let png = rep.representation(using: .png, properties: [:]) else {
    fatalError("could not encode png")
}
try png.write(to: URL(fileURLWithPath: out))
print("wrote \(out)")

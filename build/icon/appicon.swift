// Draws the hm app icon: "Hm." on an ink squircle, the dot in coral.
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

// "Hm" in paper, "." in coral, set as one line so kerning stays natural.
let font = NSFont.systemFont(ofSize: 360, weight: .heavy)
let text = NSMutableAttributedString(string: "Hm.", attributes: [
    .font: font,
    .foregroundColor: paper,
    .kern: -12,
])
text.addAttribute(.foregroundColor, value: coral, range: NSRange(location: 2, length: 1))
let bounds = text.boundingRect(with: NSSize(width: size, height: size), options: [.usesLineFragmentOrigin])
// Center on the cap height rather than the line box so the word sits
// optically in the middle.
let capMid = font.capHeight / 2
let origin = NSPoint(
    x: (size - bounds.width) / 2 - bounds.minX,
    y: size / 2 - capMid + font.descender)
text.draw(at: origin)

NSGraphicsContext.restoreGraphicsState()

guard let png = rep.representation(using: .png, properties: [:]) else {
    fatalError("could not encode png")
}
try png.write(to: URL(fileURLWithPath: out))
print("wrote \(out)")

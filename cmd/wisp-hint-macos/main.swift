import Cocoa

final class HintPanel: NSPanel {
    override var canBecomeKey: Bool { false }
    override var canBecomeMain: Bool { false }
}

struct HintRow {
    let key: String
    let label: String
}

struct HexwarePalette {
    static let canvas = NSColor(hex: "#050812")
    static let base = NSColor(hex: "#0b111b")
    static let raised = NSColor(hex: "#101925")
    static let neutral = NSColor(hex: "#1b2633")
    static let text = NSColor(hex: "#f4f7fb")
    static let muted = NSColor(hex: "#9aa9ba")
    static let ember = NSColor(hex: "#ff8a3d")
    static let frost = NSColor(hex: "#27a8ff")
    static let signal = NSColor(hex: "#ff5dbb")
}

extension NSColor {
    convenience init(hex: String, alpha: CGFloat = 1) {
        let clean = hex.trimmingCharacters(in: CharacterSet(charactersIn: "#"))
        var value: UInt64 = 0
        Scanner(string: clean).scanHexInt64(&value)
        let red = CGFloat((value >> 16) & 0xff) / 255
        let green = CGFloat((value >> 8) & 0xff) / 255
        let blue = CGFloat(value & 0xff) / 255
        self.init(srgbRed: red, green: green, blue: blue, alpha: alpha)
    }
}

final class HexwareChromeView: NSView {
    private let cut: CGFloat = 18

    override var isOpaque: Bool { false }

    override func draw(_ dirtyRect: NSRect) {
        let rect = bounds.insetBy(dx: 1.5, dy: 1.5)
        let path = hexPath(in: rect)

        NSGraphicsContext.saveGraphicsState()
        path.addClip()

        NSGradient(colors: [HexwarePalette.raised, HexwarePalette.canvas])?.draw(in: rect, angle: -90)

        HexwarePalette.frost.withAlphaComponent(0.10).setFill()
        NSBezierPath(ovalIn: NSRect(x: rect.minX - 80, y: rect.maxY - 90, width: 260, height: 180)).fill()
        HexwarePalette.ember.withAlphaComponent(0.12).setFill()
        NSBezierPath(ovalIn: NSRect(x: rect.maxX - 220, y: rect.minY - 80, width: 260, height: 180)).fill()
        HexwarePalette.signal.withAlphaComponent(0.07).setFill()
        NSBezierPath(ovalIn: NSRect(x: rect.midX - 140, y: rect.midY - 70, width: 280, height: 140)).fill()

        HexwarePalette.text.withAlphaComponent(0.035).setStroke()
        for x in stride(from: rect.minX, through: rect.maxX, by: 48) {
            let line = NSBezierPath()
            line.move(to: NSPoint(x: x, y: rect.minY))
            line.line(to: NSPoint(x: x + 60, y: rect.maxY))
            line.lineWidth = 1
            line.stroke()
        }

        HexwarePalette.frost.withAlphaComponent(0.16).setFill()
        NSRect(x: rect.minX + 30, y: rect.maxY - 3, width: rect.width * 0.34, height: 1.5).fill()
        HexwarePalette.ember.withAlphaComponent(0.22).setFill()
        NSRect(x: rect.maxX - rect.width * 0.32 - 30, y: rect.minY + 2, width: rect.width * 0.32, height: 1.5).fill()

        NSGraphicsContext.restoreGraphicsState()

        HexwarePalette.frost.withAlphaComponent(0.72).setStroke()
        path.lineWidth = 1.2
        path.stroke()

        HexwarePalette.ember.withAlphaComponent(0.42).setStroke()
        let inner = hexPath(in: rect.insetBy(dx: 4, dy: 4))
        inner.lineWidth = 0.8
        inner.stroke()
    }

    private func hexPath(in rect: NSRect) -> NSBezierPath {
        let path = NSBezierPath()
        path.move(to: NSPoint(x: rect.minX + cut, y: rect.maxY))
        path.line(to: NSPoint(x: rect.maxX - cut, y: rect.maxY))
        path.line(to: NSPoint(x: rect.maxX, y: rect.maxY - cut))
        path.line(to: NSPoint(x: rect.maxX, y: rect.minY + cut))
        path.line(to: NSPoint(x: rect.maxX - cut, y: rect.minY))
        path.line(to: NSPoint(x: rect.minX + cut, y: rect.minY))
        path.line(to: NSPoint(x: rect.minX, y: rect.minY + cut))
        path.line(to: NSPoint(x: rect.minX, y: rect.maxY - cut))
        path.close()
        return path
    }
}

func envDouble(_ name: String, _ fallback: Double) -> Double {
    guard let value = ProcessInfo.processInfo.environment[name], let parsed = Double(value) else {
        return fallback
    }
    return parsed
}

func envString(_ name: String, _ fallback: String) -> String {
    ProcessInfo.processInfo.environment[name] ?? fallback
}

func activeScreen() -> NSScreen {
    let mouse = NSEvent.mouseLocation
    return NSScreen.screens.first { screen in
        NSMouseInRect(mouse, screen.frame, false)
    } ?? NSScreen.main ?? NSScreen.screens.first!
}

func origin(for position: String, size: NSSize, screen: NSScreen, margin: CGFloat) -> NSPoint {
    let frame = screen.visibleFrame
    switch position.lowercased() {
    case "center":
        return NSPoint(x: frame.midX - size.width / 2, y: frame.midY - size.height / 2)
    case "top-left":
        return NSPoint(x: frame.minX + margin, y: frame.maxY - size.height - margin)
    case "top-right":
        return NSPoint(x: frame.maxX - size.width - margin, y: frame.maxY - size.height - margin)
    case "bottom-left":
        return NSPoint(x: frame.minX + margin, y: frame.minY + margin)
    case "bottom-center":
        return NSPoint(x: frame.midX - size.width / 2, y: frame.minY + margin)
    case "bottom-right":
        return NSPoint(x: frame.maxX - size.width - margin, y: frame.minY + margin)
    default:
        return NSPoint(x: frame.midX - size.width / 2, y: frame.maxY - size.height - margin)
    }
}

func parseHint(_ text: String) -> (title: String, rows: [HintRow]) {
    let lines = text.split(separator: "\n", omittingEmptySubsequences: true).map(String.init)
    let title = lines.first ?? "Wisp"
    let rows = lines.dropFirst().compactMap { line -> HintRow? in
        let trimmed = line.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !trimmed.isEmpty else { return nil }
        let parts = trimmed.split(maxSplits: 1, whereSeparator: { $0 == " " || $0 == "\t" }).map(String.init)
        guard parts.count == 2 else { return HintRow(key: trimmed, label: "") }
        return HintRow(key: parts[0], label: parts[1].trimmingCharacters(in: .whitespaces))
    }
    return (title, rows)
}

func addLabel(_ text: String, frame: NSRect, font: NSFont, color: NSColor, alignment: NSTextAlignment = .left) -> NSTextField {
    let label = NSTextField(labelWithString: text)
    label.frame = frame
    label.font = font
    label.textColor = color
    label.alignment = alignment
    label.backgroundColor = .clear
    label.isBordered = false
    label.isEditable = false
    label.isSelectable = false
    label.lineBreakMode = .byTruncatingTail
    label.maximumNumberOfLines = 1
    return label
}

let envText = ProcessInfo.processInfo.environment["WISP_HINT_TEXT"]
let input = FileHandle.standardInput.readDataToEndOfFile()
let stdinText = String(data: input, encoding: .utf8)?.trimmingCharacters(in: .whitespacesAndNewlines)
let text = envText ?? stdinText ?? "Wisp"
let parsed = parseHint(text)

let duration = envDouble("WISP_HINT_DURATION", 0)
let windowWidth = CGFloat(envDouble("WISP_HINT_WIDTH", 760))
let padding = CGFloat(envDouble("WISP_HINT_PADDING", 24))
let margin = CGFloat(envDouble("WISP_HINT_MARGIN", 72))
let position = envString("WISP_HINT_POSITION", "top-center")
let fontSize = CGFloat(envDouble("WISP_HINT_FONT_SIZE", 15))
let rowHeight = CGFloat(envDouble("WISP_HINT_ROW_HEIGHT", 30))
let columnGap: CGFloat = 30
let titleHeight: CGFloat = 34
let ruleGap: CGFloat = 13
let columns = parsed.rows.count > 4 ? 2 : 1
let rowsPerColumn = max(1, Int(ceil(Double(parsed.rows.count) / Double(columns))))
let gridHeight = CGFloat(rowsPerColumn) * rowHeight
let windowSize = NSSize(width: windowWidth, height: padding * 2 + titleHeight + ruleGap + gridHeight)

let app = NSApplication.shared
app.setActivationPolicy(.accessory)

let screen = activeScreen()
let windowOrigin = origin(for: position, size: windowSize, screen: screen, margin: margin)
let panel = HintPanel(
    contentRect: NSRect(origin: windowOrigin, size: windowSize),
    styleMask: [.borderless, .nonactivatingPanel],
    backing: .buffered,
    defer: false
)
panel.isReleasedWhenClosed = false
panel.isOpaque = false
panel.backgroundColor = .clear
panel.hasShadow = false
panel.level = .statusBar
panel.collectionBehavior = [.canJoinAllSpaces, .fullScreenAuxiliary, .stationary, .ignoresCycle]

let chrome = HexwareChromeView(frame: NSRect(origin: .zero, size: windowSize))
chrome.wantsLayer = true
chrome.layer?.shadowColor = HexwarePalette.frost.cgColor
chrome.layer?.shadowOpacity = 0.38
chrome.layer?.shadowRadius = 24
chrome.layer?.shadowOffset = NSSize(width: 0, height: -2)

let title = addLabel(
    parsed.title.uppercased(),
    frame: NSRect(x: padding, y: windowSize.height - padding - titleHeight, width: windowSize.width - padding * 2, height: titleHeight),
    font: NSFont.monospacedSystemFont(ofSize: fontSize + 5, weight: .bold),
    color: HexwarePalette.text,
    alignment: .center
)
title.shadow = NSShadow()
title.shadow?.shadowColor = HexwarePalette.frost.withAlphaComponent(0.75)
title.shadow?.shadowBlurRadius = 10
title.shadow?.shadowOffset = .zero
chrome.addSubview(title)

let titleY = windowSize.height - padding - titleHeight - 5
HexwarePalette.ember.withAlphaComponent(0.0).setFill()
let leftRule = NSView(frame: NSRect(x: padding + 32, y: titleY, width: (windowSize.width / 2) - 110, height: 1))
leftRule.wantsLayer = true
leftRule.layer?.backgroundColor = HexwarePalette.frost.withAlphaComponent(0.48).cgColor
chrome.addSubview(leftRule)
let rightRule = NSView(frame: NSRect(x: (windowSize.width / 2) + 78, y: titleY, width: (windowSize.width / 2) - 110, height: 1))
rightRule.wantsLayer = true
rightRule.layer?.backgroundColor = HexwarePalette.ember.withAlphaComponent(0.52).cgColor
chrome.addSubview(rightRule)

let contentWidth = windowSize.width - padding * 2
let columnWidth = (contentWidth - (columns == 2 ? columnGap : 0)) / CGFloat(columns)
let gridTop = titleY - ruleGap
let keyWidth: CGFloat = 74
let keyFont = NSFont.monospacedSystemFont(ofSize: fontSize, weight: .bold)
let labelFont = NSFont.systemFont(ofSize: fontSize, weight: .semibold)

for (index, row) in parsed.rows.enumerated() {
    let col = index / rowsPerColumn
    let rowIndex = index % rowsPerColumn
    let x = padding + CGFloat(col) * (columnWidth + columnGap)
    let y = gridTop - CGFloat(rowIndex + 1) * rowHeight

    let slot = NSView(frame: NSRect(x: x, y: y + 3, width: columnWidth, height: rowHeight - 6))
    slot.wantsLayer = true
    slot.layer?.backgroundColor = HexwarePalette.neutral.withAlphaComponent(0.34).cgColor
    slot.layer?.borderColor = (row.key == "esc" ? HexwarePalette.ember : HexwarePalette.frost).withAlphaComponent(0.30).cgColor
    slot.layer?.borderWidth = 1
    slot.layer?.cornerRadius = 6

    let keyLabel = addLabel(
        row.key.uppercased(),
        frame: NSRect(x: 11, y: 3, width: keyWidth, height: rowHeight - 12),
        font: keyFont,
        color: row.key == "esc" ? HexwarePalette.ember : HexwarePalette.frost,
        alignment: .left
    )
    let actionLabel = addLabel(
        row.label,
        frame: NSRect(x: keyWidth + 16, y: 3, width: columnWidth - keyWidth - 26, height: rowHeight - 12),
        font: labelFont,
        color: row.key == "esc" ? HexwarePalette.muted : HexwarePalette.text,
        alignment: .left
    )
    slot.addSubview(keyLabel)
    slot.addSubview(actionLabel)
    chrome.addSubview(slot)
}

panel.contentView = chrome
panel.orderFrontRegardless()

if duration > 0 {
    DispatchQueue.main.asyncAfter(deadline: .now() + duration) {
        app.terminate(nil)
    }
}

app.run()

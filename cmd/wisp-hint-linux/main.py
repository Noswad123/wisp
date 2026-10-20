#!/usr/bin/python3
"""Passive Wisp Wayland hint panel. Text is data, never markup or commands."""

import argparse
from dataclasses import dataclass
import math
import os
import signal
import sys


@dataclass(frozen=True)
class Hint:
    title: str
    rows: tuple


def parse_hint(text):
    lines = [line for line in text.split("\n") if line]
    rows = []
    for line in lines[1:]:
        line = line.strip()
        if line:
            # Match Swift's space/tab separator, not arbitrary Unicode whitespace.
            split = next((i for i, char in enumerate(line) if char in " \t"), len(line))
            rows.append((line[:split], line[split:].strip()))
    return Hint(lines[0] if lines else "Wisp", tuple(rows))


def input_text(env, stream):
    if "WISP_HINT_TEXT" in env:
        return env["WISP_HINT_TEXT"]
    return stream.read().strip()


POSITIONS = ("top-center", "center", "top-left", "top-right",
             "bottom-left", "bottom-center", "bottom-right")


@dataclass(frozen=True)
class Settings:
    position: str = "top-center"
    margin: float = 72
    width: float = 760
    padding: float = 24
    font_size: float = 15
    row_height: float = 30
    duration: float = 0

    @classmethod
    def from_env(cls, env):
        defaults = cls()
        values = {}
        for name in ("margin", "width", "padding", "font_size", "row_height", "duration"):
            fallback = getattr(defaults, name)
            try:
                value = float(env.get("WISP_HINT_" + name.upper(), fallback))
            except ValueError:
                value = fallback
            minimum = 1 if name in ("width", "font_size", "row_height") else 0
            values[name] = value if math.isfinite(value) and value >= minimum else fallback
        position = env.get("WISP_HINT_POSITION", defaults.position).lower()
        return cls(position=position if position in POSITIONS else defaults.position, **values)


def layout(hint, settings):
    columns = 2 if len(hint.rows) > 4 else 1
    rows_per_column = max(1, (len(hint.rows) + columns - 1) // columns)
    height = settings.padding * 2 + 34 + 13 + rows_per_column * settings.row_height
    return columns, rows_per_column, math.ceil(height)


def notify_ready(fd):
    """Consume the inherited descriptor; no notification is sent before map."""
    if fd is not None:
        try:
            os.write(fd, b"READY\n")
        finally:
            os.close(fd)


def load_gtk():
    # Keep parsing, --help, and unit tests usable without GI or a display.
    import gi
    gi.require_version("Gtk", "3.0")
    gi.require_version("Gdk", "3.0")
    gi.require_version("GtkLayerShell", "0.1")
    gi.require_version("Pango", "1.0")
    gi.require_version("PangoCairo", "1.0")
    gi.require_foreign("cairo")
    import cairo
    from gi.repository import Gdk, GLib, Gtk, GtkLayerShell, Pango, PangoCairo
    return cairo, Gdk, GLib, Gtk, GtkLayerShell, Pango, PangoCairo


def run_panel(deps, hint, settings, ready_fd):
    cairo, Gdk, GLib, Gtk, Layer, Pango, PangoCairo = deps
    if not Gtk.init_check()[0]:
        raise RuntimeError("cannot open a graphical display (a Wayland session is required)")
    display = Gdk.Display.get_default()
    # GTK3's Wayland backend has no separate typelib on some distributions.
    if display.__gtype__.name != "GdkWaylandDisplay":
        raise RuntimeError("native hints require Wayland, not X11")
    if not Layer.is_supported():
        raise RuntimeError("the Wayland compositor does not support gtk-layer-shell")

    monitor = None
    seat = display.get_default_seat()
    pointer = seat.get_pointer() if seat else None
    if pointer:
        _, x, y = pointer.get_position()
        monitor = display.get_monitor_at_point(x, y)
    monitor = monitor or display.get_primary_monitor() or display.get_monitor(0)
    if monitor is None:
        raise RuntimeError("no Wayland monitor is available")

    columns, rows_per_column, height = layout(hint, settings)
    geometry = monitor.get_geometry()
    width = max(1, min(math.ceil(settings.width), geometry.width))
    height = max(1, min(height, geometry.height))
    window = Gtk.Window(type=Gtk.WindowType.TOPLEVEL)
    window.set_title("Wisp Hints")
    window.set_decorated(False)
    window.set_accept_focus(False)
    window.set_focus_on_map(False)
    window.set_skip_taskbar_hint(True)
    window.set_skip_pager_hint(True)
    window.set_app_paintable(True)
    visual = window.get_screen().get_rgba_visual()
    if visual:
        window.set_visual(visual)
    Layer.init_for_window(window)
    Layer.set_namespace(window, "wisp-hint")
    Layer.set_monitor(window, monitor)
    Layer.set_layer(window, Layer.Layer.OVERLAY)
    Layer.set_exclusive_zone(window, 0)
    Layer.set_keyboard_mode(window, Layer.KeyboardMode.NONE)
    for name in ("top", "bottom", "left", "right"):
        edge = getattr(Layer.Edge, name.upper())
        anchored = name in settings.position.split("-")
        Layer.set_anchor(window, edge, anchored)
        # Avoid invalid placement on very small monitors.
        room = geometry.width - width if name in ("left", "right") else geometry.height - height
        Layer.set_margin(window, edge, min(round(settings.margin), max(0, room)))

    area = Gtk.DrawingArea()
    area.set_size_request(width, height)
    window.add(area)

    def draw(widget, cr):
        w, h = widget.get_allocated_width(), widget.get_allocated_height()

        def color(hex_color, alpha=1):
            cr.set_source_rgba(*(int(hex_color[i:i + 2], 16) / 255 for i in (0, 2, 4)), alpha)

        def outline(inset):
            cut = min(18, w / 4, h / 4)
            points = ((inset + cut, inset), (w - inset - cut, inset),
                      (w - inset, inset + cut), (w - inset, h - inset - cut),
                      (w - inset - cut, h - inset), (inset + cut, h - inset),
                      (inset, h - inset - cut), (inset, inset + cut))
            cr.move_to(*points[0])
            for point in points[1:]:
                cr.line_to(*point)
            cr.close_path()

        def text(value, x, y, available, size, ink, bold=False, center=False, mono=False):
            if available <= 0:
                return
            line = PangoCairo.create_layout(cr)
            font = Pango.FontDescription()
            font.set_family("monospace" if mono else "sans")
            font.set_absolute_size(size * Pango.SCALE)
            font.set_weight(Pango.Weight.BOLD if bold else Pango.Weight.SEMIBOLD)
            line.set_font_description(font)
            line.set_text(value, -1)
            line.set_single_paragraph_mode(True)
            line.set_width(int(available * Pango.SCALE))
            line.set_ellipsize(Pango.EllipsizeMode.END)
            if center:
                line.set_alignment(Pango.Alignment.CENTER)
            color(ink)
            cr.move_to(x, y)
            PangoCairo.show_layout(cr, line)

        cr.set_operator(cairo.OPERATOR_SOURCE)
        cr.set_source_rgba(0, 0, 0, 0)
        cr.paint()
        cr.set_operator(cairo.OPERATOR_OVER)
        outline(1.5)
        gradient = cairo.LinearGradient(0, 0, 0, h)
        gradient.add_color_stop_rgb(0, 16 / 255, 25 / 255, 37 / 255)
        gradient.add_color_stop_rgb(1, 5 / 255, 8 / 255, 18 / 255)
        cr.set_source(gradient)
        cr.fill_preserve()
        color("27a8ff", .72)
        cr.set_line_width(1.2)
        cr.stroke()
        outline(5.5)
        color("ff8a3d", .42)
        cr.set_line_width(.8)
        cr.stroke()

        pad = min(settings.padding, w / 4)
        text(hint.title.upper(), pad, pad, w - 2 * pad,
             settings.font_size + 5, "f4f7fb", bold=True, center=True, mono=True)
        for start, end, ink in ((pad + 32, w / 2 - 78, "27a8ff"),
                                (w / 2 + 78, w - pad - 32, "ff8a3d")):
            color(ink, .5)
            cr.rectangle(start, pad + 39, max(0, end - start), 1)
            cr.fill()
        column_width = (w - pad * 2 - (30 if columns == 2 else 0)) / columns
        for index, (key, label) in enumerate(hint.rows):
            col, row = divmod(index, rows_per_column)
            x = pad + col * (column_width + 30)
            y = pad + 47 + row * settings.row_height
            escape = key == "esc"
            ink = "ff8a3d" if escape else "27a8ff"
            cr.rectangle(x, y + 3, max(1, column_width), max(1, settings.row_height - 6))
            color("1b2633", .34)
            cr.fill_preserve()
            color(ink, .30)
            cr.set_line_width(1)
            cr.stroke()
            text_y = y + max(0, (settings.row_height - settings.font_size * 1.3) / 2)
            text(key.upper(), x + 11, text_y, min(74, column_width - 22),
                 settings.font_size, ink, bold=True, mono=True)
            text(label, x + 90, text_y, column_width - 100,
                 settings.font_size, "9aa9ba" if escape else "f4f7fb")
        return False

    area.connect("draw", draw)
    state = {"mapped": False, "error": None}

    def mapped(widget, event):
        widget.get_window().input_shape_combine_region(cairo.Region(), 0, 0)
        if not state["mapped"]:
            state["mapped"] = True
            try:
                notify_ready(ready_fd)
            except OSError as exc:
                state["error"] = f"readiness notification failed: {exc}"
                Gtk.main_quit()
            if settings.duration > 0:
                GLib.timeout_add(max(1, min(2147483647, round(settings.duration * 1000))), quit_panel)
        return False

    def quit_panel(*args):
        if Gtk.main_level():
            Gtk.main_quit()
        return False

    def map_timeout():
        if not state["mapped"]:
            state["error"] = "Wayland panel did not map within 5 seconds"
            Gtk.main_quit()
        return False

    window.connect("realize", lambda widget: widget.get_window().input_shape_combine_region(cairo.Region(), 0, 0))
    window.connect("map-event", mapped)
    window.connect("destroy", quit_panel)
    GLib.unix_signal_add(GLib.PRIORITY_DEFAULT, signal.SIGTERM, quit_panel)
    GLib.unix_signal_add(GLib.PRIORITY_DEFAULT, signal.SIGINT, quit_panel)
    GLib.timeout_add_seconds(5, map_timeout)
    window.show_all()
    try:
        Gtk.main()
    finally:
        window.destroy()
    if state["error"]:
        raise RuntimeError(state["error"])


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__, epilog=(
        "Input: WISP_HINT_TEXT, otherwise UTF-8 stdin through EOF. First line is the title; "
        "remaining lines are key + whitespace + label. Settings: WISP_HINT_POSITION "
        "(top-center, center, top-left/right, bottom-left/center/right), MARGIN=72, WIDTH=760, "
        "PADDING=24, FONT_SIZE=15, ROW_HEIGHT=30, DURATION=0 (sticky); all use WISP_HINT_ "
        "prefix. Optional inherited WISP_HINT_READY_FD receives READY\\n once on GTK map, "
        "then closes. No subprocesses or actions. SIGTERM/SIGINT exit cleanly."))
    parser.add_argument("--check", action="store_true", help="check imports only; no display, stdin, or window")
    args = parser.parse_args(argv)
    try:
        deps = load_gtk()
        if args.check:
            print("wisp-hint-linux: GTK3/GtkLayerShell dependencies available (display not checked)")
            return 0
        ready_fd = None
        if "WISP_HINT_READY_FD" in os.environ:
            ready_fd = int(os.environ["WISP_HINT_READY_FD"])
            if ready_fd < 3:
                raise ValueError("WISP_HINT_READY_FD must be an inherited descriptor >= 3")
            os.fstat(ready_fd)
        hint = parse_hint(input_text(os.environ, sys.stdin))
        run_panel(deps, hint, Settings.from_env(os.environ), ready_fd)
        return 0
    except (ImportError, ValueError, OSError, RuntimeError) as exc:
        print(f"wisp-hint-linux: {exc}", file=sys.stderr)
        return 1


if __name__ == "__main__":
    sys.exit(main())

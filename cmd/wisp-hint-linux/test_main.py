"""Headless tests: no GI imports or graphical session required."""

import importlib.util
import io
import os
from pathlib import Path
import sys
from types import SimpleNamespace
import unittest
from unittest.mock import Mock, patch

spec = importlib.util.spec_from_file_location("wisp_hint_linux", Path(__file__).with_name("main.py"))
hint = importlib.util.module_from_spec(spec)
sys.modules[spec.name] = hint
spec.loader.exec_module(hint)


class HintTests(unittest.TestCase):
    def test_parse(self):
        parsed = hint.parse_hint("Wisp Actions\n\na  First action\n b\tSecond action \n \nesc Close\nsolo\n")
        self.assertEqual(parsed.title, "Wisp Actions")
        self.assertEqual(parsed.rows, (("a", "First action"), ("b", "Second action"),
                                       ("esc", "Close"), ("solo", "")))

    def test_empty(self):
        self.assertEqual(hint.parse_hint("\n\n"), hint.Hint("Wisp", ()))

    def test_text_is_literal(self):
        parsed = hint.parse_hint("<b>Title & name</b>\nx <script>$(touch /tmp/no)</script>")
        self.assertEqual(parsed.title, "<b>Title & name</b>")
        self.assertEqual(parsed.rows[0][1], "<script>$(touch /tmp/no)</script>")

    def test_unicode(self):
        self.assertEqual(hint.parse_hint("\u03bb\nx \u65e5\u672c\u8a9e").rows, (("x", "\u65e5\u672c\u8a9e"),))

    def test_env_does_not_read_stdin(self):
        with patch("sys.stdin") as stream:
            self.assertEqual(hint.input_text({"WISP_HINT_TEXT": ""}, stream), "")
            stream.read.assert_not_called()
        self.assertEqual(hint.input_text({}, io.StringIO(" Wisp\nx Label\n")), "Wisp\nx Label")

    def test_defaults(self):
        settings = hint.Settings.from_env({})
        self.assertEqual(settings, hint.Settings())
        self.assertEqual(hint.layout(hint.parse_hint("Wisp"), settings), (1, 1, 125))

    def test_columns(self):
        settings = hint.Settings()
        for count, expected in ((4, (1, 4, 215)), (5, (2, 3, 185)), (6, (2, 3, 185))):
            parsed = hint.Hint("Wisp", tuple((str(i), "action") for i in range(count)))
            self.assertEqual(hint.layout(parsed, settings), expected)

    def test_settings(self):
        env = {"WISP_HINT_" + key: value for key, value in
               (("WIDTH", "640"), ("MARGIN", "0"), ("PADDING", "12"),
                ("FONT_SIZE", "18"), ("ROW_HEIGHT", "32"), ("DURATION", "1.5"),
                ("POSITION", "BOTTOM-RIGHT"))}
        self.assertEqual(hint.Settings.from_env(env), hint.Settings("bottom-right", 0, 640, 12, 18, 32, 1.5))

    def test_invalid_settings(self):
        for value in ("bad", "nan", "inf", "-1"):
            env = {"WISP_HINT_" + key: value for key in
                   ("WIDTH", "MARGIN", "PADDING", "FONT_SIZE", "ROW_HEIGHT", "DURATION", "POSITION")}
            self.assertEqual(hint.Settings.from_env(env), hint.Settings())
        self.assertEqual(hint.Settings.from_env({"WISP_HINT_WIDTH": "0"}).width, 760)

    def test_ready_contract(self):
        read_fd, write_fd = os.pipe()
        try:
            hint.notify_ready(write_fd)
            self.assertEqual(os.read(read_fd, 64), b"READY\n")
            self.assertEqual(os.read(read_fd, 64), b"")
            with self.assertRaises(OSError):
                os.fstat(write_fd)
        finally:
            os.close(read_fd)
        hint.notify_ready(None)

    def test_check_does_not_read_input_or_start_panel(self):
        with patch.object(hint, "load_gtk", return_value=()), patch.object(hint, "run_panel") as run, \
                patch.object(hint, "input_text") as read, patch("sys.stdout", new_callable=io.StringIO):
            self.assertEqual(hint.main(["--check"]), 0)
            run.assert_not_called()
            read.assert_not_called()

    def test_dependency_error(self):
        with patch.object(hint, "load_gtk", side_effect=ImportError("missing gi")), \
                patch("sys.stderr", new_callable=io.StringIO) as stderr:
            self.assertEqual(hint.main(["--check"]), 1)
            self.assertIn("missing gi", stderr.getvalue())

    def test_unsupported_graphical_sessions(self):
        gtk, gdk, layer = Mock(), Mock(), Mock()
        deps = (None, gdk, None, gtk, layer, None, None)
        gtk.init_check.return_value = (False, [])
        with self.assertRaisesRegex(RuntimeError, "cannot open"):
            hint.run_panel(deps, hint.Hint("Wisp", ()), hint.Settings(), None)
        gtk.init_check.return_value = (True, [])
        gdk.Display.get_default.return_value.__gtype__ = SimpleNamespace(name="GdkX11Display")
        with self.assertRaisesRegex(RuntimeError, "not X11"):
            hint.run_panel(deps, hint.Hint("Wisp", ()), hint.Settings(), None)
        gdk.Display.get_default.return_value.__gtype__.name = "GdkWaylandDisplay"
        layer.is_supported.return_value = False
        with self.assertRaisesRegex(RuntimeError, "does not support"):
            hint.run_panel(deps, hint.Hint("Wisp", ()), hint.Settings(), None)
        gtk.Window.assert_not_called()

    def test_help_without_dependencies(self):
        with patch.object(hint, "load_gtk") as load, patch("sys.stdout", new_callable=io.StringIO):
            with self.assertRaises(SystemExit) as result:
                hint.main(["--help"])
            self.assertEqual(result.exception.code, 0)
            load.assert_not_called()


if __name__ == "__main__":
    unittest.main()

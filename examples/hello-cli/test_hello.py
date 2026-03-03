"""Tests for hello.py — covers greet(), parse_args(), and CLI invocation."""

import subprocess
import sys
import unittest

from hello import greet, main, parse_args


class TestGreet(unittest.TestCase):
    """Unit tests for the greet() function."""

    def test_default_name(self):
        self.assertEqual(greet("World"), "Hello, World!")

    def test_custom_name(self):
        self.assertEqual(greet("Alice"), "Hello, Alice!")


class TestParseArgs(unittest.TestCase):
    """Unit tests for argument parsing."""

    def test_no_args_defaults_to_world(self):
        args = parse_args([])
        self.assertEqual(args.name, "World")

    def test_name_flag(self):
        args = parse_args(["--name", "Bob"])
        self.assertEqual(args.name, "Bob")


class TestMain(unittest.TestCase):
    """Integration: call main() and verify printed output."""

    def test_default_output(self, capsys=None):
        from io import StringIO
        import contextlib

        buf = StringIO()
        with contextlib.redirect_stdout(buf):
            main([])
        self.assertEqual(buf.getvalue().strip(), "Hello, World!")

    def test_named_output(self):
        from io import StringIO
        import contextlib

        buf = StringIO()
        with contextlib.redirect_stdout(buf):
            main(["--name", "Alice"])
        self.assertEqual(buf.getvalue().strip(), "Hello, Alice!")


class TestCLI(unittest.TestCase):
    """End-to-end: invoke hello.py as a subprocess."""

    def test_cli_default(self):
        result = subprocess.run(
            [sys.executable, "hello.py"],
            capture_output=True, text=True, cwd=sys.path[0] or ".",
        )
        self.assertEqual(result.returncode, 0)
        self.assertEqual(result.stdout.strip(), "Hello, World!")

    def test_cli_with_name(self):
        result = subprocess.run(
            [sys.executable, "hello.py", "--name", "Eve"],
            capture_output=True, text=True, cwd=sys.path[0] or ".",
        )
        self.assertEqual(result.returncode, 0)
        self.assertEqual(result.stdout.strip(), "Hello, Eve!")


if __name__ == "__main__":
    unittest.main()
